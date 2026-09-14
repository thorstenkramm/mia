package capability

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/httpserver/conformance"
	"github.com/thorstenkramm/mia/internal/identity"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestCurrentReturnsExplicitUnionedScopes(t *testing.T) {
	database, err := miSQLite.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	actor := createAccount(t, database, "multi", []user.Role{user.Supervisor})
	student := createAccount(t, database, "learner", []user.Role{user.Student})
	_, err = database.Exec(`INSERT INTO courses (id, name, name_normalized, is_active, created_at)
		VALUES ('cou_scope', 'Scope', 'scope', 1, '2026-09-14T00:00:00.000000Z')`)
	require.NoError(t, err)
	_, err = database.Exec(`INSERT INTO course_supervisors (course_id, supervisor_user_id, assigned_at)
		VALUES ('cou_scope', ?, '2026-09-14T00:00:00.000000Z')`, actor)
	require.NoError(t, err)
	_, err = database.Exec(`INSERT INTO course_students (id, course_id, student_user_id, joined_at)
		VALUES ('cst_scope_actor', 'cou_scope', ?, '2026-09-14T00:00:00.000000Z'),
		('cst_scope_student', 'cou_scope', ?, '2026-09-14T00:00:00.000000Z')`, actor, student)
	require.NoError(t, err)

	capabilities, err := New(database, nil).Current(context.Background(), actor)
	require.NoError(t, err)
	byAction := make(map[string]Capability, len(capabilities))
	for _, item := range capabilities {
		byAction[item.Action] = item
		assert.NotNil(t, item.Scope.CourseIDs)
		assert.NotNil(t, item.Scope.StudentIDs)
		assert.NotNil(t, item.Scope.CourseStudents)
	}
	assert.Equal(t, []string{"cou_scope"}, byAction["courses.supervise"].Scope.CourseIDs)
	assert.Equal(t, []string{student}, byAction["students.manage"].Scope.StudentIDs)
	assert.Equal(t, []string{"cou_scope"}, byAction["learning.participate"].Scope.CourseIDs)
	assert.False(t, byAction["accounts.administer"].Available)
	assert.Empty(t, byAction["accounts.administer"].Scope.CourseIDs)
	assert.True(t, byAction["profile.edit"].Scope.OwnResource)
}

func TestRegisteredCapabilitiesRouteRequiresCurrentAuthentication(t *testing.T) {
	server, database := capabilityRouteServer(t)
	actor := createAccount(t, database, "route-user", []user.Role{user.Mentor})
	Register(server, New(database, nil))

	conformance.Error(t, capabilityGET(server, nil), http.StatusUnauthorized, "auth_unauthenticated")
	startedAt := time.Now().Add(-5 * time.Minute).UTC().Truncate(time.Second)
	session := capabilitySession(t, server, actor, startedAt)
	conformance.Error(t, capabilityGET(server, conformance.ExpireSession(t, server.Sessions,
		server.SessionCookieName(), session)),
		http.StatusUnauthorized, "auth_unauthenticated")

	wantIdleUntil := startedAt.Add(30 * time.Minute).Unix()
	assert.Equal(t, wantIdleUntil, capabilityIdleUntil(t, server, session))
	response := capabilityGET(server, session)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"type":"account-capabilities"`)
	for _, cookie := range response.Result().Cookies() {
		assert.NotEqual(t, server.SessionCookieName(), cookie.Name, "capability discovery refreshed the session")
	}
	assert.Equal(t, wantIdleUntil, capabilityIdleUntil(t, server, session))
}

func capabilityRouteServer(t *testing.T) (*httpserver.Server, *sql.DB) {
	t.Helper()
	root := t.TempDir()
	docRoot := filepath.Join(root, "frontend")
	require.NoError(t, os.Mkdir(docRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(docRoot, "index.html"), []byte("frontend"), 0o644))
	database, err := miSQLite.Open(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	server, _, err := httpserver.New(httpserver.Options{DataDir: root, DocRoot: docRoot})
	require.NoError(t, err)
	server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
		state, loadErr := user.LoadSecurityState(ctx, database, id)
		return httpserver.IdentityState{SecurityGeneration: state.SecurityGeneration,
			MustChangePassword: state.MustChangePassword, Banned: state.Banned}, loadErr
	})
	return server, database
}

func capabilitySession(t *testing.T, server *httpserver.Server, actorID string, startedAt time.Time) []*http.Cookie {
	t.Helper()
	server.Echo.GET("/issue-capability-session", func(c *echo.Context) error {
		return server.StartSession(c, actorID, 1, "authenticated", startedAt)
	})
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"http://mia.test/issue-capability-session", nil))
	return response.Result().Cookies()
}

func capabilityIdleUntil(t *testing.T, server *httpserver.Server, cookies []*http.Cookie) int64 {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://mia.test/", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	stored, err := server.Sessions.Get(request, server.SessionCookieName())
	require.NoError(t, err)
	idleUntil, ok := stored.Values["idle_until"].(int64)
	require.True(t, ok, "session idle deadline missing")
	return idleUntil
}

func capabilityGET(server *httpserver.Server, session []*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "http://mia.test/api/v1/users/me/capabilities", nil)
	for _, cookie := range session {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, request)
	return response
}

func createAccount(t *testing.T, database *sql.DB, username string, roles []user.Role) string {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	require.NoError(t, err)
	var account user.Account
	err = miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		input := user.CreateInput{Username: username, PasswordHash: hash, Language: "en", Country: "DE",
			TimeZone: "UTC", Roles: roles}
		if roles[0] != user.Student {
			input.Email, input.EmailVerified = username+"@example.test", true
		}
		var createErr error
		account, createErr = user.Create(context.Background(), tx, input)
		return createErr
	})
	require.NoError(t, err)
	return account.ID
}
