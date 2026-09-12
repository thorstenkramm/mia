package jobs

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/httpserver/conformance"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

func TestJobsListUsesSharedPaginationAndNavigationLinks(t *testing.T) {
	database, courseID := jobsDatabase(t)
	for _, subject := range []string{"mat_one", "mat_two"} {
		if _, err := Enqueue(context.Background(), database, "material-summary", "material", subject,
			courseID, ""); err != nil {
			t.Fatal(err)
		}
	}
	oversight := NewOversight(database, func(_ context.Context, _ miSQLite.Querier, actorID string) (bool, error) {
		return actorID == "u_test", nil
	})
	server := jobsServer(t)
	Register(server, oversight)
	session, csrf := jobsSession(t, server, "u_test")
	invalid := jobsHTTP(t, server, "/api/v1/jobs?page[limit]=0", session, csrf)
	conformance.Error(t, invalid, http.StatusUnprocessableEntity, "job_invalid")
	firstPage := jobsHTTP(t, server, "/api/v1/jobs?page[limit]=1", session, csrf)
	document := conformance.Document(t, firstPage)
	data, ok := document["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("first page data = %s", firstPage.Body.String())
	}
	conformance.Resource(t, data[0], "jobs")
	meta, ok := document["meta"].(map[string]any)
	if !ok || meta["has_more"] != true {
		t.Fatalf("first page meta = %s", firstPage.Body.String())
	}
	links, ok := document["links"].(map[string]any)
	if !ok || links["next"] == nil {
		t.Fatalf("first page links = %s", firstPage.Body.String())
	}
	lastPage := jobsHTTP(t, server, "/api/v1/jobs?page[limit]=1&page[offset]=1", session, csrf)
	lastDocument := conformance.Document(t, lastPage)
	lastLinks, ok := lastDocument["links"].(map[string]any)
	if !ok {
		t.Fatalf("last page links missing: %s", lastPage.Body.String())
	}
	if _, exists := lastLinks["next"]; exists {
		t.Fatalf("last page next link present: %s", lastPage.Body.String())
	}
	if lastLinks["prev"] == nil {
		t.Fatalf("last page prev link missing: %s", lastPage.Body.String())
	}
	empty := jobsHTTP(t, server, "/api/v1/jobs?page[limit]=1&page[offset]=2", session, csrf)
	if !bytes.Contains(empty.Body.Bytes(), []byte(`"data":[]`)) {
		t.Fatalf("empty page data = %s", empty.Body.String())
	}
}

func TestJobsDetailUsesSharedResourceAndErrorDocuments(t *testing.T) {
	database, courseID := jobsDatabase(t)
	job, err := Enqueue(context.Background(), database, "material-summary", "material", "mat_detail", courseID, "")
	if err != nil {
		t.Fatal(err)
	}
	oversight := NewOversight(database, func(_ context.Context, _ miSQLite.Querier, actorID string) (bool, error) {
		return actorID == "u_test", nil
	})
	server := jobsServer(t)
	Register(server, oversight)
	session, csrf := jobsSession(t, server, "u_test")
	response := jobsHTTP(t, server, "/api/v1/jobs/"+job, session, csrf)
	if response.Code != http.StatusOK {
		t.Fatalf("job detail status = %d %s", response.Code, response.Body.String())
	}
	document := conformance.Document(t, response)
	conformance.Resource(t, document["data"], "jobs")
	missing := jobsHTTP(t, server, "/api/v1/jobs/missing", session, csrf)
	conformance.Error(t, missing, http.StatusNotFound, "job_not_found")
}

func jobsServer(t *testing.T) *httpserver.Server {
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
	server, _, err := httpserver.New(httpserver.Options{DataDir: dataDir, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	server.SetIdentityLoader(func(context.Context, string) (httpserver.IdentityState, error) {
		return httpserver.IdentityState{SecurityGeneration: 1}, nil
	})
	return server
}

func jobsSession(t *testing.T, server *httpserver.Server, accountID string) (*http.Cookie, string) {
	t.Helper()
	path := "/issue-jobs-" + accountID
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
		t.Fatal("jobs session cookies missing")
	}
	return session, csrf
}

func jobsHTTP(t *testing.T, server *httpserver.Server, path string, session *http.Cookie,
	csrf string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://mia.test"+path, nil)
	request.AddCookie(session)
	request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}
