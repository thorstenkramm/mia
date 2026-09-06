package audit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/httpserver/conformance"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

func TestAuditEventsAreAdministratorOnlyAndPaginated(t *testing.T) {
	database, err := miSQLite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, id := range []string{"u_audit_admin", "u_audit_reader"} {
		if _, err := database.Exec(`INSERT INTO users
			(id, username, username_key, password_hash, preferred_language, country, time_zone, created_at)
			VALUES (?, ?, ?, 'hash', 'en', 'DE', 'UTC', '2026-09-06T00:00:00.000000Z')`, id, id, id); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := audit.Write(context.Background(), database, audit.ActionUserProfileUpdated, "u_audit_admin",
			"u_audit_reader"); err != nil {
			t.Fatal(err)
		}
	}
	server := auditServer(t)
	oversight := audit.NewOversight(database,
		func(_ context.Context, _ miSQLite.Querier, actorID string) (bool, error) {
			return actorID == "u_audit_admin", nil
		})
	audit.Register(server, oversight)
	adminSession := auditSession(t, server, "u_audit_admin")
	firstPage := auditRequest(server, "/api/v1/audit-events?page[limit]=1", adminSession)
	if firstPage.Code != http.StatusOK {
		t.Fatalf("audit list status/body = %d %s", firstPage.Code, firstPage.Body.String())
	}
	document := conformance.Document(t, firstPage)
	items, ok := document["data"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("audit list data = %s", firstPage.Body.String())
	}
	conformance.Resource(t, items[0], "audit-events")
	links, ok := document["links"].(map[string]any)
	if !ok || links["next"] == nil {
		t.Fatalf("audit list links = %s", firstPage.Body.String())
	}
	invalid := auditRequest(server, "/api/v1/audit-events?unknown=value", adminSession)
	conformance.Error(t, invalid, http.StatusUnprocessableEntity, "audit_invalid")

	var before int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&before); err != nil {
		t.Fatal(err)
	}
	nonAdminSession := auditSession(t, server, "u_audit_reader")
	denied := auditRequest(server, "/api/v1/audit-events", nonAdminSession)
	conformance.Error(t, denied, http.StatusForbidden, "audit_unauthorized")
	var after int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("denied audit read created an event: before %d after %d", before, after)
	}
}

func auditServer(t *testing.T) *httpserver.Server {
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

func auditSession(t *testing.T, server *httpserver.Server, accountID string) *http.Cookie {
	t.Helper()
	path := "/issue-audit-session-" + accountID
	server.Echo.GET(path, func(c *echo.Context) error {
		return server.StartSession(c, accountID, 1, "authenticated", time.Now())
	})
	request := httptest.NewRequest(http.MethodGet, "http://mia.test"+path, nil)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "__Host-mia_session" {
			return cookie
		}
	}
	t.Fatal("audit session cookie missing")
	return nil
}

func auditRequest(server *httpserver.Server, path string, session *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "http://mia.test"+path, nil)
	request.AddCookie(session)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}
