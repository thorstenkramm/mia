package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
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

	blocked := serve(t, server, http.MethodPost, "/api/v1/auth/logout", rotatedCSRF, []*http.Cookie{session}, "")
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

	changed := serve(t, server, http.MethodPost, "/api/v1/auth/password-changes", csrf, []*http.Cookie{session}, `{"data":{"type":"password-changes","attributes":{"password":"a different strong password","password_confirmation":"a different strong password"}}}`)
	assertCode(t, changed, http.StatusOK, "")
	if stage(t, changed.Body.Bytes()) != "authenticated" {
		t.Fatalf("changed stage = %q", stage(t, changed.Body.Bytes()))
	}
	freshSession, _ := authCookies(t, server, changed)
	freshRequest := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/auth/logout", nil)
	freshRequest.AddCookie(freshSession)
	stored, err := server.Sessions.Get(freshRequest, server.SessionCookieName())
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

func TestLocalCookiePolicyAuthTransitions(t *testing.T) {
	server, database := testLocalServer(t)
	createAccount(t, database, true)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	assertLocalCSRFCookie(t, login)
	session, csrf := authCookies(t, server, login)
	changed := serve(t, server, http.MethodPost, "/api/v1/auth/password-changes", csrf, []*http.Cookie{session}, `{"data":{"type":"password-changes","attributes":{"password":"a different strong password","password_confirmation":"a different strong password"}}}`)
	assertLocalCSRFCookie(t, changed)
	session, csrf = authCookies(t, server, changed)
	logout := serve(t, server, http.MethodPost, "/api/v1/auth/logout", csrf, []*http.Cookie{session}, "")
	assertLocalCSRFCookie(t, logout)
}

func TestRestrictedRoutesRequireValidUnexpiredSession(t *testing.T) {
	server, database := testServer(t)
	account := createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, server, login)
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/auth/logout", nil)
	request.AddCookie(session)
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
	blocked := serve(t, server, http.MethodPost, "/api/v1/test", csrf, []*http.Cookie{session}, "")
	assertCode(t, blocked, http.StatusForbidden, "auth_password_change_required")
	changed := serve(t, server, http.MethodPost, "/api/v1/auth/password-changes", csrf, []*http.Cookie{session}, `{"data":{"type":"password-changes","attributes":{"password":"a different strong password","password_confirmation":"a different strong password"}}}`)
	assertCode(t, changed, http.StatusOK, "")

	malformed := serve(t, server, http.MethodPost, "/api/v1/auth/logout", csrf, []*http.Cookie{{Name: server.SessionCookieName(), Value: "malformed"}}, "")
	assertCode(t, malformed, http.StatusUnauthorized, "auth_unauthenticated")
	if !hasClearedSession(server, malformed) {
		t.Fatal("malformed cookie was not cleared")
	}
}

func TestAuthenticatedRefreshesOnlySuccessfulRequests(t *testing.T) {
	server, database := testServer(t)
	createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, server, login)
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/succeeds", nil)
	request.AddCookie(session)
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
	session = sessionCookie(t, server, olderRecorder)
	previousIdle, idleOK := stored.Values["idle_until"].(int64)
	if !idleOK {
		t.Fatal("older session idle deadline missing")
	}
	server.AuthenticatedPOST("/api/v1/succeeds", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	server.AuthenticatedPOST("/api/v1/fails", func(*echo.Context) error { return httpserver.NewError(httpserver.CodeInvalidRequest) })
	success := serve(t, server, http.MethodPost, "/api/v1/succeeds", csrf, []*http.Cookie{session}, "")
	if !hasSession(server, success) {
		t.Fatal("successful authenticated request did not refresh session")
	}
	refreshed := sessionCookie(t, server, success)
	request = httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/succeeds", nil)
	request.AddCookie(refreshed)
	stored, err = server.Sessions.Get(request, server.SessionCookieName())
	refreshedIdle, idleOK := stored.Values["idle_until"].(int64)
	refreshedAbsolute, absoluteOK := stored.Values["expires_at"].(int64)
	if err != nil || !idleOK || !absoluteOK || refreshedIdle <= previousIdle || refreshedIdle > refreshedAbsolute || refreshedAbsolute != absolute {
		t.Fatal("idle refresh did not advance without preserving absolute expiry")
	}
	failure := serve(t, server, http.MethodPost, "/api/v1/fails", csrf, []*http.Cookie{session}, "")
	assertCode(t, failure, http.StatusUnprocessableEntity, "auth_invalid_request")
	if hasSession(server, failure) {
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
			session, csrf := authCookies(t, server, login)
			if err := mutate(database, account.ID); err != nil {
				t.Fatal(err)
			}
			response := serve(t, server, http.MethodPost, "/api/v1/auth/logout", csrf, []*http.Cookie{session}, "")
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
	enrollment := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments", csrf, []*http.Cookie{session}, `{"data":{"type":"mfa-enrollments","attributes":{"method":"totp"}}}`)
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
	verified := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments/"+result.Data.ID+"/verifications", csrf, []*http.Cookie{session}, `{"data":{"type":"mfa-enrollment-verifications","attributes":{"code":"`+code+`"}}}`)
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
	replayed := serve(t, server, http.MethodPost, "/api/v1/auth/mfa-challenges/"+challenge.Data.Attributes.Challenge+"/verifications", csrf, []*http.Cookie{mfaSession}, `{"data":{"type":"mfa-verifications","attributes":{"code":"`+code+`"}}}`)
	assertCode(t, replayed, http.StatusForbidden, "auth_mfa_step_used")
	_ = account
}

func TestManagementProofsUseUniqueOpaqueResourceIDs(t *testing.T) {
	server, database := testServer(t)
	createAccount(t, database, false)
	csrf := csrfToken(t, server)
	login := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, loginBody("student", "correct horse battery"))
	session, csrf := authCookies(t, server, login)
	enrollment := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments", csrf, []*http.Cookie{session}, `{"data":{"type":"mfa-enrollments","attributes":{"method":"totp"}}}`)
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
	verified := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments/"+created.Data.ID+"/verifications", csrf, []*http.Cookie{session}, `{"data":{"type":"mfa-enrollment-verifications","attributes":{"code":"`+totpCode(secret, time.Now().Unix()/30)+`"}}}`)
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
		response := serve(t, server, http.MethodPost, "/api/v1/auth/mfa-management-proofs", csrf, []*http.Cookie{session}, `{"data":{"type":"mfa-management-proofs","attributes":{"password":"correct horse battery","code":"`+code+`"}}}`)
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
	if _, err := database.Exec(`INSERT INTO mfa_factors (user_id, method, totp_secret, created_at) VALUES (?, 'totp', ?, ?)`, account.ID, secret, now); err != nil {
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
	if _, err := database.Exec(`INSERT INTO mfa_factors (user_id, method, totp_secret, created_at) VALUES (?, 'totp', ?, ?)`, account.ID, []byte("12345678901234567890"), instant(time.Now())); err != nil {
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
	response := serve(t, server, http.MethodPost, "/api/v1/auth/mfa-challenges/"+challengeID+"/recovery-code-consumptions", csrfToken(t, server), []*http.Cookie{sessionCookie(t, server, recorder)}, `{"data":{"type":"mfa-recovery-code-consumptions","attributes":{"code":"invalid"}}}`)
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
	if _, err := database.Exec(`INSERT INTO mfa_factors (user_id, method, totp_secret, created_at) VALUES (?, 'totp', ?, ?)`, account.ID, secret, instant(time.Now())); err != nil {
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
	enrollment := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments", csrf, []*http.Cookie{session}, `{"data":{"type":"mfa-enrollments","attributes":{"method":"sms"}}}`)
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
	verified := serve(t, server, http.MethodPost, "/api/v1/users/me/mfa-enrollments/"+result.Data.ID+"/verifications", csrf, []*http.Cookie{session}, `{"data":{"type":"mfa-enrollment-verifications","attributes":{"code":"`+code+`"}}}`)
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
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration, MustChangePassword: account.MustChangePassword, Banned: account.Banned}, nil
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

func authCookies(t *testing.T, server *httpserver.Server, response *httptest.ResponseRecorder) (*http.Cookie, string) {
	t.Helper()
	var session *http.Cookie
	var csrf string
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case server.SessionCookieName():
			session = cookie
		case server.CSRFCookieName():
			csrf = cookie.Value
		}
	}
	if session == nil || csrf == "" {
		t.Fatalf("auth cookies session=%v csrf=%q", session != nil, csrf)
	}
	return session, csrf
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
