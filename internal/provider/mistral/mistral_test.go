package mistral

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOCRAllowlistsResponseAndClassifiesFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("missing authorization")
		}
		response.Header().Set("Content-Type", "application/json")
		if _, err := response.Write([]byte(`{"pages":[{"index":0,"markdown":"Grounded text","images":[],"dimensions":{}}],` +
			`"model":"mistral-ocr-4-1","usage_info":{"pages_processed":1,"doc_size_bytes":4},"future_field":"ignored"}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()
	client := New(Options{APIKey: "secret", Endpoint: server.URL, HTTPClient: server.Client()})
	result, err := client.OCR(context.Background(), "image/png", []byte("data"))
	if err != nil || len(result.Pages) != 1 || result.Pages[0].Markdown != "Grounded text" || result.InputUnits != 1 {
		t.Fatalf("OCR = %#v, %v", result, err)
	}

	failureServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Retry-After", "120")
		response.WriteHeader(http.StatusTooManyRequests)
	}))
	defer failureServer.Close()
	_, err = New(Options{Endpoint: failureServer.URL, HTTPClient: failureServer.Client()}).
		OCR(context.Background(), "image/png", []byte("data"))
	var providerError *Error
	if !errors.As(err, &providerError) || !providerError.Retryable || providerError.RetryAfter.Seconds() != 120 {
		t.Fatalf("classified error = %#v, %v", providerError, err)
	}
}
