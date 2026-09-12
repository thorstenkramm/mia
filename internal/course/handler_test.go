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
	"strconv"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/auth"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/httpserver/conformance"
	"github.com/thorstenkramm/mia/internal/identity"
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
	unrelated := createAccount(t, database, "unrelated", user.Supervisor)
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
		func(context.Context, miSQLite.Querier, string) (bool, error) { return materialReady, nil }, nil, nil, nil, nil))
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
	student := createAccount(t, database, "student", user.Student)
	if _, err := database.Exec(`INSERT INTO course_students (id, course_id, student_user_id, joined_at)
		VALUES ('cst_permission', ?, ?, ?)`, document.Data.ID, student, instant(time.Now())); err != nil {
		t.Fatal(err)
	}
	permissionBody := []byte(`{"data":{"type":"users","id":"` + student +
		`","attributes":{"mentoring_requests_allowed":true}}}`)
	permission := courseHTTP(t, server, http.MethodPatch, "/api/v1/users/"+student,
		supervisorSession, supervisorCSRF, "application/vnd.api+json", permissionBody)
	if permission.Code != http.StatusNoContent {
		t.Fatalf("mentoring permission update = %d %s", permission.Code, permission.Body.String())
	}
	allowed, err := user.MentoringRequestsAllowed(context.Background(), database, student)
	if err != nil || !allowed {
		t.Fatalf("mentoring permission = %t, %v", allowed, err)
	}
	voiceBody := []byte(`{"data":{"type":"users","id":"` + student +
		`","attributes":{"tts_voice":"elevenlabs-voice"}}}`)
	voiceResponse := courseHTTP(t, server, http.MethodPatch, "/api/v1/users/"+student,
		supervisorSession, supervisorCSRF, "application/vnd.api+json", voiceBody)
	if voiceResponse.Code != http.StatusNoContent {
		t.Fatalf("TTS voice update = %d %s", voiceResponse.Code, voiceResponse.Body.String())
	}
	voice, err := user.LoadTTSVoice(context.Background(), database, student)
	if err != nil || voice == nil || *voice != "elevenlabs-voice" {
		t.Fatalf("student TTS voice = %v, %v", voice, err)
	}
	var voiceAudits int
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events
		WHERE action = 'user.student_tts_voice.updated' AND actor_user_id = ? AND subject_user_id = ?`,
		supervisor, student).Scan(&voiceAudits); err != nil || voiceAudits != 1 {
		t.Fatalf("student TTS voice audits = %d, %v", voiceAudits, err)
	}
	unrelatedSession, unrelatedCSRF := courseSession(t, server, unrelated)
	deniedPermissionBody := []byte(`{"data":{"type":"users","id":"` + student +
		`","attributes":{"mentoring_requests_allowed":false,"tts_voice":"unauthorized-voice"}}}`)
	deniedPermission := courseHTTP(t, server, http.MethodPatch, "/api/v1/users/"+student,
		unrelatedSession, unrelatedCSRF, "application/vnd.api+json", deniedPermissionBody)
	assertCourseError(t, deniedPermission, http.StatusNotFound, "course_student_not_found")
	allowed, err = user.MentoringRequestsAllowed(context.Background(), database, student)
	if err != nil || !allowed {
		t.Fatalf("mentoring permission after denied update = %t, %v", allowed, err)
	}
	voice, err = user.LoadTTSVoice(context.Background(), database, student)
	if err != nil || voice == nil || *voice != "elevenlabs-voice" {
		t.Fatalf("student TTS voice after denied update = %v, %v", voice, err)
	}
	renameBody := []byte(`{"data":{"type":"courses","id":"` + document.Data.ID +
		`","attributes":{"name":"Renamed"}}}`)
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
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'course.mutation.denied'`).Scan(&audits); err != nil || audits != 7 {
		t.Fatalf("denied mutation audits = %d, %v", audits, err)
	}
	for outcome, expected := range map[string]int{
		"course_unauthorized": 2, "course_activation_unavailable": 1, "course_invalid_state": 2,
		"course_last_supervisor": 1, "course_student_not_found": 1,
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
	service := NewService(database, dataDir, nil, nil, nil, nil, nil, nil)
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

func TestCourseLogoRoutesAreExemptFromJSONAPIAcceptNegotiation(t *testing.T) {
	server, database, dataDir := courseServerFixture(t)
	admin := createAccount(t, database, "acceptadmin", user.Administrator)
	supervisor := createAccount(t, database, "acceptsupervisor", user.Supervisor)
	service := NewService(database, dataDir, nil, nil, nil, nil, nil, nil)
	created, err := service.Create(context.Background(), CreateInput{ActorID: admin,
		SupervisorIDs: []string{supervisor}, Fields: preparedFields("Accept logo")})
	if err != nil {
		t.Fatal(err)
	}
	Register(server, service)
	session, csrf := courseSession(t, server, supervisor)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		request := httptest.NewRequest(method, "http://mia.test/api/v1/courses/"+created.ID+"/logo",
			bytes.NewReader([]byte("not an image")))
		request.Header.Set("Accept", `application/vnd.api+json;profile="a,b"`)
		request.AddCookie(session)
		if method == http.MethodPut {
			request.Header.Set("Content-Type", "image/png")
			request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
			request.Header.Set("X-CSRF-Token", csrf)
		}
		response := httptest.NewRecorder()
		server.Echo.ServeHTTP(response, request)
		want := http.StatusNotFound
		if method == http.MethodPut {
			want = http.StatusUnprocessableEntity
		}
		if response.Code != want {
			t.Fatalf("%s status = %d %s", method, response.Code, response.Body.String())
		}
	}
}

func TestCourseCreateResourcesRejectClientGeneratedIDs(t *testing.T) {
	server, database, dataDir := courseServerFixture(t)
	admin := createAccount(t, database, "idadmin", user.Administrator)
	supervisor := createAccount(t, database, "idsupervisor", user.Supervisor)
	Register(server, NewService(database, dataDir, nil, nil, nil, nil, nil, nil))
	session, csrf := courseSession(t, server, admin)
	for _, testCase := range []struct {
		name, path, body, code string
	}{
		{name: "course", path: "/api/v1/courses", code: "course_invalid",
			body: `{"data":{"type":"courses","id":"client-id","attributes":{"name":"Course"},"relationships":{"supervisors":{"data":[{"type":"users","id":"` + supervisor + `"}]}}}}`},
		{name: "course student", path: "/api/v1/courses/missing/students", code: "course_student_invalid",
			body: `{"data":{"type":"course-students","id":"client-id","attributes":{"mode":"existing","username":"student"}}}`},
		{name: "temporary password", path: "/api/v1/users/missing/temporary-passwords", code: "course_student_invalid",
			body: `{"data":{"type":"temporary-passwords","id":"client-id","attributes":{"password":"a valid temporary password"}}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := courseHTTP(t, server, http.MethodPost, testCase.path, session, csrf,
				"application/vnd.api+json", []byte(testCase.body))
			conformance.Error(t, response, http.StatusUnprocessableEntity, testCase.code)
		})
	}
}

func TestStudentAdministrationHandlersInvalidateCookiesAndHideTargets(t *testing.T) {
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
	staff := createAccount(t, database, "staff", user.Mentor)
	service := NewService(database, dataDir, nil, nil, nil, nil, auth.InvalidateSecurityArtifacts, nil)
	created, err := service.Create(context.Background(), CreateInput{ActorID: admin, SupervisorIDs: []string{supervisor},
		Fields: preparedFields("Chemistry")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE courses SET is_active = 1 WHERE id = ?", created.ID); err != nil {
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
	server.AuthenticatedGET("/api/v1/test/student", func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	supervisorSession, supervisorCSRF := courseSession(t, server, supervisor)
	oversized := courseHTTPUnknownLength(t, server, http.MethodPost, "/api/v1/courses/"+created.ID+"/students",
		supervisorSession, supervisorCSRF, "application/vnd.api+json", bytes.Repeat([]byte("x"), (1<<20)+1))
	conformance.Error(t, oversized, http.StatusRequestEntityTooLarge, "request_too_large")
	provisionBody := []byte(`{"data":{"type":"course-students","attributes":{"mode":"provision",` +
		`"username":"newstudent","temporary_password":"Qz7 first temporary phrase",` +
		`"preferred_language":"en","country":"DE","time_zone":"UTC"}}}`)
	provisioned := courseHTTP(t, server, http.MethodPost, "/api/v1/courses/"+created.ID+"/students",
		supervisorSession, supervisorCSRF, "application/vnd.api+json", provisionBody)
	if provisioned.Code != http.StatusCreated {
		t.Fatalf("student provision = %d %s", provisioned.Code, provisioned.Body.String())
	}
	var document struct {
		Data struct {
			Relationships struct {
				Student struct {
					Data struct {
						ID string `json:"id"`
					} `json:"data"`
				} `json:"student"`
			} `json:"relationships"`
		} `json:"data"`
	}
	if err := json.Unmarshal(provisioned.Body.Bytes(), &document); err != nil ||
		document.Data.Relationships.Student.Data.ID == "" {
		t.Fatalf("provision response = %#v, %v", document, err)
	}
	studentID := document.Data.Relationships.Student.Data.ID
	studentSession, studentCSRF := courseSession(t, server, studentID)
	malformedUnicodeBody := []byte(`{"data":{"type":"temporary-passwords","attributes":` +
		`{"password":"Qz7 malformed \ud800 phrase"}}}`)
	malformedUnicode := courseHTTP(t, server, http.MethodPost, "/api/v1/users/"+studentID+"/temporary-passwords",
		supervisorSession, supervisorCSRF, "application/vnd.api+json", malformedUnicodeBody)
	conformance.Error(t, malformedUnicode, http.StatusBadRequest, "malformed_request")
	var originalHash string
	if err := database.QueryRow("SELECT password_hash FROM users WHERE id = ?", studentID).Scan(&originalHash); err != nil {
		t.Fatal(err)
	}
	matchesOriginal, err := identity.VerifyPassword("Qz7 first temporary phrase", originalHash)
	if err != nil || !matchesOriginal {
		t.Fatalf("password changed after malformed Unicode request: matches=%v err=%v", matchesOriginal, err)
	}
	temporaryBody := []byte(`{"data":{"type":"temporary-passwords","attributes":` +
		`{"password":"Qz7 second temporary phrase"}}}`)
	reset := courseHTTP(t, server, http.MethodPost, "/api/v1/users/"+studentID+"/temporary-passwords",
		supervisorSession, supervisorCSRF, "application/vnd.api+json", temporaryBody)
	if reset.Code != http.StatusNoContent {
		t.Fatalf("temporary password = %d %s", reset.Code, reset.Body.String())
	}
	stale := courseHTTP(t, server, http.MethodGet, "/api/v1/test/student", studentSession, studentCSRF, "", nil)
	assertCourseError(t, stale, http.StatusUnauthorized, "auth_unauthenticated")

	var generation int64
	if err := database.QueryRow("SELECT security_generation FROM users WHERE id = ?", studentID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	freshSession, freshCSRF := courseSessionGeneration(t, server, studentID, generation)
	ban := courseHTTP(t, server, http.MethodPost, "/api/v1/users/"+studentID+"/bans", supervisorSession,
		supervisorCSRF, "", nil)
	if ban.Code != http.StatusNoContent {
		t.Fatalf("student ban = %d %s", ban.Code, ban.Body.String())
	}
	banned := courseHTTP(t, server, http.MethodGet, "/api/v1/test/student", freshSession, freshCSRF, "", nil)
	assertCourseError(t, banned, http.StatusUnauthorized, "auth_unauthenticated")
	var generationAfter int64
	if err := database.QueryRow("SELECT security_generation FROM users WHERE id = ?", studentID).
		Scan(&generationAfter); err != nil || generationAfter != generation {
		t.Fatalf("ban security generation=%d want=%d err=%v", generationAfter, generation, err)
	}
	unban := courseHTTP(t, server, http.MethodDelete, "/api/v1/users/"+studentID+"/bans", supervisorSession,
		supervisorCSRF, "", nil)
	if unban.Code != http.StatusNoContent {
		t.Fatalf("student unban = %d %s", unban.Code, unban.Body.String())
	}
	unknown := courseHTTP(t, server, http.MethodPost, "/api/v1/users/u_missing/bans", supervisorSession,
		supervisorCSRF, "", nil)
	staffResponse := courseHTTP(t, server, http.MethodPost, "/api/v1/users/"+staff+"/bans", supervisorSession,
		supervisorCSRF, "", nil)
	assertCourseError(t, unknown, http.StatusNotFound, "course_student_not_found")
	assertCourseError(t, staffResponse, http.StatusNotFound, "course_student_not_found")
}

func TestCoursePatchRequiresMatchingResourceIdentity(t *testing.T) {
	server, database, dataDir := courseServerFixture(t)
	admin := createAccount(t, database, "admin", user.Administrator)
	supervisor := createAccount(t, database, "supervisor", user.Supervisor)
	service := NewService(database, dataDir, nil, nil, nil, nil, nil, nil)
	created, err := service.Create(context.Background(), CreateInput{ActorID: admin,
		SupervisorIDs: []string{supervisor}, Fields: preparedFields("History")})
	if err != nil {
		t.Fatal(err)
	}
	Register(server, service)
	session, csrf := courseSession(t, server, admin)
	for name, body := range map[string]string{
		"missing_id":    `{"data":{"type":"courses","attributes":{"name":"Renamed"}}}`,
		"empty_id":      `{"data":{"type":"courses","id":"","attributes":{"name":"Renamed"}}}`,
		"mismatched_id": `{"data":{"type":"courses","id":"cou_other","attributes":{"name":"Renamed"}}}`,
		"wrong_type":    `{"data":{"type":"course","id":"` + created.ID + `","attributes":{"name":"Renamed"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := courseHTTP(t, server, http.MethodPatch, "/api/v1/courses/"+created.ID, session, csrf,
				"application/vnd.api+json", []byte(body))
			conformance.Error(t, response, http.StatusUnprocessableEntity, "course_invalid")
		})
	}
	valid := courseHTTP(t, server, http.MethodPatch, "/api/v1/courses/"+created.ID, session, csrf,
		"application/vnd.api+json",
		[]byte(`{"data":{"type":"courses","id":"`+created.ID+`","attributes":{"name":"Renamed"}}}`))
	if valid.Code != http.StatusOK {
		t.Fatalf("matching PATCH = %d %s", valid.Code, valid.Body.String())
	}
	document := conformance.Document(t, valid)
	conformance.Resource(t, document["data"], "courses")
}

func TestCourseCollectionsUseSharedPaginationAndNavigationLinks(t *testing.T) {
	server, database, dataDir := courseServerFixture(t)
	admin := createAccount(t, database, "admin", user.Administrator)
	first := createAccount(t, database, "supone", user.Supervisor)
	second := createAccount(t, database, "suptwo", user.Supervisor)
	service := NewService(database, dataDir, nil, nil, nil, nil, nil, nil)
	created, err := service.Create(context.Background(), CreateInput{ActorID: admin,
		SupervisorIDs: []string{first, second}, Fields: preparedFields("Algebra")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), CreateInput{ActorID: admin,
		SupervisorIDs: []string{first}, Fields: preparedFields("Biology")}); err != nil {
		t.Fatal(err)
	}
	Register(server, service)
	session, csrf := courseSession(t, server, admin)
	invalid := courseHTTP(t, server, http.MethodGet, "/api/v1/courses?page[limit]=abc", session, csrf, "", nil)
	conformance.Error(t, invalid, http.StatusUnprocessableEntity, "course_invalid")
	firstPage := courseHTTP(t, server, http.MethodGet, "/api/v1/courses?page[limit]=1", session, csrf, "", nil)
	assertCollectionPage(t, firstPage, 1, true, false, true)
	supervisorsInvalid := courseHTTP(t, server, http.MethodGet,
		"/api/v1/courses/"+created.ID+"/supervisors?page[limit]=0", session, csrf, "", nil)
	conformance.Error(t, supervisorsInvalid, http.StatusUnprocessableEntity, "course_invalid")
	supervisorsFirst := courseHTTP(t, server, http.MethodGet,
		"/api/v1/courses/"+created.ID+"/supervisors?page[limit]=1", session, csrf, "", nil)
	assertCollectionPage(t, supervisorsFirst, 1, true, false, true)
	supervisorsLast := courseHTTP(t, server, http.MethodGet,
		"/api/v1/courses/"+created.ID+"/supervisors?page[limit]=1&page[offset]=1", session, csrf, "", nil)
	assertCollectionPage(t, supervisorsLast, 1, false, true, false)
	supervisorsEmpty := courseHTTP(t, server, http.MethodGet,
		"/api/v1/courses/"+created.ID+"/supervisors?page[limit]=1&page[offset]=2", session, csrf, "", nil)
	assertCollectionPage(t, supervisorsEmpty, 0, false, true, false)
	if !bytes.Contains(supervisorsEmpty.Body.Bytes(), []byte(`"data":[]`)) {
		t.Fatalf("empty page data = %s", supervisorsEmpty.Body.String())
	}
}

// assertCollectionPage verifies one paginated collection page: item count,
// meta.has_more, and presence of the prev and next navigation links.
func assertCollectionPage(t *testing.T, response *httptest.ResponseRecorder, items int,
	wantNext, wantPrev, wantHasMore bool) map[string]any {
	t.Helper()
	document := conformance.Document(t, response)
	data, ok := document["data"].([]any)
	if !ok || len(data) != items {
		t.Fatalf("collection data = %s, want %d items", response.Body.String(), items)
	}
	meta, ok := document["meta"].(map[string]any)
	if !ok || meta["has_more"] != wantHasMore {
		t.Fatalf("collection meta = %s, want has_more=%v", response.Body.String(), wantHasMore)
	}
	var links map[string]any
	if raw, present := document["links"]; present {
		links, ok = raw.(map[string]any)
		if !ok {
			t.Fatalf("collection links are not an object: %s", response.Body.String())
		}
	}
	if _, exists := links["next"]; exists != wantNext {
		t.Fatalf("collection next link presence = %v, want %v: %s", exists, wantNext, response.Body.String())
	}
	if _, exists := links["prev"]; exists != wantPrev {
		t.Fatalf("collection prev link presence = %v, want %v: %s", exists, wantPrev, response.Body.String())
	}
	return document
}

func courseServerFixture(t *testing.T) (*httpserver.Server, *sql.DB, string) {
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
	database := courseDatabaseAt(t, dataDir)
	server, _, err := httpserver.New(httpserver.Options{DataDir: dataDir, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		account, loadErr := user.LoadSecurityState(ctx, database, id)
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration,
			MustChangePassword: account.MustChangePassword, Banned: account.Banned}, loadErr
	})
	return server, database, dataDir
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
	return courseSessionGeneration(t, server, accountID, 1)
}

func courseSessionGeneration(
	t *testing.T,
	server *httpserver.Server,
	accountID string,
	generation int64,
) (*http.Cookie, string) {
	t.Helper()
	path := "/issue-course-" + accountID + "-" + strconv.FormatInt(generation, 10)
	server.Echo.GET(path, func(c *echo.Context) error {
		if err := server.StartSession(c, accountID, generation, "authenticated", time.Now()); err != nil {
			return err
		}
		server.RotateCSRF(c)
		return c.NoContent(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "http://mia.test"+path, nil)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	var session *http.Cookie
	csrf := ""
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.SessionCookieName() {
			session = cookie
		}
		if cookie.Name == server.CSRFCookieName() {
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
	request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	request.Header.Set("X-CSRF-Token", csrf)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func courseHTTPUnknownLength(t *testing.T, server *httpserver.Server, method, path string, session *http.Cookie, csrf,
	contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://mia.test"+path, bytes.NewReader(body))
	request.ContentLength = -1
	request.AddCookie(session)
	request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Content-Type", contentType)
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
