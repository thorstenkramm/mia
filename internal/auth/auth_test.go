package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/identity"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestLoginPasswordGateStrictInputAndAuditEvents(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, true)
	csrf := csrfToken(t, server)

	invalid := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, `{"data":{"type":"login-attempts","attributes":{"username":"student","password":"wrong password"},"unexpected":true}}`)
	assertCode(t, invalid, http.StatusUnprocessableEntity, "auth_invalid_request")

	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	assertCode(t, login, http.StatusOK, "")
	if stage(t, login.Body.Bytes()) != "password-change" {
		t.Fatalf("login stage = %q", stage(t, login.Body.Bytes()))
	}
	session, rotatedCSRF := authCookies(t, login)

	blocked := serve(t, server, http.MethodPost, "/api/v1/auth/logout", rotatedCSRF, []*http.Cookie{session}, "")
	if blocked.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", blocked.Code)
	}

	var failures int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'auth.session.failed'").Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 1 {
		t.Fatalf("failed-login audits = %d", failures)
	}
	var sensitive int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE metadata <> '{}' OR metadata LIKE '%wrong password%'").Scan(&sensitive); err != nil {
		t.Fatal(err)
	}
	if sensitive != 0 {
		t.Fatal("audit retained sensitive login content")
	}
	_ = account
}

func TestPasswordChangeTransitionsAndStaleStagesAreRejected(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, true)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, login)

	changed := serve(t, server, http.MethodPost, "/api/v1/auth/password-changes", csrf, []*http.Cookie{session}, `{"data":{"type":"password-changes","attributes":{"password":"a different strong password","password_confirmation":"a different strong password"}}}`)
	assertCode(t, changed, http.StatusOK, "")
	if stage(t, changed.Body.Bytes()) != "authenticated" {
		t.Fatalf("changed stage = %q", stage(t, changed.Body.Bytes()))
	}
	freshSession, _ := authCookies(t, changed)
	freshRequest := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/auth/logout", nil)
	freshRequest.AddCookie(freshSession)
	stored, err := server.Sessions.Get(freshRequest, cookieName)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt, ok := stored.Values["expires_at"].(int64)
	if !ok || time.Unix(expiresAt, 0).Before(time.Now().Add(11*time.Hour+59*time.Minute)) {
		t.Fatal("password-change transition did not start a fresh 12-hour session")
	}
	stale := serve(t, server, http.MethodPost, "/api/v1/auth/password-changes", csrf, []*http.Cookie{session}, `{"data":{"type":"password-changes","attributes":{"password":"yet another password","password_confirmation":"yet another password"}}}`)
	assertCode(t, stale, http.StatusForbidden, "auth_password_change_required")
	var gate int
	if err := database.QueryRow("SELECT must_change_password FROM users WHERE id = ?", account.ID).Scan(&gate); err != nil {
		t.Fatal(err)
	}
	if gate != 0 {
		t.Fatal("password gate remained after successful replacement")
	}
}

func TestRestrictedRoutesRequireValidUnexpiredSession(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, login)
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/auth/logout", nil)
	request.AddCookie(session)
	stored, err := server.Sessions.Get(request, cookieName)
	if err != nil {
		t.Fatal(err)
	}
	stored.Values["idle_until"] = int64(1)
	recorder := httptest.NewRecorder()
	if err := server.Sessions.Save(request, recorder, stored); err != nil {
		t.Fatal(err)
	}
	expired := recorder.Result().Cookies()[0]
	response := serve(t, server, http.MethodPost, "/api/v1/auth/logout", csrf, []*http.Cookie{expired}, "")
	assertCode(t, response, http.StatusUnauthorized, "auth_unauthenticated")
	_ = account
}

func TestLoginFailuresDelayThenThrottleAndAudit(t *testing.T) {
	server, database := testServer(t)
	createAccount(t, database, false)
	csrf := csrfToken(t, server)
	for attempt := 1; attempt <= 5; attempt++ {
		response := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "incorrect password"))
		if attempt == 1 {
			assertCode(t, response, http.StatusUnauthorized, "auth_invalid_credentials")
			if response.Header().Get("Retry-After") != "" {
				t.Fatalf("attempt %d unexpectedly had Retry-After", attempt)
			}
			continue
		}
		assertCode(t, response, http.StatusTooManyRequests, "auth_login_throttled")
		if response.Header().Get("Retry-After") == "" {
			t.Fatal("throttled login omitted Retry-After")
		}
	}
	var failed, throttled int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'auth.session.failed'").Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'auth.session.throttled'").Scan(&throttled); err != nil {
		t.Fatal(err)
	}
	if failed != 1 || throttled != 4 {
		t.Fatalf("failed/throttled audits = %d/%d", failed, throttled)
	}
}

func TestLoginRejectsUnsupportedMediaAndOversizedBody(t *testing.T) {
	server, _ := testServer(t)
	csrf := csrfToken(t, server)
	unsupported := serveMedia(t, server, csrf, "application/json", loginBody("student", "password"))
	assertCode(t, unsupported, http.StatusUnsupportedMediaType, "auth_unsupported_media_type")
	oversized := serveMedia(t, server, csrf, "application/vnd.api+json", strings.Repeat("x", 1<<20+1))
	assertCode(t, oversized, http.StatusRequestEntityTooLarge, "auth_request_too_large")
}

func TestLoginRejectsMalformedPasswordUnicode(t *testing.T) {
	for name, body := range map[string]string{
		"invalid_utf8":       string(append(append([]byte(`{"data":{"type":"login-attempts","attributes":{"username":"student","password":"`), 0xff), []byte(`"}}}`)...)),
		"unpaired_surrogate": `{"data":{"type":"login-attempts","attributes":{"username":"student","password":"\uD800"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server, _ := testServer(t)
			csrf := csrfToken(t, server)
			response := serveMedia(t, server, csrf, "application/vnd.api+json", body)
			assertCode(t, response, http.StatusUnprocessableEntity, "auth_invalid_request")
		})
	}
}

func TestCurrentGateAndInvalidSessionsAreEnforcedAndCleared(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, login)
	server.AuthenticatedPOST("/api/v1/test", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	if _, err := database.Exec("UPDATE users SET must_change_password = 1 WHERE id = ?", account.ID); err != nil {
		t.Fatal(err)
	}
	blocked := serve(t, server, http.MethodPost, "/api/v1/test", csrf, []*http.Cookie{session}, "")
	assertCode(t, blocked, http.StatusForbidden, "auth_password_change_required")
	changed := serve(t, server, http.MethodPost, "/api/v1/auth/password-changes", csrf, []*http.Cookie{session}, `{"data":{"type":"password-changes","attributes":{"password":"a different strong password","password_confirmation":"a different strong password"}}}`)
	assertCode(t, changed, http.StatusOK, "")

	malformed := serve(t, server, http.MethodPost, "/api/v1/auth/logout", csrf, []*http.Cookie{{Name: cookieName, Value: "malformed"}}, "")
	assertCode(t, malformed, http.StatusUnauthorized, "auth_unauthenticated")
	if !hasClearedSession(malformed) {
		t.Fatal("malformed cookie was not cleared")
	}
}

func TestAuthenticatedRefreshesOnlySuccessfulRequests(t *testing.T) {
	server, database := testServer(t)
	createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, login)
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/succeeds", nil)
	request.AddCookie(session)
	stored, err := server.Sessions.Get(request, cookieName)
	if err != nil {
		t.Fatal(err)
	}
	_, idleOK := stored.Values["idle_until"].(int64)
	absolute, absoluteOK := stored.Values["expires_at"].(int64)
	if !idleOK || !absoluteOK {
		t.Fatal("initial session timers missing")
	}
	stored.Values["idle_until"] = time.Now().Add(5 * time.Second).Unix()
	olderRecorder := httptest.NewRecorder()
	if err := server.Sessions.Save(request, olderRecorder, stored); err != nil {
		t.Fatal(err)
	}
	session = sessionCookie(t, olderRecorder)
	previousIdle, idleOK := stored.Values["idle_until"].(int64)
	if !idleOK {
		t.Fatal("older session idle deadline missing")
	}
	server.AuthenticatedPOST("/api/v1/succeeds", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	server.AuthenticatedPOST("/api/v1/fails", func(*echo.Context) error { return httpserver.NewError(httpserver.CodeInvalidRequest) })
	success := serve(t, server, http.MethodPost, "/api/v1/succeeds", csrf, []*http.Cookie{session}, "")
	if !hasSession(success) {
		t.Fatal("successful authenticated request did not refresh session")
	}
	refreshed := sessionCookie(t, success)
	request = httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/succeeds", nil)
	request.AddCookie(refreshed)
	stored, err = server.Sessions.Get(request, cookieName)
	refreshedIdle, idleOK := stored.Values["idle_until"].(int64)
	refreshedAbsolute, absoluteOK := stored.Values["expires_at"].(int64)
	if err != nil || !idleOK || !absoluteOK || refreshedIdle <= previousIdle || refreshedIdle > refreshedAbsolute || refreshedAbsolute != absolute {
		t.Fatal("idle refresh did not advance without preserving absolute expiry")
	}
	failure := serve(t, server, http.MethodPost, "/api/v1/fails", csrf, []*http.Cookie{session}, "")
	assertCode(t, failure, http.StatusUnprocessableEntity, "auth_invalid_request")
	if hasSession(failure) {
		t.Fatal("failed authenticated request refreshed session")
	}
}

func TestBanDeletionAndSecurityGenerationInvalidateCookies(t *testing.T) {
	for name, mutate := range map[string]func(*sql.DB, string) error{
		"ban": func(database *sql.DB, id string) error {
			_, err := database.Exec("UPDATE users SET is_banned = 1 WHERE id = ?", id)
			return err
		},
		"generation": func(database *sql.DB, id string) error {
			_, err := database.Exec("UPDATE users SET security_generation = security_generation + 1 WHERE id = ?", id)
			return err
		},
		"deletion": func(database *sql.DB, id string) error {
			_, err := database.Exec("DELETE FROM users WHERE id = ?", id)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			server, database := testServer(t)
			account := createAccount(t, database, false)
			csrf := csrfToken(t, server)
			login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
			session, csrf := authCookies(t, login)
			if err := mutate(database, account.ID); err != nil {
				t.Fatal(err)
			}
			response := serve(t, server, http.MethodPost, "/api/v1/auth/logout", csrf, []*http.Cookie{session}, "")
			assertCode(t, response, http.StatusUnauthorized, "auth_unauthenticated")
			if !hasClearedSession(response) {
				t.Fatal("invalidated cookie was not cleared")
			}
		})
	}
}

func testServer(t *testing.T) (*httpserver.Server, *sql.DB) {
	t.Helper()
	directory := t.TempDir()
	docRoot := filepath.Join(directory, "public")
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	database, err := miSQLite.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	server, routes, err := httpserver.New(httpserver.Options{DataDir: directory, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		account, err := user.LoadSecurityState(ctx, database, id)
		if err != nil {
			return httpserver.IdentityState{}, httpserver.ErrIdentityNotFound
		}
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration, MustChangePassword: account.MustChangePassword, Banned: account.Banned}, nil
	})
	Register(server, routes, database)
	return server, database
}

func createAccount(t *testing.T, database *sql.DB, gated bool) user.Account {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{Username: "student", PasswordHash: hash, Language: "en", Country: "DE", TimeZone: "UTC", Roles: []user.Role{user.Student}})
		if createErr != nil {
			return createErr
		}
		if gated {
			_, createErr = tx.ExecContext(context.Background(), "UPDATE users SET must_change_password = 1 WHERE id = ?", account.ID)
		}
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return account
}

func csrfToken(t *testing.T, server *httpserver.Server) string {
	t.Helper()
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/missing", nil))
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "__Host-mia_csrf" {
			return cookie.Value
		}
	}
	t.Fatal("no csrf cookie")
	return ""
}

func serve(t *testing.T, server *httpserver.Server, method, path, csrf string, cookies []*http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://mia.test"+path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/vnd.api+json")
	request.Header.Set("X-CSRF-Token", csrf)
	if csrf != "" {
		request.AddCookie(&http.Cookie{Name: "__Host-mia_csrf", Value: csrf})
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func serveMedia(t *testing.T, server *httpserver.Server, csrf, mediaType, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/auth/login", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", mediaType)
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: "__Host-mia_csrf", Value: csrf})
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func loginBody(username, password string) string {
	return `{"data":{"type":"login-attempts","attributes":{"username":"` + username + `","password":"` + password + `"}}}`
}

func assertCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if code != "" && !bytes.Contains(response.Body.Bytes(), []byte(`"code":"`+code+`"`)) {
		t.Fatalf("body %s does not contain code %q", response.Body.String(), code)
	}
}

func authCookies(t *testing.T, response *httptest.ResponseRecorder) (*http.Cookie, string) {
	t.Helper()
	var session *http.Cookie
	var csrf string
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case cookieName:
			session = cookie
		case "__Host-mia_csrf":
			csrf = cookie.Value
		}
	}
	if session == nil || csrf == "" {
		t.Fatalf("auth cookies session=%v csrf=%q", session != nil, csrf)
	}
	return session, csrf
}

func hasSession(response *httptest.ResponseRecorder) bool {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == cookieName && cookie.MaxAge >= 0 {
			return true
		}
	}
	return false
}

func hasClearedSession(response *httptest.ResponseRecorder) bool {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == cookieName && cookie.MaxAge < 0 {
			return true
		}
	}
	return false
}

func sessionCookie(t *testing.T, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == cookieName {
			return cookie
		}
	}
	t.Fatal("response omitted session cookie")
	return nil
}

func stage(t *testing.T, body []byte) string {
	t.Helper()
	var response struct {
		Data struct {
			Attributes struct {
				Stage string `json:"stage"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	return response.Data.Attributes.Stage
}
