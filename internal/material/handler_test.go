package material

import (
	"bytes"
	"context"
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
	"github.com/thorstenkramm/mia/internal/httpserver/conformance"
	"github.com/thorstenkramm/mia/internal/jobs"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestMaterialPatchRequiresMatchingResourceIdentity(t *testing.T) {
	server, service, supervisor, _, courseID := materialServer(t)
	created := readyLinkMaterial(t, service, supervisor, courseID, "Reference video")
	session, csrf := materialSession(t, server, supervisor)
	brief, err := json.Marshal(validBrief("Corrected grounded summary"))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"missing_id":    `{"data":{"type":"materials","attributes":{"brief":` + string(brief) + `}}}`,
		"empty_id":      `{"data":{"type":"materials","id":"","attributes":{"brief":` + string(brief) + `}}}`,
		"mismatched_id": `{"data":{"type":"materials","id":"mat_other","attributes":{"brief":` + string(brief) + `}}}`,
		"wrong_type":    `{"data":{"type":"material","id":"` + created.ID + `","attributes":{"brief":` + string(brief) + `}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := materialHTTP(t, server, http.MethodPatch, "/api/v1/materials/"+created.ID, session, csrf,
				"application/vnd.api+json", []byte(body))
			conformance.Error(t, response, http.StatusUnprocessableEntity, "material_invalid")
		})
	}
	valid := materialHTTP(t, server, http.MethodPatch, "/api/v1/materials/"+created.ID, session, csrf,
		"application/vnd.api+json",
		[]byte(`{"data":{"type":"materials","id":"`+created.ID+`","attributes":{"brief":`+string(brief)+`}}}`))
	if valid.Code != http.StatusOK {
		t.Fatalf("matching PATCH = %d %s", valid.Code, valid.Body.String())
	}
	document := conformance.Document(t, valid)
	conformance.Resource(t, document["data"], "materials")
}

func TestMaterialRoutesClassifyProtocolErrorsAndPaginateCollections(t *testing.T) {
	server, service, supervisor, _, courseID := materialServer(t)
	readyLinkMaterial(t, service, supervisor, courseID, "First video")
	readyLinkMaterial(t, service, supervisor, courseID, "Second video")
	session, csrf := materialSession(t, server, supervisor)
	unsupported := materialHTTP(t, server, http.MethodPost, "/api/v1/courses/"+courseID+"/materials",
		session, csrf, "application/json", []byte(`{"data":{"type":"materials","attributes":{}}}`))
	conformance.Error(t, unsupported, http.StatusUnsupportedMediaType, "unsupported_media_type")
	oversized := materialHTTP(t, server, http.MethodPost, "/api/v1/courses/"+courseID+"/materials",
		session, csrf, "application/vnd.api+json", bytes.Repeat([]byte("x"), (1<<20)+1))
	conformance.Error(t, oversized, http.StatusRequestEntityTooLarge, "request_too_large")
	malformed := materialHTTP(t, server, http.MethodPost, "/api/v1/courses/"+courseID+"/materials",
		session, csrf, "application/vnd.api+json", []byte(`{"data":`))
	conformance.Error(t, malformed, http.StatusBadRequest, "malformed_request")
	invalid := materialHTTP(t, server, http.MethodGet, "/api/v1/courses/"+courseID+"/materials?page[limit]=0",
		session, csrf, "", nil)
	conformance.Error(t, invalid, http.StatusUnprocessableEntity, "material_invalid")
	firstPage := materialHTTP(t, server, http.MethodGet, "/api/v1/courses/"+courseID+"/materials?page[limit]=1",
		session, csrf, "", nil)
	document := conformance.Document(t, firstPage)
	data, ok := document["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("first page data = %s", firstPage.Body.String())
	}
	conformance.Resource(t, data[0], "materials")
	meta, ok := document["meta"].(map[string]any)
	if !ok || meta["has_more"] != true {
		t.Fatalf("first page meta = %s", firstPage.Body.String())
	}
	links, ok := document["links"].(map[string]any)
	if !ok || links["next"] == nil {
		t.Fatalf("first page links = %s", firstPage.Body.String())
	}
	if _, exists := links["prev"]; exists {
		t.Fatalf("first page prev link present: %s", firstPage.Body.String())
	}
	empty := materialHTTP(t, server, http.MethodGet,
		"/api/v1/courses/"+courseID+"/materials?page[limit]=1&page[offset]=2", session, csrf, "", nil)
	if !bytes.Contains(empty.Body.Bytes(), []byte(`"data":[]`)) {
		t.Fatalf("empty page data = %s", empty.Body.String())
	}
}

func TestMaterialNonJSONRoutesAreAcceptExemptAndAuthenticatedRoutesDoNotConsumePublicLimit(t *testing.T) {
	server, service, supervisor, _, courseID := materialServer(t)
	created := readyLinkMaterial(t, service, supervisor, courseID, "Route wiring")
	session, csrf := materialSession(t, server, supervisor)
	parameterizedAccept := "application/vnd.api+json;profile=example"

	for _, path := range []string{
		"/api/v1/material-files/missing/download",
		"/api/v1/material-files/missing/content",
	} {
		request := httptest.NewRequest(http.MethodGet, "http://mia.test"+path, nil)
		request.Header.Set("Accept", parameterizedAccept)
		for _, cookie := range session {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		server.Echo.ServeHTTP(response, request)
		conformance.Error(t, response, http.StatusNotFound, "material_not_found")
	}
	uploadRequest := httptest.NewRequest(http.MethodPost,
		"http://mia.test/api/v1/materials/"+created.ID+"/files", strings.NewReader("invalid multipart"))
	uploadRequest.Header.Set("Accept", parameterizedAccept)
	uploadRequest.Header.Set("Content-Type", "multipart/form-data")
	for _, cookie := range session {
		uploadRequest.AddCookie(cookie)
	}
	uploadRequest.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	uploadRequest.Header.Set("X-CSRF-Token", csrf)
	uploadResponse := httptest.NewRecorder()
	server.Echo.ServeHTTP(uploadResponse, uploadRequest)
	conformance.Error(t, uploadResponse, http.StatusUnprocessableEntity, "material_invalid")

	for _, testCase := range []struct {
		name, method, path, contentType string
		body                            []byte
		wantStatus                      int
	}{
		{name: "material JSON route", method: http.MethodGet, path: "/api/v1/materials/" + created.ID,
			wantStatus: http.StatusOK},
		{name: "material upload route", method: http.MethodPost, path: "/api/v1/materials/" + created.ID + "/files",
			contentType: "multipart/form-data", body: []byte("invalid multipart"),
			wantStatus: http.StatusUnprocessableEntity},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			const remote = "198.51.100.21:1234"
			for attempt := 0; attempt < 40; attempt++ {
				request := httptest.NewRequest(testCase.method, "http://mia.test"+testCase.path,
					bytes.NewReader(testCase.body))
				request.RemoteAddr = remote
				for _, cookie := range session {
					request.AddCookie(cookie)
				}
				if testCase.contentType != "" {
					request.Header.Set("Content-Type", testCase.contentType)
					request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
					request.Header.Set("X-CSRF-Token", csrf)
				}
				response := httptest.NewRecorder()
				server.Echo.ServeHTTP(response, request)
				if response.Code != testCase.wantStatus {
					t.Fatalf("attempt %d status = %d %s", attempt, response.Code, response.Body.String())
				}
			}
			probe := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/unregistered", nil)
			probe.RemoteAddr = remote
			probeResponse := httptest.NewRecorder()
			server.Echo.ServeHTTP(probeResponse, probe)
			conformance.Error(t, probeResponse, http.StatusNotFound, "not_found")
		})
	}
}

func TestMaterialFilesCollectionRejectsEveryQueryParameter(t *testing.T) {
	server, service, supervisor, _, courseID := materialServer(t)
	created := readyLinkMaterial(t, service, supervisor, courseID, "Bounded files")
	session, csrf := materialSession(t, server, supervisor)
	for _, query := range []string{"page%5Blimit%5D=1", "unknown=value"} {
		response := materialHTTP(t, server, http.MethodGet,
			"/api/v1/materials/"+created.ID+"/files?"+query, session, csrf, "", nil)
		conformance.Error(t, response, http.StatusBadRequest, "malformed_request")
	}
}

func TestMaterialCreateRejectsClientGeneratedIDAsDomainError(t *testing.T) {
	server, _, supervisor, _, courseID := materialServer(t)
	session, csrf := materialSession(t, server, supervisor)
	response := materialHTTP(t, server, http.MethodPost, "/api/v1/courses/"+courseID+"/materials", session, csrf,
		"application/vnd.api+json", []byte(`{"data":{"type":"materials","id":"client-id","attributes":{}}}`))
	conformance.Error(t, response, http.StatusUnprocessableEntity, "material_invalid")
}

func readyLinkMaterial(t *testing.T, service *Service, supervisor, courseID, name string) Material {
	t.Helper()
	brief := validBrief("Grounded summary")
	created, err := service.Create(context.Background(), CreateInput{CourseID: courseID, ActorID: supervisor,
		Scope: "course-wide", Name: name, Kind: "youtube", Format: "link",
		ExternalURL: "https://www.youtube.com/watch?v=abc", Brief: &brief})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := service.Finalize(context.Background(), created.ID, supervisor)
	if err != nil || ready.State != "ready" {
		t.Fatalf("finalize link material = %#v, %v", ready, err)
	}
	return ready
}

func materialServer(t *testing.T) (*httpserver.Server, *Service, string, string, string) {
	t.Helper()
	service, database, dataDir, supervisor, student, courseID := materialFixture(t, nil)
	docRoot := filepath.Join(t.TempDir(), "frontend")
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644); err != nil {
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
	oversight := jobs.NewOversight(database, func(context.Context, miSQLite.Querier, string) (bool, error) {
		return false, nil
	})
	Register(server, service, oversight)
	return server, service, supervisor, student, courseID
}

func materialSession(t *testing.T, server *httpserver.Server, accountID string) ([]*http.Cookie, string) {
	t.Helper()
	path := "/issue-material-" + strings.ReplaceAll(accountID, "_", "-")
	server.Echo.GET(path, func(c *echo.Context) error {
		if err := server.StartSession(c, accountID, 1, "authenticated", time.Now()); err != nil {
			return err
		}
		server.RotateCSRF(c)
		return c.NoContent(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "http://mia.test"+path, nil)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	var cookies []*http.Cookie
	csrf := ""
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.SessionCookieName() {
			cookies = append(cookies, cookie)
		}
		if cookie.Name == server.BrowserCookieName() {
			cookies = append(cookies, cookie)
		}
		if cookie.Name == server.CSRFCookieName() {
			csrf = cookie.Value
		}
	}
	if len(cookies) != 2 || csrf == "" {
		t.Fatal("material session cookies missing")
	}
	return cookies, csrf
}

func materialHTTP(t *testing.T, server *httpserver.Server, method, path string, session []*http.Cookie, csrf,
	contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://mia.test"+path, bytes.NewReader(body))
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	request.Header.Set("X-CSRF-Token", csrf)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}
