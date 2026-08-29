package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticServingUsesFallbackAndRejectsAPI(t *testing.T) {
	temporary := t.TempDir()
	docRoot := filepath.Join(temporary, "frontend")
	dataDir := filepath.Join(temporary, "data")
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{DataDir: dataDir, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://mia.test/unknown", nil)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "frontend" {
		t.Fatalf("fallback response = %d %q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/missing", nil)
	response = httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || response.Header().Get("Content-Type") != jsonAPI {
		t.Fatalf("API response = %d %q", response.Code, response.Header().Get("Content-Type"))
	}
}

func TestCSRFRequestsAreRateLimitedBeforeRejection(t *testing.T) {
	temporary := t.TempDir()
	docRoot := filepath.Join(temporary, "frontend")
	dataDir := filepath.Join(temporary, "data")
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{DataDir: dataDir, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 30; attempt++ {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/unknown", nil)
		request.RemoteAddr = "198.51.100.1:1234"
		server.Echo.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("attempt %d status = %d", attempt, response.Code)
		}
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/unknown", nil)
	request.RemoteAddr = "198.51.100.1:1234"
	server.Echo.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("exhausted request status = %d", response.Code)
	}
}

func TestStaticServingRejectsUnsafePathsAndMethods(t *testing.T) {
	temporary := t.TempDir()
	docRoot := filepath.Join(temporary, "frontend")
	dataDir := filepath.Join(temporary, "data")
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, ".secret"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(docRoot, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(docRoot, "escape")); err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{DataDir: dataDir, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/.secret", "/dir", "/escape"} {
		response := httptest.NewRecorder()
		server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://mia.test"+path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://mia.test/", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST static status = %d", response.Code)
	}
}

func TestSessionKeyRejectsInsecureRestoredFile(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 64)
	if err := os.WriteFile(filepath.Join(directory, "session.key"), key, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSessionStore(directory); err == nil {
		t.Fatal("loadSessionStore accepted insecure session key")
	}
}

func TestStaticServingAllowsAnchoredRootAndInRootSymlinks(t *testing.T) {
	temporary := t.TempDir()
	actualRoot := filepath.Join(temporary, "actual")
	rootLink := filepath.Join(temporary, "frontend")
	dataDir := filepath.Join(temporary, "data")
	if err := os.Mkdir(actualRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(actualRoot, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actualRoot, "index.html"), []byte("frontend"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actualRoot, "assets", "app.js"), []byte("app"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actualRoot, rootLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("assets", filepath.Join(actualRoot, "linked-assets")); err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{DataDir: dataDir, DocRoot: rootLink})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://mia.test/linked-assets/app.js", nil))
	if response.Code != http.StatusOK || response.Body.String() != "app" {
		t.Fatalf("symlink response = %d %q", response.Code, response.Body.String())
	}
}

func TestStaticDirectoryRequestsDoNotLeakDescriptors(t *testing.T) {
	if _, err := os.ReadDir("/dev/fd"); err != nil {
		t.Skip("descriptor inspection is unavailable")
	}
	temporary := t.TempDir()
	docRoot := filepath.Join(temporary, "frontend")
	dataDir := filepath.Join(temporary, "data")
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(docRoot, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{DataDir: dataDir, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	for requestNumber := 0; requestNumber < 200; requestNumber++ {
		response := httptest.NewRecorder()
		server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://mia.test/directory", nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("request %d status = %d", requestNumber, response.Code)
		}
	}
	after, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) > len(before)+3 {
		t.Fatalf("descriptor count grew from %d to %d", len(before), len(after))
	}
}

func TestStaticNestedMissingComponentDoesNotLeakDescriptors(t *testing.T) {
	if _, err := os.ReadDir("/dev/fd"); err != nil {
		t.Skip("descriptor inspection is unavailable")
	}
	temporary := t.TempDir()
	docRoot := filepath.Join(temporary, "frontend")
	dataDir := filepath.Join(temporary, "data")
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(docRoot, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{DataDir: dataDir, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	for requestNumber := 0; requestNumber < 200; requestNumber++ {
		response := httptest.NewRecorder()
		server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://mia.test/nested/missing/file.js", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("request %d status = %d", requestNumber, response.Code)
		}
	}
	after, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) > len(before)+3 {
		t.Fatalf("descriptor count grew from %d to %d", len(before), len(after))
	}
}
