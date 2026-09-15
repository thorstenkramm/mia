package invitation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/course"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/httpserver/conformance"
	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/lifecycle"
	"github.com/thorstenkramm/mia/internal/provider/smtp"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestCreateInvitationRequiresAdministratorForAdminRole(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	supervisor := createSupervisorAccount(t, database, "supervisor")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Admin can create admin invitation.
	response := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf, session,
		`{"data":{"type":"invitations","attributes":{"email":"new-admin@example.test","role":"administrator"}}}`)
	assertStatus(t, response, http.StatusCreated)

	// Supervisor cannot create admin invitation.
	csrf2, session2 := loginSession(t, server, "supervisor", "correct horse battery")
	response2 := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf2, session2,
		`{"data":{"type":"invitations","attributes":{"email":"another-admin@example.test","role":"administrator"}}}`)
	assertCode(t, response2, http.StatusForbidden, "invitation_role_unauthorized")

	_ = admin
	_ = supervisor
}

func TestAccountDeletionRemovesInvitationsForEveryRetainedStatus(t *testing.T) {
	_, database := testServer(t)
	administrator := createAdminAccount(t, database, "deletion-admin")
	target := createSupervisorAccount(t, database, "deleted-invitee")
	now := "2026-09-06T00:00:00.000000Z"
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO invitations
			(id, email, email_normalized, role, token_digest, status, inviter_id, created_at, updated_at)
			VALUES ('inv_delete_pending', 'deleted-invitee@example.test', 'deleted-invitee@example.test',
			'mentor', ?, 'pending', ?, ?, ?)`, []any{make([]byte, 32), administrator.ID, now, now}},
		{`INSERT INTO invitations
			(id, email, email_normalized, role, status, inviter_id, created_at, updated_at, accepted_at, accepted_by)
			VALUES ('inv_delete_accepted', 'deleted-invitee@example.test', 'deleted-invitee@example.test',
			'supervisor', 'accepted', ?, ?, ?, ?, ?)`, []any{administrator.ID, now, now, now, target.ID}},
		{`INSERT INTO invitations
			(id, email, email_normalized, role, status, inviter_id, created_at, updated_at, revoked_at, revoked_by)
			VALUES ('inv_delete_revoked', 'deleted-invitee@example.test', 'deleted-invitee@example.test',
			'mentor', 'revoked', ?, ?, ?, ?, ?)`, []any{administrator.ID, now, now, now, administrator.ID}},
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"pending", "accepted", "revoked"} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM invitations WHERE email_normalized = ? AND status = ?",
			"deleted-invitee@example.test", status).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s invitation setup count/error = %d/%v", status, count, err)
		}
	}
	service := NewService(database, nil)
	registry := &lifecycle.Registry{}
	registry.RegisterAccount(service)
	deletion := user.NewDeletionService(database, t.TempDir(), registry, nil)
	if err := deletion.Delete(context.Background(), administrator.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM invitations WHERE email_normalized = ?",
		"deleted-invitee@example.test").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("account deletion retained %d addressed invitations", count)
	}
}

func TestCreateMentorInvitationRequiresSupervisor(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	_ = createSupervisorAccount(t, database, "supervisor")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Admin cannot create mentor invitation.
	response := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf, session,
		`{"data":{"type":"invitations","attributes":{"email":"mentor@example.test","role":"mentor"}}}`)
	assertCode(t, response, http.StatusForbidden, "invitation_role_unauthorized")

	// Supervisor can create mentor invitation.
	csrf2, session2 := loginSession(t, server, "supervisor", "correct horse battery")
	response2 := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf2, session2,
		`{"data":{"type":"invitations","attributes":{"email":"mentor@example.test","role":"mentor"}}}`)
	assertStatus(t, response2, http.StatusCreated)
}

func TestInvitationListPagination(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Create 5 invitations.
	for i := 0; i < 5; i++ {
		response := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf, session,
			`{"data":{"type":"invitations","attributes":{"email":"invite`+string(rune('a'+i))+`@example.test","role":"supervisor"}}}`)
		assertStatus(t, response, http.StatusCreated)
	}

	// List with page limit of 2.
	response := serve(t, server, http.MethodGet, "/api/v1/invitations?page[limit]=2", csrf, session, "")
	assertStatus(t, response, http.StatusOK)
	var result struct {
		Data  []any          `json:"data"`
		Links map[string]any `json:"links"`
		Meta  map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 2 {
		t.Fatalf("expected 2 invitations, got %d", len(result.Data))
	}
	// Verify there's a next link since there are more invitations.
	if result.Links["next"] == nil {
		t.Fatal("expected next link with 5 total invitations and limit 2")
	}
	if result.Meta["has_more"] != true {
		t.Fatal("expected meta.has_more true with 5 total invitations and limit 2")
	}

	_ = admin
}

func TestInvitationPreviewAndAcceptRoutes(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Create an invitation via API first.
	response := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf, session,
		`{"data":{"type":"invitations","attributes":{"email":"newuser@example.test","role":"supervisor"}}}`)
	assertStatus(t, response, http.StatusCreated)

	// Create a new invitation via service to get a fresh token (since we can't reverse the digest).
	inv, token, err := service.Create(context.Background(), CreateInput{
		Email:     "accept-test@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = inv

	// Preview invitation (public route).
	previewCSRF := csrfToken(t, server)
	preview := serve(t, server, http.MethodPost, "/api/v1/invitation-previews", previewCSRF, nil,
		`{"data":{"type":"invitation-previews","attributes":{"token":"`+token+`"}}}`)
	assertStatus(t, preview, http.StatusOK)

	// Accept invitation (public route).
	accept := serve(t, server, http.MethodPost, "/api/v1/invitation-acceptances", previewCSRF, nil,
		`{"data":{"type":"invitation-acceptances","attributes":{"token":"`+token+`","username":"newstaff","password":"a very secure password","password_confirmation":"a very secure password","language":"en","country":"DE","time_zone":"Europe/Berlin"}}}`)
	assertStatus(t, accept, http.StatusCreated)

	// Verify user was created.
	var userCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM users WHERE username = 'newstaff'").Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if userCount != 1 {
		t.Fatalf("expected 1 user, got %d", userCount)
	}
}

func TestInvitationHidesExistenceFromUnauthorized(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	supervisor := createSupervisorAccount(t, database, "supervisor")

	// Admin creates an admin invitation.
	csrf, session := loginSession(t, server, "admin", "correct horse battery")
	response := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf, session,
		`{"data":{"type":"invitations","attributes":{"email":"secret-admin@example.test","role":"administrator"}}}`)
	assertStatus(t, response, http.StatusCreated)
	document := conformance.Document(t, response)
	conformance.Resource(t, document["data"], "invitations")
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// Supervisor tries to get the admin invitation - should get not found.
	csrf2, session2 := loginSession(t, server, "supervisor", "correct horse battery")
	response2 := serve(t, server, http.MethodGet, "/api/v1/invitations/"+created.Data.ID, csrf2, session2, "")
	assertCode(t, response2, http.StatusNotFound, "invitation_not_found")

	_ = admin
	_ = supervisor
}

func TestInvitationTokenRateLimiting(t *testing.T) {
	server, _ := testServer(t)
	csrf := csrfToken(t, server)

	// Make 11 requests with the same token (limit is 10/hour).
	for i := 0; i < 11; i++ {
		response := serve(t, server, http.MethodPost, "/api/v1/invitation-previews", csrf, nil,
			`{"data":{"type":"invitation-previews","attributes":{"token":"invalid-token-for-rate-limit-test"}}}`)
		if i < 10 {
			// First 10 should get invalid invitation (token doesn't exist).
			assertCode(t, response, http.StatusUnprocessableEntity, "invitation_invalid")
		} else {
			// 11th should be rate limited with Retry-After header and 429 status.
			conformance.Error(t, response, http.StatusTooManyRequests, "rate_limited")
			if response.Header().Get("Retry-After") == "" {
				t.Fatal("expected Retry-After header on rate limited request")
			}
		}
	}
}

func TestInvitationCreateAndCommandResourcesRejectClientGeneratedIDs(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "id-admin")
	csrf, session := loginSession(t, server, "id-admin", "correct horse battery")
	for _, testCase := range []struct {
		name, path, body, code string
		cookie                 []*http.Cookie
	}{
		{name: "invitation create", path: "/api/v1/invitations", code: "auth_invalid_request", cookie: session,
			body: `{"data":{"type":"invitations","id":"client-id","attributes":{"email":"id@example.test","role":"supervisor"}}}`},
		{name: "invitation preview", path: "/api/v1/invitation-previews", code: "invitation_invalid",
			body: `{"data":{"type":"invitation-previews","id":"client-id","attributes":{"token":"invalid"}}}`},
		{name: "invitation acceptance", path: "/api/v1/invitation-acceptances", code: "invitation_invalid",
			body: `{"data":{"type":"invitation-acceptances","id":"client-id","attributes":{}}}`},
		{name: "role grant", path: "/api/v1/users/" + admin.ID + "/roles", code: "auth_invalid_request",
			cookie: session, body: `{"data":{"type":"user-roles","id":"client-id","attributes":{"role":"mentor"}}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := serve(t, server, http.MethodPost, testCase.path, csrf, testCase.cookie, testCase.body)
			conformance.Error(t, response, http.StatusUnprocessableEntity, testCase.code)
		})
	}
}

// Public invitation routes must classify protocol failures like every other
// JSON:API route (415/413/400) while keeping domain failures at the generic
// existence-hiding 422 invitation_invalid.
func TestPublicInvitationRoutesClassifyProtocolErrors(t *testing.T) {
	server, _ := testServer(t)
	csrf := csrfToken(t, server)
	for _, path := range []string{"/api/v1/invitation-previews", "/api/v1/invitation-acceptances"} {
		unsupported := serveWithMedia(t, server, path, csrf, "application/json", `{"data":{"type":"x","attributes":{}}}`)
		conformance.Error(t, unsupported, http.StatusUnsupportedMediaType, "unsupported_media_type")
		oversized := serveWithMedia(t, server, path, csrf, "application/vnd.api+json", strings.Repeat("x", 1<<20+1))
		conformance.Error(t, oversized, http.StatusRequestEntityTooLarge, "request_too_large")
		malformed := serveWithMedia(t, server, path, csrf, "application/vnd.api+json", `{"data":`)
		conformance.Error(t, malformed, http.StatusBadRequest, "malformed_request")
	}
	// A valid document with an unusable token keeps the generic domain error.
	domain := serve(t, server, http.MethodPost, "/api/v1/invitation-previews", csrf, nil,
		`{"data":{"type":"invitation-previews","attributes":{"token":"no-such-token"}}}`)
	conformance.Error(t, domain, http.StatusUnprocessableEntity, "invitation_invalid")
}

func serveWithMedia(t *testing.T, server *httpserver.Server, path, csrf, mediaType, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://mia.test"+path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", mediaType)
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func TestInvitationTimestampFormat(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	response := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf, session,
		`{"data":{"type":"invitations","attributes":{"email":"timestamp@example.test","role":"supervisor"}}}`)
	assertStatus(t, response, http.StatusCreated)

	var result struct {
		Data struct {
			Attributes struct {
				CreatedAt string `json:"created_at"`
				UpdatedAt string `json:"updated_at"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}

	// Check that timestamps have exactly 6 fractional digits.
	if !strings.Contains(result.Data.Attributes.CreatedAt, ".") {
		t.Fatalf("created_at missing fractional seconds: %s", result.Data.Attributes.CreatedAt)
	}
	parts := strings.Split(result.Data.Attributes.CreatedAt, ".")
	if len(parts) != 2 || len(parts[1]) != 7 { // 6 digits + 'Z'
		t.Fatalf("created_at has wrong fractional format: %s", result.Data.Attributes.CreatedAt)
	}
}

func TestFaultyInvitationDeletionIsAudited(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create an invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "faulty@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mark it as faulty.
	if err := service.MarkFaulty(context.Background(), inv.ID,
		httpserver.StableCode(httpserver.CodeInvitationDeliveryRejected)); err != nil {
		t.Fatal(err)
	}
	inv, err = service.Get(context.Background(), inv.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Delete the faulty invitation.
	csrf, session := loginSession(t, server, "admin", "correct horse battery")
	response := serveIfMatch(t, server, "/api/v1/invitations/"+inv.ID, csrf, session, ETag(inv))
	assertStatus(t, response, http.StatusOK)
	if !bytes.Contains(response.Body.Bytes(), []byte(`"effect":"deleted"`)) {
		t.Fatalf("DELETE did not identify physical deletion: %s", response.Body.String())
	}

	// Check audit record exists.
	var auditCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.faulty_deleted'").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("expected 1 audit record, got %d", auditCount)
	}
}

func TestAdministratorCanViewAllInvitations(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	supervisor := createSupervisorAccount(t, database, "supervisor")

	// Supervisor creates a mentor invitation.
	csrf1, session1 := loginSession(t, server, "supervisor", "correct horse battery")
	response := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf1, session1,
		`{"data":{"type":"invitations","attributes":{"email":"mentor@example.test","role":"mentor"}}}`)
	assertStatus(t, response, http.StatusCreated)
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// Admin can view the mentor invitation.
	csrf2, session2 := loginSession(t, server, "admin", "correct horse battery")
	response2 := serve(t, server, http.MethodGet, "/api/v1/invitations/"+created.Data.ID, csrf2, session2, "")
	assertStatus(t, response2, http.StatusOK)

	// Admin can see it in the list.
	response3 := serve(t, server, http.MethodGet, "/api/v1/invitations", csrf2, session2, "")
	assertStatus(t, response3, http.StatusOK)
	var list struct {
		Data []any          `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(response3.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 {
		t.Fatalf("expected 1 invitation in admin list, got %d", len(list.Data))
	}
	if list.Meta["has_more"] != false {
		t.Fatal("expected meta.has_more false with a single invitation")
	}

	_ = supervisor
}

func TestNullableInviterIDAfterDeletion(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create an invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "test@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Delete the inviter (simulating ON DELETE SET NULL).
	if _, err := database.Exec("UPDATE invitations SET inviter_id = NULL WHERE id = ?", inv.ID); err != nil {
		t.Fatal(err)
	}

	// Should still be able to load the invitation.
	loaded, err := service.Get(context.Background(), inv.ID, admin.ID)
	if err != nil {
		t.Fatalf("failed to load invitation with null inviter: %v", err)
	}
	if loaded.InviterID != nil {
		t.Fatal("expected null inviter_id")
	}
}

// Test helpers

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
	server, _, err := httpserver.New(httpserver.Options{DataDir: directory, DocRoot: docRoot})
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
	service := NewService(database, nil)
	deliveries := NewDeliveryManager(service, database, successMailer{}, nil)
	t.Cleanup(deliveries.Close)
	Register(server, service, "https://mia.test", deliveries)
	RegisterRoleRoutes(server, database, nil, course.IsSoleSupervisor)
	registerLoginRoute(t, server, database)
	return server, database
}

func registerLoginRoute(t *testing.T, server *httpserver.Server, database *sql.DB) {
	t.Helper()
	server.Echo.POST("/api/v1/auth/login", func(c *echo.Context) error {
		var req struct {
			Data struct {
				Attributes struct {
					Username string `json:"username"`
					Password string `json:"password"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		account, err := user.FindForLogin(c.Request().Context(), database, req.Data.Attributes.Username)
		if err != nil {
			return httpserver.NewError(httpserver.CodeInvalidCredentials)
		}
		if _, err := identity.VerifyPassword(req.Data.Attributes.Password, account.PasswordHash); err != nil {
			return httpserver.NewError(httpserver.CodeInvalidCredentials)
		}
		server.RotateCSRF(c)
		if err := server.StartSession(c, account.ID, account.SecurityGeneration, "authenticated", time.Now()); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, map[string]any{"data": map[string]any{"type": "sessions", "id": account.ID}})
	})
}

func createAdminAccount(t *testing.T, database *sql.DB, username string) user.Account {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{
			Username:      username,
			Email:         username + "@example.test",
			PasswordHash:  hash,
			Language:      "en",
			Country:       "DE",
			TimeZone:      "UTC",
			EmailVerified: true,
			Roles:         []user.Role{user.Administrator},
		})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return account
}

func createSupervisorAccount(t *testing.T, database *sql.DB, username string) user.Account {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{
			Username:      username,
			Email:         username + "@example.test",
			PasswordHash:  hash,
			Language:      "en",
			Country:       "DE",
			TimeZone:      "UTC",
			EmailVerified: true,
			Roles:         []user.Role{user.Supervisor},
		})
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

func loginSession(t *testing.T, server *httpserver.Server, username, password string) (string, []*http.Cookie) {
	t.Helper()
	csrf := csrfToken(t, server)
	body := `{"data":{"type":"login-attempts","attributes":{"username":"` + username + `","password":"` + password + `"}}}`
	response := serve(t, server, http.MethodPost, "/api/v1/auth/login", csrf, nil, body)
	if response.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", response.Code, response.Body.String())
	}
	var cookies []*http.Cookie
	var newCSRF string
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case server.SessionCookieName():
			cookies = append(cookies, cookie)
		case server.BrowserCookieName():
			cookies = append(cookies, cookie)
		case server.CSRFCookieName():
			newCSRF = cookie.Value
		}
	}
	if len(cookies) != 2 || newCSRF == "" {
		t.Fatal("login did not return session cookies")
	}
	return newCSRF, cookies
}

func serve(t *testing.T, server *httpserver.Server, method, path, csrf string, session []*http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://mia.test"+path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/vnd.api+json")
	request.Header.Set("X-CSRF-Token", csrf)
	if csrf != "" {
		request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	}
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func serveReviewedRoleGrant(t *testing.T, server *httpserver.Server, database *sql.DB, actorUsername,
	targetID, csrf string, session []*http.Cookie, role user.Role) *httptest.ResponseRecorder {
	t.Helper()
	actor, err := user.FindForLogin(context.Background(), database, actorUsername)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := user.GetAdministrationAccount(context.Background(), database, actor.ID, targetID,
		course.IsSoleSupervisor)
	if err != nil {
		t.Fatal(err)
	}
	return serveRoleGrant(t, server, targetID, csrf, session, role, detail.ETag)
}

func serveReviewedMentorGrant(t *testing.T, server *httpserver.Server, database *sql.DB, actorUsername,
	targetID, csrf string, session []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	actor, err := user.FindForLogin(context.Background(), database, actorUsername)
	if err != nil {
		t.Fatal(err)
	}
	target, err := user.ResolveMentorTarget(context.Background(), database, actor.ID, targetID)
	if err != nil {
		t.Fatal(err)
	}
	return serveRoleGrant(t, server, targetID, csrf, session, user.Mentor, target.ETag)
}

func serveRoleGrant(t *testing.T, server *httpserver.Server, targetID, csrf string, session []*http.Cookie,
	role user.Role, etag string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"data":{"type":"user-roles","attributes":{"role":"` + string(role) + `"}}}`
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/users/"+targetID+"/roles",
		strings.NewReader(body))
	request.Header.Set("Content-Type", "application/vnd.api+json")
	request.Header.Set("If-Match", etag)
	request.Header.Set("X-CSRF-Token", csrf)
	if csrf != "" {
		request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	}
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func serveIfMatch(
	t *testing.T, server *httpserver.Server, path, csrf string, session []*http.Cookie, etag string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodDelete, "http://mia.test"+path, nil)
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("If-Match", etag)
	request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func assertStatus(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("expected status %d, got %d: %s", status, response.Code, response.Body.String())
	}
}

func assertCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("expected status %d, got %d: %s", status, response.Code, response.Body.String())
	}
	if code != "" && !bytes.Contains(response.Body.Bytes(), []byte(`"code":"`+code+`"`)) {
		t.Fatalf("body %s does not contain code %q", response.Body.String(), code)
	}
}

func TestDirectRoleGrant(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	supervisor := createSupervisorAccount(t, database, "supervisor")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Admin can grant supervisor role to existing staff.
	response := serveReviewedRoleGrant(t, server, database, "admin", supervisor.ID, csrf, session,
		user.Administrator)
	assertStatus(t, response, http.StatusNoContent)

	// Verify role was granted.
	var roleCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM user_roles WHERE user_id = ? AND role = 'administrator'", supervisor.ID).Scan(&roleCount); err != nil {
		t.Fatal(err)
	}
	if roleCount != 1 {
		t.Fatalf("expected 1 administrator role, got %d", roleCount)
	}

	_ = admin
}

func TestRoleGrantRouteRequiresAuthenticationCSRFAndReviewedState(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "precondition-admin")
	target := createMentorAccount(t, database, "precondition-target")
	csrf, session := loginSession(t, server, "precondition-admin", "correct horse battery")
	detail, err := user.GetAdministrationAccount(context.Background(), database, admin.ID, target.ID,
		course.IsSoleSupervisor)
	if err != nil {
		t.Fatal(err)
	}

	unauthenticated := serveRoleGrant(t, server, target.ID, csrf, nil, user.Administrator, detail.ETag)
	assertCode(t, unauthenticated, http.StatusUnauthorized, "auth_unauthenticated")
	expired := conformance.ExpireSession(t, server.Sessions, server.SessionCookieName(), session)
	expiredResponse := serveRoleGrant(t, server, target.ID, csrf, expired, user.Administrator, detail.ETag)
	assertCode(t, expiredResponse, http.StatusUnauthorized, "auth_unauthenticated")
	csrfRejected := serveRoleGrant(t, server, target.ID, "", session, user.Administrator, detail.ETag)
	assertCode(t, csrfRejected, http.StatusForbidden, "csrf_invalid")
	missing := serveRoleGrant(t, server, target.ID, csrf, session, user.Administrator, "")
	assertCode(t, missing, http.StatusPreconditionRequired, "user_account_precondition_required")

	if _, err := database.Exec("UPDATE users SET username = ?, username_key = ? WHERE id = ?",
		"changed-target", "changed-target", target.ID); err != nil {
		t.Fatal(err)
	}
	stale := serveRoleGrant(t, server, target.ID, csrf, session, user.Administrator, detail.ETag)
	assertCode(t, stale, http.StatusPreconditionFailed, "user_account_precondition_failed")
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM user_roles WHERE user_id = ? AND role = 'administrator'",
		target.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("stale reviewed state granted administrator role")
	}
}

func TestTokenGenerationPreventsStaleDeliveryMarking(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create an invitation.
	inv, originalToken, err := service.Create(context.Background(), CreateInput{
		Email:     "generation-test@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	originalGeneration := inv.TokenGeneration
	// Token generation should start at 1.
	if originalGeneration != 1 {
		t.Fatalf("expected initial generation 1, got %d", originalGeneration)
	}

	// Resend increments generation.
	inv2, resentToken, err := service.Resend(context.Background(), inv.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inv2.TokenGeneration != originalGeneration+1 {
		t.Fatalf("expected generation %d, got %d", originalGeneration+1, inv2.TokenGeneration)
	}
	if _, err := service.Preview(context.Background(), originalToken); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("resend left previous token usable: %v", err)
	}
	if _, err := service.Preview(context.Background(), resentToken); err != nil {
		t.Fatalf("resend did not leave new token usable: %v", err)
	}

	// MarkFaultyIfGeneration with old generation should not mark faulty.
	if err := service.MarkFaultyIfGeneration(
		context.Background(), inv.ID, httpserver.StableCode(httpserver.CodeInvitationDeliveryRejected),
		originalGeneration, time.Now(),
	); err != nil {
		t.Fatal(err)
	}

	// Verify invitation is still pending.
	loaded, err := service.Get(context.Background(), inv.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != StatusPending {
		t.Fatalf("expected status pending, got %s", loaded.Status)
	}

	// MarkFaultyIfGeneration with current generation should mark faulty.
	if err := service.MarkFaultyIfGeneration(
		context.Background(), inv.ID, httpserver.StableCode(httpserver.CodeInvitationDeliveryRejected),
		inv2.TokenGeneration, time.Now(),
	); err != nil {
		t.Fatal(err)
	}

	// Verify invitation is now faulty.
	loaded2, err := service.Get(context.Background(), inv.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded2.Status != StatusFaulty {
		t.Fatalf("expected status faulty, got %s", loaded2.Status)
	}
}

func TestNonPendingPublicStateEquivalence(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create and accept an invitation.
	inv, token, err := service.Create(context.Background(), CreateInput{
		Email:     "accepted@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Accept(context.Background(), AcceptInput{
		Token:        token,
		Username:     "accepteduser",
		PasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$dGVzdHNhbHRzYWx0c2Fs$K0VIUEFlT09CMEMDAwXVxDAwNDAwMDAwMA",
		Language:     "en",
		Country:      "DE",
		TimeZone:     "Europe/Berlin",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Previewing accepted invitation returns same error as invalid token.
	_, err = service.Preview(context.Background(), token)
	if !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("expected ErrInvitationInvalid, got %v", err)
	}

	// Create and revoke an invitation.
	inv2, token2, err := service.Create(context.Background(), CreateInput{
		Email:     "revoked@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Delete(context.Background(), inv2.ID, admin.ID, ETag(inv2)); err != nil {
		t.Fatal(err)
	}

	// Previewing revoked invitation returns same error as invalid token.
	_, err = service.Preview(context.Background(), token2)
	if !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("expected ErrInvitationInvalid, got %v", err)
	}

	// Create and mark faulty an invitation.
	inv3, token3, err := service.Create(context.Background(), CreateInput{
		Email:     "faulty-preview@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkFaulty(context.Background(), inv3.ID,
		httpserver.StableCode(httpserver.CodeInvitationDeliveryRejected)); err != nil {
		t.Fatal(err)
	}

	// Previewing faulty invitation returns same error as invalid token.
	_, err = service.Preview(context.Background(), token3)
	if !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("expected ErrInvitationInvalid, got %v", err)
	}

	_ = inv
}

func TestInvalidAcceptanceProfiles(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create an invitation.
	_, token, err := service.Create(context.Background(), CreateInput{
		Email:     "profile-test@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	csrf := csrfToken(t, server)

	// Invalid language.
	response := serve(t, server, http.MethodPost, "/api/v1/invitation-acceptances", csrf, nil,
		`{"data":{"type":"invitation-acceptances","attributes":{"token":"`+token+`","username":"newuser1","password":"a very secure password","password_confirmation":"a very secure password","language":"invalid-lang","country":"DE","time_zone":"Europe/Berlin"}}}`)
	assertCode(t, response, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Invalid country.
	response2 := serve(t, server, http.MethodPost, "/api/v1/invitation-acceptances", csrf, nil,
		`{"data":{"type":"invitation-acceptances","attributes":{"token":"`+token+`","username":"newuser2","password":"a very secure password","password_confirmation":"a very secure password","language":"en","country":"INVALID","time_zone":"Europe/Berlin"}}}`)
	assertCode(t, response2, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Invalid timezone.
	response3 := serve(t, server, http.MethodPost, "/api/v1/invitation-acceptances", csrf, nil,
		`{"data":{"type":"invitation-acceptances","attributes":{"token":"`+token+`","username":"newuser3","password":"a very secure password","password_confirmation":"a very secure password","language":"en","country":"DE","time_zone":"Invalid/Zone"}}}`)
	assertCode(t, response3, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Invitation should still be pending after validation errors.
	preview, err := service.Preview(context.Background(), token)
	if err != nil {
		t.Fatalf("expected invitation to still be pending: %v", err)
	}
	if preview != RoleSupervisor {
		t.Fatalf("expected supervisor role, got %s", preview)
	}

	_ = admin
}

func TestInvitationAcceptanceInvalidCountryUsesValidationResponse(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "country-admin")
	service := NewService(database, nil)
	_, token, err := service.Create(context.Background(), CreateInput{
		Email:     "country-validation@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	csrf := csrfToken(t, server)
	response := serve(t, server, http.MethodPost, "/api/v1/invitation-acceptances", csrf, nil,
		`{"data":{"type":"invitation-acceptances","attributes":{"token":"`+token+`","username":"countryuser","password":"a very secure password","password_confirmation":"a very secure password","language":"en","country":"INVALID","time_zone":"Europe/Berlin"}}}`)
	assertCode(t, response, http.StatusUnprocessableEntity, "auth_invalid_request")
}

func TestPaginationLimitsAndUnknownParams(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Unknown query parameter should be rejected.
	response := serve(t, server, http.MethodGet, "/api/v1/invitations?unknown=value", csrf, session, "")
	assertCode(t, response, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Negative offset should be rejected.
	response2 := serve(t, server, http.MethodGet, "/api/v1/invitations?page[offset]=-1", csrf, session, "")
	assertCode(t, response2, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Offset over 10000 should be rejected.
	response3 := serve(t, server, http.MethodGet, "/api/v1/invitations?page[offset]=10001", csrf, session, "")
	assertCode(t, response3, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Limit over 100 should be rejected.
	response4 := serve(t, server, http.MethodGet, "/api/v1/invitations?page[limit]=101", csrf, session, "")
	assertCode(t, response4, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Zero limit should be rejected.
	response5 := serve(t, server, http.MethodGet, "/api/v1/invitations?page[limit]=0", csrf, session, "")
	assertCode(t, response5, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Duplicate pagination parameters should be rejected (shared parser wiring).
	response6 := serve(t, server, http.MethodGet, "/api/v1/invitations?page[limit]=2&page[limit]=3", csrf, session, "")
	assertCode(t, response6, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Valid pagination should work.
	response7 := serve(t, server, http.MethodGet, "/api/v1/invitations?page[limit]=25&page[offset]=0", csrf, session, "")
	assertStatus(t, response7, http.StatusOK)
}

func TestListNavigationLinks(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Create 3 invitations.
	for i := 0; i < 3; i++ {
		response := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf, session,
			`{"data":{"type":"invitations","attributes":{"email":"nav`+string(rune('a'+i))+`@example.test","role":"supervisor"}}}`)
		assertStatus(t, response, http.StatusCreated)
	}

	// List with limit 2 - should have next link.
	response := serve(t, server, http.MethodGet, "/api/v1/invitations?page[limit]=2", csrf, session, "")
	assertStatus(t, response, http.StatusOK)
	var result struct {
		Data  []any          `json:"data"`
		Links map[string]any `json:"links"`
		Meta  map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 2 {
		t.Fatalf("expected 2 invitations, got %d", len(result.Data))
	}
	if result.Links["next"] == nil {
		t.Fatal("expected next link")
	}
	if result.Links["prev"] != nil {
		t.Fatal("expected no prev link at offset 0")
	}
	if result.Meta["has_more"] != true {
		t.Fatal("expected meta.has_more true on the first page")
	}

	// List from offset 2 - should have prev link but no next.
	response2 := serve(t, server, http.MethodGet, "/api/v1/invitations?page[limit]=2&page[offset]=2", csrf, session, "")
	assertStatus(t, response2, http.StatusOK)
	var result2 struct {
		Data  []any          `json:"data"`
		Links map[string]any `json:"links"`
		Meta  map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(response2.Body.Bytes(), &result2); err != nil {
		t.Fatal(err)
	}
	if len(result2.Data) != 1 {
		t.Fatalf("expected 1 invitation, got %d", len(result2.Data))
	}
	if result2.Links["prev"] == nil {
		t.Fatal("expected prev link")
	}
	if result2.Links["next"] != nil {
		t.Fatal("expected no next link")
	}
	if result2.Meta["has_more"] != false {
		t.Fatal("expected meta.has_more false on the last page")
	}
}

// Finding 5: Test that student and mentor cannot list invitations.
func TestListInvitationsUnauthorizedForStudentAndMentor(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	mentor := createMentorAccount(t, database, "mentor")
	student := createStudentAccount(t, database, "student")

	// Mentor cannot list invitations.
	csrf1, session1 := loginSession(t, server, "mentor", "correct horse battery")
	response1 := serve(t, server, http.MethodGet, "/api/v1/invitations", csrf1, session1, "")
	assertCode(t, response1, http.StatusForbidden, "invitation_list_unauthorized")

	// Student cannot list invitations.
	csrf2, session2 := loginSession(t, server, "student", "correct horse battery")
	response2 := serve(t, server, http.MethodGet, "/api/v1/invitations", csrf2, session2, "")
	assertCode(t, response2, http.StatusForbidden, "invitation_list_unauthorized")

	_ = mentor
	_ = student
}

// Finding 4: Test that duplicate username returns proper error.
func TestDuplicateUsernameReturnsConflict(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create an invitation.
	_, token, err := service.Create(context.Background(), CreateInput{
		Email:     "duplicate-user@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	csrf := csrfToken(t, server)

	// Try to accept with a username that already exists.
	response := serve(t, server, http.MethodPost, "/api/v1/invitation-acceptances", csrf, nil,
		`{"data":{"type":"invitation-acceptances","attributes":{"token":"`+token+`","username":"admin","password":"a very secure password","password_confirmation":"a very secure password","language":"en","country":"DE","time_zone":"Europe/Berlin"}}}`)
	assertCode(t, response, http.StatusConflict, "username_taken")

	// Verify invitation is still pending (preserved after validation error).
	preview, err := service.Preview(context.Background(), token)
	if err != nil {
		t.Fatalf("expected invitation to still be pending: %v", err)
	}
	if preview != RoleSupervisor {
		t.Fatalf("expected supervisor role, got %s", preview)
	}
}

// Finding 6: Test supervisor granting mentor role.
func TestSupervisorCanGrantMentorRole(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	supervisor := createSupervisorAccount(t, database, "supervisor")
	mentor := createMentorAccount(t, database, "mentor2")

	// Supervisor can grant mentor role to existing staff.
	csrf, session := loginSession(t, server, "supervisor", "correct horse battery")
	// This should fail because mentor already has staff role but not
	// administrator/supervisor, and we're granting mentor which is idempotent.
	response := serveReviewedMentorGrant(t, server, database, "supervisor", mentor.ID, csrf, session)
	assertStatus(t, response, http.StatusNoContent)

	_ = admin
	_ = supervisor
}

func TestMentorGrantRequiresCurrentPreflightAndAuditsOnlyChange(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	const supervisorUsername = "reviewing-supervisor"
	supervisor := createSupervisorAccount(t, database, supervisorUsername)
	target := createSupervisorAccount(t, database, "reviewed-target")
	csrf, session := loginSession(t, server, supervisorUsername, "correct horse battery")

	missing := serveRoleGrant(t, server, target.ID, csrf, session, user.Mentor, "")
	assertCode(t, missing, http.StatusPreconditionRequired, "user_account_precondition_required")
	preflight, err := user.ResolveMentorTarget(context.Background(), database, supervisor.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE users SET name = 'Changed after review' WHERE id = ?", target.ID); err != nil {
		t.Fatal(err)
	}
	stale := serveRoleGrant(t, server, target.ID, csrf, session, user.Mentor, preflight.ETag)
	assertCode(t, stale, http.StatusPreconditionFailed, "user_account_precondition_failed")
	var roleCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM user_roles WHERE user_id = ? AND role = 'mentor'", target.ID).
		Scan(&roleCount); err != nil {
		t.Fatal(err)
	}
	if roleCount != 0 {
		t.Fatalf("stale grant created %d mentor roles", roleCount)
	}

	fresh := serveReviewedMentorGrant(t, server, database, supervisorUsername, target.ID, csrf, session)
	assertStatus(t, fresh, http.StatusNoContent)
	replay := serveReviewedMentorGrant(t, server, database, supervisorUsername, target.ID, csrf, session)
	assertStatus(t, replay, http.StatusNoContent)
	var auditCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'user.user.role_granted'
		AND actor_user_id = ? AND subject_user_id = ?`, supervisor.ID, target.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("mentor grant audit count = %d, want 1", auditCount)
	}
}

// Finding 6: Test role grant idempotency.
func TestRoleGrantIdempotency(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	supervisor := createSupervisorAccount(t, database, "supervisor")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Grant administrator role to supervisor.
	response1 := serveReviewedRoleGrant(t, server, database, "admin", supervisor.ID, csrf, session,
		user.Administrator)
	assertStatus(t, response1, http.StatusNoContent)

	// Grant same role again - should be idempotent.
	response2 := serveReviewedRoleGrant(t, server, database, "admin", supervisor.ID, csrf, session,
		user.Administrator)
	assertStatus(t, response2, http.StatusNoContent)

	// Verify only one role entry exists.
	var roleCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM user_roles WHERE user_id = ? AND role = 'administrator'", supervisor.ID).Scan(&roleCount); err != nil {
		t.Fatal(err)
	}
	if roleCount != 1 {
		t.Fatalf("expected 1 administrator role, got %d", roleCount)
	}

	_ = admin
}

// Finding 6: Test automatic student role with supervisor grant.
func TestSupervisorGrantIncludesStudentRole(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	mentor := createMentorAccount(t, database, "mentor")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Grant supervisor role to mentor.
	response := serveReviewedRoleGrant(t, server, database, "admin", mentor.ID, csrf, session,
		user.Supervisor)
	assertStatus(t, response, http.StatusNoContent)

	// Verify student role was also granted.
	var studentCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM user_roles WHERE user_id = ? AND role = 'student'", mentor.ID).Scan(&studentCount); err != nil {
		t.Fatal(err)
	}
	if studentCount != 1 {
		t.Fatalf("expected 1 student role after supervisor grant, got %d", studentCount)
	}

	_ = admin
}

// Finding 6: Test role grant to student-only account fails.
func TestRoleGrantToStudentOnlyFails(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	student := createStudentAccount(t, database, "student")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Try to grant administrator role to student-only account - should fail.
	response := serveReviewedRoleGrant(t, server, database, "admin", student.ID, csrf, session,
		user.Administrator)
	assertCode(t, response, http.StatusNotFound, "user_not_found")
}

// Finding 6: Test role grant to banned account fails.
func TestRoleGrantToBannedAccountFails(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	mentor := createMentorAccount(t, database, "mentor")

	// Ban the mentor.
	if _, err := database.Exec("UPDATE users SET is_banned = 1 WHERE id = ?", mentor.ID); err != nil {
		t.Fatal(err)
	}

	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Try to grant role to banned account - should fail.
	response := serveReviewedRoleGrant(t, server, database, "admin", mentor.ID, csrf, session,
		user.Administrator)
	assertCode(t, response, http.StatusNotFound, "user_not_found")
}

// Finding 6: Test role grant to unverified account fails.
func TestRoleGrantToUnverifiedAccountFails(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	mentor := createMentorAccount(t, database, "mentor")

	// Unverify the mentor's email.
	if _, err := database.Exec("UPDATE users SET email_verified_at = NULL WHERE id = ?", mentor.ID); err != nil {
		t.Fatal(err)
	}

	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Try to grant role to unverified account - should fail.
	response := serveReviewedRoleGrant(t, server, database, "admin", mentor.ID, csrf, session,
		user.Administrator)
	assertCode(t, response, http.StatusNotFound, "user_not_found")
}

// Finding 6: Test role grant to unknown account fails.
func TestRoleGrantToUnknownAccountFails(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Try to grant role to non-existent account.
	response := serve(t, server, http.MethodPost, "/api/v1/users/u_unknown-user-id/roles", csrf, session,
		`{"data":{"type":"user-roles","attributes":{"role":"administrator"}}}`)
	assertCode(t, response, http.StatusNotFound, "user_not_found")
}

// Finding 6: Test role grant auditing.
func TestRoleGrantAudited(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	supervisor := createSupervisorAccount(t, database, "supervisor")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Count existing audit events.
	var initialCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'user.user.role_granted'").Scan(&initialCount); err != nil {
		t.Fatal(err)
	}

	// Grant role.
	response := serveReviewedRoleGrant(t, server, database, "admin", supervisor.ID, csrf, session,
		user.Administrator)
	assertStatus(t, response, http.StatusNoContent)

	// Verify audit event was created with metadata.
	var auditCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'user.user.role_granted'").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != initialCount+1 {
		t.Fatalf("expected %d audit record, got %d", initialCount+1, auditCount)
	}

	// Verify metadata contains role.
	var metadata string
	if err := database.QueryRow("SELECT metadata FROM audit_events WHERE action = 'user.user.role_granted' ORDER BY created_at DESC LIMIT 1").Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, `"role":"administrator"`) {
		t.Fatalf("expected metadata to contain role, got: %s", metadata)
	}

	_ = admin
}

// Finding 7: Test non-numeric pagination values are rejected.
func TestNonNumericPaginationValuesRejected(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Non-numeric offset should be rejected.
	response1 := serve(t, server, http.MethodGet, "/api/v1/invitations?page[offset]=abc", csrf, session, "")
	assertCode(t, response1, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Non-numeric limit should be rejected.
	response2 := serve(t, server, http.MethodGet, "/api/v1/invitations?page[limit]=xyz", csrf, session, "")
	assertCode(t, response2, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Empty string offset should be rejected.
	response3 := serve(t, server, http.MethodGet, "/api/v1/invitations?page[offset]=", csrf, session, "")
	assertCode(t, response3, http.StatusUnprocessableEntity, "auth_invalid_request")

	// Floating point values should be rejected.
	response4 := serve(t, server, http.MethodGet, "/api/v1/invitations?page[limit]=1.5", csrf, session, "")
	assertCode(t, response4, http.StatusUnprocessableEntity, "auth_invalid_request")
}

// Finding 3: Test audit metadata for invitation events.
func TestInvitationAuditMetadata(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create an invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "audit-test@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify creation audit has metadata.
	var metadata string
	if err := database.QueryRow("SELECT metadata FROM audit_events WHERE action = 'invitation.invitation.created' ORDER BY created_at DESC LIMIT 1").Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, `"invitation_id":"`+inv.ID+`"`) {
		t.Fatalf("expected metadata to contain invitation_id, got: %s", metadata)
	}
	if !strings.Contains(metadata, `"role":"supervisor"`) {
		t.Fatalf("expected metadata to contain role, got: %s", metadata)
	}

	// Revoke the invitation.
	if _, err := service.Delete(context.Background(), inv.ID, admin.ID, ETag(inv)); err != nil {
		t.Fatal(err)
	}

	// Verify revocation audit has metadata.
	if err := database.QueryRow("SELECT metadata FROM audit_events WHERE action = 'invitation.invitation.revoked' ORDER BY created_at DESC LIMIT 1").Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, `"invitation_id":"`+inv.ID+`"`) {
		t.Fatalf("expected revocation metadata to contain invitation_id, got: %s", metadata)
	}
}

func createMentorAccount(t *testing.T, database *sql.DB, username string) user.Account {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{
			Username:      username,
			Email:         username + "@example.test",
			PasswordHash:  hash,
			Language:      "en",
			Country:       "DE",
			TimeZone:      "UTC",
			EmailVerified: true,
			Roles:         []user.Role{user.Mentor},
		})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return account
}

func createStudentAccount(t *testing.T, database *sql.DB, username string) user.Account {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{
			Username:     username,
			PasswordHash: hash,
			Language:     "en",
			Country:      "DE",
			TimeZone:     "UTC",
			Roles:        []user.Role{user.Student},
		})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return account
}

// Finding 6: Test delivery manager marks sent_at on success.
func TestDeliveryManagerUpdatesSentAtOnSuccess(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "delivery-success@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify sent_at is initially null.
	var sentAt sql.NullString
	if err := database.QueryRow("SELECT sent_at FROM invitations WHERE id = ?", inv.ID).Scan(&sentAt); err != nil {
		t.Fatal(err)
	}
	if sentAt.Valid {
		t.Fatal("expected sent_at to be null initially")
	}

	// Use delivery manager with success mailer.
	manager := NewDeliveryManager(service, database, successMailer{}, nil)
	defer manager.Close()

	// Deliver invitation.
	if !manager.Admit(context.Background(), inv.ID, inv.Email, string(inv.Role), "https://test/invite", inv.TokenGeneration) {
		t.Fatal("expected delivery to be admitted")
	}

	// Wait for delivery to complete.
	manager.Close()

	// Verify sent_at is now set.
	if err := database.QueryRow("SELECT sent_at FROM invitations WHERE id = ?", inv.ID).Scan(&sentAt); err != nil {
		t.Fatal(err)
	}
	if !sentAt.Valid {
		t.Fatal("expected sent_at to be set after successful delivery")
	}

	// Verify audit event was created.
	var auditCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.delivered'").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("expected 1 delivered audit event, got %d", auditCount)
	}
}

// Finding 6: Test delivery manager marks faulty on rejection.
func TestDeliveryManagerMarksFaultyOnRejection(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create invitation.
	inv, token, err := service.Create(context.Background(), CreateInput{
		Email:     "delivery-reject@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Use delivery manager with rejection mailer.
	manager := NewDeliveryManager(service, database, rejectionMailer{}, nil)
	defer manager.Close()

	// Deliver invitation.
	if !manager.Admit(context.Background(), inv.ID, inv.Email, string(inv.Role), "https://test/invite", inv.TokenGeneration) {
		t.Fatal("expected delivery to be admitted")
	}

	// Wait for delivery to complete.
	manager.Close()

	// Verify invitation is now faulty with a sanitized failed delivery result.
	var status, deliveryState string
	var deliveryCode, attemptedAt sql.NullString
	if err := database.QueryRow(`SELECT status, delivery_state, failure_code, delivery_attempted_at
		FROM invitations WHERE id = ?`, inv.ID).Scan(&status, &deliveryState, &deliveryCode, &attemptedAt); err != nil {
		t.Fatal(err)
	}
	if status != "faulty" || deliveryState != "failed" || !deliveryCode.Valid ||
		deliveryCode.String != "invitation_delivery_rejected" || !attemptedAt.Valid {
		t.Fatalf("unexpected definite-failure state: %s %s %v %v", status, deliveryState, deliveryCode, attemptedAt)
	}
	if _, err := service.Preview(context.Background(), token); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("definite failure left token usable: %v", err)
	}
}

// Finding 6: Test delivery manager audits timeout.
func TestDeliveryManagerAuditsTimeout(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "delivery-timeout@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Use delivery manager with timeout mailer.
	manager := NewDeliveryManager(service, database, timeoutMailer{}, nil)
	defer manager.Close()

	// Deliver invitation.
	if !manager.Admit(context.Background(), inv.ID, inv.Email, string(inv.Role), "https://test/invite", inv.TokenGeneration) {
		t.Fatal("expected delivery to be admitted")
	}

	// Wait for delivery to complete.
	manager.Close()

	// Verify invitation is still pending (timeout doesn't mark faulty).
	var status string
	if err := database.QueryRow("SELECT status FROM invitations WHERE id = ?", inv.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("expected status pending after timeout, got %s", status)
	}

	// Verify timeout audit event was created with invitation_id metadata.
	var metadata string
	if err := database.QueryRow("SELECT metadata FROM audit_events WHERE action = 'invitation.invitation.delivery_timeout'").Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, `"invitation_id":"`+inv.ID+`"`) {
		t.Fatalf("expected metadata to contain invitation_id, got: %s", metadata)
	}
}

// Finding 6: Test delivery manager audits ambiguous outcome.
func TestDeliveryManagerAuditsAmbiguous(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "delivery-ambiguous@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Use delivery manager with ambiguous mailer.
	manager := NewDeliveryManager(service, database, ambiguousMailer{}, nil)
	defer manager.Close()

	// Deliver invitation.
	if !manager.Admit(context.Background(), inv.ID, inv.Email, string(inv.Role), "https://test/invite", inv.TokenGeneration) {
		t.Fatal("expected delivery to be admitted")
	}

	// Wait for delivery to complete.
	manager.Close()

	// Verify invitation is still pending (ambiguous doesn't mark faulty).
	var status string
	if err := database.QueryRow("SELECT status FROM invitations WHERE id = ?", inv.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("expected status pending after ambiguous, got %s", status)
	}

	// Verify ambiguous audit event was created with invitation_id metadata.
	var metadata string
	if err := database.QueryRow("SELECT metadata FROM audit_events WHERE action = 'invitation.invitation.delivery_ambiguous'").Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, `"invitation_id":"`+inv.ID+`"`) {
		t.Fatalf("expected metadata to contain invitation_id, got: %s", metadata)
	}
}

// Test mailers for delivery manager tests.
type successMailer struct{}

func (successMailer) SendInvitation(_ context.Context, _, _, _ string) error { return nil }

type rejectionMailer struct{}

func (rejectionMailer) SendInvitation(_ context.Context, _, _, _ string) error {
	return smtp.ErrRejected
}

type timeoutMailer struct{}

func (timeoutMailer) SendInvitation(_ context.Context, _, _, _ string) error {
	return smtp.ErrTimeout
}

type ambiguousMailer struct{}

func (ambiguousMailer) SendInvitation(_ context.Context, _, _, _ string) error {
	return smtp.ErrAmbiguous
}

// Finding 2: Test that non-pending tokens return generic error even with invalid profile data.
func TestNonPendingTokenReturnsGenericErrorWithInvalidProfile(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create and accept an invitation.
	_, token, err := service.Create(context.Background(), CreateInput{
		Email:     "nonpending-profile@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Accept(context.Background(), AcceptInput{
		Token:        token,
		Username:     "acceptedprofileuser",
		PasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$dGVzdHNhbHRzYWx0c2Fs$K0VIUEFlT09CMEMDAwXVxDAwNDAwMDAwMA",
		Language:     "en",
		Country:      "DE",
		TimeZone:     "Europe/Berlin",
	})
	if err != nil {
		t.Fatal(err)
	}

	csrf := csrfToken(t, server)

	// Try to accept with the consumed token and invalid password (too short).
	// Should get generic invitation_invalid, not password error.
	response := serve(t, server, http.MethodPost, "/api/v1/invitation-acceptances", csrf, nil,
		`{"data":{"type":"invitation-acceptances","attributes":{"token":"`+token+`","username":"newuser","password":"short","password_confirmation":"short","language":"en","country":"DE","time_zone":"Europe/Berlin"}}}`)
	assertCode(t, response, http.StatusUnprocessableEntity, "invitation_invalid")

	// Try with mismatched passwords - should still get invitation_invalid.
	response2 := serve(t, server, http.MethodPost, "/api/v1/invitation-acceptances", csrf, nil,
		`{"data":{"type":"invitation-acceptances","attributes":{"token":"`+token+`","username":"newuser","password":"validpassword123","password_confirmation":"differentpassword","language":"en","country":"DE","time_zone":"Europe/Berlin"}}}`)
	assertCode(t, response2, http.StatusUnprocessableEntity, "invitation_invalid")

	// Try with invalid timezone - should still get invitation_invalid.
	response3 := serve(t, server, http.MethodPost, "/api/v1/invitation-acceptances", csrf, nil,
		`{"data":{"type":"invitation-acceptances","attributes":{"token":"`+token+`","username":"newuser","password":"validpassword123","password_confirmation":"validpassword123","language":"en","country":"DE","time_zone":"Invalid/Zone"}}}`)
	assertCode(t, response3, http.StatusUnprocessableEntity, "invitation_invalid")
}

// Finding 2: Test that revoked tokens return generic error even with invalid profile data.
func TestRevokedTokenReturnsGenericErrorWithInvalidProfile(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create and revoke an invitation.
	inv, token, err := service.Create(context.Background(), CreateInput{
		Email:     "revoked-profile@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Delete(context.Background(), inv.ID, admin.ID, ETag(inv)); err != nil {
		t.Fatal(err)
	}

	csrf := csrfToken(t, server)

	// Try to accept with the revoked token and invalid password.
	// Should get generic invitation_invalid, not password error.
	response := serve(t, server, http.MethodPost, "/api/v1/invitation-acceptances", csrf, nil,
		`{"data":{"type":"invitation-acceptances","attributes":{"token":"`+token+`","username":"newuser","password":"short","password_confirmation":"short","language":"en","country":"DE","time_zone":"Europe/Berlin"}}}`)
	assertCode(t, response, http.StatusUnprocessableEntity, "invitation_invalid")
}

// Finding 3: Test that timeout/ambiguous audits include outcome_code metadata.
func TestDeliveryAuditIncludesOutcomeCode(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Test timeout outcome code.
	inv1, _, err := service.Create(context.Background(), CreateInput{
		Email:     "timeout-outcome@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	manager1 := NewDeliveryManager(service, database, timeoutMailer{}, nil)
	if !manager1.Admit(context.Background(), inv1.ID, inv1.Email, string(inv1.Role), "https://test/invite", inv1.TokenGeneration) {
		t.Fatal("expected delivery to be admitted")
	}
	manager1.Close()

	var metadata1 string
	if err := database.QueryRow("SELECT metadata FROM audit_events WHERE action = 'invitation.invitation.delivery_timeout' ORDER BY created_at DESC LIMIT 1").Scan(&metadata1); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata1, `"outcome_code":"invitation_delivery_timeout"`) {
		t.Fatalf("expected stable timeout outcome code, got: %s", metadata1)
	}

	// Test ambiguous outcome code.
	inv2, _, err := service.Create(context.Background(), CreateInput{
		Email:     "ambiguous-outcome@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	manager2 := NewDeliveryManager(service, database, ambiguousMailer{}, nil)
	if !manager2.Admit(context.Background(), inv2.ID, inv2.Email, string(inv2.Role), "https://test/invite", inv2.TokenGeneration) {
		t.Fatal("expected delivery to be admitted")
	}
	manager2.Close()

	var metadata2 string
	if err := database.QueryRow("SELECT metadata FROM audit_events WHERE action = 'invitation.invitation.delivery_ambiguous' ORDER BY created_at DESC LIMIT 1").Scan(&metadata2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata2, `"outcome_code":"invitation_delivery_ambiguous"`) {
		t.Fatalf("expected stable ambiguous outcome code, got: %s", metadata2)
	}
}

func TestInvitationDeliveryStateReconcilesTimeout(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "delivery-state-admin")
	service := NewService(database, nil)
	inv, token, err := service.Create(context.Background(), CreateInput{
		Email: "delivery-state@example.test", Role: RoleSupervisor, InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewDeliveryManager(service, database, timeoutMailer{}, nil)
	if !manager.Admit(context.Background(), inv.ID, inv.Email, string(inv.Role), "https://test/invite", 1) {
		t.Fatal("expected delivery admission")
	}
	manager.Close()

	csrf, session := loginSession(t, server, "delivery-state-admin", "correct horse battery")
	response := serve(t, server, http.MethodGet, "/api/v1/invitations/"+inv.ID, csrf, session, "")
	assertStatus(t, response, http.StatusOK)
	if response.Header().Get("ETag") == "" || strings.HasPrefix(response.Header().Get("ETag"), "W/") {
		t.Fatalf("expected strong ETag, got %q", response.Header().Get("ETag"))
	}
	if response.Header().Get("ETag") == ETag(inv) {
		t.Fatal("delivery outcome did not change the reviewed invitation validator")
	}
	var document struct {
		Data struct {
			Attributes struct {
				Status              string  `json:"status"`
				DeliveryState       string  `json:"delivery_state"`
				DeliveryCode        *string `json:"delivery_code"`
				DeliveryAttemptedAt *string `json:"delivery_attempted_at"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	attributes := document.Data.Attributes
	if attributes.Status != "pending" || attributes.DeliveryState != "ambiguous" ||
		attributes.DeliveryCode == nil || *attributes.DeliveryCode != "invitation_delivery_timeout" ||
		attributes.DeliveryAttemptedAt == nil {
		t.Fatalf("unexpected delivery reconciliation: %+v", attributes)
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000000Z", *attributes.DeliveryAttemptedAt); err != nil {
		t.Fatalf("invalid delivery attempt instant: %v", err)
	}
	if _, err := service.Preview(context.Background(), token); err != nil {
		t.Fatalf("timeout invalidated usable token: %v", err)
	}
}

func TestInvitationDeleteRequiresCurrentReviewedETag(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "etag-admin")
	service := NewService(database, nil)
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email: "etag@example.test", Role: RoleSupervisor, InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	csrf, session := loginSession(t, server, "etag-admin", "correct horse battery")
	path := "/api/v1/invitations/" + inv.ID

	missing := serve(t, server, http.MethodDelete, path, csrf, session, "")
	assertCode(t, missing, http.StatusPreconditionRequired, "invitation_precondition_required")
	current, err := service.Get(context.Background(), inv.ID, admin.ID)
	if err != nil || current.Status != StatusPending {
		t.Fatalf("missing precondition changed invitation: %+v, %v", current, err)
	}
	malformed := serveIfMatch(t, server, path, csrf, session, `W/"not-current"`)
	assertCode(t, malformed, http.StatusPreconditionFailed, "invitation_precondition_failed")
	staleETag := ETag(current)
	if _, _, err := service.Resend(context.Background(), inv.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	stale := serveIfMatch(t, server, path, csrf, session, staleETag)
	assertCode(t, stale, http.StatusPreconditionFailed, "invitation_precondition_failed")
	current, err = service.Get(context.Background(), inv.ID, admin.ID)
	if err != nil || current.Status != StatusPending {
		t.Fatalf("stale precondition changed invitation: %+v, %v", current, err)
	}
	var deniedCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events
		WHERE action = 'invitation.invitation.revocation_denied'`).Scan(&deniedCount); err != nil {
		t.Fatal(err)
	}
	if deniedCount != 3 {
		t.Fatalf("expected three audited precondition denials, got %d", deniedCount)
	}

	deleted := serveIfMatch(t, server, path, csrf, session, ETag(current))
	assertStatus(t, deleted, http.StatusOK)
	if !bytes.Contains(deleted.Body.Bytes(), []byte(`"effect":"revoked"`)) {
		t.Fatalf("DELETE did not identify revoke effect: %s", deleted.Body.String())
	}
	var retainedStatus string
	if err := database.QueryRow("SELECT status FROM invitations WHERE id = ?", inv.ID).Scan(&retainedStatus); err != nil {
		t.Fatal(err)
	}
	if retainedStatus != "revoked" {
		t.Fatalf("expected retained revoked invitation, got %s", retainedStatus)
	}
}

func TestInvitationDeleteHidesMissingAndOutOfScopeTargetsBeforeRequiringETag(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "hidden-delete-admin")
	_ = createSupervisorAccount(t, database, "hidden-delete-supervisor")
	service := NewService(database, nil)
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email: "hidden-delete@example.test", Role: RoleAdministrator, InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	csrf, session := loginSession(t, server, "hidden-delete-supervisor", "correct horse battery")

	outOfScope := serve(t, server, http.MethodDelete, "/api/v1/invitations/"+inv.ID, csrf, session, "")
	assertCode(t, outOfScope, http.StatusNotFound, "invitation_not_found")
	missing := serve(t, server, http.MethodDelete, "/api/v1/invitations/inv_missing", csrf, session, "")
	assertCode(t, missing, http.StatusNotFound, "invitation_not_found")
	if outOfScope.Body.String() != missing.Body.String() {
		t.Fatalf("hidden DELETE responses differ: out-of-scope=%s missing=%s",
			outOfScope.Body.String(), missing.Body.String())
	}
	var status Status
	if err := database.QueryRow("SELECT status FROM invitations WHERE id = ?", inv.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != StatusPending {
		t.Fatalf("hidden DELETE changed invitation to %s", status)
	}
}

func TestPendingDeleteReviewCannotDeleteConcurrentFault(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "fault-race-admin")
	service := NewService(database, nil)
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email: "fault-race@example.test", Role: RoleSupervisor, InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewed := ETag(inv)
	if err := service.MarkFaulty(context.Background(), inv.ID,
		httpserver.StableCode(httpserver.CodeInvitationDeliveryRejected)); err != nil {
		t.Fatal(err)
	}
	csrf, session := loginSession(t, server, "fault-race-admin", "correct horse battery")
	response := serveIfMatch(t, server, "/api/v1/invitations/"+inv.ID, csrf, session, reviewed)
	assertCode(t, response, http.StatusPreconditionFailed, "invitation_precondition_failed")
	var status string
	if err := database.QueryRow("SELECT status FROM invitations WHERE id = ?", inv.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "faulty" {
		t.Fatalf("stale pending review changed concurrent fault to %s", status)
	}
}

func TestPendingDeleteReviewCannotChangeConcurrentTerminalEffect(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "terminal-race-admin")
	service := NewService(database, nil)
	csrf, session := loginSession(t, server, "terminal-race-admin", "correct horse battery")
	cases := []struct {
		name   string
		status Status
	}{
		{name: "accepted", status: StatusAccepted},
		{name: "revoked", status: StatusRevoked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv, token, err := service.Create(context.Background(), CreateInput{
				Email: "terminal-race-" + tc.name + "@example.test",
				Role:  RoleSupervisor, InviterID: admin.ID,
			})
			if err != nil {
				t.Fatal(err)
			}
			reviewed := ETag(inv)
			switch tc.status {
			case StatusAccepted:
				_, err = service.Accept(context.Background(), AcceptInput{
					Token: token, Username: "terminaluser" + tc.name,
					PasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$dGVzdHNhbHRzYWx0c2Fs$K0VIUEFlT09CMEMDAwXVxDAwNDAwMDAwMA",
					Language:     "en", Country: "DE", TimeZone: "Europe/Berlin",
				})
			case StatusRevoked:
				_, err = service.Delete(context.Background(), inv.ID, admin.ID, reviewed)
			}
			if err != nil {
				t.Fatal(err)
			}

			response := serveIfMatch(t, server, "/api/v1/invitations/"+inv.ID, csrf, session, reviewed)
			assertCode(t, response, http.StatusPreconditionFailed, "invitation_precondition_failed")
			var status Status
			if err := database.QueryRow("SELECT status FROM invitations WHERE id = ?", inv.ID).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != tc.status {
				t.Fatalf("stale pending review changed %s invitation to %s", tc.status, status)
			}
		})
	}
}

// Finding 3: Test that stale delivery does not audit against newer generation.
func TestStaleDeliveryDoesNotAudit(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "stale-audit@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	originalGeneration := inv.TokenGeneration

	// Resend to increment generation.
	inv2, _, err := service.Resend(context.Background(), inv.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inv2.TokenGeneration <= originalGeneration {
		t.Fatal("expected generation to be incremented after resend")
	}

	// Count existing timeout audits.
	var initialCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.delivery_timeout'").Scan(&initialCount); err != nil {
		t.Fatal(err)
	}

	// Simulate stale delivery with old generation.
	manager := NewDeliveryManager(service, database, timeoutMailer{}, nil)
	if !manager.Admit(context.Background(), inv.ID, inv.Email, string(inv.Role), "https://test/invite", originalGeneration) {
		t.Fatal("expected delivery to be admitted")
	}
	manager.Close()

	// Verify no new audit was created for the stale generation.
	var newCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.delivery_timeout'").Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if newCount != initialCount {
		t.Fatalf("expected no new audit for stale generation, but count changed from %d to %d", initialCount, newCount)
	}

	// Verify delivery with current generation does create audit.
	manager2 := NewDeliveryManager(service, database, timeoutMailer{}, nil)
	if !manager2.Admit(context.Background(), inv.ID, inv.Email, string(inv.Role), "https://test/invite", inv2.TokenGeneration) {
		t.Fatal("expected delivery to be admitted")
	}
	manager2.Close()

	var finalCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.delivery_timeout'").Scan(&finalCount); err != nil {
		t.Fatal(err)
	}
	if finalCount != initialCount+1 {
		t.Fatalf("expected one new audit for current generation, got count %d (was %d)", finalCount, initialCount)
	}
}

// Finding 4: Test that preview includes platform attribute.
func TestPreviewIncludesPlatform(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create an invitation.
	_, token, err := service.Create(context.Background(), CreateInput{
		Email:     "preview-platform@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	csrf := csrfToken(t, server)
	response := serve(t, server, http.MethodPost, "/api/v1/invitation-previews", csrf, nil,
		`{"data":{"type":"invitation-previews","attributes":{"token":"`+token+`"}}}`)
	assertStatus(t, response, http.StatusOK)

	var result struct {
		Data struct {
			Attributes struct {
				Platform string `json:"platform"`
				Role     string `json:"role"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.Attributes.Platform != "mia" {
		t.Fatalf("expected platform 'mia', got %q", result.Data.Attributes.Platform)
	}
	if result.Data.Attributes.Role != "supervisor" {
		t.Fatalf("expected role 'supervisor', got %q", result.Data.Attributes.Role)
	}
}

// Finding 1: Test that token_generation starts at 1.
func TestTokenGenerationStartsAtOne(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create an invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "generation-one@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	if inv.TokenGeneration != 1 {
		t.Fatalf("expected token_generation to be 1, got %d", inv.TokenGeneration)
	}

	// Verify in database.
	var dbGeneration int
	if err := database.QueryRow("SELECT token_generation FROM invitations WHERE id = ?", inv.ID).Scan(&dbGeneration); err != nil {
		t.Fatal(err)
	}
	if dbGeneration != 1 {
		t.Fatalf("expected database token_generation to be 1, got %d", dbGeneration)
	}
}

// Finding 1: Test lifecycle constraint - pending cannot have accepted_at.
func TestLifecycleConstraintPendingNoAcceptedAt(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create a pending invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "lifecycle-pending@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try to set accepted_at while still pending - should fail.
	_, err = database.Exec("UPDATE invitations SET accepted_at = '2024-01-01T00:00:00.000000Z' WHERE id = ?", inv.ID)
	if err == nil {
		t.Fatal("expected error when setting accepted_at on pending invitation")
	}
	if !strings.Contains(err.Error(), "pending invitation must not have accepted_at") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Finding 1: Test lifecycle constraint - accepted must have accepted_at and accepted_by.
func TestLifecycleConstraintAcceptedRequiresFields(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create a pending invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "lifecycle-accepted@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try to set status to accepted without accepted_at - should fail.
	_, err = database.Exec("UPDATE invitations SET status = 'accepted', token_digest = NULL WHERE id = ?", inv.ID)
	if err == nil {
		t.Fatal("expected error when setting accepted status without accepted_at")
	}
	if !strings.Contains(err.Error(), "accepted invitation must have accepted_at") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to set status to accepted with accepted_at but without accepted_by - should fail.
	_, err = database.Exec("UPDATE invitations SET status = 'accepted', token_digest = NULL, accepted_at = '2024-01-01T00:00:00.000000Z' WHERE id = ?", inv.ID)
	if err == nil {
		t.Fatal("expected error when setting accepted status without accepted_by")
	}
	if !strings.Contains(err.Error(), "accepted_by at transition") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Finding 1: Test lifecycle constraint - faulty must have fault_at and failure_code.
func TestLifecycleConstraintFaultyRequiresFields(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create a pending invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "lifecycle-faulty@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try to set status to faulty without fault_at - should fail.
	_, err = database.Exec("UPDATE invitations SET status = 'faulty', token_digest = NULL WHERE id = ?", inv.ID)
	if err == nil {
		t.Fatal("expected error when setting faulty status without fault_at")
	}
	if !strings.Contains(err.Error(), "faulty invitation must have fault_at and failure_code") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to set status to faulty with fault_at but without failure_code - should fail.
	_, err = database.Exec("UPDATE invitations SET status = 'faulty', token_digest = NULL, fault_at = '2024-01-01T00:00:00.000000Z' WHERE id = ?", inv.ID)
	if err == nil {
		t.Fatal("expected error when setting faulty status without failure_code")
	}
	if !strings.Contains(err.Error(), "faulty invitation must have fault_at and failure_code") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Finding 1: Test lifecycle constraint - revoked must have revoked_at and revoked_by.
func TestLifecycleConstraintRevokedRequiresFields(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create a pending invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "lifecycle-revoked@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try to set status to revoked without revoked_at - should fail.
	_, err = database.Exec("UPDATE invitations SET status = 'revoked', token_digest = NULL WHERE id = ?", inv.ID)
	if err == nil {
		t.Fatal("expected error when setting revoked status without revoked_at")
	}
	if !strings.Contains(err.Error(), "revoked invitation must have revoked_at") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to set status to revoked with revoked_at but without revoked_by - should fail.
	_, err = database.Exec("UPDATE invitations SET status = 'revoked', token_digest = NULL, revoked_at = '2024-01-01T00:00:00.000000Z' WHERE id = ?", inv.ID)
	if err == nil {
		t.Fatal("expected error when setting revoked status without revoked_by")
	}
	if !strings.Contains(err.Error(), "revoked_by at transition") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Finding 1: Test that account deletion allows NULL attribution on terminal states.
func TestAccountDeletionAllowsNullAttribution(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create and accept an invitation.
	inv, token, err := service.Create(context.Background(), CreateInput{
		Email:     "deletion-test@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := service.Accept(context.Background(), AcceptInput{
		Token:        token,
		Username:     "deletionuser",
		PasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$dGVzdHNhbHRzYWx0c2Fs$K0VIUEFlT09CMEMDAwXVxDAwNDAwMDAwMA",
		Language:     "en",
		Country:      "DE",
		TimeZone:     "Europe/Berlin",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify accepted_by is set.
	var acceptedBy sql.NullString
	if err := database.QueryRow("SELECT accepted_by FROM invitations WHERE id = ?", inv.ID).Scan(&acceptedBy); err != nil {
		t.Fatal(err)
	}
	if !acceptedBy.Valid || acceptedBy.String != account.ID {
		t.Fatalf("expected accepted_by to be %s, got %v", account.ID, acceptedBy)
	}

	// Simulate user deletion by setting accepted_by to NULL (as ON DELETE SET NULL would).
	_, err = database.Exec("UPDATE invitations SET accepted_by = NULL WHERE id = ?", inv.ID)
	if err != nil {
		t.Fatalf("should allow NULL accepted_by after user deletion: %v", err)
	}

	// Verify the invitation is still valid.
	var status string
	if err := database.QueryRow("SELECT status FROM invitations WHERE id = ?", inv.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "accepted" {
		t.Fatalf("expected status accepted, got %s", status)
	}
}

// Finding 1: Test that accepting user deletion cascades to delete the invitation.
// Per product contract: accepted_by uses ON DELETE CASCADE.
func TestAcceptingUserDeletionCascadesToInvitation(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create and accept an invitation.
	inv, token, err := service.Create(context.Background(), CreateInput{
		Email:     "cascade-deletion@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := service.Accept(context.Background(), AcceptInput{
		Token:        token,
		Username:     "deletableuser",
		PasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$dGVzdHNhbHRzYWx0c2Fs$K0VIUEFlT09CMEMDAwXVxDAwNDAwMDAwMA",
		Language:     "en",
		Country:      "DE",
		TimeZone:     "Europe/Berlin",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify invitation exists with accepted_by set.
	var acceptedByBefore sql.NullString
	if err := database.QueryRow("SELECT accepted_by FROM invitations WHERE id = ?", inv.ID).Scan(&acceptedByBefore); err != nil {
		t.Fatal(err)
	}
	if !acceptedByBefore.Valid || acceptedByBefore.String != account.ID {
		t.Fatalf("expected accepted_by to be %s before deletion, got %v", account.ID, acceptedByBefore)
	}

	// Delete the accepting user - triggers ON DELETE CASCADE.
	if _, err := database.Exec("DELETE FROM users WHERE id = ?", account.ID); err != nil {
		t.Fatalf("failed to delete user: %v", err)
	}

	// Verify invitation was deleted via CASCADE (not SET NULL).
	var invCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM invitations WHERE id = ?", inv.ID).Scan(&invCount); err != nil {
		t.Fatal(err)
	}
	if invCount != 0 {
		t.Fatalf("expected invitation to be deleted via CASCADE, but found %d", invCount)
	}
}

// Finding 1: Test that revoker deletion allows NULL revoked_by.
func TestRevokerDeletionAllowsNullAttribution(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	admin2 := createAdminAccount(t, database, "admin2")
	service := NewService(database, nil)

	// Create an invitation with admin2.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "revoker-deletion@example.test",
		Role:      RoleSupervisor,
		InviterID: admin2.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Revoke the invitation.
	if _, err := service.Delete(context.Background(), inv.ID, admin2.ID, ETag(inv)); err != nil {
		t.Fatal(err)
	}

	// Verify revoked_by is set.
	var revokedByBefore sql.NullString
	if err := database.QueryRow("SELECT revoked_by FROM invitations WHERE id = ?", inv.ID).Scan(&revokedByBefore); err != nil {
		t.Fatal(err)
	}
	if !revokedByBefore.Valid || revokedByBefore.String != admin2.ID {
		t.Fatalf("expected revoked_by to be %s before deletion, got %v", admin2.ID, revokedByBefore)
	}

	// Delete admin2 - triggers ON DELETE SET NULL.
	if _, err := database.Exec("DELETE FROM user_roles WHERE user_id = ?", admin2.ID); err != nil {
		t.Fatalf("failed to delete user roles: %v", err)
	}
	if _, err := database.Exec("DELETE FROM users WHERE id = ?", admin2.ID); err != nil {
		t.Fatalf("failed to delete user: %v", err)
	}

	// Verify revoked_by is now NULL due to ON DELETE SET NULL.
	var revokedByAfter sql.NullString
	if err := database.QueryRow("SELECT revoked_by FROM invitations WHERE id = ?", inv.ID).Scan(&revokedByAfter); err != nil {
		t.Fatal(err)
	}
	if revokedByAfter.Valid {
		t.Fatalf("expected revoked_by to be NULL after user deletion, got %s", revokedByAfter.String)
	}

	// Verify the invitation is still valid with revoked status.
	var status string
	if err := database.QueryRow("SELECT status FROM invitations WHERE id = ?", inv.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "revoked" {
		t.Fatalf("expected status revoked after user deletion, got %s", status)
	}

	_ = admin
}

// Finding 1: Test lifecycle exclusivity - terminal states cannot have other terminal fields.
func TestLifecycleExclusivityOnInsert(t *testing.T) {
	_, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")

	// Try to insert accepted invitation with revoked_at - should fail.
	_, err := database.Exec(`INSERT INTO invitations
		(id, email, email_normalized, role, token_digest, token_generation, status, inviter_id, created_at, updated_at, accepted_at, accepted_by, revoked_at)
		VALUES ('inv_test1', 'test1@example.test', 'test1@example.test', 'supervisor', NULL, 1, 'accepted', NULL, '2024-01-01T00:00:00.000000Z', '2024-01-01T00:00:00.000000Z', '2024-01-01T00:00:00.000000Z', 'u_test', '2024-01-01T00:00:00.000000Z')`)
	if err == nil {
		t.Fatal("expected error when inserting accepted invitation with revoked_at")
	}
	if !strings.Contains(err.Error(), "accepted invitation must not have revoked or faulty fields") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to insert revoked invitation with accepted_at - should fail.
	_, err = database.Exec(`INSERT INTO invitations
		(id, email, email_normalized, role, token_digest, token_generation, status, inviter_id, created_at, updated_at, revoked_at, revoked_by, accepted_at)
		VALUES ('inv_test2', 'test2@example.test', 'test2@example.test', 'supervisor', NULL, 1, 'revoked', NULL, '2024-01-01T00:00:00.000000Z', '2024-01-01T00:00:00.000000Z', '2024-01-01T00:00:00.000000Z', 'u_test', '2024-01-01T00:00:00.000000Z')`)
	if err == nil {
		t.Fatal("expected error when inserting revoked invitation with accepted_at")
	}
	if !strings.Contains(err.Error(), "revoked invitation must not have accepted or faulty fields") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to insert faulty invitation with accepted_by - should fail.
	_, err = database.Exec(`INSERT INTO invitations
		(id, email, email_normalized, role, token_digest, token_generation, status, failure_code, inviter_id, created_at, updated_at, fault_at, accepted_by)
		VALUES ('inv_test3', 'test3@example.test', 'test3@example.test', 'supervisor', NULL, 1, 'faulty', 'smtp_rejected', NULL, '2024-01-01T00:00:00.000000Z', '2024-01-01T00:00:00.000000Z', '2024-01-01T00:00:00.000000Z', 'u_test')`)
	if err == nil {
		t.Fatal("expected error when inserting faulty invitation with accepted_by")
	}
	if !strings.Contains(err.Error(), "faulty invitation must not have accepted or revoked fields") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to insert pending invitation with accepted_by - should fail.
	_, err = database.Exec(`INSERT INTO invitations
		(id, email, email_normalized, role, token_digest, token_generation, status, inviter_id, created_at, updated_at, accepted_by)
		VALUES ('inv_test4', 'test4@example.test', 'test4@example.test', 'supervisor', x'0102030405060708091011121314151617181920212223242526272829303132', 1, 'pending', NULL, '2024-01-01T00:00:00.000000Z', '2024-01-01T00:00:00.000000Z', 'u_test')`)
	if err == nil {
		t.Fatal("expected error when inserting pending invitation with accepted_by")
	}
	if !strings.Contains(err.Error(), "pending invitation must not have") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Finding 2: Test that terminal invitations do not receive stale timeout audits.
func TestTerminalInvitationDoesNotReceiveStaleAudit(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create and accept an invitation.
	inv, token, err := service.Create(context.Background(), CreateInput{
		Email:     "terminal-audit@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	originalGeneration := inv.TokenGeneration

	_, err = service.Accept(context.Background(), AcceptInput{
		Token:        token,
		Username:     "terminaluser",
		PasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$dGVzdHNhbHRzYWx0c2Fs$K0VIUEFlT09CMEMDAwXVxDAwNDAwMDAwMA",
		Language:     "en",
		Country:      "DE",
		TimeZone:     "Europe/Berlin",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Count existing timeout audits.
	var initialCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.delivery_timeout'").Scan(&initialCount); err != nil {
		t.Fatal(err)
	}

	// Simulate stale delivery with matching generation but terminal status.
	manager := NewDeliveryManager(service, database, timeoutMailer{}, nil)
	if !manager.Admit(context.Background(), inv.ID, inv.Email, string(inv.Role), "https://test/invite", originalGeneration) {
		t.Fatal("expected delivery to be admitted")
	}
	manager.Close()

	// Verify no new audit was created for the terminal invitation.
	var newCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.delivery_timeout'").Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if newCount != initialCount {
		t.Fatalf("expected no new audit for terminal invitation, but count changed from %d to %d", initialCount, newCount)
	}
}

// Finding 2: Test revoked invitation race condition.
func TestRevokedInvitationRaceCondition(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create and revoke an invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "revoked-race@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	originalGeneration := inv.TokenGeneration

	if _, err := service.Delete(context.Background(), inv.ID, admin.ID, ETag(inv)); err != nil {
		t.Fatal(err)
	}

	// Count existing timeout audits.
	var initialCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.delivery_timeout'").Scan(&initialCount); err != nil {
		t.Fatal(err)
	}

	// Simulate stale delivery.
	manager := NewDeliveryManager(service, database, timeoutMailer{}, nil)
	if !manager.Admit(context.Background(), inv.ID, inv.Email, string(inv.Role), "https://test/invite", originalGeneration) {
		t.Fatal("expected delivery to be admitted")
	}
	manager.Close()

	// Verify no new audit was created for the revoked invitation.
	var newCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.delivery_timeout'").Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if newCount != initialCount {
		t.Fatalf("expected no new audit for revoked invitation, but count changed from %d to %d", initialCount, newCount)
	}
}

// Finding 2: Test faulty invitation race condition - stale delivery should not audit.
func TestFaultyInvitationRaceCondition(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	service := NewService(database, nil)

	// Create invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "faulty-race@example.test",
		Role:      RoleSupervisor,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	originalGeneration := inv.TokenGeneration

	// Mark invitation as faulty (simulating SMTP rejection).
	if err := service.MarkFaulty(context.Background(), inv.ID,
		httpserver.StableCode(httpserver.CodeInvitationDeliveryRejected)); err != nil {
		t.Fatal(err)
	}

	// Count existing timeout audits.
	var initialCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.delivery_timeout'").Scan(&initialCount); err != nil {
		t.Fatal(err)
	}

	// Simulate stale delivery after invitation became faulty.
	manager := NewDeliveryManager(service, database, timeoutMailer{}, nil)
	if !manager.Admit(context.Background(), inv.ID, inv.Email, string(inv.Role), "https://test/invite", originalGeneration) {
		t.Fatal("expected delivery to be admitted")
	}
	manager.Close()

	// Verify no new audit was created for the faulty invitation.
	var newCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.delivery_timeout'").Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if newCount != initialCount {
		t.Fatalf("expected no new audit for faulty invitation, but count changed from %d to %d", initialCount, newCount)
	}
}

// Finding 3: Test denied role grant is audited.
func TestDeniedRoleGrantIsAudited(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	student := createStudentAccount(t, database, "student")
	csrf, session := loginSession(t, server, "admin", "correct horse battery")

	// Count existing denied audits.
	var initialCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'user.user.role_grant_denied'").Scan(&initialCount); err != nil {
		t.Fatal(err)
	}

	// Try to grant role to student-only account - should fail and be audited.
	response := serveReviewedRoleGrant(t, server, database, "admin", student.ID, csrf, session,
		user.Administrator)
	assertCode(t, response, http.StatusNotFound, "user_not_found")

	// Verify denial was audited.
	var newCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'user.user.role_grant_denied'").Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if newCount != initialCount+1 {
		t.Fatalf("expected denied audit, count changed from %d to %d", initialCount, newCount)
	}
}

// Finding 3: Test denied invitation creation is audited.
func TestDeniedInvitationCreationIsAudited(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	_ = createSupervisorAccount(t, database, "supervisor")
	csrf, session := loginSession(t, server, "supervisor", "correct horse battery")

	// Count existing denied audits.
	var initialCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.creation_denied'").Scan(&initialCount); err != nil {
		t.Fatal(err)
	}

	// Supervisor tries to create admin invitation - should fail and be audited.
	response := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf, session,
		`{"data":{"type":"invitations","attributes":{"email":"test-denied@example.test","role":"administrator"}}}`)
	assertCode(t, response, http.StatusForbidden, "invitation_role_unauthorized")

	// Verify denial was audited.
	var newCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.creation_denied'").Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if newCount != initialCount+1 {
		t.Fatalf("expected denied audit, count changed from %d to %d", initialCount, newCount)
	}
}

// Finding 3: Test denied invitation deletion is audited.
func TestDeniedInvitationDeletionIsAudited(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	_ = createSupervisorAccount(t, database, "supervisor")
	service := NewService(database, nil)

	// Admin creates an admin invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "delete-denied@example.test",
		Role:      RoleAdministrator,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Count existing denied audits.
	var initialCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.revocation_denied'").Scan(&initialCount); err != nil {
		t.Fatal(err)
	}

	// Supervisor tries to delete admin invitation - should fail and be audited.
	csrf, session := loginSession(t, server, "supervisor", "correct horse battery")
	response := serveIfMatch(t, server, "/api/v1/invitations/"+inv.ID, csrf, session, ETag(inv))
	assertCode(t, response, http.StatusNotFound, "invitation_not_found")

	// Verify denial was audited.
	var newCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.revocation_denied'").Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if newCount != initialCount+1 {
		t.Fatalf("expected denied audit, count changed from %d to %d", initialCount, newCount)
	}
}

// Finding 3: Test denied invitation resend is audited.
func TestDeniedInvitationResendIsAudited(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	_ = createSupervisorAccount(t, database, "supervisor")
	service := NewService(database, nil)

	// Admin creates an admin invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "resend-denied@example.test",
		Role:      RoleAdministrator,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Count existing denied audits.
	var initialCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.resend_denied'").Scan(&initialCount); err != nil {
		t.Fatal(err)
	}

	// Supervisor tries to resend admin invitation - should fail and be audited.
	csrf, session := loginSession(t, server, "supervisor", "correct horse battery")
	response := serve(t, server, http.MethodPost, "/api/v1/invitations/"+inv.ID+"/resends", csrf, session, "")
	assertCode(t, response, http.StatusNotFound, "invitation_not_found")

	// Verify denial was audited.
	var newCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'invitation.invitation.resend_denied'").Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if newCount != initialCount+1 {
		t.Fatalf("expected denied audit, count changed from %d to %d", initialCount, newCount)
	}
}

// Finding 4: Test scoped fetch returns same error for missing and out-of-scope IDs.
func TestScopedFetchExistenceEquivalence(t *testing.T) {
	server, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	_ = createSupervisorAccount(t, database, "supervisor")
	service := NewService(database, nil)

	// Admin creates an admin invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "scoped-test@example.test",
		Role:      RoleAdministrator,
		InviterID: admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Supervisor tries to get admin invitation - returns same error as non-existent.
	csrf, session := loginSession(t, server, "supervisor", "correct horse battery")
	response1 := serve(t, server, http.MethodGet, "/api/v1/invitations/"+inv.ID, csrf, session, "")
	assertCode(t, response1, http.StatusNotFound, "invitation_not_found")

	// Supervisor tries to get non-existent invitation - same error.
	response2 := serve(t, server, http.MethodGet, "/api/v1/invitations/inv_nonexistent-id", csrf, session, "")
	assertCode(t, response2, http.StatusNotFound, "invitation_not_found")

	// Verify responses are equivalent (both return same error code).
	if response1.Code != response2.Code {
		t.Fatalf("responses should be equivalent: existing out-of-scope=%d, non-existent=%d", response1.Code, response2.Code)
	}
}

// Finding 5: Test full direct-grant authorization matrix.
// Per product-requirements.md:401-402:
// - An administrator may grant the administrator or supervisor role.
// - Any supervisor may grant the mentor role.
func TestDirectGrantAuthorizationMatrix(t *testing.T) {
	// Test admin grants admin to existing staff.
	t.Run("admin grants admin to supervisor", func(t *testing.T) {
		server, database := testServer(t)
		_ = createAdminAccount(t, database, "admin")
		supervisor := createSupervisorAccount(t, database, "supervisor")
		csrf, session := loginSession(t, server, "admin", "correct horse battery")
		response := serveReviewedRoleGrant(t, server, database, "admin", supervisor.ID, csrf, session,
			user.Administrator)
		assertStatus(t, response, http.StatusNoContent)
	})

	// Test admin grants supervisor to mentor.
	t.Run("admin grants supervisor to mentor", func(t *testing.T) {
		server, database := testServer(t)
		_ = createAdminAccount(t, database, "admin")
		mentor := createMentorAccount(t, database, "mentor")
		csrf, session := loginSession(t, server, "admin", "correct horse battery")
		response := serveReviewedRoleGrant(t, server, database, "admin", mentor.ID, csrf, session,
			user.Supervisor)
		assertStatus(t, response, http.StatusNoContent)
	})

	// Test admin cannot grant mentor role (only supervisors can per product requirements).
	t.Run("admin cannot grant mentor", func(t *testing.T) {
		server, database := testServer(t)
		_ = createAdminAccount(t, database, "admin")
		supervisor := createSupervisorAccount(t, database, "supervisor")
		csrf, session := loginSession(t, server, "admin", "correct horse battery")
		response := serve(t, server, http.MethodPost, "/api/v1/users/"+supervisor.ID+"/roles", csrf, session,
			`{"data":{"type":"user-roles","attributes":{"role":"mentor"}}}`)
		assertCode(t, response, http.StatusForbidden, "user_role_unauthorized")
	})

	// Test supervisor cannot grant admin role.
	t.Run("supervisor cannot grant admin", func(t *testing.T) {
		server, database := testServer(t)
		_ = createAdminAccount(t, database, "admin")
		_ = createSupervisorAccount(t, database, "supervisor")
		mentor := createMentorAccount(t, database, "mentor")
		csrf, session := loginSession(t, server, "supervisor", "correct horse battery")
		response := serve(t, server, http.MethodPost, "/api/v1/users/"+mentor.ID+"/roles", csrf, session,
			`{"data":{"type":"user-roles","attributes":{"role":"administrator"}}}`)
		assertCode(t, response, http.StatusForbidden, "user_role_unauthorized")
	})

	// Test supervisor cannot grant supervisor role.
	t.Run("supervisor cannot grant supervisor", func(t *testing.T) {
		server, database := testServer(t)
		_ = createAdminAccount(t, database, "admin")
		_ = createSupervisorAccount(t, database, "supervisor")
		mentor := createMentorAccount(t, database, "mentor")
		csrf, session := loginSession(t, server, "supervisor", "correct horse battery")
		response := serve(t, server, http.MethodPost, "/api/v1/users/"+mentor.ID+"/roles", csrf, session,
			`{"data":{"type":"user-roles","attributes":{"role":"supervisor"}}}`)
		assertCode(t, response, http.StatusForbidden, "user_role_unauthorized")
	})

	// Test supervisor can grant mentor role.
	t.Run("supervisor grants mentor", func(t *testing.T) {
		server, database := testServer(t)
		_ = createAdminAccount(t, database, "admin")
		_ = createSupervisorAccount(t, database, "supervisor")
		mentor := createMentorAccount(t, database, "mentor")
		csrf, session := loginSession(t, server, "supervisor", "correct horse battery")
		response := serveReviewedMentorGrant(t, server, database, "supervisor", mentor.ID, csrf, session)
		assertStatus(t, response, http.StatusNoContent)
	})
}

// Finding 5: Test unrelated supervisor cannot manage another supervisor's mentor invitation.
func TestUnrelatedSupervisorCannotManageMentorInvitation(t *testing.T) {
	server, database := testServer(t)
	_ = createAdminAccount(t, database, "admin")
	_ = createSupervisorAccount(t, database, "supervisor1")
	_ = createSupervisor2Account(t, database, "supervisor2")
	service := NewService(database, nil)

	// Supervisor1 creates a mentor invitation.
	csrf1, session1 := loginSession(t, server, "supervisor1", "correct horse battery")
	response := serve(t, server, http.MethodPost, "/api/v1/invitations", csrf1, session1,
		`{"data":{"type":"invitations","attributes":{"email":"mentor-test@example.test","role":"mentor"}}}`)
	assertStatus(t, response, http.StatusCreated)
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// Supervisor2 (unrelated) tries to view - should not see it.
	csrf2, session2 := loginSession(t, server, "supervisor2", "correct horse battery")
	response2 := serve(t, server, http.MethodGet, "/api/v1/invitations/"+created.Data.ID, csrf2, session2, "")
	assertCode(t, response2, http.StatusNotFound, "invitation_not_found")

	// Supervisor2 tries to delete - should fail.
	response3 := serveIfMatch(t, server, "/api/v1/invitations/"+created.Data.ID, csrf2, session2,
		response.Header().Get("ETag"))
	assertCode(t, response3, http.StatusNotFound, "invitation_not_found")

	// Supervisor2 tries to resend - should fail.
	response4 := serve(t, server, http.MethodPost, "/api/v1/invitations/"+created.Data.ID+"/resends", csrf2, session2, "")
	assertCode(t, response4, http.StatusNotFound, "invitation_not_found")

	// Admin can manage it.
	csrfAdmin, sessionAdmin := loginSession(t, server, "admin", "correct horse battery")
	responseAdmin := serve(t, server, http.MethodGet, "/api/v1/invitations/"+created.Data.ID, csrfAdmin, sessionAdmin, "")
	assertStatus(t, responseAdmin, http.StatusOK)

	_ = service
}

func createSupervisor2Account(t *testing.T, database *sql.DB, username string) user.Account {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{
			Username:      username,
			Email:         username + "@example.test",
			PasswordHash:  hash,
			Language:      "en",
			Country:       "DE",
			TimeZone:      "UTC",
			EmailVerified: true,
			Roles:         []user.Role{user.Supervisor},
		})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return account
}

// Test inviter_id SET NULL: deleting the inviter preserves the invitation with null inviter.
func TestPendingInvitationPreservedWhenInviterDeleted(t *testing.T) {
	_, database := testServer(t)
	admin := createAdminAccount(t, database, "admin")
	supervisor := createSupervisorAccount(t, database, "inviter-supervisor")
	service := NewService(database, nil)

	// Supervisor creates a mentor invitation.
	inv, _, err := service.Create(context.Background(), CreateInput{
		Email:     "inviter-test@example.test",
		Role:      RoleMentor,
		InviterID: supervisor.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify invitation has inviter_id set.
	var inviterID sql.NullString
	if err := database.QueryRow("SELECT inviter_id FROM invitations WHERE id = ?", inv.ID).Scan(&inviterID); err != nil {
		t.Fatal(err)
	}
	if !inviterID.Valid || inviterID.String != supervisor.ID {
		t.Fatalf("expected inviter_id %s, got %v", supervisor.ID, inviterID)
	}

	// Delete the inviter (ON DELETE SET NULL should preserve invitation).
	if _, err := database.Exec("DELETE FROM users WHERE id = ?", supervisor.ID); err != nil {
		t.Fatal(err)
	}

	// Verify invitation still exists with null inviter_id.
	var invCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM invitations WHERE id = ?", inv.ID).Scan(&invCount); err != nil {
		t.Fatal(err)
	}
	if invCount != 1 {
		t.Fatalf("expected invitation to be preserved, got %d", invCount)
	}

	if err := database.QueryRow("SELECT inviter_id FROM invitations WHERE id = ?", inv.ID).Scan(&inviterID); err != nil {
		t.Fatal(err)
	}
	if inviterID.Valid {
		t.Fatalf("expected null inviter_id after deletion, got %s", inviterID.String)
	}

	_ = admin
}
