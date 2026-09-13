package httpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAuthErrorCodesUseGlobalPrefix(t *testing.T) {
	for _, code := range []Code{CodeInvalidCredentials, CodeInvalidRequest, CodeInvalidPassword, CodeUnauthenticated, CodePasswordChangeRequired, CodeLoginThrottled} {
		if len(code) < 5 || string(code[:5]) != "auth_" {
			t.Fatalf("auth code = %q", code)
		}
	}
}

func TestBoundedErrorDocumentUsesCompleteGenericReplacement(t *testing.T) {
	body, err := boundedErrorDocument(CodeUserProfileInvalid, definition{
		status: 422,
		title:  strings.Repeat("unsafe-title", maxErrorBodyBytes),
		detail: strings.Repeat("unsafe-detail", maxErrorBodyBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > maxErrorBodyBytes {
		t.Fatalf("error body length = %d", len(body))
	}
	if !json.Valid(body) || !utf8.Valid(body) {
		t.Fatalf("error body is not valid UTF-8 JSON: %q", body)
	}
	var document errorDocument
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Errors) != 1 || document.Errors[0].Status != "422" ||
		document.Errors[0].Code != string(CodeUserProfileInvalid) ||
		document.Errors[0].Title != genericErrorTitle || document.Errors[0].Detail != genericErrorDetail {
		t.Fatalf("generic error document = %#v", document)
	}
}

func TestBoundedErrorDocumentAlwaysProducesValidUTF8(t *testing.T) {
	body, err := boundedErrorDocument(CodeInvalidRequest, definition{
		status: 422,
		title:  string([]byte{'x', 0xff}),
		detail: string([]byte{'y', 0xfe}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(body) || !utf8.Valid(body) {
		t.Fatalf("error body is not valid UTF-8 JSON: %q", body)
	}
}
