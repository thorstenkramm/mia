package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/httpserver/conformance"
	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/provider/sms"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestLoginPasswordGateStrictInputAndAuditEvents(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, true)
	csrf := csrfToken(t, server)

	malformed := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, `{"data":{"type":"login-attempts","attributes":{"username":"student","password":"wrong password"},"unexpected":true}}`)
	assertCode(t, malformed, http.StatusBadRequest, "malformed_request")
	assertAuditCount(t, database, "auth.session.failed", 0)

	wrongCredentials := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil,
		loginBody("student", "wrong password"))
	assertCode(t, wrongCredentials, http.StatusUnauthorized, "auth_invalid_credentials")

	invalid := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, `{"data":{"type":"login-attempts","attributes":{"password":"wrong password"}}}`)
	assertCode(t, invalid, http.StatusUnprocessableEntity, "auth_invalid_request")

	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	assertCode(t, login, http.StatusOK, "")
	if stage(t, login.Body.Bytes()) != "password-change" {
		t.Fatalf("login stage = %q", stage(t, login.Body.Bytes()))
	}
	session, rotatedCSRF := authCookies(t, server, login)

	blocked := serve(t, server, http.MethodPost, "/api/v1/auth/logout", rotatedCSRF, session, "")
	if blocked.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", blocked.Code)
	}

	var failures int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'auth.session.failed'").Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 2 {
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
	session, csrf := authCookies(t, server, login)

	changed := serve(t, server, http.MethodPost, "/api/v1/auth/password-changes", csrf, session, `{"data":{"type":"password-changes","attributes":{"password":"a different strong password","password_confirmation":"a different strong password"}}}`)
	assertCode(t, changed, http.StatusOK, "")
	if stage(t, changed.Body.Bytes()) != "authenticated" {
		t.Fatalf("changed stage = %q", stage(t, changed.Body.Bytes()))
	}
	changedSessionCookie := sessionCookie(t, server, changed)
	changedBrowserCookie := responseCookie(changed, server.BrowserCookieName())
	if changedBrowserCookie == nil || changedBrowserCookie.MaxAge != changedSessionCookie.MaxAge ||
		changedBrowserCookie.MaxAge < int((11*time.Hour+59*time.Minute)/time.Second) {
		t.Fatal("authenticated transition did not persist the browser generation through the session lifetime")
	}
	freshSession, _ := authCookies(t, server, changed)
	freshRequest := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/auth/logout", nil)
	for _, cookie := range freshSession {
		freshRequest.AddCookie(cookie)
	}
	stored, err := server.Sessions.Get(freshRequest, server.SessionCookieName())
	if err != nil {
		t.Fatal(err)
	}
	expiresAt, ok := stored.Values["expires_at"].(int64)
	if !ok || time.Unix(expiresAt, 0).Before(time.Now().Add(11*time.Hour+59*time.Minute)) {
		t.Fatal("password-change transition did not start a fresh 12-hour session")
	}
	stale := serve(t, server, http.MethodPost, "/api/v1/auth/password-changes", csrf, session, `{"data":{"type":"password-changes","attributes":{"password":"yet another password","password_confirmation":"yet another password"}}}`)
	assertCode(t, stale, http.StatusForbidden, "auth_password_change_required")
	var gate int
	if err := database.QueryRow("SELECT must_change_password FROM users WHERE id = ?", account.ID).Scan(&gate); err != nil {
		t.Fatal(err)
	}
	if gate != 0 {
		t.Fatal("password gate remained after successful replacement")
	}
}

func TestLocalCookiePolicyAuthTransitions(t *testing.T) {
	server, database := testLocalServer(t)
	createAccount(t, database, true)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	assertLocalCSRFCookie(t, login)
	session, csrf := authCookies(t, server, login)
	changed := serve(t, server, http.MethodPost, "/api/v1/auth/password-changes", csrf, session, `{"data":{"type":"password-changes","attributes":{"password":"a different strong password","password_confirmation":"a different strong password"}}}`)
	assertLocalCSRFCookie(t, changed)
	session, csrf = authCookies(t, server, changed)
	logout := serve(t, server, http.MethodPost, "/api/v1/auth/logout", csrf, session, "")
	assertLocalCSRFCookie(t, logout)
}

func TestRestrictedRoutesRequireValidUnexpiredSession(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, server, login)
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/auth/logout", nil)
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	stored, err := server.Sessions.Get(request, server.SessionCookieName())
	if err != nil {
		t.Fatal(err)
	}
	stored.Values["idle_until"] = int64(1)
	recorder := httptest.NewRecorder()
	if err := server.Sessions.Save(request, recorder, stored); err != nil {
		t.Fatal(err)
	}
	expired := recorder.Result().Cookies()[0]
	response := serve(t, server, http.MethodPost, "/api/v1/auth/logout", csrf,
		replaceSessionCookie(server, session, expired), "")
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
	conformance.Error(t, unsupported, http.StatusUnsupportedMediaType, "unsupported_media_type")
	oversized := serveMedia(t, server, csrf, "application/vnd.api+json", strings.Repeat("x", 1<<20+1))
	conformance.Error(t, oversized, http.StatusRequestEntityTooLarge, "request_too_large")
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
			conformance.Error(t, response, http.StatusBadRequest, "malformed_request")
		})
	}
}

func TestAuthCreateResourcesRejectClientGeneratedIDsAsDomainErrors(t *testing.T) {
	server, _ := testServer(t)
	csrf := csrfToken(t, server)
	for _, testCase := range []struct {
		name, path, body, code string
	}{
		{name: "login", path: "/api/v1/auth/login", code: "auth_invalid_request",
			body: `{"data":{"type":"login-attempts","id":"client-id","attributes":{"username":"student","password":"password"}}}`},
		{name: "password recovery", path: "/api/v1/auth/password-recovery-requests", code: "auth_invalid_request",
			body: `{"data":{"type":"password-recovery-requests","id":"client-id","attributes":{"username":"staff"}}}`},
		{name: "password reset", path: "/api/v1/auth/password-resets", code: "auth_invalid_request",
			body: `{"data":{"type":"password-resets","id":"client-id","attributes":{"token":"invalid","password":"password","password_confirmation":"password"}}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := serve(t, server, http.MethodPost, testCase.path, csrf, nil, testCase.body)
			conformance.Error(t, response, http.StatusUnprocessableEntity, testCase.code)
		})
	}
}

func TestCurrentGateAndInvalidSessionsAreEnforcedAndCleared(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, server, login)
	server.AuthenticatedPOST("/api/v1/test", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	if _, err := database.Exec("UPDATE users SET must_change_password = 1 WHERE id = ?", account.ID); err != nil {
		t.Fatal(err)
	}
	blocked := serve(t, server, http.MethodPost, "/api/v1/test", csrf, session, "")
	assertCode(t, blocked, http.StatusForbidden, "auth_password_change_required")
	changed := serve(t, server, http.MethodPost, "/api/v1/auth/password-changes", csrf, session, `{"data":{"type":"password-changes","attributes":{"password":"a different strong password","password_confirmation":"a different strong password"}}}`)
	assertCode(t, changed, http.StatusOK, "")

	malformed := serve(t, server, http.MethodPost, "/api/v1/auth/logout", csrf, []*http.Cookie{{Name: server.SessionCookieName(), Value: "malformed"}}, "")
	assertCode(t, malformed, http.StatusUnauthorized, "auth_unauthenticated")
	if !hasClearedSession(server, malformed) {
		t.Fatal("malformed cookie was not cleared")
	}
}

func TestOnlyContinueWorkingExtendsAuthenticatedSession(t *testing.T) {
	server, database := testServer(t)
	createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, server, login)
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/succeeds", nil)
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	stored, err := server.Sessions.Get(request, server.SessionCookieName())
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
	session = replaceSessionCookie(server, session, sessionCookie(t, server, olderRecorder))
	previousIdle, idleOK := stored.Values["idle_until"].(int64)
	if !idleOK {
		t.Fatal("older session idle deadline missing")
	}
	server.AuthenticatedPOST("/api/v1/succeeds", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	server.AuthenticatedPOST("/api/v1/fails", func(*echo.Context) error { return httpserver.NewError(httpserver.CodeInvalidRequest) })
	success := serve(t, server, http.MethodPost, "/api/v1/succeeds", csrf, session, "")
	if hasSession(server, success) {
		t.Fatal("ordinary authenticated request refreshed session")
	}
	if responseCookie(success, server.BrowserCookieName()) != nil {
		t.Fatal("ordinary authenticated request refreshed browser generation")
	}
	if success.Header().Get("Mia-Session-Idle-Expires-At") != httpserver.FormatInstant(time.Unix(previousIdle, 0)) ||
		success.Header().Get("Mia-Session-Absolute-Expires-At") != httpserver.FormatInstant(time.Unix(absolute, 0)) {
		t.Fatal("ordinary response omitted unchanged authoritative deadlines")
	}
	continued := serve(t, server, http.MethodPost, "/api/v1/auth/session-continuations", csrf, session, "")
	assertCode(t, continued, http.StatusOK, "")
	if responseCookie(continued, server.BrowserCookieName()) != nil {
		t.Fatal("Continue working refreshed browser generation")
	}
	refreshed := replaceSessionCookie(server, session, sessionCookie(t, server, continued))
	request = httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/succeeds", nil)
	for _, cookie := range refreshed {
		request.AddCookie(cookie)
	}
	stored, err = server.Sessions.Get(request, server.SessionCookieName())
	refreshedIdle, idleOK := stored.Values["idle_until"].(int64)
	refreshedAbsolute, absoluteOK := stored.Values["expires_at"].(int64)
	if err != nil || !idleOK || !absoluteOK || refreshedIdle <= previousIdle || refreshedIdle > refreshedAbsolute || refreshedAbsolute != absolute {
		t.Fatal("idle refresh did not advance without preserving absolute expiry")
	}
	failure := serve(t, server, http.MethodPost, "/api/v1/fails", csrf, session, "")
	assertCode(t, failure, http.StatusUnprocessableEntity, "auth_invalid_request")
	if hasSession(server, failure) {
		t.Fatal("failed authenticated request refreshed session")
	}
}

func TestBrowserRestartRetainsBoundAuthenticatedSession(t *testing.T) {
	server, database := testServer(t)
	createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil,
		loginBody("student", "correct horse battery"))
	assertCode(t, login, http.StatusOK, "")

	var restartedCookies []*http.Cookie
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name != server.SessionCookieName() && cookie.Name != server.BrowserCookieName() {
			continue
		}
		if cookie.MaxAge <= 0 {
			t.Fatalf("%s is not persistent across a browser restart", cookie.Name)
		}
		restartedCookies = append(restartedCookies, cookie)
	}
	if len(restartedCookies) != 2 {
		t.Fatalf("persistent authentication cookies = %d, expected 2", len(restartedCookies))
	}
	if restartedCookies[0].MaxAge != restartedCookies[1].MaxAge {
		t.Fatal("browser generation and authentication cookie lifetimes differ")
	}

	discovered := serve(t, server, http.MethodGet, "/api/v1/auth/session", "", restartedCookies, "")
	assertCode(t, discovered, http.StatusOK, "")
	if stage(t, discovered.Body.Bytes()) != "authenticated" {
		t.Fatalf("session after browser restart = %s", discovered.Body.String())
	}
}

func TestSessionDiscoveryReturnsAnonymousAndAuthoritativeStages(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, true)

	anonymous := serve(t, server, http.MethodGet, "/api/v1/auth/session", "", nil, "")
	assertCode(t, anonymous, http.StatusOK, "")
	if !strings.Contains(anonymous.Body.String(), `"data":null`) ||
		!strings.Contains(anonymous.Body.String(), `"stage":"anonymous"`) {
		t.Fatalf("anonymous discovery = %s", anonymous.Body.String())
	}
	if csrfCookieValue(t, server, anonymous) == "" || responseCookie(anonymous, server.BrowserCookieName()) == nil {
		t.Fatal("anonymous discovery omitted public browser state")
	}

	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil,
		loginBody("student", "correct horse battery"))
	cookies, _ := authCookies(t, server, login)
	discovered := serve(t, server, http.MethodGet, "/api/v1/auth/session", "", cookies, "")
	assertCode(t, discovered, http.StatusOK, "")
	if stage(t, discovered.Body.Bytes()) != "password-change" ||
		!strings.Contains(discovered.Body.String(), `"idle_expires_at":"`) ||
		!strings.Contains(discovered.Body.String(), `"absolute_expires_at":"`) {
		t.Fatalf("staged discovery = %s", discovered.Body.String())
	}
	if discovered.Header().Get("Mia-Session-Idle-Expires-At") != login.Header().Get("Mia-Session-Idle-Expires-At") ||
		discovered.Header().Get("Mia-Session-Absolute-Expires-At") != login.Header().Get("Mia-Session-Absolute-Expires-At") {
		t.Fatal("discovery changed the login deadlines")
	}
	if hasSession(server, discovered) {
		t.Fatal("session discovery refreshed authentication")
	}

	if _, err := database.Exec("UPDATE users SET must_change_password = 0 WHERE id = ?", account.ID); err != nil {
		t.Fatal(err)
	}
	stale := serve(t, server, http.MethodGet, "/api/v1/auth/session", "", cookies, "")
	if !strings.Contains(stale.Body.String(), `"stage":"anonymous"`) || !hasClearedSession(server, stale) {
		t.Fatalf("stale discovery = %d %s", stale.Code, stale.Body.String())
	}
}

func TestMFADiscoveryReturnsOnlyOpaqueChallenge(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, false)
	const challengeID = "mfc_00000000-0000-4000-8000-000000000001"
	server.Echo.GET("/issue-mfa-discovery", func(c *echo.Context) error {
		return server.StartMFASession(c, account.ID, account.SecurityGeneration, challengeID, time.Now())
	})
	issued := httptest.NewRecorder()
	server.Echo.ServeHTTP(issued, httptest.NewRequest(http.MethodGet, "http://mia.test/issue-mfa-discovery", nil))
	discovered := serve(t, server, http.MethodGet, "/api/v1/auth/session", "", sessionCookies(t, server, issued), "")
	if stage(t, discovered.Body.Bytes()) != "mfa" ||
		!strings.Contains(discovered.Body.String(), `"mfa_challenge_id":"`+challengeID+`"`) ||
		strings.Contains(discovered.Body.String(), "destination") || strings.Contains(discovered.Body.String(), "secret") {
		t.Fatalf("MFA discovery = %s", discovered.Body.String())
	}
}

func TestLogoutReorderingUsesReducedStatelessGuarantee(t *testing.T) {
	server, database := testServer(t)
	createAccount(t, database, false)
	csrf := csrfToken(t, server)
	firstLogin := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil,
		loginBody("student", "correct horse battery"))
	firstCookies, firstCSRF := authCookies(t, server, firstLogin)
	secondLogin := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil,
		loginBody("student", "correct horse battery"))
	secondCookies, _ := authCookies(t, server, secondLogin)

	late := serve(t, server, http.MethodPost, "/api/v1/auth/session-continuations", firstCSRF, firstCookies, "")
	lateSession := sessionCookie(t, server, late)
	delayedTransition := serve(t, server, http.MethodPost, "/api/v1/auth/login", firstCSRF, firstCookies,
		loginBody("student", "correct horse battery"))
	delayedTransitionCookies, _ := authCookies(t, server, delayedTransition)
	logout := serve(t, server, http.MethodPost, "/api/v1/auth/logout", firstCSRF, firstCookies, "")
	newMarker := responseCookie(logout, server.BrowserCookieName())
	if newMarker == nil {
		t.Fatal("logout omitted rotated browser generation")
	}
	staleCookies := []*http.Cookie{lateSession, newMarker}
	stale := serve(t, server, http.MethodGet, "/api/v1/auth/session", "", staleCookies, "")
	if !strings.Contains(stale.Body.String(), `"stage":"anonymous"`) {
		t.Fatalf("late session survived logout: %s", stale.Body.String())
	}
	reconciled := serve(t, server, http.MethodGet, "/api/v1/auth/session", "", delayedTransitionCookies, "")
	if stage(t, reconciled.Body.Bytes()) != "authenticated" {
		t.Fatalf("delayed authentication transition was not reconciled: %s", reconciled.Body.String())
	}
	otherBrowser := serve(t, server, http.MethodGet, "/api/v1/auth/session", "", secondCookies, "")
	if stage(t, otherBrowser.Body.Bytes()) != "authenticated" {
		t.Fatalf("other browser invalidated: %s", otherBrowser.Body.String())
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
			session, csrf := authCookies(t, server, login)
			if err := mutate(database, account.ID); err != nil {
				t.Fatal(err)
			}
			response := serve(t, server, http.MethodPost, "/api/v1/auth/logout", csrf, session, "")
			assertCode(t, response, http.StatusUnauthorized, "auth_unauthenticated")
			if !hasClearedSession(server, response) {
				t.Fatal("invalidated cookie was not cleared")
			}
		})
	}
}

func TestStaffPasswordRecoveryAndReset(t *testing.T) {
	mailer := newMailRecorder(nil)
	server, database := testServerWithMailer(t, mailer)
	account := createStaffAccount(t, database)
	csrf := csrfToken(t, server)
	response := serve(t, server, http.MethodPost, "/api/v1/auth/password-recovery-requests", csrf, nil, recoveryBody("staff"))
	if response.Code != http.StatusNoContent {
		t.Fatalf("recovery response=%d", response.Code)
	}
	link := mailer.wait(t)
	if strings.Contains(link, "?") {
		t.Fatalf("recovery link leaks a query component: %q", link)
	}
	token := strings.TrimPrefix(link, "https://mia.test/password-reset#token=")
	digest := sha256.Sum256([]byte(token))
	var stored int
	if err := database.QueryRow("SELECT COUNT(*) FROM password_reset_challenges WHERE token_digest = ?", digest[:]).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatalf("digest-stored challenges=%d", stored)
	}
	unknown := serve(t, server, http.MethodPost, "/api/v1/auth/password-recovery-requests", csrf, nil, recoveryBody("unknown"))
	if unknown.Code != http.StatusNoContent || challengeCount(t, database) != 1 {
		t.Fatalf("hidden recovery response=%d challenges=%d", unknown.Code, challengeCount(t, database))
	}
	reset := resetBody(token, "new correct horse battery", "new correct horse battery")
	response = serve(t, server, http.MethodPost, "/api/v1/auth/password-resets", csrf, nil, reset)
	if response.Code != http.StatusNoContent {
		t.Fatalf("reset response=%d body=%s", response.Code, response.Body.String())
	}
	var remaining int
	if err := database.QueryRow("SELECT COUNT(*) FROM password_reset_challenges WHERE user_id = ? AND consumed_at IS NULL", account.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("unconsumed reset challenges=%d", remaining)
	}
	assertAuditCount(t, database, "auth.password_recovery.requested", 1)
	assertAuditCount(t, database, "auth.password.reset", 1)
	var sensitive int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE metadata <> '{}' OR metadata LIKE '%'||?||'%' OR metadata LIKE '%new correct horse battery%'", token).Scan(&sensitive); err != nil {
		t.Fatal(err)
	}
	if sensitive != 0 {
		t.Fatal("audit retained recovery token or password content")
	}
	replay := serve(t, server, http.MethodPost, "/api/v1/auth/password-resets", csrf, nil, reset)
	assertCode(t, replay, http.StatusUnprocessableEntity, "auth_invalid_reset_token")
}

func TestPasswordRecoveryHiddenStatesCreateNoChallenge(t *testing.T) {
	mailer := newMailRecorder(nil)
	server, database := testServerWithMailer(t, mailer)
	createAccount(t, database, false)
	staff := createStaffAccount(t, database)
	if _, err := database.Exec("UPDATE users SET is_banned = 1 WHERE id = ?", staff.ID); err != nil {
		t.Fatal(err)
	}
	csrf := csrfToken(t, server)
	for _, username := range []string{"student", "staff", "bad name!"} {
		response := serve(t, server, http.MethodPost, "/api/v1/auth/password-recovery-requests", csrf, nil, recoveryBody(username))
		if response.Code != http.StatusNoContent || response.Header().Get("Retry-After") != "" {
			t.Fatalf("hidden recovery for %q: status=%d", username, response.Code)
		}
	}
	if challengeCount(t, database) != 0 || mailer.count() != 0 {
		t.Fatalf("hidden states created challenges=%d deliveries=%d", challengeCount(t, database), mailer.count())
	}
}

func TestPasswordRecoveryThrottleStaysHiddenAndIsAuditedOnce(t *testing.T) {
	mailer := newMailRecorder(nil)
	server, database := testServerWithMailer(t, mailer)
	createStaffAccount(t, database)
	csrf := csrfToken(t, server)
	for range 4 {
		response := serve(t, server, http.MethodPost, "/api/v1/auth/password-recovery-requests", csrf, nil, recoveryBody("staff"))
		if response.Code != http.StatusNoContent || response.Header().Get("Retry-After") != "" {
			t.Fatalf("throttled recovery: status=%d retry-after=%q", response.Code, response.Header().Get("Retry-After"))
		}
	}
	if challengeCount(t, database) != 3 {
		t.Fatalf("challenges after throttle=%d", challengeCount(t, database))
	}
	assertAuditCount(t, database, "auth.password_recovery.throttled", 1)
	for range 3 {
		mailer.wait(t)
	}
}

func TestPasswordResetValidationAndLifecycle(t *testing.T) {
	mailer := newMailRecorder(nil)
	server, database := testServerWithMailer(t, mailer)
	createStaffAccount(t, database)
	csrf := csrfToken(t, server)
	for range 2 {
		response := serve(t, server, http.MethodPost, "/api/v1/auth/password-recovery-requests", csrf, nil, recoveryBody("staff"))
		if response.Code != http.StatusNoContent {
			t.Fatalf("recovery response=%d", response.Code)
		}
		mailer.wait(t)
	}
	links := mailer.snapshot()
	first := strings.TrimPrefix(links[0], "https://mia.test/password-reset#token=")
	second := strings.TrimPrefix(links[1], "https://mia.test/password-reset#token=")
	if challengeCount(t, database) != 2 {
		t.Fatalf("live challenges=%d", challengeCount(t, database))
	}
	tooShort := serve(t, server, http.MethodPost, "/api/v1/auth/password-resets", csrf, nil, resetBody(second, "short", "short"))
	assertCode(t, tooShort, http.StatusUnprocessableEntity, "auth_invalid_password")
	mismatch := serve(t, server, http.MethodPost, "/api/v1/auth/password-resets", csrf, nil, resetBody(second, "a long enough password", "a different long password"))
	assertCode(t, mismatch, http.StatusUnprocessableEntity, "auth_invalid_password")
	uppercase := serve(t, server, http.MethodPost, "/api/v1/auth/password-resets", csrf, nil, resetBody(strings.ToUpper(second), "new correct horse battery", "new correct horse battery"))
	assertCode(t, uppercase, http.StatusUnprocessableEntity, "auth_invalid_reset_token")
	firstDigest := sha256.Sum256([]byte(first))
	if _, err := database.Exec("UPDATE password_reset_challenges SET expires_at = '2000-01-01T00:00:00.000000Z' WHERE token_digest = ?", firstDigest[:]); err != nil {
		t.Fatal(err)
	}
	expired := serve(t, server, http.MethodPost, "/api/v1/auth/password-resets", csrf, nil, resetBody(first, "new correct horse battery", "new correct horse battery"))
	assertCode(t, expired, http.StatusUnprocessableEntity, "auth_invalid_reset_token")
	success := serve(t, server, http.MethodPost, "/api/v1/auth/password-resets", csrf, nil, resetBody(second, "new correct horse battery", "new correct horse battery"))
	if success.Code != http.StatusNoContent {
		t.Fatalf("reset after failed validations=%d body=%s", success.Code, success.Body.String())
	}
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("staff", "new correct horse battery"))
	assertCode(t, login, http.StatusOK, "")
}

func TestPasswordResetRejectsAccountBannedAfterIssue(t *testing.T) {
	mailer := newMailRecorder(nil)
	server, database := testServerWithMailer(t, mailer)
	staff := createStaffAccount(t, database)
	csrf := csrfToken(t, server)
	response := serve(t, server, http.MethodPost, "/api/v1/auth/password-recovery-requests", csrf, nil, recoveryBody("staff"))
	if response.Code != http.StatusNoContent {
		t.Fatalf("recovery response=%d", response.Code)
	}
	link := mailer.wait(t)
	token := strings.TrimPrefix(link, "https://mia.test/password-reset#token=")
	if _, err := database.Exec("UPDATE users SET is_banned = 1 WHERE id = ?", staff.ID); err != nil {
		t.Fatal(err)
	}
	var hashBefore string
	if err := database.QueryRow("SELECT password_hash FROM users WHERE id = ?", staff.ID).Scan(&hashBefore); err != nil {
		t.Fatal(err)
	}
	banned := serve(t, server, http.MethodPost, "/api/v1/auth/password-resets", csrf, nil, resetBody(token, "new correct horse battery", "new correct horse battery"))
	assertCode(t, banned, http.StatusUnprocessableEntity, "auth_invalid_reset_token")
	var hashAfter string
	if err := database.QueryRow("SELECT password_hash FROM users WHERE id = ?", staff.ID).Scan(&hashAfter); err != nil {
		t.Fatal(err)
	}
	if hashAfter != hashBefore {
		t.Fatal("banned account password was reset")
	}
}

func TestPasswordResetThrottleAuditedOnce(t *testing.T) {
	server, database := testServer(t)
	csrf := csrfToken(t, server)
	const unknownToken = "3f2a1b4c-9d8e-4f6a-8b7c-1d2e3f4a5b6c"
	for range 6 {
		response := serve(t, server, http.MethodPost, "/api/v1/auth/password-resets", csrf, nil, resetBody(unknownToken, "new correct horse battery", "new correct horse battery"))
		assertCode(t, response, http.StatusUnprocessableEntity, "auth_invalid_reset_token")
	}
	assertAuditCount(t, database, "auth.password_reset.throttled", 1)
}

func TestConcurrentResetProducesOneSuccess(t *testing.T) {
	mailer := newMailRecorder(nil)
	server, database := testServerWithMailer(t, mailer)
	createStaffAccount(t, database)
	csrf := csrfToken(t, server)
	response := serve(t, server, http.MethodPost, "/api/v1/auth/password-recovery-requests", csrf, nil, recoveryBody("staff"))
	if response.Code != http.StatusNoContent {
		t.Fatalf("recovery response=%d", response.Code)
	}
	token := strings.TrimPrefix(mailer.wait(t), "https://mia.test/password-reset#token=")
	reset := resetBody(token, "new correct horse battery", "new correct horse battery")
	codes := make(chan int, 2)
	for range 2 {
		go func() {
			codes <- serve(t, server, http.MethodPost, "/api/v1/auth/password-resets", csrf, nil, reset).Code
		}()
	}
	first, second := <-codes, <-codes
	if min(first, second) != http.StatusNoContent || max(first, second) != http.StatusUnprocessableEntity {
		t.Fatalf("concurrent reset codes=%d,%d", first, second)
	}
}

func TestTOTPEnrollmentLoginAndStepReplay(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, server, login)
	enrollment := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments", csrf, session, `{"data":{"type":"mfa-enrollments","attributes":{"method":"totp"}}}`)
	if enrollment.Code != http.StatusCreated {
		t.Fatalf("enrollment status=%d body=%s", enrollment.Code, enrollment.Body.String())
	}
	var result struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(enrollment.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var secret []byte
	if err := database.QueryRow("SELECT totp_secret FROM mfa_enrollments WHERE id = ?", result.Data.ID).Scan(&secret); err != nil {
		t.Fatal(err)
	}
	code := totpCode(secret, time.Now().Unix()/30)
	verified := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments/"+result.Data.ID+"/verifications", csrf, session, `{"data":{"type":"mfa-enrollment-verifications","attributes":{"code":"`+code+`"}}}`)
	if verified.Code != http.StatusOK {
		t.Fatalf("verification status=%d body=%s", verified.Code, verified.Body.String())
	}
	login = serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	if stage(t, login.Body.Bytes()) != "mfa" {
		t.Fatalf("stage=%q", stage(t, login.Body.Bytes()))
	}
	mfaSession, csrf := authCookies(t, server, login)
	var challenge struct {
		Data struct {
			Attributes struct {
				Challenge string `json:"mfa_challenge_id"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &challenge); err != nil {
		t.Fatal(err)
	}
	// Enrollment claims the accepted step, so it cannot immediately authenticate
	// a login challenge for the same factor.
	replayed := serve(t, server, http.MethodPost, "/api/v1/auth/mfa-challenges/"+challenge.Data.Attributes.Challenge+"/verifications", csrf, mfaSession, `{"data":{"type":"mfa-verifications","attributes":{"code":"`+code+`"}}}`)
	assertCode(t, replayed, http.StatusForbidden, "auth_mfa_step_used")
	_ = account
}

func TestManagementProofsUseUniqueOpaqueResourceIDs(t *testing.T) {
	server, database := testServer(t)
	createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, server, login)
	enrollment := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments", csrf, session, `{"data":{"type":"mfa-enrollments","attributes":{"method":"totp"}}}`)
	if enrollment.Code != http.StatusCreated {
		t.Fatalf("enrollment status=%d body=%s", enrollment.Code, enrollment.Body.String())
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(enrollment.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	var secret []byte
	if err := database.QueryRow("SELECT totp_secret FROM mfa_enrollments WHERE id = ?", created.Data.ID).Scan(&secret); err != nil {
		t.Fatal(err)
	}
	verified := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments/"+created.Data.ID+"/verifications", csrf, session, `{"data":{"type":"mfa-enrollment-verifications","attributes":{"code":"`+totpCode(secret, time.Now().Unix()/30)+`"}}}`)
	if verified.Code != http.StatusOK {
		t.Fatalf("verification status=%d body=%s", verified.Code, verified.Body.String())
	}
	var recovery struct {
		Data struct {
			Attributes struct {
				RecoveryCodes []string `json:"recovery_codes"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(verified.Body.Bytes(), &recovery); err != nil {
		t.Fatal(err)
	}
	if len(recovery.Data.Attributes.RecoveryCodes) < 2 {
		t.Fatalf("recovery codes = %d", len(recovery.Data.Attributes.RecoveryCodes))
	}
	seen := map[string]bool{}
	for _, code := range recovery.Data.Attributes.RecoveryCodes[:2] {
		response := serve(t, server, http.MethodPost, "/api/v1/auth/mfa-management-proofs", csrf, session, `{"data":{"type":"mfa-management-proofs","attributes":{"password":"correct horse battery","code":"`+code+`"}}}`)
		if response.Code != http.StatusCreated {
			t.Fatalf("proof status=%d body=%s", response.Code, response.Body.String())
		}
		document := conformance.Document(t, response)
		proof := conformance.Resource(t, document["data"], "mfa-management-proofs")
		id, ok := proof["id"].(string)
		if !ok {
			t.Fatalf("proof id is not a string: %v", proof["id"])
		}
		attributes, ok := proof["attributes"].(map[string]any)
		if !ok {
			t.Fatalf("proof attributes are not an object: %v", proof["attributes"])
		}
		token, ok := attributes["proof"].(string)
		if !ok {
			t.Fatalf("proof token attribute is not a string")
		}
		if id == "mfp" || token == "" || id == token || strings.Contains(id, token) {
			t.Fatalf("proof resource id %q is constant or derived from the secret", id)
		}
		digest := sha256.Sum256([]byte(token))
		var storedID string
		if err := database.QueryRow("SELECT id FROM mfa_management_proofs WHERE token_digest = ?", digest[:]).Scan(&storedID); err != nil {
			t.Fatal(err)
		}
		if storedID != id {
			t.Fatalf("resource id %q does not match persisted id %q", id, storedID)
		}
		if seen[id] {
			t.Fatalf("duplicate proof resource id %q", id)
		}
		seen[id] = true
	}
}

func TestMFAChallengeFailuresPersistAndInvalidateAtFive(t *testing.T) {
	_, database := testServer(t)
	account := createAccount(t, database, false)
	secret := []byte("12345678901234567890")
	challengeID := "mfc_" + uuid.NewString()
	now := instant(time.Now())
	if _, err := database.Exec(`INSERT INTO mfa_factors (id, user_id, method, totp_secret, created_at)
		VALUES (?, ?, 'totp', ?, ?)`, "mff_"+uuid.NewString(), account.ID, secret, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO mfa_challenges (id, user_id, method, expires_at, created_at) VALUES (?, ?, 'totp', ?, ?)`, challengeID, account.ID, instant(time.Now().Add(time.Minute)), now); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		code, _, err := verifyLiveChallenge(context.Background(), database, account.ID, challengeID, "000000")
		if err != nil || code != httpserver.CodeInvalidMFACode {
			t.Fatalf("attempt %d code=%q err=%v", attempt, code, err)
		}
	}
	var remaining, failures int
	if err := database.QueryRow("SELECT COUNT(*), COALESCE(MAX(failures), 0) FROM mfa_challenges WHERE id = ?", challengeID).Scan(&remaining, &failures); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 || failures != 0 {
		t.Fatalf("remaining challenge/failures = %d/%d", remaining, failures)
	}
	assertAuditCount(t, database, "auth.mfa.challenge.failed", 5)
}

func TestInvalidRecoveryCodeCountsAsChallengeFailure(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, false)
	challengeID := "mfc_" + uuid.NewString()
	if _, err := database.Exec(`INSERT INTO mfa_factors (id, user_id, method, totp_secret, created_at)
		VALUES (?, ?, 'totp', ?, ?)`, "mff_"+uuid.NewString(), account.ID,
		[]byte("12345678901234567890"), instant(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO mfa_challenges (id, user_id, method, expires_at, created_at) VALUES (?, ?, 'totp', ?, ?)`, challengeID, account.ID, instant(time.Now().Add(time.Minute)), instant(time.Now())); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/", nil)
	recorder := httptest.NewRecorder()
	context := server.Echo.NewContext(request, recorder)
	if err := server.StartMFASession(context, account.ID, account.SecurityGeneration, challengeID, time.Now()); err != nil {
		t.Fatal(err)
	}
	response := serve(t, server, http.MethodPost, "/api/v1/auth/mfa-challenges/"+challengeID+"/recovery-code-consumptions", csrfToken(t, server), sessionCookies(t, server, recorder), `{"data":{"type":"mfa-recovery-code-consumptions","attributes":{"code":"invalid"}}}`)
	assertCode(t, response, http.StatusUnprocessableEntity, "auth_invalid_recovery_code")
	var failures int
	if err := database.QueryRow("SELECT failures FROM mfa_challenges WHERE id = ?", challengeID).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 1 {
		t.Fatalf("recovery-code failures=%d", failures)
	}
}

func TestMFAVerificationPrecedesPasswordChange(t *testing.T) {
	_, database := testServer(t)
	account := createAccount(t, database, true)
	secret := []byte("12345678901234567890")
	challengeID := "mfc_" + uuid.NewString()
	if _, err := database.Exec(`INSERT INTO mfa_factors (id, user_id, method, totp_secret, created_at)
		VALUES (?, ?, 'totp', ?, ?)`, "mff_"+uuid.NewString(), account.ID, secret, instant(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO mfa_challenges (id, user_id, method, expires_at, created_at) VALUES (?, ?, 'totp', ?, ?)`, challengeID, account.ID, instant(time.Now().Add(time.Minute)), instant(time.Now())); err != nil {
		t.Fatal(err)
	}
	code, transition, err := verifyLiveChallenge(context.Background(), database, account.ID, challengeID, totpCode(secret, time.Now().Unix()/30))
	if err != nil || code != "" || transition != "password-change" {
		t.Fatalf("verification code=%q transition=%q err=%v", code, transition, err)
	}
}

func TestSMSEnrollmentDeliversAndVerifiesOneCode(t *testing.T) {
	sender := &smsRecorder{}
	server, database := testServerWithSMS(t, sender)
	account := createAccount(t, database, false)
	if _, err := database.Exec("UPDATE users SET mobile = ?, mobile_verified_at = ? WHERE id = ?", "+49123456789", instant(time.Now()), account.ID); err != nil {
		t.Fatal(err)
	}
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, server, login)
	enrollment := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments", csrf, session, `{"data":{"type":"mfa-enrollments","attributes":{"method":"sms"}}}`)
	if enrollment.Code != http.StatusCreated {
		t.Fatalf("SMS enrollment=%d %s", enrollment.Code, enrollment.Body.String())
	}
	var result struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(enrollment.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	code := sender.last(t)
	verified := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments/"+result.Data.ID+"/verifications", csrf, session, `{"data":{"type":"mfa-enrollment-verifications","attributes":{"code":"`+code+`"}}}`)
	if verified.Code != http.StatusOK {
		t.Fatalf("SMS verification=%d %s", verified.Code, verified.Body.String())
	}
	var method, destination string
	if err := database.QueryRow("SELECT method, sms_destination FROM mfa_factors WHERE user_id = ?", account.ID).Scan(&method, &destination); err != nil {
		t.Fatal(err)
	}
	if method != "sms" || destination != "+49123456789" {
		t.Fatalf("SMS factor=%q/%q", method, destination)
	}
}

func TestSMSRateLimitedRoutesReturnRetryAfter(t *testing.T) {
	t.Run("login challenge creation", func(t *testing.T) {
		server, database := testServerWithSMS(t, &smsRecorder{})
		account := createAccount(t, database, false)
		destination := "+4915111111111"
		if _, err := database.Exec(`INSERT INTO mfa_factors
			(id, user_id, method, sms_destination, created_at) VALUES (?, ?, 'sms', ?, ?)`,
			"mff_"+uuid.NewString(), account.ID, destination, instant(time.Now())); err != nil {
			t.Fatal(err)
		}
		insertCurrentSMSAttempt(t, database, account.ID, destination)
		response := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrfToken(t, server), nil,
			loginBody("student", "correct horse battery"))
		assertSMSRetryAfter(t, response)
	})

	t.Run("login challenge resend", func(t *testing.T) {
		server, database := testServerWithSMS(t, &smsRecorder{})
		account := createAccount(t, database, false)
		destination := "+4915222222222"
		challengeID := "mfc_" + uuid.NewString()
		if _, err := database.Exec(`INSERT INTO mfa_factors
			(id, user_id, method, sms_destination, created_at) VALUES (?, ?, 'sms', ?, ?)`,
			"mff_"+uuid.NewString(), account.ID, destination, instant(time.Now())); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO mfa_challenges
			(id, user_id, method, sms_code, expires_at, created_at) VALUES (?, ?, 'sms', '123456', ?, ?)`,
			challengeID, account.ID, instant(time.Now().Add(30*time.Minute)), instant(time.Now())); err != nil {
			t.Fatal(err)
		}
		insertCurrentSMSAttempt(t, database, account.ID, destination)
		request := httptest.NewRequest(http.MethodPost, "http://mia.test/", nil)
		recorder := httptest.NewRecorder()
		context := server.Echo.NewContext(request, recorder)
		if err := server.StartMFASession(context, account.ID, account.SecurityGeneration, challengeID, time.Now()); err != nil {
			t.Fatal(err)
		}
		response := serve(t, server, http.MethodPost, "/api/v1/auth/mfa-challenges/"+challengeID+"/resends",
			csrfToken(t, server), sessionCookies(t, server, recorder), "")
		assertSMSRetryAfter(t, response)
	})

	t.Run("enrollment creation", func(t *testing.T) {
		server, database := testServerWithSMS(t, &smsRecorder{})
		account := createAccount(t, database, false)
		destination := "+4915333333333"
		if _, err := database.Exec("UPDATE users SET mobile = ?, mobile_verified_at = ? WHERE id = ?", destination,
			instant(time.Now()), account.ID); err != nil {
			t.Fatal(err)
		}
		csrf := csrfToken(t, server)
		login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil,
			loginBody("student", "correct horse battery"))
		cookies, csrf := authCookies(t, server, login)
		insertCurrentSMSAttempt(t, database, account.ID, destination)
		response := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments", csrf, cookies,
			`{"data":{"type":"mfa-enrollments","attributes":{"method":"sms"}}}`)
		assertSMSRetryAfter(t, response)
	})

	t.Run("enrollment resend", func(t *testing.T) {
		server, database := testServerWithSMS(t, &smsRecorder{})
		account := createAccount(t, database, false)
		destination := "+4915444444444"
		csrf := csrfToken(t, server)
		login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil,
			loginBody("student", "correct horse battery"))
		cookies, csrf := authCookies(t, server, login)
		enrollmentID := "mfe_" + uuid.NewString()
		if _, err := database.Exec(`INSERT INTO mfa_enrollments
			(id, user_id, method, sms_destination, sms_code, expires_at, created_at)
			VALUES (?, ?, 'sms', ?, '123456', ?, ?)`, enrollmentID, account.ID, destination,
			instant(time.Now().Add(30*time.Minute)), instant(time.Now())); err != nil {
			t.Fatal(err)
		}
		insertCurrentSMSAttempt(t, database, account.ID, destination)
		response := serve(t, server, http.MethodPost,
			"/api/v1/users/me/mfa-enrollments/"+enrollmentID+"/resends", csrf, cookies, "")
		assertSMSRetryAfter(t, response)
	})
}

func insertCurrentSMSAttempt(t *testing.T, database *sql.DB, accountID, destination string) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO sms_delivery_attempts (id, user_id, destination, created_at)
		VALUES (?, ?, ?, ?)`, "sms_"+uuid.NewString(), accountID, destination, instant(time.Now())); err != nil {
		t.Fatal(err)
	}
}

func assertSMSRetryAfter(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	assertCode(t, response, http.StatusTooManyRequests, "rate_limited")
	if response.Header().Get("Retry-After") == "" {
		t.Fatal("SMS rate limit omitted Retry-After")
	}
}

type smsRecorder struct {
	mu    sync.Mutex
	codes []string
}

func (recorder *smsRecorder) Send(_ context.Context, _ string, code string) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.codes = append(recorder.codes, code)
	return nil
}

func (recorder *smsRecorder) last(t *testing.T) string {
	t.Helper()
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.codes) == 0 {
		t.Fatal("SMS sender received no code")
	}
	return recorder.codes[len(recorder.codes)-1]
}

type mailRecorder struct {
	mu        sync.Mutex
	links     []string
	delivered chan struct{}
	err       error
}

func newMailRecorder(err error) *mailRecorder {
	return &mailRecorder{delivered: make(chan struct{}, 16), err: err}
}

func (recorder *mailRecorder) SendPasswordRecovery(_ context.Context, _, link string) error {
	recorder.mu.Lock()
	recorder.links = append(recorder.links, link)
	recorder.mu.Unlock()
	recorder.delivered <- struct{}{}
	return recorder.err
}

func (recorder *mailRecorder) wait(t *testing.T) string {
	t.Helper()
	select {
	case <-recorder.delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery delivery did not happen")
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.links[len(recorder.links)-1]
}

func (recorder *mailRecorder) count() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.links)
}

func (recorder *mailRecorder) snapshot() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.links...)
}

func recoveryBody(username string) string {
	return `{"data":{"type":"password-recovery-requests","attributes":{"username":"` + username + `"}}}`
}

func resetBody(token, password, confirmation string) string {
	return `{"data":{"type":"password-resets","attributes":{"token":"` + token + `","password":"` + password + `","password_confirmation":"` + confirmation + `"}}}`
}

func challengeCount(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM password_reset_challenges").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertAuditCount(t *testing.T, database *sql.DB, action string, expected int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = ?", action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("audit rows for %s = %d, expected %d", action, count, expected)
	}
}

func TestMFAStateIsAuthoritativeAndSecretFree(t *testing.T) {
	server, database := testServerWithSMS(t, &smsRecorder{})
	account := createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil,
		loginBody("student", "correct horse battery"))
	cookies, _ := authCookies(t, server, login)

	none := serve(t, server, http.MethodGet, "/api/v1/users/me/mfa", "", cookies, "")
	if none.Code != http.StatusOK || !strings.Contains(none.Body.String(), `"enroll":true`) {
		t.Fatalf("empty MFA state status=%d body=%s", none.Code, none.Body.String())
	}
	factorID := "mff_" + uuid.NewString()
	enrollmentID := "mfe_" + uuid.NewString()
	secret := "overview-must-not-return-this-secret"
	destination := "+4915112345678"
	if _, err := database.Exec(`INSERT INTO mfa_factors
		(id, user_id, method, sms_destination, created_at) VALUES (?, ?, 'sms', ?, ?)`, factorID, account.ID,
		destination, instant(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO mfa_enrollments
		(id, user_id, method, sms_destination, sms_code, proof_digest, expires_at, created_at)
		VALUES (?, ?, 'sms', ?, ?, ?, ?, ?)`, enrollmentID, account.ID, destination, secret, []byte(secret),
		instant(time.Now().Add(30*time.Minute)), instant(time.Now())); err != nil {
		t.Fatal(err)
	}

	response := serve(t, server, http.MethodGet, "/api/v1/users/me/mfa", "", cookies, "")
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, factorID) || !strings.Contains(body, enrollmentID) ||
		!strings.Contains(body, `"replaces_factor_id":"`+factorID+`"`) || !strings.Contains(body, `"sms_resend":true`) {
		t.Fatalf("MFA state status=%d body=%s", response.Code, body)
	}
	for _, forbidden := range []string{secret, destination, "sms_destination", "proof_digest", "sms_code"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("MFA state exposed %q: %s", forbidden, body)
		}
	}
	if _, err := database.Exec(`INSERT INTO sms_delivery_attempts
		(id, user_id, destination, created_at) VALUES (?, ?, ?, ?)`, "sms_"+uuid.NewString(), account.ID,
		destination, instant(time.Now())); err != nil {
		t.Fatal(err)
	}
	coolingDown := serve(t, server, http.MethodGet, "/api/v1/users/me/mfa", "", cookies, "")
	if !strings.Contains(coolingDown.Body.String(), `"sms_resend":false`) {
		t.Fatalf("SMS cooldown was not reflected in action eligibility: %s", coolingDown.Body.String())
	}
	if _, err := database.Exec("UPDATE mfa_enrollments SET expires_at = ? WHERE id = ?",
		instant(time.Now().Add(-time.Second)), enrollmentID); err != nil {
		t.Fatal(err)
	}
	expired := serve(t, server, http.MethodGet, "/api/v1/users/me/mfa", "", cookies, "")
	if strings.Contains(expired.Body.String(), enrollmentID) || !strings.Contains(expired.Body.String(), `"replace":true`) {
		t.Fatalf("expired enrollment remained authoritative: %s", expired.Body.String())
	}
}

func TestLostFactorResetAppliesClassSpecificSessionEffects(t *testing.T) {
	server, database := testServer(t)
	student := createAccount(t, database, false)
	studentSupervisor := createNamedStaffAccount(t, database, "student-reset-supervisor", user.Supervisor)
	studentCSRF := csrfToken(t, server)
	studentLogin := serve(t, server, http.MethodPost, "/api/v1/auth/login", studentCSRF, nil,
		loginBody("student", "correct horse battery"))
	studentCookies, _ := authCookies(t, server, studentLogin)
	insertMFAArtifacts(t, database, student.ID)
	before := student.SecurityGeneration
	result, err := ResetLostFactor(context.Background(), database, studentSupervisor.ID, student.ID,
		func(context.Context, miSQLite.Querier, string, string) (bool, error) { return true, nil })
	if err != nil || result.SessionEffect != "invalidated" {
		t.Fatalf("student reset result=%+v err=%v", result, err)
	}
	assertResetState(t, database, student.ID, before+1)
	invalidated := serve(t, server, http.MethodGet, "/api/v1/users/me/mfa", "", studentCookies, "")
	assertCode(t, invalidated, http.StatusUnauthorized, "auth_unauthenticated")

	actor := createNamedStaffAccount(t, database, "reset-admin", user.Administrator)
	target := createNamedStaffAccount(t, database, "reset-target", user.Mentor)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil,
		loginBody("reset-target", "correct horse battery"))
	oldCookies, _ := authCookies(t, server, login)
	insertMFAArtifacts(t, database, target.ID)
	result, err = ResetLostFactor(context.Background(), database, actor.ID, target.ID, nil)
	if err != nil || result.SessionEffect != "invalidated" {
		t.Fatalf("staff reset result=%+v err=%v", result, err)
	}
	assertResetState(t, database, target.ID, target.SecurityGeneration+1)
	invalidated = serve(t, server, http.MethodGet, "/api/v1/users/me/mfa", "", oldCookies, "")
	assertCode(t, invalidated, http.StatusUnauthorized, "auth_unauthenticated")
	if _, err := ResetLostFactor(context.Background(), database, actor.ID, target.ID, nil); !errors.Is(err, ErrMFAResetUnavailable) {
		t.Fatalf("stale reset error=%v", err)
	}
	if _, err := ResetLostFactor(context.Background(), database, actor.ID, actor.ID, nil); !errors.Is(err, ErrMFAResetNotFound) {
		t.Fatalf("self reset error=%v", err)
	}
	mfaStageTarget := createNamedStaffAccount(t, database, "mfa-stage-reset-target", user.Supervisor)
	insertMFAArtifacts(t, database, mfaStageTarget.ID)
	mfaLoginCSRF := csrfToken(t, server)
	mfaLogin := serve(t, server, http.MethodPost, "/api/v1/auth/login", mfaLoginCSRF, nil,
		loginBody("mfa-stage-reset-target", "correct horse battery"))
	mfaCookies, mfaCSRF := authCookies(t, server, mfaLogin)
	if _, err := ResetLostFactor(context.Background(), database, actor.ID, mfaStageTarget.ID, nil); err != nil {
		t.Fatal(err)
	}
	mfaChanged := serve(t, server, http.MethodPost, "/api/v1/auth/password-changes", mfaCSRF, mfaCookies,
		`{"data":{"type":"password-changes","attributes":{"password":"third strong password","password_confirmation":"third strong password"}}}`)
	assertCode(t, mfaChanged, http.StatusForbidden, "auth_password_change_required")
	assertSessionCleared(t, server, mfaChanged)
	assertResetState(t, database, mfaStageTarget.ID, mfaStageTarget.SecurityGeneration+1)

	localTarget := createNamedStaffAccount(t, database, "local-reset-target", user.Administrator)
	insertMFAArtifacts(t, database, localTarget.ID)
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		return ResetMFA(context.Background(), tx, localTarget.ID)
	}); err != nil {
		t.Fatal(err)
	}
	assertResetState(t, database, localTarget.ID, localTarget.SecurityGeneration+1)

	concurrent := createNamedStudentAccount(t, database, "concurrent-reset-student")
	insertMFAArtifacts(t, database, concurrent.ID)
	errorsByCall := make(chan error, 2)
	var resetters sync.WaitGroup
	for range 2 {
		resetters.Add(1)
		go func() {
			defer resetters.Done()
			_, resetErr := ResetLostFactor(context.Background(), database, studentSupervisor.ID, concurrent.ID,
				func(context.Context, miSQLite.Querier, string, string) (bool, error) { return true, nil })
			errorsByCall <- resetErr
		}()
	}
	resetters.Wait()
	close(errorsByCall)
	succeeded, stale := 0, 0
	for resetErr := range errorsByCall {
		if resetErr == nil {
			succeeded++
		} else if errors.Is(resetErr, ErrMFAResetUnavailable) {
			stale++
		} else {
			t.Fatalf("concurrent reset error=%v", resetErr)
		}
	}
	if succeeded != 1 || stale != 1 {
		t.Fatalf("concurrent resets succeeded=%d stale=%d", succeeded, stale)
	}
	assertAuditCount(t, database, "auth.mfa.reset", 4)
}

func TestMFAResetRouteIsCSRFSafeAndExistenceHiding(t *testing.T) {
	server, database := testServer(t)
	createAccount(t, database, false)
	target := createNamedStudentAccount(t, database, "reset-student")
	insertMFAArtifacts(t, database, target.ID)
	RegisterMFARecovery(server, database,
		func(_ context.Context, _ miSQLite.Querier, targetID, actorID string) (bool, error) {
			return targetID == target.ID && actorID != targetID, nil
		})
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil,
		loginBody("student", "correct horse battery"))
	cookies, csrf := authCookies(t, server, login)
	body := `{"data":{"type":"mfa-resets","attributes":{}}}`
	missingCSRF := serve(t, server, http.MethodPost, "/api/v1/users/"+target.ID+"/mfa-resets", "", cookies, body)
	assertCode(t, missingCSRF, http.StatusForbidden, "csrf_invalid")
	hidden := serve(t, server, http.MethodPost, "/api/v1/users/u_missing/mfa-resets", csrf, cookies, body)
	assertCode(t, hidden, http.StatusNotFound, "auth_mfa_reset_not_found")
	success := serve(t, server, http.MethodPost, "/api/v1/users/"+target.ID+"/mfa-resets", csrf, cookies, body)
	if success.Code != http.StatusOK || strings.Contains(success.Body.String(), target.ID) ||
		!strings.Contains(success.Body.String(), `"session_effect":"invalidated"`) {
		t.Fatalf("reset response status=%d body=%s", success.Code, success.Body.String())
	}
}

func TestMFAResetRouteAppliesLayeredRateLimitBeforeMutation(t *testing.T) {
	server, database := testServer(t)
	createAccount(t, database, false)
	target := createNamedStudentAccount(t, database, "rate-limited-reset-student")
	insertMFAArtifacts(t, database, target.ID)
	RegisterMFARecovery(server, database,
		func(_ context.Context, _ miSQLite.Querier, targetID, actorID string) (bool, error) {
			return targetID == target.ID && actorID != targetID, nil
		})
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil,
		loginBody("student", "correct horse battery"))
	cookies, csrf := authCookies(t, server, login)
	body := `{"data":{"type":"mfa-resets","attributes":{}}}`
	for attempt := range 10 {
		response := serve(t, server, http.MethodPost,
			fmt.Sprintf("/api/v1/users/u_missing_%d/mfa-resets", attempt), csrf, cookies, body)
		assertCode(t, response, http.StatusNotFound, "auth_mfa_reset_not_found")
	}
	denied := serve(t, server, http.MethodPost, "/api/v1/users/"+target.ID+"/mfa-resets", csrf, cookies, body)
	assertCode(t, denied, http.StatusTooManyRequests, "rate_limited")
	if denied.Header().Get("Retry-After") == "" {
		t.Fatal("rate-limited MFA reset omitted Retry-After")
	}
	var factors int
	if err := database.QueryRow("SELECT COUNT(*) FROM mfa_factors WHERE user_id = ?", target.ID).Scan(&factors); err != nil {
		t.Fatal(err)
	}
	if factors != 1 {
		t.Fatalf("rate-limited reset mutated factors: %d", factors)
	}
	assertAuditCount(t, database, "auth.mfa.reset.throttled", 1)
}

func insertMFAArtifacts(t *testing.T, database *sql.DB, accountID string) {
	t.Helper()
	now := instant(time.Now())
	if _, err := database.Exec(`INSERT INTO mfa_factors
		(id, user_id, method, totp_secret, created_at) VALUES (?, ?, 'totp', ?, ?)`,
		"mff_"+uuid.NewString(), accountID, make([]byte, 20), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO mfa_enrollments
		(id, user_id, method, totp_secret, expires_at, created_at) VALUES (?, ?, 'totp', ?, ?, ?)`,
		"mfe_"+uuid.NewString(), accountID, make([]byte, 20), instant(time.Now().Add(time.Minute)), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO mfa_challenges
		(id, user_id, method, expires_at, created_at) VALUES (?, ?, 'totp', ?, ?)`,
		"mfc_"+uuid.NewString(), accountID, instant(time.Now().Add(time.Minute)), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO mfa_management_proofs
		(id, user_id, token_digest, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		"mfp_"+uuid.NewString(), accountID, uuid.NewString(), instant(time.Now().Add(time.Minute)), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO mfa_recovery_codes
		(id, user_id, digest, created_at) VALUES (?, ?, ?, ?)`,
		"mrc_"+uuid.NewString(), accountID, uuid.NewString(), now); err != nil {
		t.Fatal(err)
	}
}

func assertResetState(t *testing.T, database *sql.DB, accountID string, generation int64) {
	t.Helper()
	for _, table := range []string{"mfa_factors", "mfa_enrollments", "mfa_challenges", "mfa_management_proofs", "mfa_recovery_codes"} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE user_id = ?", accountID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows=%d err=%v", table, count, err)
		}
	}
	var current int64
	var gate int
	if err := database.QueryRow("SELECT security_generation, must_change_password FROM users WHERE id = ?", accountID).
		Scan(&current, &gate); err != nil || current != generation || gate != 1 {
		t.Fatalf("reset account generation=%d gate=%d err=%v", current, gate, err)
	}
}

func createNamedStaffAccount(t *testing.T, database *sql.DB, username string, role user.Role) user.Account {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{Username: username,
			Email: username + "@example.test", PasswordHash: hash, Language: "en", Country: "DE", TimeZone: "UTC",
			EmailVerified: true, Roles: []user.Role{role}})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return account
}

func createNamedStudentAccount(t *testing.T, database *sql.DB, username string) user.Account {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{Username: username,
			PasswordHash: hash, Language: "en", Country: "DE", TimeZone: "UTC", Roles: []user.Role{user.Student}})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return account
}

func testServer(t *testing.T) (*httpserver.Server, *sql.DB) {
	return testServerWithMailerAndSMS(t, recoveryMailerFunc(func(context.Context, string, string) error { return nil }), sms.Unavailable{}, httpserver.CookiePolicy{})
}

func testServerWithMailer(t *testing.T, mailer recoveryMailer) (*httpserver.Server, *sql.DB) {
	return testServerWithMailerAndSMS(t, mailer, sms.Unavailable{}, httpserver.CookiePolicy{})
}

func testServerWithSMS(t *testing.T, sender sms.Sender) (*httpserver.Server, *sql.DB) {
	return testServerWithMailerAndSMS(t, recoveryMailerFunc(func(context.Context, string, string) error { return nil }), sender, httpserver.CookiePolicy{})
}

func testLocalServer(t *testing.T) (*httpserver.Server, *sql.DB) {
	return testServerWithMailerAndSMS(t, recoveryMailerFunc(func(context.Context, string, string) error { return nil }), sms.Unavailable{}, httpserver.CookiePolicy{SessionName: "mia_session", CSRFName: "mia_csrf"})
}

func testServerWithMailerAndSMS(t *testing.T, mailer recoveryMailer, sender sms.Sender, policy httpserver.CookiePolicy) (*httpserver.Server, *sql.DB) {
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
	server, routes, err := httpserver.New(httpserver.Options{DataDir: directory, DocRoot: docRoot, CookiePolicy: policy})
	if err != nil {
		t.Fatal(err)
	}
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		account, err := user.LoadSecurityState(ctx, database, id)
		if err != nil {
			return httpserver.IdentityState{}, httpserver.ErrIdentityNotFound
		}
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration,
			MustChangePassword: account.MustChangePassword, Banned: account.Banned}, nil
	})
	deliveries := NewDeliveryManager(database, mailer, nil)
	t.Cleanup(deliveries.Close)
	Register(server, routes, database, "https://mia.test", deliveries, sender)
	return server, database
}

func createStaffAccount(t *testing.T, database *sql.DB) user.Account {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{Username: "staff", Email: "staff@example.test", PasswordHash: hash, Language: "en", Country: "DE", TimeZone: "UTC", EmailVerified: true, Roles: []user.Role{user.Administrator}})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return account
}

type recoveryMailerFunc func(context.Context, string, string) error

func (function recoveryMailerFunc) SendPasswordRecovery(ctx context.Context, recipient, link string) error {
	return function(ctx, recipient, link)
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
		if cookie.Name == server.CSRFCookieName() {
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
		request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
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
	request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func loginBody(username, password string) string {
	return `{"data":{"type":"login-attempts","attributes":{"username":"` + username + `","password":"` + password + `"}}}`
}

func assertCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if code != "" {
		conformance.Error(t, response, status, code)
		return
	}
	if response.Code != status {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func authCookies(t *testing.T, server *httpserver.Server, response *httptest.ResponseRecorder) ([]*http.Cookie, string) {
	t.Helper()
	var cookies []*http.Cookie
	var csrf string
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case server.SessionCookieName():
			cookies = append(cookies, cookie)
		case server.BrowserCookieName():
			cookies = append(cookies, cookie)
		case server.CSRFCookieName():
			csrf = cookie.Value
		}
	}
	if len(cookies) != 2 || csrf == "" {
		t.Fatalf("auth cookies count=%d csrf=%q", len(cookies), csrf)
	}
	return cookies, csrf
}

func assertSessionCleared(t *testing.T, server *httpserver.Server, response *httptest.ResponseRecorder) {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.SessionCookieName() && cookie.MaxAge < 0 {
			return
		}
	}
	t.Fatal("response did not clear the invalidated session cookie")
}

func assertLocalCSRFCookie(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "mia_csrf" {
			if cookie.Secure {
				t.Fatal("local CSRF cookie is Secure")
			}
			return
		}
	}
	t.Fatal("local CSRF cookie missing")
}

func hasSession(server *httpserver.Server, response *httptest.ResponseRecorder) bool {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.SessionCookieName() && cookie.MaxAge >= 0 {
			return true
		}
	}
	return false
}

func hasClearedSession(server *httpserver.Server, response *httptest.ResponseRecorder) bool {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.SessionCookieName() && cookie.MaxAge < 0 {
			return true
		}
	}
	return false
}

func responseCookie(response *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func sessionCookie(t *testing.T, server *httpserver.Server, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.SessionCookieName() {
			return cookie
		}
	}
	t.Fatal("response omitted session cookie")
	return nil
}

func csrfCookieValue(t *testing.T, server *httpserver.Server, response *httptest.ResponseRecorder) string {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.CSRFCookieName() {
			return cookie.Value
		}
	}
	t.Fatal("response omitted CSRF cookie")
	return ""
}

func sessionCookies(t *testing.T, server *httpserver.Server, response *httptest.ResponseRecorder) []*http.Cookie {
	t.Helper()
	var cookies []*http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.SessionCookieName() || cookie.Name == server.BrowserCookieName() {
			cookies = append(cookies, cookie)
		}
	}
	if len(cookies) != 2 {
		t.Fatalf("response session cookie count = %d", len(cookies))
	}
	return cookies
}

func replaceSessionCookie(server *httpserver.Server, cookies []*http.Cookie, replacement *http.Cookie) []*http.Cookie {
	result := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == server.SessionCookieName() {
			result = append(result, replacement)
		} else {
			result = append(result, cookie)
		}
	}
	return result
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
