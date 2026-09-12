package mentoring

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestMentoringRoutesUseSharedProtocolAndHideScope(t *testing.T) {
	fixture := newFixture(t)
	docRoot := filepath.Join(t.TempDir(), "frontend")
	require.NoError(t, os.Mkdir(docRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644))
	server, _, err := httpserver.New(httpserver.Options{DataDir: fixture.directory, DocRoot: docRoot})
	require.NoError(t, err)
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		account, loadErr := user.LoadSecurityState(ctx, fixture.database, id)
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration}, loadErr
	})
	Register(server, fixture.service)
	studentCookie, studentCSRF := mentoringCookie(t, server, fixture.student)
	otherCookie, _ := mentoringCookie(t, server, fixture.other)

	requestBody := []byte(`{"data":{"type":"mentoring-sessions","attributes":{"topic":"Fractions"}}}`)
	unavailable := mentoringHTTP(server, http.MethodPost,
		"/api/v1/courses/"+fixture.course+"/mentoring-sessions", studentCookie, studentCSRF,
		"application/vnd.api+json", requestBody)
	assert.Equal(t, http.StatusConflict, unavailable.Code)
	assert.Contains(t, unavailable.Body.String(), `"code":"mentoring_unavailable"`)
	var deniedAudits int
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM audit_events
		WHERE action = 'mentoring.mutation.denied' AND actor_user_id = ?
		AND json_extract(metadata, '$.outcome_code') = 'mentoring_unavailable'`, fixture.student).Scan(&deniedAudits))
	assert.Equal(t, 1, deniedAudits)

	require.NoError(t, fixture.service.AssignCourseMentor(context.Background(), fixture.course, fixture.mentor,
		fixture.supervisor))
	require.NoError(t, fixture.service.AssignStudentMentor(context.Background(), fixture.course, fixture.student,
		fixture.mentor, fixture.supervisor))
	require.NoError(t, user.SetMentoringRequestsAllowed(context.Background(), fixture.database, fixture.student, true))
	require.NoError(t, os.MkdirAll(filepath.Join(fixture.directory, "users", fixture.student), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(fixture.directory, "users", fixture.student, "avatar.png"),
		[]byte("avatar-png"), 0o600))
	_, err = fixture.database.Exec("UPDATE users SET name = 'Student Name', nickname = 'Learner' WHERE id = ?",
		fixture.student)
	require.NoError(t, err)
	mentorCookie, _ := mentoringCookie(t, server, fixture.mentor)
	profilePath := "/api/v1/courses/" + fixture.course + "/mentor-students/" + fixture.student
	profile := mentoringHTTP(server, http.MethodGet, profilePath, mentorCookie, "", "", nil)
	assert.Equal(t, http.StatusOK, profile.Code)
	assert.Contains(t, profile.Body.String(), `"username":"student"`)
	assert.Contains(t, profile.Body.String(), `"name":"Student Name"`)
	assert.Contains(t, profile.Body.String(), `"nickname":"Learner"`)
	assert.Contains(t, profile.Body.String(), `"avatar_url":"`+profilePath+`/avatar"`)
	assert.NotContains(t, profile.Body.String(), `"email"`)
	avatar := mentoringHTTP(server, http.MethodGet, profilePath+"/avatar", mentorCookie, "", "", nil)
	assert.Equal(t, http.StatusOK, avatar.Code)
	assert.Equal(t, "image/png", avatar.Header().Get("Content-Type"))
	assert.Equal(t, "avatar-png", avatar.Body.String())
	require.NoError(t, fixture.service.RemoveStudentMentor(context.Background(), fixture.course, fixture.student,
		fixture.mentor, fixture.supervisor))
	revokedProfile := mentoringHTTP(server, http.MethodGet, profilePath, mentorCookie, "", "", nil)
	assert.Equal(t, http.StatusNotFound, revokedProfile.Code)
	revokedAvatar := mentoringHTTP(server, http.MethodGet, profilePath+"/avatar", mentorCookie, "", "", nil)
	assert.Equal(t, http.StatusNotFound, revokedAvatar.Code)
	require.NoError(t, fixture.service.AssignStudentMentor(context.Background(), fixture.course, fixture.student,
		fixture.mentor, fixture.supervisor))
	created := mentoringHTTP(server, http.MethodPost,
		"/api/v1/courses/"+fixture.course+"/mentoring-sessions", studentCookie, studentCSRF,
		"application/vnd.api+json", requestBody)
	assert.Equal(t, http.StatusCreated, created.Code)
	assert.Equal(t, "application/vnd.api+json", created.Header().Get("Content-Type"))
	assert.Contains(t, created.Body.String(), `"mentor":{"data":null}`)

	var createdDocument struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createdDocument))
	invalidUpdates := []string{
		`{"response":null}`,
		`{"response":12}`,
		`{"scheduled_for":null}`,
		`{"scheduled_for":false}`,
		`{"scheduled_for":"2026-09-06T12:00:00Z","closure_reason":null}`,
		`{"closure_reason":false}`,
	}
	for _, attributes := range invalidUpdates {
		body := []byte(`{"data":{"type":"mentoring-sessions","id":"` + createdDocument.Data.ID +
			`","attributes":` + attributes + `}}`)
		invalid := mentoringHTTP(server, http.MethodPatch, "/api/v1/mentoring-sessions/"+createdDocument.Data.ID,
			studentCookie, studentCSRF, "application/vnd.api+json", body)
		assert.Equal(t, http.StatusUnprocessableEntity, invalid.Code, attributes)
		assert.Contains(t, invalid.Body.String(), `"code":"mentoring_invalid"`, attributes)
	}
	unchanged, err := fixture.service.Get(context.Background(), createdDocument.Data.ID, fixture.student)
	require.NoError(t, err)
	assert.Empty(t, unchanged.Response)
	assert.Nil(t, unchanged.ScheduledFor)
	assert.Empty(t, unchanged.ClosureReason)
	hidden := mentoringHTTP(server, http.MethodGet, "/api/v1/mentoring-sessions/"+createdDocument.Data.ID,
		otherCookie, "", "", nil)
	assert.Equal(t, http.StatusNotFound, hidden.Code)
	assert.Contains(t, hidden.Body.String(), `"code":"mentoring_not_found"`)

	malformed := mentoringHTTP(server, http.MethodPost,
		"/api/v1/courses/"+fixture.course+"/mentoring-sessions", studentCookie, studentCSRF,
		"application/vnd.api+json", []byte(`{"data":{"type":"mentoring-sessions","attributes":{"topic":"x","extra":true}}}`))
	assert.Equal(t, http.StatusBadRequest, malformed.Code)
	trailing := mentoringHTTP(server, http.MethodGet, "/api/v1/mentoring-sessions/"+createdDocument.Data.ID+"/",
		studentCookie, "", "", nil)
	assert.Equal(t, http.StatusNotFound, trailing.Code)
}

func mentoringCookie(t *testing.T, server *httpserver.Server, accountID string) (*http.Cookie, string) {
	t.Helper()
	path := "/issue-mentoring-" + strings.ReplaceAll(accountID, "_", "-")
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
	var session *http.Cookie
	csrf := ""
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == server.SessionCookieName() {
			session = cookie
		}
		if cookie.Name == server.CSRFCookieName() {
			csrf = cookie.Value
		}
	}
	require.NotNil(t, session)
	return session, csrf
}

func mentoringHTTP(server *httpserver.Server, method, path string, session *http.Cookie, csrf, contentType string,
	body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://mia.test"+path, bytes.NewReader(body))
	if session != nil {
		request.AddCookie(session)
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
