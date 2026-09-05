package httpserver

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver/conformance"
)

func TestDecodeJSONAPIClassifiesProtocolFailures(t *testing.T) {
	server := newKernelServer(t)
	type document struct {
		Data struct {
			Type       string `json:"type"`
			Attributes struct {
				Name string `json:"name"`
			} `json:"attributes"`
		} `json:"data"`
		Meta struct {
			Name string `json:"name"`
		} `json:"meta"`
	}
	server.Public(http.MethodPost, "/api/v1/decode-test", func(c *echo.Context) error {
		var request document
		if err := DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	})
	valid := `{"data":{"type":"decode-tests","attributes":{"name":"a"}}}`
	oversized := `{"data":{"type":"decode-tests","attributes":{"name":"` +
		strings.Repeat("a", DefaultRequestBodyLimit) + `"}}}`
	cases := []struct {
		name, contentType, body string
		chunked                 bool
		wantStatus              int
		wantCode                string
	}{
		{"valid document", jsonAPI, valid, false, http.StatusNoContent, ""},
		{"missing content type", "", valid, false, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"plain json content type", "application/json", valid, false, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"charset parameter", "application/vnd.api+json; charset=utf-8", valid, false,
			http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"declared oversized body", jsonAPI, oversized, false, http.StatusRequestEntityTooLarge, "request_too_large"},
		{"chunked oversized body", jsonAPI, oversized, true, http.StatusRequestEntityTooLarge, "request_too_large"},
		{"malformed json", jsonAPI, `{"data":`, false, http.StatusBadRequest, "malformed_request"},
		{"trailing input", jsonAPI, valid + `{}`, false, http.StatusBadRequest, "malformed_request"},
		{"invalid utf-8", jsonAPI, "{\"data\":{\"type\":\"decode-tests\",\"attributes\":{\"name\":\"\xff\"}}}", false,
			http.StatusBadRequest, "malformed_request"},
		{"unpaired surrogate escape", jsonAPI, `{"data":{"type":"decode-tests","attributes":{"name":"\ud800"}}}`, false,
			http.StatusBadRequest, "malformed_request"},
		{"unknown document member", jsonAPI, `{"data":{"type":"decode-tests","attributes":{"name":"a"}},"extra":1}`, false,
			http.StatusBadRequest, "malformed_request"},
		{"duplicate top-level data", jsonAPI, `{"data":{"type":"decode-tests","attributes":{"name":"a"}},"data":{"type":"decode-tests","attributes":{"name":"b"}}}`, false,
			http.StatusBadRequest, "malformed_request"},
		{"duplicate resource member", jsonAPI, `{"data":{"type":"decode-tests","type":"other","attributes":{"name":"a"}}}`, false,
			http.StatusBadRequest, "malformed_request"},
		{"duplicate attribute", jsonAPI, `{"data":{"type":"decode-tests","attributes":{"name":"a","name":"b"}}}`, false,
			http.StatusBadRequest, "malformed_request"},
		{"same key in separate objects", jsonAPI, `{"data":{"type":"decode-tests","attributes":{"name":"a"}},"meta":{"name":"b"}}`, false,
			http.StatusNoContent, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var body io.Reader = strings.NewReader(testCase.body)
			if testCase.chunked {
				// An opaque reader hides the length so the request carries no
				// Content-Length and only the bounded reader can catch it.
				body = struct{ io.Reader }{strings.NewReader(testCase.body)}
			}
			request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/decode-test", body)
			if testCase.contentType != "" {
				request.Header.Set("Content-Type", testCase.contentType)
			}
			request.AddCookie(&http.Cookie{Name: "__Host-mia_csrf", Value: "token"})
			request.Header.Set("X-CSRF-Token", "token")
			response := httptest.NewRecorder()
			server.Echo.ServeHTTP(response, request)
			if testCase.wantCode == "" {
				if response.Code != testCase.wantStatus {
					t.Fatalf("status = %d %s", response.Code, response.Body.String())
				}
				return
			}
			conformance.Error(t, response, testCase.wantStatus, testCase.wantCode)
		})
	}
}

func TestDecodeJSONAPIErrorsExposeStableCodes(t *testing.T) {
	server := newKernelServer(t)
	var decodeErr error
	server.Public(http.MethodPost, "/api/v1/decode-code-test", func(c *echo.Context) error {
		decodeErr = DecodeJSONAPI(c, &struct{}{})
		return decodeErr
	})
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/decode-code-test", strings.NewReader("{"))
	request.Header.Set("Content-Type", "text/plain")
	request.AddCookie(&http.Cookie{Name: "__Host-mia_csrf", Value: "token"})
	request.Header.Set("X-CSRF-Token", "token")
	server.Echo.ServeHTTP(httptest.NewRecorder(), request)
	var domain *Error
	if !errors.As(decodeErr, &domain) || domain.Code() != CodeRequestMediaTypeUnsupported {
		t.Fatalf("decode error = %v", decodeErr)
	}
}
