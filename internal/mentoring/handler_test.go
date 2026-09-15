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
	server := newMentoringServer(t, fixture)
	studentCookie, studentCSRF := mentoringCookie(t, server, fixture.student)
	otherCookie, _ := mentoringCookie(t, server, fixture.other)

	requestBody := []byte(`{"data":{"type":"mentoring-sessions","attributes":{"topic":"Fractions"}}}`)
	eligibilityPath := "/api/v1/courses/" + fixture.course + "/mentoring-request-eligibility"
	disabled := mentoringHTTP(server, http.MethodGet, eligibilityPath, studentCookie, "", "", nil)
	assert.Equal(t, http.StatusOK, disabled.Code)
	assert.Contains(t, disabled.Body.String(), `"state":"disabled"`)
	assert.NotContains(t, disabled.Body.String(), "mentor_id")
	accessLost := mentoringHTTP(server, http.MethodGet, eligibilityPath, otherCookie, "", "", nil)
	assert.Equal(t, http.StatusOK, accessLost.Code)
	assert.Contains(t, accessLost.Body.String(), `"state":"access-lost"`)
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
	allowed := mentoringHTTP(server, http.MethodGet, eligibilityPath, studentCookie, "", "", nil)
	assert.Equal(t, http.StatusOK, allowed.Code)
	assert.Contains(t, allowed.Body.String(), `"state":"allowed"`)
	require.NoError(t, os.MkdirAll(filepath.Join(fixture.directory, "users", fixture.student), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(fixture.directory, "users", fixture.student, "avatar.png"),
		[]byte("avatar-png"), 0o600))
	_, err := fixture.database.Exec("UPDATE users SET name = 'Student Name', nickname = 'Learner' WHERE id = ?",
		fixture.student)
	require.NoError(t, err)
	mentorCookie, mentorCSRF := mentoringCookie(t, server, fixture.mentor)
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
	assert.Equal(t, "no-store", avatar.Header().Get("Cache-Control"))
	assert.Empty(t, avatar.Header().Get("ETag"))
	assert.Equal(t, "avatar-png", avatar.Body.String())
	removalPath := "/api/v1/courses/" + fixture.course + "/students/" + fixture.student + "/mentors/" + fixture.mentor
	supervisorCookie, supervisorCSRF := mentoringCookie(t, server, fixture.supervisor)
	review := mentoringHTTP(server, http.MethodGet, removalPath, supervisorCookie, "", "", nil)
	assert.Equal(t, http.StatusOK, review.Code)
	assert.NotEmpty(t, review.Header().Get("ETag"))
	assert.Contains(t, review.Body.String(), `"direct_reassignment_preserves_open_work":true`)
	missingPrecondition := mentoringHTTP(server, http.MethodDelete, removalPath, supervisorCookie, supervisorCSRF, "", nil)
	assert.Equal(t, http.StatusPreconditionRequired, missingPrecondition.Code)
	validRemoval := mentoringHTTPRequest(server, http.MethodDelete, removalPath, supervisorCookie, supervisorCSRF, "", nil,
		http.Header{"If-Match": []string{review.Header().Get("ETag")}})
	assert.Equal(t, http.StatusNoContent, validRemoval.Code)
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
	assert.Contains(t, created.Body.String(), `"completion_eligibility":{"checked_at":`)

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

	_, err = fixture.service.Update(context.Background(), createdDocument.Data.ID,
		UpdateInput{MentorID: optionalValue(fixture.mentor), ActorID: fixture.supervisor})
	require.NoError(t, err)
	scheduled := fixture.now.Add(time.Hour)
	_, err = fixture.service.Update(context.Background(), createdDocument.Data.ID,
		UpdateInput{ScheduledFor: OptionalTime{Set: true, Value: &scheduled}, ActorID: fixture.mentor})
	require.NoError(t, err)
	completionPath := "/api/v1/mentoring-sessions/" + createdDocument.Data.ID + "/completion-eligibility"
	earlyCompletion := mentoringHTTP(server, http.MethodGet, completionPath, mentorCookie, "", "", nil)
	assert.Equal(t, http.StatusOK, earlyCompletion.Code)
	assert.Contains(t, earlyCompletion.Body.String(), `"state":"not-allowed"`)
	assert.Contains(t, earlyCompletion.Body.String(), `"recheck_after":"2026-09-05T13:00:00.000000Z"`)
	fixture.service.now = func() time.Time { return scheduled }
	allowedCompletion := mentoringHTTP(server, http.MethodGet, completionPath, mentorCookie, "", "", nil)
	assert.Contains(t, allowedCompletion.Body.String(), `"state":"allowed"`)
	completeBody := []byte(`{"data":{"type":"mentoring-sessions","id":"` + createdDocument.Data.ID +
		`","attributes":{"closure_reason":"completed"}}}`)
	completed := mentoringHTTP(server, http.MethodPatch, "/api/v1/mentoring-sessions/"+createdDocument.Data.ID,
		mentorCookie, mentorCSRF, "application/vnd.api+json", completeBody)
	assert.Equal(t, http.StatusOK, completed.Code)
	assert.Contains(t, completed.Body.String(), `"completion_eligibility":{"checked_at":`)
	assert.Contains(t, completed.Body.String(), `"state":"terminal"`)

	malformed := mentoringHTTP(server, http.MethodPost,
		"/api/v1/courses/"+fixture.course+"/mentoring-sessions", studentCookie, studentCSRF,
		"application/vnd.api+json", []byte(`{"data":{"type":"mentoring-sessions","attributes":{"topic":"x","extra":true}}}`))
	assert.Equal(t, http.StatusBadRequest, malformed.Code)
	trailing := mentoringHTTP(server, http.MethodGet, "/api/v1/mentoring-sessions/"+createdDocument.Data.ID+"/",
		studentCookie, "", "", nil)
	assert.Equal(t, http.StatusNotFound, trailing.Code)
}

func TestMentorRemovalRoutesRejectUnreviewedAndNonExactValidatorsWithoutMutation(t *testing.T) {
	routes := []struct {
		name string
		path func(fixture) string
	}{
		{name: "course mentor", path: func(f fixture) string {
			return "/api/v1/courses/" + f.course + "/mentors/" + f.mentor
		}},
		{name: "student mentor", path: func(f fixture) string {
			return "/api/v1/courses/" + f.course + "/students/" + f.student + "/mentors/" + f.mentor
		}},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			fixture := newFixture(t)
			server := newMentoringServer(t, fixture)
			supervisorCookie, supervisorCSRF := mentoringCookie(t, server, fixture.supervisor)
			ctx := context.Background()
			require.NoError(t, fixture.service.AssignCourseMentor(ctx, fixture.course, fixture.mentor,
				fixture.supervisor))
			require.NoError(t, fixture.service.AssignStudentMentor(ctx, fixture.course, fixture.student,
				fixture.mentor, fixture.supervisor))
			require.NoError(t, user.SetMentoringRequestsAllowed(ctx, fixture.database, fixture.student, true))
			created, err := fixture.service.Create(ctx, CreateInput{CourseID: fixture.course, StudentID: fixture.student,
				Topic: "Removal review", ProposedFor: timePointer(fixture.now.Add(30 * time.Minute))})
			require.NoError(t, err)
			_, err = fixture.service.Update(ctx, created.ID,
				UpdateInput{MentorID: optionalValue(fixture.mentor), ActorID: fixture.supervisor})
			require.NoError(t, err)
			scheduled := fixture.now.Add(time.Hour)
			response, instructions, meetingURL := "Prior response", "Original instructions", "https://meet.example.test/a"
			_, err = fixture.service.Update(ctx, created.ID, UpdateInput{ActorID: fixture.mentor,
				Response: optionalValue(response), ScheduledFor: OptionalTime{Set: true, Value: &scheduled},
				MeetingInstructions: optionalValue(instructions), MeetingURL: optionalValue(meetingURL)})
			require.NoError(t, err)

			path := route.path(fixture)
			staleReview := mentoringHTTP(server, http.MethodGet, path, supervisorCookie, "", "", nil)
			require.Equal(t, http.StatusOK, staleReview.Code)
			staleETag := staleReview.Header().Get("ETag")
			require.NotEmpty(t, staleETag)
			_, err = fixture.database.ExecContext(ctx, `UPDATE mentoring_sessions
				SET meeting_instructions = 'Changed after review' WHERE id = ?`, created.ID)
			require.NoError(t, err)
			expected := loadRemovalState(t, fixture, created.ID)

			freshReview := mentoringHTTP(server, http.MethodGet, path, supervisorCookie, "", "", nil)
			require.Equal(t, http.StatusOK, freshReview.Code)
			freshETag := freshReview.Header().Get("ETag")
			require.NotEqual(t, staleETag, freshETag)
			csrfAttempts := []struct {
				name, cookie, header string
			}{
				{name: "missing"},
				{name: "invalid", cookie: supervisorCSRF, header: "not-the-current-token"},
			}
			for _, attempt := range csrfAttempts {
				t.Run("csrf "+attempt.name, func(t *testing.T) {
					auditsBefore := mentoringAuditCount(t, fixture)
					result := mentoringHTTPRequestWithCSRFTokens(server, http.MethodDelete, path, supervisorCookie,
						attempt.cookie, attempt.header, "", nil,
						http.Header{"If-Match": []string{freshETag}})
					assert.Equal(t, http.StatusForbidden, result.Code)
					assert.Contains(t, result.Body.String(), `"code":"csrf_invalid"`)
					assert.Equal(t, expected, loadRemovalState(t, fixture, created.ID))
					assert.Equal(t, auditsBefore, mentoringAuditCount(t, fixture))
				})
			}
			attempts := []struct {
				name       string
				headers    http.Header
				status     int
				stableCode string
			}{
				{name: "missing", status: http.StatusPreconditionRequired,
					stableCode: "mentoring_precondition_required"},
				{name: "stale", headers: http.Header{"If-Match": []string{staleETag}},
					status: http.StatusPreconditionFailed, stableCode: "mentoring_precondition_failed"},
				{name: "weak", headers: http.Header{"If-Match": []string{"W/" + freshETag}},
					status: http.StatusPreconditionFailed, stableCode: "mentoring_precondition_failed"},
				{name: "wildcard", headers: http.Header{"If-Match": []string{"*"}},
					status: http.StatusPreconditionFailed, stableCode: "mentoring_precondition_failed"},
				{name: "multiple", headers: http.Header{"If-Match": []string{freshETag, `"other"`}},
					status: http.StatusPreconditionFailed, stableCode: "mentoring_precondition_failed"},
			}
			for _, attempt := range attempts {
				t.Run(attempt.name, func(t *testing.T) {
					result := mentoringHTTPRequest(server, http.MethodDelete, path, supervisorCookie, supervisorCSRF,
						"", nil, attempt.headers)
					assert.Equal(t, attempt.status, result.Code)
					assert.Contains(t, result.Body.String(), `"code":"`+attempt.stableCode+`"`)
					assert.Equal(t, expected, loadRemovalState(t, fixture, created.ID))
				})
			}
		})
	}
}

func TestMentoringSessionListMapsCompletionProjectionFailureToUnavailable(t *testing.T) {
	fixture := newFixture(t)
	server := newMentoringServer(t, fixture)
	mentorCookie, _ := mentoringCookie(t, server, fixture.mentor)
	ctx := context.Background()
	require.NoError(t, fixture.service.AssignCourseMentor(ctx, fixture.course, fixture.mentor, fixture.supervisor))
	require.NoError(t, fixture.service.AssignStudentMentor(ctx, fixture.course, fixture.student, fixture.mentor,
		fixture.supervisor))
	require.NoError(t, user.SetMentoringRequestsAllowed(ctx, fixture.database, fixture.student, true))
	created, err := fixture.service.Create(ctx, CreateInput{CourseID: fixture.course, StudentID: fixture.student,
		Topic: "Projection failure"})
	require.NoError(t, err)
	_, err = fixture.service.Update(ctx, created.ID,
		UpdateInput{MentorID: optionalValue(fixture.mentor), ActorID: fixture.supervisor})
	require.NoError(t, err)

	requestContext, cancel := context.WithCancel(context.Background())
	fixture.service.now = func() time.Time {
		cancel()
		return fixture.now
	}
	request := httptest.NewRequest(http.MethodGet,
		"http://mia.test/api/v1/courses/"+fixture.course+"/mentoring-sessions", nil).WithContext(requestContext)
	for _, cookie := range mentorCookie {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)

	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"mentoring_state_unavailable"`)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

type removalState struct {
	CourseAssignments, StudentAssignments int
	MentorID, ProposedFor, ScheduledFor   string
	Instructions, MeetingURL              string
	Response, RespondedBy                 string
}

func loadRemovalState(t *testing.T, fixture fixture, sessionID string) removalState {
	t.Helper()
	var state removalState
	require.NoError(t, fixture.database.QueryRow(`SELECT
		(SELECT COUNT(*) FROM course_mentors WHERE course_id = ? AND mentor_user_id = ?),
		(SELECT COUNT(*) FROM mentor_assignments WHERE course_id = ? AND student_user_id = ? AND mentor_user_id = ?),
		COALESCE(mentor_user_id, ''), COALESCE(proposed_for, ''), COALESCE(scheduled_for, ''),
		COALESCE(meeting_instructions, ''), COALESCE(meeting_url, ''), COALESCE(response, ''),
		COALESCE(responded_by, '') FROM mentoring_sessions WHERE id = ?`, fixture.course, fixture.mentor,
		fixture.course, fixture.student, fixture.mentor, sessionID).Scan(&state.CourseAssignments,
		&state.StudentAssignments, &state.MentorID, &state.ProposedFor, &state.ScheduledFor, &state.Instructions,
		&state.MeetingURL, &state.Response, &state.RespondedBy))
	return state
}

func mentoringAuditCount(t *testing.T, fixture fixture) int {
	t.Helper()
	var count int
	require.NoError(t, fixture.database.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&count))
	return count
}

func newMentoringServer(t *testing.T, fixture fixture) *httpserver.Server {
	t.Helper()
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
	return server
}

func mentoringCookie(t *testing.T, server *httpserver.Server, accountID string) ([]*http.Cookie, string) {
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
	return cookies, csrf
}

func mentoringHTTP(server *httpserver.Server, method, path string, session []*http.Cookie, csrf, contentType string,
	body []byte) *httptest.ResponseRecorder {
	return mentoringHTTPRequest(server, method, path, session, csrf, contentType, body, nil)
}

func mentoringHTTPRequest(server *httpserver.Server, method, path string, session []*http.Cookie, csrf,
	contentType string, body []byte, headers http.Header) *httptest.ResponseRecorder {
	return mentoringHTTPRequestWithCSRFTokens(server, method, path, session, csrf, csrf, contentType, body, headers)
}

func mentoringHTTPRequestWithCSRFTokens(server *httpserver.Server, method, path string, session []*http.Cookie,
	csrfCookie, csrfHeader, contentType string, body []byte, headers http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://mia.test"+path, bytes.NewReader(body))
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	if csrfCookie != "" {
		request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrfCookie})
	}
	if csrfHeader != "" {
		request.Header.Set("X-CSRF-Token", csrfHeader)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}
