package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/httpserver/conformance"
	"github.com/thorstenkramm/mia/internal/lifecycle"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

func TestAdministrationReadsAreMinimalFilteredAndDeterministic(t *testing.T) {
	server, database, nonAdmin, _, dataDir := profileServer(t)
	admin := handlerAccount(t, database, "AdminReader", []Role{Administrator}, true)
	handlerAccount(t, database, "zeta", []Role{Student}, false)
	target := handlerAccount(t, database, "Alpha", []Role{Mentor}, true)
	service := NewDeletionService(database, dataDir, &lifecycle.Registry{}, nil)
	service.SetSoleSupervisorCheck(noSoleSupervisor)
	RegisterAdministrationRoutes(server, service)

	unauthenticated := profileRequest(t, server, http.MethodGet, "/api/v1/users", nil, "", "", "")
	conformance.Error(t, unauthenticated, http.StatusUnauthorized, "auth_unauthenticated")
	nonAdminSession, _ := issueSession(t, server, nonAdmin)
	denied := profileRequest(t, server, http.MethodGet, "/api/v1/users", nonAdminSession, "", "", "")
	conformance.Error(t, denied, http.StatusForbidden, "user_administration_unauthorized")

	adminSession, _ := issueSession(t, server, admin)
	expired := conformance.ExpireSession(t, server.Sessions, server.SessionCookieName(), adminSession)
	conformance.Error(t, profileRequest(t, server, http.MethodGet, "/api/v1/users", expired, "", "", ""),
		http.StatusUnauthorized, "auth_unauthenticated")
	filtered := profileRequest(t, server, http.MethodGet,
		"/api/v1/users?filter%5Busername%5D=alpha&page%5Blimit%5D=1", adminSession, "", "", "")
	require.Equal(t, http.StatusOK, filtered.Code, filtered.Body.String())
	var collection map[string]any
	require.NoError(t, json.Unmarshal(filtered.Body.Bytes(), &collection))
	data, ok := collection["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 1)
	resource, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, target, resource["id"])
	attributes, ok := resource["attributes"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"username", "account_class", "account_state", "permanent_roles"}, mapKeys(attributes))
	assert.NotContains(t, filtered.Body.String(), "example.test")

	empty := profileRequest(t, server, http.MethodGet, "/api/v1/users?filter%5Busername%5D=missing",
		adminSession, "", "", "")
	require.Equal(t, http.StatusOK, empty.Code)
	assert.Contains(t, empty.Body.String(), `"data":[]`)
	invalid := profileRequest(t, server, http.MethodGet, "/api/v1/users?search=alpha", adminSession, "", "", "")
	conformance.Error(t, invalid, http.StatusUnprocessableEntity, "user_account_query_invalid")
	paged := profileRequest(t, server, http.MethodGet,
		"/api/v1/users?filter%5Brole%5D=student&page%5Blimit%5D=1", adminSession, "", "", "")
	require.Equal(t, http.StatusOK, paged.Code)
	assert.Contains(t, paged.Body.String(), "filter%5Brole%5D=student")
}

func TestAdministrationDetailAndDeletionBindReviewedState(t *testing.T) {
	server, database, _, student, dataDir := profileServer(t)
	admin := handlerAccount(t, database, "review-admin", []Role{Administrator}, true)
	registry := &lifecycle.Registry{}
	service := NewDeletionService(database, dataDir, registry, nil)
	service.SetSoleSupervisorCheck(noSoleSupervisor)
	RegisterAdministrationRoutes(server, service)
	RegisterDeletionRoutes(server, service)
	session, csrf := issueSession(t, server, admin)

	detail := profileRequest(t, server, http.MethodGet, "/api/v1/users/"+student, session, "", "", "")
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	etag := detail.Header().Get("ETag")
	assert.NotEmpty(t, etag)
	assert.NotContains(t, detail.Body.String(), "password")

	missing := profileRequest(t, server, http.MethodDelete, "/api/v1/users/"+student, session, csrf, "", "")
	conformance.Error(t, missing, http.StatusPreconditionRequired, "user_account_precondition_required")
	_, err := database.Exec("UPDATE users SET is_banned = 1 WHERE id = ?", student)
	require.NoError(t, err)
	stale := administrationDelete(t, server, "/api/v1/users/"+student, session, csrf, etag)
	conformance.Error(t, stale, http.StatusPreconditionFailed, "user_account_precondition_failed")
	var count int
	require.NoError(t, database.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", student).Scan(&count))
	assert.Equal(t, 1, count)

	fresh := profileRequest(t, server, http.MethodGet, "/api/v1/users/"+student, session, "", "", "")
	deleted := administrationDelete(t, server, "/api/v1/users/"+student, session, csrf, fresh.Header().Get("ETag"))
	assert.Equal(t, http.StatusNoContent, deleted.Code, deleted.Body.String())
}

func TestReviewedRoleGrantRejectsMissingAndStaleValidators(t *testing.T) {
	_, database, _, _, _ := profileServer(t)
	admin := handlerAccount(t, database, "grant-review-admin", []Role{Administrator}, true)
	target := handlerAccount(t, database, "grant-review-target", []Role{Mentor}, true)
	detail, err := GetAdministrationAccount(context.Background(), database, admin, target, noSoleSupervisor)
	require.NoError(t, err)
	err = miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		return RequireReviewedRoleGrant(context.Background(), tx, target, admin, "", noSoleSupervisor)
	})
	assert.ErrorIs(t, err, ErrAccountPreconditionRequired)
	err = miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		if err := RequireReviewedRoleGrant(context.Background(), tx, target, admin, detail.ETag,
			noSoleSupervisor); err != nil {
			return err
		}
		return GrantRole(context.Background(), tx, target, Supervisor, admin)
	})
	require.NoError(t, err)
	err = miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		return RequireReviewedRoleGrant(context.Background(), tx, target, admin, detail.ETag, noSoleSupervisor)
	})
	assert.ErrorIs(t, err, ErrAccountPreconditionFailed)
}

func TestMentorTargetPreflightIsExactMinimalAndIndistinguishable(t *testing.T) {
	server, database, nonSupervisor, student, dataDir := profileServer(t)
	actor := handlerAccount(t, database, "mentor-grant-supervisor", []Role{Supervisor}, true)
	target := handlerAccount(t, database, "mentor-grant-target", []Role{Supervisor}, true)
	require.NoError(t, setUserName(database, target, "Exact Target"))
	service := NewDeletionService(database, dataDir, &lifecycle.Registry{}, nil)
	service.SetSoleSupervisorCheck(noSoleSupervisor)
	RegisterAdministrationRoutes(server, service)
	session, csrf := issueSession(t, server, actor)

	missingCSRF := mentorTargetPreflight(t, server, session, "", target)
	conformance.Error(t, missingCSRF, http.StatusForbidden, "csrf_invalid")
	invalidUserIDs := []string{
		``, `null`, `1`, `true`, `{}`, `[]`, `""`, `"not-an-id"`,
		`"u_00000000-0000-3000-8000-000000000000"`,
		`"u_00000000-0000-4000-C000-000000000000"`,
	}
	for _, value := range invalidUserIDs {
		attributes := `{}`
		if value != "" {
			attributes = `{"user_id":` + value + `}`
		}
		invalidTarget := profileRequest(t, server, http.MethodPost,
			"/api/v1/mentor-role-target-preflights", session, csrf, "application/vnd.api+json",
			`{"data":{"type":"mentor-role-target-preflights","attributes":`+attributes+`}}`)
		conformance.Error(t, invalidTarget, http.StatusUnprocessableEntity, "auth_invalid_request")
	}
	success := mentorTargetPreflight(t, server, session, csrf, target)
	require.Equal(t, http.StatusOK, success.Code, success.Body.String())
	assert.Regexp(t, `^"[0-9a-f]{64}"$`, success.Header().Get("ETag"))
	document := conformance.Document(t, success)
	resource := conformance.Resource(t, document["data"], "mentor-role-targets")
	assert.Equal(t, target, resource["id"])
	attributes, ok := resource["attributes"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"username": "mentor-grant-target", "display_name": "Exact Target", "grant_state": "available",
	}, attributes)
	assert.NotContains(t, success.Body.String(), "example.test")
	assert.NotContains(t, success.Body.String(), "supervisor")
	alreadyGranted := mentorTargetPreflight(t, server, session, csrf, nonSupervisor)
	require.Equal(t, http.StatusOK, alreadyGranted.Code, alreadyGranted.Body.String())
	assert.Contains(t, alreadyGranted.Body.String(), `"display_name":null`)
	assert.Contains(t, alreadyGranted.Body.String(), `"grant_state":"already_granted"`)

	unknown := mentorTargetPreflight(t, server, session, csrf, "u_00000000-0000-4000-8000-000000000000")
	ineligible := mentorTargetPreflight(t, server, session, csrf, student)
	for _, response := range []*httptest.ResponseRecorder{unknown, ineligible} {
		conformance.Error(t, response, http.StatusNotFound, "user_mentor_target_unavailable")
		assert.Empty(t, response.Header().Get("Retry-After"))
	}
	assert.Equal(t, unknown.Body.String(), ineligible.Body.String())
	for range 6 {
		mentorTargetPreflight(t, server, session, csrf, "u_00000000-0000-4000-8000-000000000000")
	}
	throttled := mentorTargetPreflight(t, server, session, csrf, target)
	assert.Equal(t, unknown.Code, throttled.Code)
	assert.Equal(t, unknown.Body.String(), throttled.Body.String())
	assert.Empty(t, throttled.Header().Get("Retry-After"))
	var subject sql.NullString
	require.NoError(t, database.QueryRow(`SELECT subject_user_id FROM audit_events
		WHERE action = 'user.mentor_target.throttled' ORDER BY created_at DESC LIMIT 1`).Scan(&subject))
	assert.False(t, subject.Valid)

	nonSupervisorSession, nonSupervisorCSRF := issueSession(t, server, nonSupervisor)
	denied := mentorTargetPreflight(t, server, nonSupervisorSession, nonSupervisorCSRF, target)
	conformance.Error(t, denied, http.StatusForbidden, "user_role_unauthorized")
}

func TestMentorTargetPreflightSharesSourceIPLimitAcrossSupervisors(t *testing.T) {
	server, database, _, _, dataDir := profileServer(t)
	service := NewDeletionService(database, dataDir, &lifecycle.Registry{}, nil)
	service.SetSoleSupervisorCheck(noSoleSupervisor)
	RegisterAdministrationRoutes(server, service)
	const source = "198.51.100.50:4312"
	unknownID := "u_00000000-0000-4000-8000-000000000000"
	var unavailableBody string
	for actorIndex := range 3 {
		actor := handlerAccount(t, database, fmt.Sprintf("ip-limit-supervisor-%d", actorIndex),
			[]Role{Supervisor}, true)
		session, csrf := issueSession(t, server, actor)
		for range 10 {
			response := mentorTargetPreflightFrom(t, server, session, csrf, unknownID, source)
			conformance.Error(t, response, http.StatusNotFound, "user_mentor_target_unavailable")
			if unavailableBody == "" {
				unavailableBody = response.Body.String()
			}
		}
	}
	fourth := handlerAccount(t, database, "ip-limit-supervisor-four", []Role{Supervisor}, true)
	session, csrf := issueSession(t, server, fourth)
	throttled := mentorTargetPreflightFrom(t, server, session, csrf, unknownID, source)
	assert.Equal(t, http.StatusNotFound, throttled.Code)
	assert.Equal(t, unavailableBody, throttled.Body.String())
	assert.Empty(t, throttled.Header().Get("Retry-After"))
}

func TestMentorTargetThrottleAuditFailureUsesCentralErrorHandling(t *testing.T) {
	server, database, _, _, dataDir := profileServer(t)
	actor := handlerAccount(t, database, "audit-failure-supervisor", []Role{Supervisor}, true)
	service := NewDeletionService(database, dataDir, &lifecycle.Registry{}, nil)
	service.SetSoleSupervisorCheck(noSoleSupervisor)
	RegisterAdministrationRoutes(server, service)
	session, csrf := issueSession(t, server, actor)
	unknownID := "u_00000000-0000-4000-8000-000000000000"
	for range 10 {
		response := mentorTargetPreflight(t, server, session, csrf, unknownID)
		conformance.Error(t, response, http.StatusNotFound, "user_mentor_target_unavailable")
	}
	_, err := database.Exec(`CREATE TRIGGER fail_mentor_target_throttle_audit
		BEFORE INSERT ON audit_events WHEN NEW.action = 'user.mentor_target.throttled'
		BEGIN SELECT RAISE(ABORT, 'forced audit failure'); END`)
	require.NoError(t, err)
	failed := mentorTargetPreflight(t, server, session, csrf, unknownID)
	conformance.Error(t, failed, http.StatusInternalServerError, "internal_error")
}

func TestReviewedMentorGrantBecomesStaleAndAlreadyGrantedStateIsAuthoritative(t *testing.T) {
	_, database, _, _, _ := profileServer(t)
	actor := handlerAccount(t, database, "mentor-review-supervisor", []Role{Supervisor}, true)
	target := handlerAccount(t, database, "mentor-review-target", []Role{Administrator}, true)
	preflight, err := ResolveMentorTarget(context.Background(), database, actor, target)
	require.NoError(t, err)
	assert.Equal(t, MentorGrantAvailable, preflight.GrantState)

	err = miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		if checkErr := RequireReviewedMentorGrant(context.Background(), tx, actor, target, preflight.ETag); checkErr != nil {
			return checkErr
		}
		return GrantRole(context.Background(), tx, target, Mentor, actor)
	})
	require.NoError(t, err)
	err = RequireReviewedMentorGrant(context.Background(), database, actor, target, preflight.ETag)
	assert.ErrorIs(t, err, ErrAccountPreconditionFailed)
	after, err := ResolveMentorTarget(context.Background(), database, actor, target)
	require.NoError(t, err)
	assert.Equal(t, MentorGrantAlreadyGranted, after.GrantState)
	assert.NotEqual(t, preflight.ETag, after.ETag)
	assert.NoError(t, RequireReviewedMentorGrant(context.Background(), database, actor, target, after.ETag))
}

func mentorTargetPreflight(t *testing.T, server *httpserver.Server, session []*http.Cookie,
	csrf, targetID string) *httptest.ResponseRecorder {
	t.Helper()
	return mentorTargetPreflightFrom(t, server, session, csrf, targetID, "192.0.2.1:1234")
}

func mentorTargetPreflightFrom(t *testing.T, server *httpserver.Server, session []*http.Cookie,
	csrf, targetID, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/mentor-role-target-preflights",
		strings.NewReader(`{"data":{"type":"mentor-role-target-preflights","attributes":{"user_id":"`+
			targetID+`"}}}`))
	request.RemoteAddr = remoteAddr
	request.Header.Set("Content-Type", "application/vnd.api+json")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func setUserName(database *sql.DB, accountID, name string) error {
	_, err := database.Exec("UPDATE users SET name = ? WHERE id = ?", name, accountID)
	return err
}

func administrationDelete(t *testing.T, server *httpserver.Server, path string,
	session []*http.Cookie, csrf, etag string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodDelete, "http://mia.test"+path, nil)
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	request.AddCookie(&http.Cookie{Name: server.CSRFCookieName(), Value: csrf})
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("If-Match", etag)
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func noSoleSupervisor(context.Context, miSQLite.Querier, string) (bool, error) {
	return false, nil
}
