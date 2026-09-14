package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
