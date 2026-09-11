package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func errorLogServer(t *testing.T) (*Server, *bytes.Buffer) {
	t.Helper()
	temporary := t.TempDir()
	docRoot := filepath.Join(temporary, "frontend")
	dataDir := filepath.Join(temporary, "data")
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	records := &bytes.Buffer{}
	server, _, err := New(Options{DataDir: dataDir, DocRoot: docRoot,
		Logger: slog.New(slog.NewJSONHandler(records, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return server, records
}

// An opaque 500 is only diagnosable if the cause reaches the operator's log, so
// an unmapped handler error must be recorded with the request id that
// correlates it to the request entry.
func TestUnmappedHandlerErrorIsLoggedWithItsCause(t *testing.T) {
	server, records := errorLogServer(t)
	server.Echo.GET("/api/v1/boom", func(*echo.Context) error {
		return errors.New("brief pump failure")
	})
	request := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/boom", nil)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "internal_error") {
		t.Fatalf("body = %s", response.Body.String())
	}
	if !strings.Contains(records.String(), "brief pump failure") {
		t.Fatalf("cause missing from log: %s", records.String())
	}
	if !strings.Contains(records.String(), "unhandled request error") {
		t.Fatalf("error record missing: %s", records.String())
	}
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(records.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["msg"] != "unhandled request error" {
			continue
		}
		found = true
		if record["code"] != "internal_error" {
			t.Errorf("code = %v", record["code"])
		}
		if requestID, ok := record["request_id"].(string); !ok || requestID == "" {
			t.Errorf("request_id = %v", record["request_id"])
		}
	}
	if !found {
		t.Fatal("no unhandled request error record")
	}
}

// Expected client faults are part of normal operation. Recording them as server
// errors would bury the failures that need attention.
func TestClientErrorsAreNotLoggedAsServerFailures(t *testing.T) {
	server, records := errorLogServer(t)
	server.Echo.GET("/api/v1/denied", func(*echo.Context) error {
		return NewError(CodeUnauthenticated)
	})
	request := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/denied", nil)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(records.String(), "unhandled request error") {
		t.Fatalf("client error logged as a server failure: %s", records.String())
	}
}

// The record carries the cause and the correlation id and nothing else. Request
// content, credentials, headers, and cookies must never be added to it.
func TestServerErrorLogCarriesOnlyDiagnosticFields(t *testing.T) {
	server, records := errorLogServer(t)
	server.Echo.GET("/api/v1/boom", func(*echo.Context) error {
		return errors.New("storage unavailable")
	})
	request := httptest.NewRequest(http.MethodGet,
		"http://mia.test/api/v1/boom?token=correct-horse-battery-staple", nil)
	request.Header.Set("Cookie", "__Host-mia_session=secret-session-value")
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)

	permitted := map[string]bool{"time": true, "level": true, "msg": true, "request_id": true,
		"code": true, "error": true}
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(records.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["msg"] != "unhandled request error" {
			continue
		}
		found = true
		for key := range record {
			if !permitted[key] {
				t.Errorf("unexpected field %q in the error record", key)
			}
		}
		if record["error"] != "storage unavailable" {
			t.Errorf("error = %v", record["error"])
		}
	}
	if !found {
		t.Fatalf("no unhandled request error record: %s", records.String())
	}
	if strings.Contains(records.String(), "secret-session-value") ||
		strings.Contains(records.String(), "correct-horse-battery-staple") {
		t.Fatal("request credentials reached the log")
	}
}
