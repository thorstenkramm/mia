package user

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/httpserver/conformance"
	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/lifecycle"
	"github.com/thorstenkramm/mia/internal/provider/sms"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

func TestAccountDeletionHandlerEnforcesAdministratorAndSafeguards(t *testing.T) {
	server, database, staff, student, dataDir := profileServer(t)
	administrator := handlerAccount(t, database, "deletion-admin", []Role{Administrator}, true)
	RegisterDeletionRoutes(server, NewDeletionService(database, dataDir, &lifecycle.Registry{}, nil))
	staffSession, staffCSRF := issueSession(t, server, staff)
	denied := profileRequest(t, server, http.MethodDelete, "/api/v1/users/"+student,
		staffSession, staffCSRF, "", "")
	conformance.Error(t, denied, http.StatusForbidden, "user_deletion_unauthorized")
	adminSession, adminCSRF := issueSession(t, server, administrator)
	deleted := profileRequest(t, server, http.MethodDelete, "/api/v1/users/"+student,
		adminSession, adminCSRF, "", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("student deletion status/body = %d %s", deleted.Code, deleted.Body.String())
	}
	lastAdministrator := profileRequest(t, server, http.MethodDelete, "/api/v1/users/"+administrator,
		adminSession, adminCSRF, "", "")
	conformance.Error(t, lastAdministrator, http.StatusConflict, "user_last_administrator")
}

func TestProfileHandlersRejectProtectedFieldsAndStudentSelfEdit(t *testing.T) {
	server, database, staff, student, _ := profileServer(t)
	staffSession, csrf := issueSession(t, server, staff)
	valid := profileRequest(t, server, http.MethodPatch, "/api/v1/users/me", staffSession, csrf,
		"application/vnd.api+json", `{"data":{"type":"users","id":"`+staff+`","attributes":{"name":"  New Name  ","country":"us"}}}`)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid PATCH status/body = %d %s", valid.Code, valid.Body.String())
	}
	// Protected fields are not decodable attributes; the shared decoder rejects
	// the unexpected document member as a malformed request.
	protected := profileRequest(t, server, http.MethodPatch, "/api/v1/users/me", staffSession, csrf,
		"application/vnd.api+json", `{"data":{"type":"users","id":"`+staff+`","attributes":{"email":"other@example.test"}}}`)
	assertUserCode(t, protected, http.StatusBadRequest, "malformed_request")
	studentSession, studentCSRF := issueSession(t, server, student)
	denied := profileRequest(t, server, http.MethodPatch, "/api/v1/users/me", studentSession, studentCSRF,
		"application/vnd.api+json", `{"data":{"type":"users","id":"`+student+
			`","attributes":{"tts_voice":"self-selected"}}}`)
	assertUserCode(t, denied, http.StatusForbidden, "user_profile_unauthorized")
	var studentVoice sql.NullString
	if err := database.QueryRow("SELECT tts_voice FROM users WHERE id = ?", student).Scan(&studentVoice); err != nil {
		t.Fatal(err)
	}
	if studentVoice.Valid {
		t.Fatalf("student self-selected TTS voice = %q", studentVoice.String)
	}
	var name string
	if err := database.QueryRow("SELECT name FROM users WHERE id = ?", staff).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "New Name" {
		t.Fatalf("stored profile name = %q", name)
	}
}

func TestPatchProfileRequiresMatchingResourceIdentity(t *testing.T) {
	server, _, staff, _, _ := profileServer(t)
	session, csrf := issueSession(t, server, staff)
	for name, body := range map[string]string{
		"missing_id":    `{"data":{"type":"users","attributes":{"name":"New"}}}`,
		"empty_id":      `{"data":{"type":"users","id":"","attributes":{"name":"New"}}}`,
		"mismatched_id": `{"data":{"type":"users","id":"u_someone-else","attributes":{"name":"New"}}}`,
		"wrong_type":    `{"data":{"type":"accounts","id":"` + staff + `","attributes":{"name":"New"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := profileRequest(t, server, http.MethodPatch, "/api/v1/users/me", session, csrf,
				"application/vnd.api+json", body)
			conformance.Error(t, response, http.StatusUnprocessableEntity, "user_profile_invalid")
		})
	}
	valid := profileRequest(t, server, http.MethodPatch, "/api/v1/users/me", session, csrf,
		"application/vnd.api+json", `{"data":{"type":"users","id":"`+staff+`","attributes":{"name":"Matched"}}}`)
	if valid.Code != http.StatusOK {
		t.Fatalf("matching PATCH status/body = %d %s", valid.Code, valid.Body.String())
	}
	document := conformance.Document(t, valid)
	conformance.Resource(t, document["data"], "users")
}

func TestProfileRoutesClassifyProtocolErrors(t *testing.T) {
	server, _, staff, _, _ := profileServer(t)
	session, csrf := issueSession(t, server, staff)
	unsupported := profileRequest(t, server, http.MethodPatch, "/api/v1/users/me", session, csrf,
		"application/json", `{"data":{"type":"users","id":"`+staff+`","attributes":{}}}`)
	conformance.Error(t, unsupported, http.StatusUnsupportedMediaType, "unsupported_media_type")
	oversized := profileRequest(t, server, http.MethodPatch, "/api/v1/users/me", session, csrf,
		"application/vnd.api+json", strings.Repeat("x", 1<<20+1))
	conformance.Error(t, oversized, http.StatusRequestEntityTooLarge, "request_too_large")
	malformed := profileRequest(t, server, http.MethodPatch, "/api/v1/users/me", session, csrf,
		"application/vnd.api+json", `{"data":`)
	conformance.Error(t, malformed, http.StatusBadRequest, "malformed_request")
}

func TestAvatarHandlersNormalizeServeSafelyAndDeleteIdempotently(t *testing.T) {
	server, _, staff, _, dataDir := profileServer(t)
	session, csrf := issueSession(t, server, staff)
	source := image.NewNRGBA(image.Rect(0, 0, 800, 400))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	upload := profileRequestBytes(t, server, http.MethodPut, "/api/v1/users/me/avatar", session, csrf,
		"image/png", encoded.Bytes())
	if upload.Code != http.StatusNoContent {
		t.Fatalf("avatar PUT status/body = %d %s", upload.Code, upload.Body.String())
	}
	storedPath := filepath.Join(dataDir, "users", staff, "avatar.png")
	info, err := os.Stat(storedPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("avatar mode = %o", info.Mode().Perm())
	}
	download := profileRequest(t, server, http.MethodGet, "/api/v1/users/me/avatar", session, csrf, "", "")
	if download.Code != http.StatusOK || download.Header().Get("Content-Type") != "image/png" ||
		download.Header().Get("Cache-Control") != "private, no-cache" || download.Header().Get("ETag") == "" ||
		download.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("avatar GET = %d headers=%v", download.Code, download.Header())
	}
	configuration, err := png.DecodeConfig(bytes.NewReader(download.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Width != 512 || configuration.Height != 256 {
		t.Fatalf("served avatar dimensions = %dx%d", configuration.Width, configuration.Height)
	}
	conditionalRequest := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/users/me/avatar", nil)
	conditionalRequest.AddCookie(session)
	conditionalRequest.Header.Set("If-None-Match", download.Header().Get("ETag"))
	conditional := httptest.NewRecorder()
	server.Echo.ServeHTTP(conditional, conditionalRequest)
	if conditional.Code != http.StatusNotModified {
		t.Fatalf("conditional avatar status = %d", conditional.Code)
	}
	for range 2 {
		deleted := profileRequest(t, server, http.MethodDelete, "/api/v1/users/me/avatar", session, csrf, "", "")
		if deleted.Code != http.StatusNoContent {
			t.Fatalf("avatar DELETE status = %d", deleted.Code)
		}
	}
	missing := profileRequest(t, server, http.MethodGet, "/api/v1/users/me/avatar", session, csrf, "", "")
	assertUserCode(t, missing, http.StatusNotFound, "not_found")
}

func TestAvatarUploadRejectsDeclaredTypeMismatch(t *testing.T) {
	server, _, staff, _, _ := profileServer(t)
	session, csrf := issueSession(t, server, staff)
	response := profileRequestBytes(t, server, http.MethodPut, "/api/v1/users/me/avatar", session, csrf,
		"image/png", []byte("not a PNG"))
	assertUserCode(t, response, http.StatusUnprocessableEntity, "user_avatar_invalid")
}

func TestAvatarRoutesAreExemptFromJSONAPIAcceptNegotiation(t *testing.T) {
	server, _, staff, _, _ := profileServer(t)
	session, csrf := issueSession(t, server, staff)
	for _, testCase := range []struct {
		method, contentType string
		status              int
	}{
		{method: http.MethodGet, status: http.StatusNotFound},
		{method: http.MethodPut, contentType: "image/png", status: http.StatusUnprocessableEntity},
	} {
		request := httptest.NewRequest(testCase.method, "http://mia.test/api/v1/users/me/avatar",
			strings.NewReader("not an image"))
		request.Header.Set("Accept", `application/vnd.api+json;profile="a,b"`)
		request.AddCookie(session)
		if testCase.method == http.MethodPut {
			request.Header.Set("Content-Type", testCase.contentType)
			request.AddCookie(&http.Cookie{Name: "__Host-mia_csrf", Value: csrf})
			request.Header.Set("X-CSRF-Token", csrf)
		}
		response := httptest.NewRecorder()
		server.Echo.ServeHTTP(response, request)
		if response.Code != testCase.status {
			t.Fatalf("%s status = %d %s", testCase.method, response.Code, response.Body.String())
		}
	}
}

func TestUserCreateResourcesRejectClientGeneratedIDs(t *testing.T) {
	server, _, staff, _, _ := profileServer(t)
	session, csrf := issueSession(t, server, staff)
	response := profileRequest(t, server, http.MethodPost, "/api/v1/users/me/mobile-change-challenges", session, csrf,
		"application/vnd.api+json",
		`{"data":{"type":"mobile-change-challenges","id":"client-id","attributes":{"mobile":"+49123456789"}}}`)
	conformance.Error(t, response, http.StatusUnprocessableEntity, "user_profile_invalid")
}

type handlerSMS struct{ code string }

func (sender *handlerSMS) Send(_ context.Context, _ string, code string) error {
	sender.code = code
	return nil
}

func TestMobileHandlersNeverReturnDestinationOrCode(t *testing.T) {
	sender := &handlerSMS{}
	server, _, staff, _, _ := profileServerWithSender(t, sender)
	session, csrf := issueSession(t, server, staff)
	created := profileRequest(t, server, http.MethodPost, "/api/v1/users/me/mobile-change-challenges", session, csrf,
		"application/vnd.api+json",
		`{"data":{"type":"mobile-change-challenges","attributes":{"mobile":"+49123456789"}}}`)
	if created.Code != http.StatusCreated || sender.code == "" ||
		bytes.Contains(created.Body.Bytes(), []byte(sender.code)) || bytes.Contains(created.Body.Bytes(), []byte("49123456789")) {
		t.Fatalf("mobile challenge response = %d %s", created.Code, created.Body.String())
	}
	var document struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	verified := profileRequest(t, server, http.MethodPost,
		"/api/v1/users/me/mobile-change-challenges/"+document.Data.ID+"/verifications", session, csrf,
		"application/vnd.api+json",
		`{"data":{"type":"mobile-change-verifications","attributes":{"code":"`+sender.code+`"}}}`)
	if verified.Code != http.StatusNoContent {
		t.Fatalf("mobile verification response = %d %s", verified.Code, verified.Body.String())
	}
	profile := profileRequest(t, server, http.MethodGet, "/api/v1/users/me", session, csrf, "", "")
	if bytes.Contains(profile.Body.Bytes(), []byte("49123456789")) {
		t.Fatalf("profile exposed mobile number: %s", profile.Body.String())
	}
}

func profileServer(t *testing.T) (*httpserver.Server, *sql.DB, string, string, string) {
	return profileServerWithSender(t, sms.Unavailable{})
}

func profileServerWithSender(t *testing.T, sender sms.Sender) (*httpserver.Server, *sql.DB, string, string, string) {
	t.Helper()
	root := t.TempDir()
	dataDir, docRoot := filepath.Join(root, "data"), filepath.Join(root, "frontend")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644); err != nil {
		t.Fatal(err)
	}
	database, err := miSQLite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	server, _, err := httpserver.New(httpserver.Options{DataDir: dataDir, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		account, loadErr := LoadSecurityState(ctx, database, id)
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration,
			MustChangePassword: account.MustChangePassword, Banned: account.Banned}, loadErr
	})
	staff := handlerAccount(t, database, "staff", []Role{Mentor}, true)
	student := handlerAccount(t, database, "student", []Role{Student}, false)
	RegisterProfileRoutes(server, NewService(database, dataDir, sender, func(ctx context.Context,
		query miSQLite.Querier, accountID string) error {
		_, invalidateErr := query.ExecContext(ctx,
			"DELETE FROM mfa_enrollments WHERE user_id = ? AND method = 'sms'", accountID)
		return invalidateErr
	}))
	return server, database, staff, student, dataDir
}

func handlerAccount(t *testing.T, database *sql.DB, username string, roles []Role, staff bool) string {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		input := CreateInput{Username: username, PasswordHash: hash, Language: "en", Country: "DE", TimeZone: "UTC", Roles: roles}
		if staff {
			input.Email, input.EmailVerified = username+"@example.test", true
		}
		var createErr error
		account, createErr = Create(context.Background(), tx, input)
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return account.ID
}

func issueSession(t *testing.T, server *httpserver.Server, accountID string) (*http.Cookie, string) {
	t.Helper()
	server.Echo.GET("/issue-"+accountID, func(c *echo.Context) error {
		if err := server.StartSession(c, accountID, 1, "authenticated", time.Now()); err != nil {
			return err
		}
		httpserver.RotateCSRF(c)
		return c.NoContent(http.StatusNoContent)
	})
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://mia.test/issue-"+accountID, nil))
	var session *http.Cookie
	csrf := ""
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case "__Host-mia_session":
			session = cookie
		case "__Host-mia_csrf":
			csrf = cookie.Value
		}
	}
	if session == nil || csrf == "" {
		t.Fatal("session issuance did not return both cookies")
	}
	return session, csrf
}

func profileRequest(t *testing.T, server *httpserver.Server, method, path string, session *http.Cookie, csrf,
	contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	return profileRequestBytes(t, server, method, path, session, csrf, contentType, []byte(body))
}

func profileRequestBytes(t *testing.T, server *httpserver.Server, method, path string, session *http.Cookie, csrf,
	contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://mia.test"+path, bytes.NewReader(body))
	request.AddCookie(session)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if method != http.MethodGet {
		request.AddCookie(&http.Cookie{Name: "__Host-mia_csrf", Value: csrf})
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func assertUserCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status/body = %d %s", response.Code, response.Body.String())
	}
	var document struct {
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Errors) != 1 || document.Errors[0].Code != code {
		t.Fatalf("error body = %s", response.Body.String())
	}
}
