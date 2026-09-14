// Package conformance provides shared black-box JSON:API protocol assertions
// for route-family tests. Feature tests use it to verify envelope, resource,
// and error structure without duplicating protocol checks.
package conformance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/sessions"
)

// ContentType is the JSON:API media type every document response must use.
const ContentType = "application/vnd.api+json"

// Document asserts the JSON:API response content type and the top-level
// data-XOR-errors contract, returning the decoded document.
func Document(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != ContentType {
		t.Fatalf("response content type = %q, want %q", got, ContentType)
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode JSON:API document: %v (%s)", err, response.Body.String())
	}
	_, hasData := document["data"]
	_, hasErrors := document["errors"]
	if hasData == hasErrors {
		t.Fatalf("document must contain data XOR errors: %s", response.Body.String())
	}
	return document
}

// ExpireSession returns cookies whose encrypted session has an elapsed idle deadline.
func ExpireSession(t *testing.T, store *sessions.CookieStore, sessionName string,
	cookies []*http.Cookie) []*http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://mia.test/", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	stored, err := store.Get(request, sessionName)
	if err != nil {
		t.Fatal(err)
	}
	stored.Values["idle_until"] = int64(1)
	response := httptest.NewRecorder()
	if err := store.Save(request, response, stored); err != nil {
		t.Fatal(err)
	}
	replacement := response.Result().Cookies()[0]
	result := append([]*http.Cookie(nil), cookies...)
	for index, cookie := range result {
		if cookie.Name == sessionName {
			result[index] = replacement
		}
	}
	return result
}

// Resource asserts one resource object carries the expected type, a non-empty
// string id, and an attributes object, returning the resource.
func Resource(t *testing.T, value any, wantType string) map[string]any {
	t.Helper()
	resource, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("resource is not an object: %v", value)
	}
	if gotType, ok := resource["type"].(string); !ok || gotType != wantType {
		t.Fatalf("resource type = %v, want %q", resource["type"], wantType)
	}
	if id, ok := resource["id"].(string); !ok || id == "" {
		t.Fatalf("resource id = %v, want non-empty string", resource["id"])
	}
	if _, ok := resource["attributes"].(map[string]any); !ok {
		t.Fatalf("resource attributes missing or not an object: %v", resource["attributes"])
	}
	return resource
}

// Error asserts a JSON:API error response with the expected HTTP status and
// stable code, and that every error object carries string status, code, title,
// and detail members.
func Error(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d %s, want %d", response.Code, response.Body.String(), status)
	}
	document := Document(t, response)
	list, ok := document["errors"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("errors member missing or empty: %s", response.Body.String())
	}
	for _, entry := range list {
		object, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("error entry is not an object: %v", entry)
		}
		if gotStatus, ok := object["status"].(string); !ok || gotStatus != strconv.Itoa(status) {
			t.Fatalf("error status = %v, want %q", object["status"], strconv.Itoa(status))
		}
		for _, member := range []string{"code", "title", "detail"} {
			if value, ok := object[member].(string); !ok || value == "" {
				t.Fatalf("error %s = %v, want non-empty string", member, object[member])
			}
		}
	}
	first, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("error entry is not an object: %v", list[0])
	}
	if gotCode, ok := first["code"].(string); !ok || gotCode != code {
		t.Fatalf("error code = %v, want %q", first["code"], code)
	}
}
