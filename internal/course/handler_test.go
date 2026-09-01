package course

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
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestCourseHandlersCreateAndBlockActivationUntilMaterialOwnerIsWired(t *testing.T) {
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
	database := courseDatabaseAt(t, dataDir)
	admin := createAccount(t, database, "admin", user.Administrator)
	supervisor := createAccount(t, database, "supervisor", user.Supervisor)
	server, _, err := httpserver.New(httpserver.Options{DataDir: dataDir, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		account, loadErr := user.LoadSecurityState(ctx, database, id)
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration,
			MustChangePassword: account.MustChangePassword, Banned: account.Banned}, loadErr
	})
	materialReady := false
	Register(server, NewService(database, dataDir, nil,
		func(context.Context, miSQLite.Querier, string) (bool, error) { return materialReady, nil }, nil, nil))
	adminSession, adminCSRF := courseSession(t, server, admin)
	body := []byte(`{"data":{"type":"courses","attributes":{"name":"Math","learning_goals":"Learn",` +
		`"ai_tutor_instructions":"Guide","language":"en"},"relationships":{"supervisors":{"data":[` +
		`{"type":"users","id":"` + supervisor + `"}]}}}}`)
	created := courseHTTP(t, server, http.MethodPost, "/api/v1/courses", adminSession, adminCSRF,
		"application/vnd.api+json", body)
	if created.Code != http.StatusCreated {
		t.Fatalf("course create = %d %s", created.Code, created.Body.String())
	}
	var document struct {
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				LogoURL *string `json:"logo_url"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &document); err != nil || document.Data.ID == "" ||
		document.Data.Attributes.LogoURL != nil {
		t.Fatalf("course create document = %#v, %v", document, err)
	}
	supervisorSession, supervisorCSRF := courseSession(t, server, supervisor)
	renameBody := []byte(`{"data":{"type":"courses","attributes":{"name":"Renamed"}}}`)
	deniedRename := courseHTTP(t, server, http.MethodPatch, "/api/v1/courses/"+document.Data.ID,
		supervisorSession, supervisorCSRF, "application/vnd.api+json", renameBody)
	assertCourseError(t, deniedRename, http.StatusForbidden, "course_unauthorized")
	adminRename := courseHTTP(t, server, http.MethodPatch, "/api/v1/courses/"+document.Data.ID,
		adminSession, adminCSRF, "application/vnd.api+json", renameBody)
	if adminRename.Code != http.StatusOK || !bytes.Contains(adminRename.Body.Bytes(), []byte(`"name":"Renamed"`)) {
		t.Fatalf("administrator rename = %d %s", adminRename.Code, adminRename.Body.String())
	}
	activation := courseHTTP(t, server, http.MethodPost, "/api/v1/courses/"+document.Data.ID+"/activations",
		supervisorSession, supervisorCSRF, "", nil)
	assertCourseError(t, activation, http.StatusConflict, "course_activation_unavailable")
	materialReady = true
	activated := courseHTTP(t, server, http.MethodPost, "/api/v1/courses/"+document.Data.ID+"/activations",
		supervisorSession, supervisorCSRF, "", nil)
	if activated.Code != http.StatusOK {
		t.Fatalf("course activation = %d %s", activated.Code, activated.Body.String())
	}
	alreadyActive := courseHTTP(t, server, http.MethodPost, "/api/v1/courses/"+document.Data.ID+"/activations",
		supervisorSession, supervisorCSRF, "", nil)
	assertCourseError(t, alreadyActive, http.StatusConflict, "course_invalid_state")
	activeDeletion := courseHTTP(t, server, http.MethodDelete, "/api/v1/courses/"+document.Data.ID,
		adminSession, adminCSRF, "", nil)
	assertCourseError(t, activeDeletion, http.StatusConflict, "course_invalid_state")
	deactivated := courseHTTP(t, server, http.MethodPost, "/api/v1/courses/"+document.Data.ID+"/deactivations",
		supervisorSession, supervisorCSRF, "", nil)
	if deactivated.Code != http.StatusOK {
		t.Fatalf("course deactivation = %d %s", deactivated.Code, deactivated.Body.String())
	}
	denied := courseHTTP(t, server, http.MethodPost, "/api/v1/courses", supervisorSession, supervisorCSRF,
		"application/vnd.api+json", body)
	assertCourseError(t, denied, http.StatusForbidden, "course_unauthorized")
	lastSupervisor := courseHTTP(t, server, http.MethodDelete,
		"/api/v1/courses/"+document.Data.ID+"/supervisors/"+supervisor, adminSession, adminCSRF, "", nil)
	assertCourseError(t, lastSupervisor, http.StatusConflict, "course_last_supervisor")
	var audits int
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'course.mutation.denied'`).Scan(&audits); err != nil || audits != 6 {
		t.Fatalf("denied mutation audits = %d, %v", audits, err)
	}
	for outcome, expected := range map[string]int{
		"course_unauthorized": 2, "course_activation_unavailable": 1, "course_invalid_state": 2,
		"course_last_supervisor": 1,
	} {
		if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'course.mutation.denied'
			AND json_extract(metadata, '$.outcome_code') = ?`, outcome).Scan(&audits); err != nil || audits != expected {
			t.Fatalf("denied mutation outcome %q count = %d, %v", outcome, audits, err)
		}
	}
	deleted := courseHTTP(t, server, http.MethodDelete, "/api/v1/courses/"+document.Data.ID,
		adminSession, adminCSRF, "", nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("course delete = %d %s", deleted.Code, deleted.Body.String())
	}
	deniedAfterDeletion := courseHTTP(t, server, http.MethodDelete, "/api/v1/courses/"+document.Data.ID,
		adminSession, adminCSRF, "", nil)
	assertCourseError(t, deniedAfterDeletion, http.StatusNotFound, "course_not_found")
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE course_id = ?
		OR COALESCE(metadata, '') LIKE '%' || ? || '%'`, document.Data.ID, document.Data.ID).Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("audit rows identifying deleted course = %d, %v", audits, err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'course.mutation.denied'
		AND json_extract(metadata, '$.outcome_code') = 'course_not_found'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("post-deletion denial audit count = %d, %v", audits, err)
	}
}

func TestCourseLogoHandlersNormalizeAuthorizeAndDelete(t *testing.T) {
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
	database := courseDatabaseAt(t, dataDir)
	admin := createAccount(t, database, "admin", user.Administrator)
	supervisor := createAccount(t, database, "supervisor", user.Supervisor)
	unrelated := createAccount(t, database, "unrelated", user.Supervisor)
	service := NewService(database, dataDir, nil, nil, nil, nil)
	created, err := service.Create(context.Background(), CreateInput{ActorID: admin, SupervisorIDs: []string{supervisor},
		Fields: preparedFields("Literature")})
	if err != nil {
		t.Fatal(err)
	}
	server, _, err := httpserver.New(httpserver.Options{DataDir: dataDir, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		account, loadErr := user.LoadSecurityState(ctx, database, id)
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration,
			MustChangePassword: account.MustChangePassword, Banned: account.Banned}, loadErr
	})
	Register(server, service)
	session, csrf := courseSession(t, server, supervisor)

	invalid := courseHTTP(t, server, http.MethodPut, "/api/v1/courses/"+created.ID+"/logo", session, csrf,
		"image/png", []byte("not png"))
	assertCourseError(t, invalid, http.StatusUnprocessableEntity, "course_logo_invalid")

	source := image.NewNRGBA(image.Rect(0, 0, 800, 400))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	uploaded := courseHTTP(t, server, http.MethodPut, "/api/v1/courses/"+created.ID+"/logo", session, csrf,
		"image/png", encoded.Bytes())
	if uploaded.Code != http.StatusNoContent {
		t.Fatalf("logo upload = %d %s", uploaded.Code, uploaded.Body.String())
	}
	courseAfterUpload := courseHTTP(t, server, http.MethodGet, "/api/v1/courses/"+created.ID, session, csrf, "", nil)
	if courseAfterUpload.Code != http.StatusOK || !bytes.Contains(courseAfterUpload.Body.Bytes(),
		[]byte(`"logo_url":"/api/v1/courses/`+created.ID+`/logo"`)) {
		t.Fatalf("course logo URL after upload = %d %s", courseAfterUpload.Code, courseAfterUpload.Body.String())
	}
	downloaded := courseHTTP(t, server, http.MethodGet, "/api/v1/courses/"+created.ID+"/logo", session, csrf, "", nil)
	if downloaded.Code != http.StatusOK || downloaded.Header().Get("Content-Type") != "image/png" ||
		downloaded.Header().Get("ETag") == "" || downloaded.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("logo download = %d headers=%v", downloaded.Code, downloaded.Header())
	}
	configuration, err := png.DecodeConfig(bytes.NewReader(downloaded.Body.Bytes()))
	if err != nil || configuration.Width != 512 || configuration.Height != 256 {
		t.Fatalf("normalized logo = %dx%d, %v", configuration.Width, configuration.Height, err)
	}
	unrelatedSession, unrelatedCSRF := courseSession(t, server, unrelated)
	denied := courseHTTP(t, server, http.MethodGet, "/api/v1/courses/"+created.ID+"/logo", unrelatedSession,
		unrelatedCSRF, "", nil)
	assertCourseError(t, denied, http.StatusNotFound, "course_not_found")
	deleted := courseHTTP(t, server, http.MethodDelete, "/api/v1/courses/"+created.ID+"/logo", session, csrf, "", nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("logo delete = %d %s", deleted.Code, deleted.Body.String())
	}
}

func courseDatabaseAt(t *testing.T, directory string) *sql.DB {
	t.Helper()
	database, err := miSQLite.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	return database
}

func courseSession(t *testing.T, server *httpserver.Server, accountID string) (*http.Cookie, string) {
	t.Helper()
	path := "/issue-course-" + accountID
	server.Echo.GET(path, func(c *echo.Context) error {
		if err := server.StartSession(c, accountID, 1, "authenticated", time.Now()); err != nil {
			return err
		}
		httpserver.RotateCSRF(c)
		return c.NoContent(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "http://mia.test"+path, nil)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	var session *http.Cookie
	csrf := ""
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "__Host-mia_session" {
			session = cookie
		}
		if cookie.Name == "__Host-mia_csrf" {
			csrf = cookie.Value
		}
	}
	if session == nil || csrf == "" {
		t.Fatal("course session cookies missing")
	}
	return session, csrf
}

func courseHTTP(t *testing.T, server *httpserver.Server, method, path string, session *http.Cookie, csrf,
	contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://mia.test"+path, bytes.NewReader(body))
	request.AddCookie(session)
	request.AddCookie(&http.Cookie{Name: "__Host-mia_csrf", Value: csrf})
	request.Header.Set("X-CSRF-Token", csrf)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func assertCourseError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"`+code+`"`)) {
		t.Fatalf("course error = %d %s", response.Code, response.Body.String())
	}
}
