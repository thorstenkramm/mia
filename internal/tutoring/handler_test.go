package tutoring

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestTutoringRoutesUseSharedProtocolAndScopeActiveReview(t *testing.T) {
	fixture := newTutoringFixture(t)
	docRoot := filepath.Join(t.TempDir(), "frontend")
	require.NoError(t, os.Mkdir(docRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644))
	server, _, err := httpserver.New(httpserver.Options{DataDir: fixture.directory, DocRoot: docRoot})
	require.NoError(t, err)
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		account, loadErr := user.LoadSecurityState(ctx, fixture.database, id)
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration,
			MustChangePassword: account.MustChangePassword, Banned: account.Banned}, loadErr
	})
	manager := NewManager(fixture.database, fixture.service, nil, fixture.directory, nil)
	Register(server, fixture.service, manager)
	studentSession, studentCSRF := tutoringSession(t, server, fixture.student)
	supervisorSession, _ := tutoringSession(t, server, fixture.supervisor)
	administrator, err := user.Create(context.Background(), fixture.database, user.CreateInput{Username: "admin.only",
		Email: "admin@example.org", EmailVerified: true, PasswordHash: "hash", Language: "en", Country: "US",
		TimeZone: "UTC", Roles: []user.Role{user.Administrator}})
	require.NoError(t, err)
	administratorSession, _ := tutoringSession(t, server, administrator.ID)

	none := tutoringHTTP(server, http.MethodGet, "/api/v1/users/me/active-tutoring-session",
		studentSession, "", "", nil)
	assert.Equal(t, http.StatusOK, none.Code)
	assert.JSONEq(t, `{"data":null}`, none.Body.String())
	assert.Equal(t, "no-store", none.Header().Get("Cache-Control"))
	assert.NotEmpty(t, none.Header().Get("Mia-Session-Idle-Expires-At"))
	for _, cookie := range none.Result().Cookies() {
		assert.NotEqual(t, server.SessionCookieName(), cookie.Name, "discovery must not refresh browser authentication")
		assert.NotEqual(t, server.BrowserCookieName(), cookie.Name, "discovery must not rotate the browser marker")
	}
	forbidden := tutoringHTTP(server, http.MethodGet, "/api/v1/users/me/active-tutoring-session",
		administratorSession, "", "", nil)
	assert.Equal(t, http.StatusForbidden, forbidden.Code)
	assert.Contains(t, forbidden.Body.String(), `"code":"tutoring_unauthorized"`)

	requestID := uuid.NewString()
	body := []byte(`{"data":{"type":"tutoring-sessions","attributes":{"client_request_id":"` + requestID +
		`"},"relationships":{"materials":{"data":[{"type":"materials","id":"` + fixture.materialID + `"}]}}}}`)
	created := tutoringHTTP(server, http.MethodPost, "/api/v1/courses/"+fixture.course+"/tutoring-sessions",
		studentSession, studentCSRF, "application/vnd.api+json", body)
	assert.Equal(t, http.StatusCreated, created.Code)
	assert.Equal(t, "application/vnd.api+json", created.Header().Get("Content-Type"))
	assert.Contains(t, created.Body.String(), `"type":"tutoring-sessions"`)
	discovered := tutoringHTTP(server, http.MethodGet, "/api/v1/users/me/active-tutoring-session",
		studentSession, "", "", nil)
	assert.Equal(t, http.StatusOK, discovered.Code)
	assert.Contains(t, discovered.Body.String(), `"course_id":"`+fixture.course+`"`)
	assert.Contains(t, discovered.Body.String(), `"course_name":"Course A"`)
	assert.NotContains(t, discovered.Body.String(), fixture.materialID)
	replayed := tutoringHTTP(server, http.MethodPost, "/api/v1/courses/"+fixture.course+"/tutoring-sessions",
		studentSession, studentCSRF, "application/vnd.api+json", body)
	assert.Equal(t, http.StatusOK, replayed.Code)

	session, _, err := fixture.service.Start(context.Background(), StartInput{CourseID: fixture.course,
		StudentID: fixture.student, RequestID: requestID, SelectedMaterialIDs: []string{fixture.materialID}})
	require.NoError(t, err)
	activeStatus := tutoringHTTP(server, http.MethodGet, "/api/v1/tutoring-sessions/"+session.ID,
		supervisorSession, "", "", nil)
	assert.Equal(t, http.StatusOK, activeStatus.Code)
	assert.NotContains(t, activeStatus.Body.String(), fixture.materialID)
	activeMessages := tutoringHTTP(server, http.MethodGet, "/api/v1/tutoring-sessions/"+session.ID+"/messages",
		supervisorSession, "", "", nil)
	assert.Equal(t, http.StatusNotFound, activeMessages.Code)
	assert.Contains(t, activeMessages.Body.String(), `"code":"tutoring_not_found"`)

	malformed := tutoringHTTP(server, http.MethodPost, "/api/v1/courses/"+fixture.course+"/tutoring-sessions",
		studentSession, studentCSRF, "application/vnd.api+json",
		[]byte(`{"data":{"type":"tutoring-sessions","attributes":{"client_request_id":"`+requestID+
			`","unknown":true}}}`))
	assert.Equal(t, http.StatusBadRequest, malformed.Code)
	unauthorized := tutoringHTTP(server, http.MethodGet, "/api/v1/tutoring-sessions/"+session.ID, nil, "", "", nil)
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	trailing := tutoringHTTP(server, http.MethodGet, "/api/v1/tutoring-sessions/"+session.ID+"/", studentSession,
		"", "", nil)
	assert.Equal(t, http.StatusNotFound, trailing.Code)
}

func TestActiveSessionDiscoveryDependencyFailureReturnsInternalError(t *testing.T) {
	fixture := newTutoringFixture(t)
	docRoot := filepath.Join(t.TempDir(), "frontend")
	require.NoError(t, os.Mkdir(docRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644))
	server, _, err := httpserver.New(httpserver.Options{DataDir: fixture.directory, DocRoot: docRoot})
	require.NoError(t, err)
	server.SetIdentityLoader(func(context.Context, string) (httpserver.IdentityState, error) {
		return httpserver.IdentityState{SecurityGeneration: 1}, nil
	})
	Register(server, fixture.service, NewManager(fixture.database, fixture.service, nil, fixture.directory, nil))
	sessionCookie, _ := tutoringSession(t, server, fixture.student)
	require.NoError(t, fixture.database.Close())

	response := tutoringHTTP(server, http.MethodGet, "/api/v1/users/me/active-tutoring-session",
		sessionCookie, "", "", nil)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Equal(t, "application/vnd.api+json", response.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.JSONEq(t, `{"errors":[{"status":"500","code":"internal_error","title":"Internal Server Error",`+
		`"detail":"The request could not be completed."}]}`, response.Body.String())
}

func TestTutorResponseEventsReturnSanitizedTerminalSnapshot(t *testing.T) {
	fixture := newTutoringFixture(t)
	docRoot := filepath.Join(t.TempDir(), "frontend")
	require.NoError(t, os.Mkdir(docRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644))
	server, _, err := httpserver.New(httpserver.Options{DataDir: fixture.directory, DocRoot: docRoot})
	require.NoError(t, err)
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		account, loadErr := user.LoadSecurityState(ctx, fixture.database, id)
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration}, loadErr
	})
	manager := NewManager(fixture.database, fixture.service, nil, fixture.directory, nil)
	Register(server, fixture.service, manager)
	sessionCookie, _ := tutoringSession(t, server, fixture.student)
	session, _, err := fixture.service.Start(context.Background(), StartInput{CourseID: fixture.course,
		StudentID: fixture.student, RequestID: uuid.NewString()})
	require.NoError(t, err)
	message, err := fixture.service.Submit(context.Background(), SubmitInput{SessionID: session.ID,
		StudentID: fixture.student, RequestID: uuid.NewString(), Content: "stream"})
	require.NoError(t, err)
	require.NoError(t, fixture.service.Interrupt(context.Background(), message.Response.ID, fixture.student))
	response := tutoringHTTP(server, http.MethodGet, "/api/v1/tutor-responses/"+message.Response.ID+"/events",
		sessionCookie, "", "", nil)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "text/event-stream", response.Header().Get("Content-Type"))
	assert.Contains(t, response.Body.String(), "event: snapshot\n")
	assert.NotContains(t, response.Body.String(), "provider")
	assert.NotEmpty(t, response.Header().Get("Mia-Session-Idle-Expires-At"))
	assert.NotEmpty(t, response.Header().Get("Mia-Session-Absolute-Expires-At"))
	for _, cookie := range response.Result().Cookies() {
		assert.NotEqual(t, server.SessionCookieName(), cookie.Name, "SSE establishment must not refresh idle expiry")
	}
}

func tutoringSession(t *testing.T, server *httpserver.Server, accountID string) ([]*http.Cookie, string) {
	t.Helper()
	path := "/issue-tutoring-" + strings.ReplaceAll(accountID, "_", "-")
	server.Echo.GET(path, func(c *echo.Context) error {
		if err := server.StartSession(c, accountID, 1, "authenticated", time.Now()); err != nil {
			return err
		}
		server.RotateCSRF(c)
		return c.NoContent(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "http://mia.test"+path, nil)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	var cookies []*http.Cookie
	csrf := ""
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.SessionCookieName() {
			cookies = append(cookies, cookie)
		}
		if cookie.Name == server.BrowserCookieName() {
			cookies = append(cookies, cookie)
		}
		if cookie.Name == server.CSRFCookieName() {
			csrf = cookie.Value
		}
	}
	require.Len(t, cookies, 2)
	require.NotEmpty(t, csrf)
	return cookies, csrf
}

func tutoringHTTP(server *httpserver.Server, method, path string, session []*http.Cookie, csrf, contentType string,
	body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://mia.test"+path, bytes.NewReader(body))
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}
