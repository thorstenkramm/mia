package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/labstack/echo/v5"
)

func TestAPIResponsePolicyAppliesNoStoreToEveryRepresentation(t *testing.T) {
	server := newKernelServer(t)
	tests := []struct {
		name, path, contentType string
		handler                 echo.HandlerFunc
	}{
		{name: "JSON", path: "/api/v1/json", contentType: jsonAPI, handler: func(c *echo.Context) error {
			return JSONAPI(c, http.StatusOK, map[string]any{"data": []any{}})
		}},
		{name: "bodyless", path: "/api/v1/bodyless", handler: func(c *echo.Context) error {
			return c.NoContent(http.StatusNoContent)
		}},
		{name: "binary", path: "/api/v1/binary", contentType: "application/octet-stream", handler: func(c *echo.Context) error {
			c.Response().Header().Set(echo.HeaderCacheControl, "public, max-age=3600")
			return c.Blob(http.StatusOK, "application/octet-stream", []byte("binary"))
		}},
		{name: "NDJSON", path: "/api/v1/ndjson", contentType: "application/x-ndjson", handler: func(c *echo.Context) error {
			return c.Stream(http.StatusOK, "application/x-ndjson", strings.NewReader("{}\n"))
		}},
		{name: "SSE", path: "/api/v1/events", contentType: "text/event-stream", handler: func(c *echo.Context) error {
			return c.Stream(http.StatusOK, "text/event-stream", strings.NewReader("data: {}\n\n"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server.Echo.GET(test.path, test.handler)
			response := httptest.NewRecorder()
			server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Header().Get(echo.HeaderCacheControl) != "no-store" {
				t.Fatalf("Cache-Control = %q", response.Header().Get(echo.HeaderCacheControl))
			}
			if test.contentType != "" && response.Header().Get(echo.HeaderContentType) != test.contentType {
				t.Fatalf("Content-Type = %q", response.Header().Get(echo.HeaderContentType))
			}
		})
	}
}

func TestAPIResponsePolicyOverridesLaterBeforeCallback(t *testing.T) {
	server := newKernelServer(t)
	server.Echo.GET("/api/v1/callback", func(c *echo.Context) error {
		response, err := echo.UnwrapResponse(c.Response())
		if err != nil {
			return err
		}
		response.Before(func() {
			response.Header().Set(echo.HeaderCacheControl, "public, max-age=3600")
		})
		return c.Blob(http.StatusOK, "application/octet-stream", []byte("protected"))
	})

	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/callback", nil))
	if response.Header().Get(echo.HeaderCacheControl) != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get(echo.HeaderCacheControl))
	}
}

func TestAPIResponsePolicyProtectsEveryErrorClass(t *testing.T) {
	server := newKernelServer(t)
	unsafeCause := "arbitrary dependency failure text"
	unsafeFrameworkDetail := "arbitrary framework detail"
	unsafePanicValue := "panic value must not be disclosed"
	server.Echo.GET("/api/v1/domain-error", func(c *echo.Context) error {
		return NewError(CodeUserNotFound)
	})
	server.Echo.GET("/api/v1/dependency-error", func(c *echo.Context) error {
		return errors.New(unsafeCause)
	})
	server.Echo.GET("/api/v1/panic", func(c *echo.Context) error {
		panic(unsafePanicValue)
	})
	server.Echo.GET("/api/v1/wrong-method", func(c *echo.Context) error {
		return echo.NewHTTPError(http.StatusMethodNotAllowed, unsafeFrameworkDetail)
	})

	for _, test := range []struct {
		name, method, path, code string
		status                   int
	}{
		{name: "domain", method: http.MethodGet, path: "/api/v1/domain-error", status: http.StatusNotFound,
			code: string(CodeUserNotFound)},
		{name: "dependency", method: http.MethodGet, path: "/api/v1/dependency-error",
			status: http.StatusInternalServerError, code: string(CodeInternalError)},
		{name: "panic", method: http.MethodGet, path: "/api/v1/panic",
			status: http.StatusInternalServerError, code: string(CodeInternalError)},
		{name: "unknown route", method: http.MethodGet, path: "/api/v1/unknown", status: http.StatusNotFound,
			code: string(CodeNotFound)},
		{name: "unsupported method", method: http.MethodGet, path: "/api/v1/wrong-method",
			status: http.StatusMethodNotAllowed, code: string(CodeMethodNotAllowed)},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.Echo.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != test.status || response.Header().Get(echo.HeaderCacheControl) != "no-store" ||
				response.Header().Get(echo.HeaderContentType) != jsonAPI || len(response.Body.Bytes()) > maxErrorBodyBytes {
				t.Fatalf("error response = %d headers=%v bytes=%d", response.Code, response.Header(), response.Body.Len())
			}
			if !json.Valid(response.Body.Bytes()) || !utf8.Valid(response.Body.Bytes()) {
				t.Fatalf("invalid UTF-8 JSON error body = %q", response.Body.Bytes())
			}
			if !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("unsafe or incorrect error body = %s", response.Body.String())
			}
			for _, unsafeDetail := range []string{unsafeCause, unsafeFrameworkDetail, unsafePanicValue} {
				if strings.Contains(response.Body.String(), unsafeDetail) {
					t.Fatalf("error body disclosed %q: %s", unsafeDetail, response.Body.String())
				}
			}
		})
	}

	static := httptest.NewRecorder()
	server.Echo.ServeHTTP(static, httptest.NewRequest(http.MethodGet, "/", nil))
	if static.Header().Get(echo.HeaderCacheControl) != "" {
		t.Fatalf("frontend Cache-Control = %q", static.Header().Get(echo.HeaderCacheControl))
	}
}
