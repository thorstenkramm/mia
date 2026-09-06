package speech

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

func TestSpeechRoutesShareGenerationServeMP3AndHideOwnership(t *testing.T) {
	fixture := newSpeechFixture(t)
	docRoot := filepath.Join(t.TempDir(), "frontend")
	require.NoError(t, os.Mkdir(docRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644))
	server, _, err := httpserver.New(httpserver.Options{DataDir: fixture.directory, DocRoot: docRoot})
	require.NoError(t, err)
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		account, loadErr := user.LoadSecurityState(ctx, fixture.database, id)
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration}, loadErr
	})
	provider := &fakeSynthesizer{gate: make(chan struct{}), data: []byte("ID3audio")}
	service := NewService(fixture.database, fixture.directory, provider, 30, nil)
	t.Cleanup(func() { require.NoError(t, service.Stop(context.Background())) })
	Register(server, service)
	studentCookie, csrf := speechCookie(t, server, fixture.student)
	otherCookie, _ := speechCookie(t, server, fixture.otherUser)
	path := "/api/v1/tutor-responses/" + fixture.response + "/speech"
	unauthenticatedPOST := speechHTTP(server, http.MethodPost, path, nil, "unauthenticated-csrf")
	assert.Equal(t, http.StatusUnauthorized, unauthenticatedPOST.Code)
	assert.Contains(t, unauthenticatedPOST.Body.String(), `"code":"auth_unauthenticated"`)
	unauthenticatedGET := speechHTTP(server, http.MethodGet, path, nil, "")
	assert.Equal(t, http.StatusUnauthorized, unauthenticatedGET.Code)
	assert.Contains(t, unauthenticatedGET.Body.String(), `"code":"auth_unauthenticated"`)

	created := speechHTTP(server, http.MethodPost, path, studentCookie, csrf)
	assert.Equal(t, http.StatusAccepted, created.Code)
	assert.Equal(t, "application/vnd.api+json", created.Header().Get("Content-Type"))
	assert.Contains(t, created.Body.String(), `"state":"generating"`)
	replayed := speechHTTP(server, http.MethodPost, path, studentCookie, csrf)
	assert.Equal(t, http.StatusAccepted, replayed.Code)
	assert.Equal(t, created.Body.String(), replayed.Body.String())
	require.Eventually(t, func() bool { return provider.calls.Load() == 1 }, time.Second, 10*time.Millisecond)

	pending := speechHTTP(server, http.MethodGet, path, studentCookie, "")
	assert.Equal(t, http.StatusOK, pending.Code)
	assert.Equal(t, "application/vnd.api+json", pending.Header().Get("Content-Type"))
	close(provider.gate)
	require.Eventually(t, func() bool {
		return speechHTTP(server, http.MethodGet, path, studentCookie, "").Header().Get("Content-Type") == "audio/mpeg"
	}, time.Second, 10*time.Millisecond)
	audio := speechHTTP(server, http.MethodGet, path, studentCookie, "")
	assert.Equal(t, http.StatusOK, audio.Code)
	assert.Equal(t, "audio/mpeg", audio.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", audio.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "ID3audio", audio.Body.String())

	hidden := speechHTTP(server, http.MethodGet, path, otherCookie, "")
	assert.Equal(t, http.StatusNotFound, hidden.Code)
	assert.Contains(t, hidden.Body.String(), `"code":"speech_not_found"`)
	for state, responseID := range insertIneligibleResponses(t, fixture) {
		ineligiblePath := "/api/v1/tutor-responses/" + responseID + "/speech"
		post := speechHTTP(server, http.MethodPost, ineligiblePath, studentCookie, csrf)
		assert.Equal(t, http.StatusNotFound, post.Code, state+" POST")
		assert.Contains(t, post.Body.String(), `"code":"speech_not_found"`, state+" POST")
		get := speechHTTP(server, http.MethodGet, ineligiblePath, studentCookie, "")
		assert.Equal(t, http.StatusNotFound, get.Code, state+" GET")
		assert.Contains(t, get.Body.String(), `"code":"speech_not_found"`, state+" GET")
	}
	trailing := speechHTTP(server, http.MethodGet, path+"/", studentCookie, "")
	assert.Equal(t, http.StatusNotFound, trailing.Code)
}

func insertIneligibleResponses(t *testing.T, fixture speechFixture) map[string]string {
	t.Helper()
	var sessionID string
	require.NoError(t, fixture.database.QueryRow("SELECT session_id FROM tutor_responses WHERE id = ?", fixture.response).
		Scan(&sessionID))
	now := instant(time.Now())
	ids := make(map[string]string, 4)
	for index, state := range []string{"queued", "generating", "failed", "interrupted"} {
		messageID := "msg_" + uuid.NewString()
		responseID := "rsp_" + uuid.NewString()
		_, err := fixture.database.Exec(`INSERT INTO student_messages
			(id, tutoring_session_id, sequence, content, client_request_id, request_digest, created_at)
			VALUES (?, ?, ?, 'question', ?, zeroblob(32), ?)`, messageID, sessionID, index+2, uuid.NewString(), now)
		require.NoError(t, err)
		var startedAt, finishedAt, failureCode any
		if state != "queued" {
			startedAt = now
		}
		if state == "failed" || state == "interrupted" {
			finishedAt = now
		}
		if state == "failed" {
			failureCode = "test_failure"
		}
		_, err = fixture.database.Exec(`INSERT INTO tutor_responses
			(id, session_id, student_message_id, attempt, state, content, failure_code, created_at, started_at, finished_at)
			VALUES (?, ?, ?, 1, ?, 'ineligible', ?, ?, ?, ?)`, responseID, sessionID, messageID, state,
			failureCode, now, startedAt, finishedAt)
		require.NoError(t, err)
		ids[state] = responseID
	}
	return ids
}

func TestSpeechRoutesReturnStableUnavailable(t *testing.T) {
	fixture := newSpeechFixture(t)
	docRoot := filepath.Join(t.TempDir(), "frontend")
	require.NoError(t, os.Mkdir(docRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644))
	server, _, err := httpserver.New(httpserver.Options{DataDir: fixture.directory, DocRoot: docRoot})
	require.NoError(t, err)
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		account, loadErr := user.LoadSecurityState(ctx, fixture.database, id)
		return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration}, loadErr
	})
	service := NewService(fixture.database, fixture.directory, nil, 30, nil)
	Register(server, service)
	cookie, csrf := speechCookie(t, server, fixture.student)
	path := "/api/v1/tutor-responses/" + fixture.response + "/speech"
	for _, request := range []*httptest.ResponseRecorder{
		speechHTTP(server, http.MethodPost, path, cookie, csrf),
		speechHTTP(server, http.MethodGet, path, cookie, ""),
	} {
		assert.Equal(t, http.StatusServiceUnavailable, request.Code)
		assert.Contains(t, request.Body.String(), `"code":"speech_unavailable"`)
	}
}

func speechCookie(t *testing.T, server *httpserver.Server, accountID string) (*http.Cookie, string) {
	t.Helper()
	path := "/issue-speech-" + strings.ReplaceAll(accountID, "_", "-")
	server.Echo.GET(path, func(c *echo.Context) error {
		if err := server.StartSession(c, accountID, 1, "authenticated", time.Now()); err != nil {
			return err
		}
		httpserver.RotateCSRF(c)
		return c.NoContent(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "http://mia.test"+path, nil)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	var session *http.Cookie
	csrf := ""
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "__Host-mia_session" {
			session = cookie
		}
		if cookie.Name == "__Host-mia_csrf" {
			csrf = cookie.Value
		}
	}
	require.NotNil(t, session)
	return session, csrf
}

func speechHTTP(server *httpserver.Server, method, path string, session *http.Cookie,
	csrf string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://mia.test"+path, bytes.NewReader(nil))
	if session != nil {
		request.AddCookie(session)
	}
	if csrf != "" {
		request.AddCookie(&http.Cookie{Name: "__Host-mia_csrf", Value: csrf})
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}
