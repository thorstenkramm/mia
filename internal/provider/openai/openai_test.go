package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStructuredReturnsOnlyAllowlistedOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if _, err := response.Write([]byte(`{"status":"completed","output":[{"type":"message","status":"completed",` +
			`"role":"assistant","content":[{"type":"output_text","text":"{\"ok\":true}","annotations":[]}]}],` +
			`"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6},"provider_secret":"ignored"}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()
	result, err := New(Options{APIKey: "secret", Model: "test", Endpoint: server.URL,
		HTTPClient: server.Client()}).Structured(context.Background(), "instructions", []string{"input"},
		map[string]any{"type": "object"})
	if err != nil || string(result.JSON) != `{"ok":true}` || result.InputTokens != 4 || result.OutputTokens != 2 {
		t.Fatalf("Structured = %#v, %v", result, err)
	}
}

func TestStructuredRejectsMalformedAndDoesNotRetryPermanentFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	_, err := New(Options{Endpoint: server.URL, HTTPClient: server.Client()}).
		Structured(context.Background(), "instructions", []string{"input"}, map[string]any{"type": "object"})
	var providerError *Error
	if !errors.As(err, &providerError) || providerError.Retryable {
		t.Fatalf("permanent error = %#v, %v", providerError, err)
	}
}
