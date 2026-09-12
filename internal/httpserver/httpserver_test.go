package httpserver

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

func TestCookiePolicyAttributesAndLocalHTTPCookieJarCSRF(t *testing.T) {
	for name, policy := range map[string]CookiePolicy{
		"production HTTPS": {SessionName: "__Host-mia_session", CSRFName: "__Host-mia_csrf", Secure: true},
		"local HTTP":       {SessionName: "mia_session", CSRFName: "mia_csrf"},
	} {
		t.Run(name, func(t *testing.T) {
			temporary := t.TempDir()
			docRoot := filepath.Join(temporary, "frontend")
			if err := os.Mkdir(docRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644); err != nil {
				t.Fatal(err)
			}
			server, _, err := New(Options{DataDir: temporary, DocRoot: docRoot, CookiePolicy: policy})
			if err != nil {
				t.Fatal(err)
			}
			server.SetIdentityLoader(func(context.Context, string) (IdentityState, error) {
				return IdentityState{SecurityGeneration: 1}, nil
			})
			server.Echo.GET("/issue", func(c *echo.Context) error {
				if err := server.StartSession(c, "usr_test", 1, "authenticated", time.Now()); err != nil {
					return err
				}
				server.RotateCSRF(c)
				return c.NoContent(http.StatusNoContent)
			})
			response := httptest.NewRecorder()
			server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://mia.test/issue", nil))
			if len(response.Result().Cookies()) != 2 {
				t.Fatalf("issued cookie count = %d", len(response.Result().Cookies()))
			}
			var session, csrf *http.Cookie
			for _, cookie := range response.Result().Cookies() {
				switch cookie.Name {
				case policy.SessionName:
					session = cookie
				case policy.CSRFName:
					csrf = cookie
				}
			}
			if session == nil || csrf == nil {
				t.Fatal("session or CSRF cookie missing")
			}
			if session.Secure != policy.Secure || csrf.Secure != policy.Secure || !session.HttpOnly || csrf.HttpOnly || session.Path != "/" || csrf.Path != "/" || session.SameSite != http.SameSiteLaxMode || csrf.SameSite != http.SameSiteLaxMode || session.Domain != "" || csrf.Domain != "" {
				t.Fatalf("unexpected cookie attributes: session=%#v csrf=%#v", session, csrf)
			}
			server.AuthenticatedPOST("/api/v1/cookie-refresh", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
			server.AuthenticatedPOST("/api/v1/cookie-clear", func(c *echo.Context) error {
				if err := server.EndSession(c); err != nil {
					return err
				}
				return c.NoContent(http.StatusNoContent)
			})
			for _, path := range []string{"/api/v1/cookie-refresh", "/api/v1/cookie-clear"} {
				request := httptest.NewRequest(http.MethodPost, "https://mia.test"+path, nil)
				request.AddCookie(session)
				request.AddCookie(csrf)
				request.Header.Set("X-CSRF-Token", csrf.Value)
				result := httptest.NewRecorder()
				server.Echo.ServeHTTP(result, request)
				if result.Code != http.StatusNoContent {
					t.Fatalf("%s status = %d", path, result.Code)
				}
				cookies := result.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Name != policy.SessionName || cookies[0].Secure != policy.Secure || !cookies[0].HttpOnly || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteLaxMode {
					t.Fatalf("%s cookies = %#v", path, cookies)
				}
				if path == "/api/v1/cookie-clear" && cookies[0].MaxAge >= 0 {
					t.Fatal("clear did not expire the active session cookie")
				}
			}
			server.Echo.POST("/api/v1/csrf-policy", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
			for _, test := range []struct {
				name, header, fetchSite string
				cookies                 []string
				status                  int
			}{
				{name: "missing", status: http.StatusForbidden},
				{name: "mismatched", header: "wrong", cookies: []string{"token"}, status: http.StatusForbidden},
				{name: "duplicated", header: "token", cookies: []string{"token", "token"}, status: http.StatusForbidden},
				{name: "cross-site", header: "token", fetchSite: "cross-site", cookies: []string{"token"}, status: http.StatusForbidden},
				{name: "matching", header: "token", cookies: []string{"token"}, status: http.StatusNoContent},
			} {
				t.Run(test.name, func(t *testing.T) {
					request := httptest.NewRequest(http.MethodPost, "https://mia.test/api/v1/csrf-policy", nil)
					request.Header.Set("X-CSRF-Token", test.header)
					request.Header.Set("Sec-Fetch-Site", test.fetchSite)
					for _, value := range test.cookies {
						request.AddCookie(&http.Cookie{Name: policy.CSRFName, Value: value})
					}
					result := httptest.NewRecorder()
					server.Echo.ServeHTTP(result, request)
					if result.Code != test.status {
						t.Fatalf("status = %d, want %d", result.Code, test.status)
					}
				})
			}
		})
	}

	temporary := t.TempDir()
	docRoot := filepath.Join(temporary, "frontend")
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, _, err := New(Options{DataDir: temporary, DocRoot: docRoot, CookiePolicy: CookiePolicy{SessionName: "mia_session", CSRFName: "mia_csrf"}})
	if err != nil {
		t.Fatal(err)
	}
	server.SetIdentityLoader(func(context.Context, string) (IdentityState, error) {
		return IdentityState{SecurityGeneration: 1}, nil
	})
	server.Echo.GET("/issue", func(c *echo.Context) error {
		if err := server.StartSession(c, "usr_test", 1, "authenticated", time.Now()); err != nil {
			return err
		}
		server.RotateCSRF(c)
		return c.NoContent(http.StatusNoContent)
	})
	server.AuthenticatedPOST("/api/v1/local-cookie-jar", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	httpServer := httptest.NewServer(server.Echo)
	defer httpServer.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	response, err := client.Get(httpServer.URL + "/issue")
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	var token, session string
	for _, cookie := range jar.Cookies(response.Request.URL) {
		if cookie.Name == "mia_csrf" {
			token = cookie.Value
		}
		if cookie.Name == "mia_session" {
			session = cookie.Value
		}
	}
	if token == "" || session == "" {
		t.Fatal("cookie jar did not retain local session and CSRF cookies")
	}
	request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/api/v1/local-cookie-jar", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-CSRF-Token", token)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("local cookie-jar request status = %d", response.StatusCode)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	request, err = http.NewRequest(http.MethodPost, httpServer.URL+"/api/v1/local-cookie-jar", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-CSRF-Token", token)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site local request status = %d", response.StatusCode)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewRejectsUnsupportedCookiePolicies(t *testing.T) {
	for _, policy := range []CookiePolicy{
		{SessionName: "other_session", CSRFName: "mia_csrf"},
		{SessionName: "__Host-mia_session", CSRFName: "__Host-mia_csrf"},
		{SessionName: "mia_session", CSRFName: "mia_csrf", Secure: true},
	} {
		if _, _, err := New(Options{DataDir: t.TempDir(), CookiePolicy: policy}); err == nil {
			t.Fatalf("unsupported cookie policy accepted: %#v", policy)
		}
	}
}

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
	server, _, err := New(Options{DataDir: dataDir, DocRoot: docRoot})
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
	server, _, err := New(Options{DataDir: dataDir, DocRoot: docRoot})
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
	server, _, err := New(Options{DataDir: dataDir, DocRoot: docRoot})
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
	server, _, err := New(Options{DataDir: dataDir, DocRoot: rootLink})
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
	server, _, err := New(Options{DataDir: dataDir, DocRoot: docRoot})
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
	server, _, err := New(Options{DataDir: dataDir, DocRoot: docRoot})
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
