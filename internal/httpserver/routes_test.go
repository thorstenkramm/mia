package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver/conformance"
)

func newKernelServer(t *testing.T) *Server {
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
	if err := os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, _, err := New(Options{DataDir: dataDir, DocRoot: docRoot})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func issueKernelSession(t *testing.T, server *Server) []*http.Cookie {
	t.Helper()
	server.SetIdentityLoader(func(context.Context, string) (IdentityState, error) {
		return IdentityState{SecurityGeneration: 1}, nil
	})
	server.Echo.GET("/issue-session", func(c *echo.Context) error {
		if err := server.StartSession(c, "user-1", 1, "authenticated", time.Now()); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	})
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://mia.test/issue-session", nil))
	var cookies []*http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.SessionCookieName() || cookie.Name == server.BrowserCookieName() {
			cookies = append(cookies, cookie)
		}
	}
	if len(cookies) != 2 {
		t.Fatal("session issuance did not return session cookies")
	}
	return cookies
}

func TestAcceptNegotiationIsSpecMinimal(t *testing.T) {
	server := newKernelServer(t)
	server.Public(http.MethodGet, "/api/v1/accept-test", func(c *echo.Context) error {
		c.Response().Header().Set(echo.HeaderContentType, jsonAPI)
		return c.JSON(http.StatusOK, map[string]any{"data": map[string]any{
			"type": "accept-tests", "id": "1", "attributes": map[string]any{"name": "value"}}})
	})
	cases := []struct {
		name, accept string
		acceptable   bool
	}{
		{"missing accept", "", true},
		{"wildcard", "*/*", true},
		{"parameterless json api", "application/vnd.api+json", true},
		{"weight is not a media-type parameter", "application/vnd.api+json;q=0.5", true},
		{"one parameterless instance among parameterized", `application/vnd.api+json;profile="p", application/vnd.api+json`, true},
		{"unrelated media type", "text/html", true},
		{"single parameterized instance", "application/vnd.api+json;charset=utf-8", false},
		{"all instances parameterized", `application/vnd.api+json;profile="p", application/vnd.api+json;ext="e"`, false},
		{"quoted comma remains one parameterized instance", `application/vnd.api+json;profile="a,b"`, false},
		{"quoted comma plus parameterless instance", `application/vnd.api+json;profile="a,b", application/vnd.api+json`, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/accept-test", nil)
			if testCase.accept != "" {
				request.Header.Set("Accept", testCase.accept)
			}
			response := httptest.NewRecorder()
			server.Echo.ServeHTTP(response, request)
			if !testCase.acceptable {
				conformance.Error(t, response, http.StatusNotAcceptable, "not_acceptable")
				return
			}
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d %s", response.Code, response.Body.String())
			}
			document := conformance.Document(t, response)
			conformance.Resource(t, document["data"], "accept-tests")
		})
	}
}

func TestAcceptNegotiationExemptsNonJSONRepresentations(t *testing.T) {
	server := newKernelServer(t)
	session := issueKernelSession(t, server)
	server.AuthenticatedRoute(http.MethodGet, "/api/v1/binary-test", RepresentationBinary, func(c *echo.Context) error {
		return c.Blob(http.StatusOK, "image/png", []byte{0x89})
	})
	server.AuthenticatedGET("/api/v1/json-test", func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/binary-test", nil)
	request.Header.Set("Accept", "application/vnd.api+json;charset=utf-8")
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("binary route status = %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/json-test", nil)
	request.Header.Set("Accept", "application/vnd.api+json;charset=utf-8")
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	response = httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	conformance.Error(t, response, http.StatusNotAcceptable, "not_acceptable")
}

func TestAuthenticatedRoutesDoNotConsumePublicLimit(t *testing.T) {
	server := newKernelServer(t)
	session := issueKernelSession(t, server)
	server.AuthenticatedGET("/api/v1/authenticated-test", func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	// The burst budget is 30; forty successful requests prove none consumed it.
	for attempt := 0; attempt < 40; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/authenticated-test", nil)
		request.RemoteAddr = "198.51.100.7:1234"
		for _, cookie := range session {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		server.Echo.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("attempt %d status = %d %s", attempt, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/missing", nil)
	request.RemoteAddr = "198.51.100.7:1234"
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	conformance.Error(t, response, http.StatusNotFound, "not_found")
}

func TestPublicRoutesConsumePublicLimit(t *testing.T) {
	server := newKernelServer(t)
	server.Public(http.MethodGet, "/api/v1/public-test", func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	for attempt := 0; attempt < 30; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/public-test", nil)
		request.RemoteAddr = "198.51.100.8:1234"
		response := httptest.NewRecorder()
		server.Echo.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("attempt %d status = %d %s", attempt, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/public-test", nil)
	request.RemoteAddr = "198.51.100.8:1234"
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	conformance.Error(t, response, http.StatusTooManyRequests, "rate_limited")
	if response.Header().Get("Retry-After") == "" {
		t.Fatal("rate-limit denial did not set Retry-After")
	}
}

func TestPasswordRecoveryRouteStaysExemptFromPublicLimit(t *testing.T) {
	server := newKernelServer(t)
	server.AuthenticationSensitive(http.MethodPost, "/api/v1/auth/password-recovery-requests", func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	for attempt := 0; attempt < 30; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/exhaust", nil)
		request.RemoteAddr = "198.51.100.9:1234"
		response := httptest.NewRecorder()
		server.Echo.ServeHTTP(response, request)
	}
	probe := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/exhaust", nil)
	probe.RemoteAddr = "198.51.100.9:1234"
	probeResponse := httptest.NewRecorder()
	server.Echo.ServeHTTP(probeResponse, probe)
	conformance.Error(t, probeResponse, http.StatusTooManyRequests, "rate_limited")
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/auth/password-recovery-requests", nil)
	request.RemoteAddr = "198.51.100.9:1234"
	request.AddCookie(&http.Cookie{Name: "__Host-mia_csrf", Value: "token"})
	request.Header.Set("X-CSRF-Token", "token")
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("recovery status = %d %s", response.Code, response.Body.String())
	}
	wrongMethod := httptest.NewRequest(http.MethodGet,
		"http://mia.test/api/v1/auth/password-recovery-requests", nil)
	wrongMethod.RemoteAddr = "198.51.100.9:1234"
	wrongMethodResponse := httptest.NewRecorder()
	server.Echo.ServeHTTP(wrongMethodResponse, wrongMethod)
	conformance.Error(t, wrongMethodResponse, http.StatusTooManyRequests, "rate_limited")
}

func TestAuthRegistrarStagesClassifyPublicLimitConsumption(t *testing.T) {
	for _, testCase := range []struct {
		stage        string
		currentStage string
		limited      bool
	}{
		{stage: "mfa", currentStage: "mfa", limited: true},
		{stage: "password-change", currentStage: "password-change", limited: true},
		{stage: "authenticated", currentStage: "authenticated", limited: false},
		{stage: "any", currentStage: "authenticated", limited: false},
	} {
		t.Run(testCase.stage, func(t *testing.T) {
			server := newKernelServer(t)
			server.SetIdentityLoader(func(context.Context, string) (IdentityState, error) {
				return IdentityState{SecurityGeneration: 1,
					MustChangePassword: testCase.currentStage == "password-change"}, nil
			})
			path := "/api/v1/stage-" + testCase.stage
			AuthRouteRegistrar{server: server}.POST(path, testCase.stage, func(c *echo.Context) error {
				return c.NoContent(http.StatusNoContent)
			})
			session := issueKernelStageSession(t, server, testCase.currentStage)
			for attempt := 0; attempt < 31; attempt++ {
				request := httptest.NewRequest(http.MethodPost, "http://mia.test"+path, nil)
				request.RemoteAddr = "198.51.100.10:1234"
				for _, cookie := range session {
					request.AddCookie(cookie)
				}
				request.AddCookie(&http.Cookie{Name: "__Host-mia_csrf", Value: "token"})
				request.Header.Set("X-CSRF-Token", "token")
				response := httptest.NewRecorder()
				server.Echo.ServeHTTP(response, request)
				if attempt == 30 && testCase.limited {
					conformance.Error(t, response, http.StatusTooManyRequests, "rate_limited")
					continue
				}
				if response.Code != http.StatusNoContent {
					t.Fatalf("attempt %d status = %d %s", attempt, response.Code, response.Body.String())
				}
			}
		})
	}
}

func TestUnknownRoutesAndMethodsPrecedeAcceptNegotiation(t *testing.T) {
	server := newKernelServer(t)
	server.Public(http.MethodGet, "/api/v1/known", func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	for _, testCase := range []struct {
		name, method, path string
		status             int
	}{
		{name: "unknown route", method: http.MethodGet, path: "/api/v1/unknown", status: http.StatusNotFound},
		{name: "unsupported method", method: http.MethodPost, path: "/api/v1/known", status: http.StatusMethodNotAllowed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(testCase.method, "http://mia.test"+testCase.path, nil)
			request.Header.Set("Accept", "application/vnd.api+json;charset=utf-8")
			if testCase.method == http.MethodPost {
				request.AddCookie(&http.Cookie{Name: "__Host-mia_csrf", Value: "token"})
				request.Header.Set("X-CSRF-Token", "token")
			}
			response := httptest.NewRecorder()
			server.Echo.ServeHTTP(response, request)
			conformance.Error(t, response, testCase.status, map[int]string{
				http.StatusNotFound: "not_found", http.StatusMethodNotAllowed: "method_not_allowed",
			}[testCase.status])
		})
	}
}

func issueKernelStageSession(t *testing.T, server *Server, stage string) []*http.Cookie {
	t.Helper()
	path := "/issue-stage-" + stage
	server.Echo.GET(path, func(c *echo.Context) error {
		if stage == "mfa" {
			return server.StartMFASession(c, "user-1", 1, "mfc_test", time.Now())
		}
		return server.StartSession(c, "user-1", 1, stage, time.Now())
	})
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://mia.test"+path, nil))
	var cookies []*http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.SessionCookieName() || cookie.Name == server.BrowserCookieName() {
			cookies = append(cookies, cookie)
		}
	}
	if len(cookies) != 2 {
		t.Fatal("stage session cookies missing")
	}
	return cookies
}

func TestTrailingSlashVariantsReturnStrictNotFound(t *testing.T) {
	server := newKernelServer(t)
	server.Public(http.MethodGet, "/api/v1/things", func(c *echo.Context) error {
		c.Response().Header().Set(echo.HeaderContentType, jsonAPI)
		return c.JSON(http.StatusOK, map[string]any{"data": []any{}})
	})
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/things", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("canonical status = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/things/", nil))
	conformance.Error(t, response, http.StatusNotFound, "not_found")
}
