package capability

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/thorstenkramm/mia/internal/mentoring"
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
	session := capabilitySession(t, server, actor, "authenticated", startedAt)
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

func TestUnassignedAccountsDiscoverExplicitEmptyScopes(t *testing.T) {
	for _, role := range []user.Role{user.Student, user.Mentor, user.Supervisor, user.Administrator} {
		t.Run(string(role), func(t *testing.T) {
			server, database := capabilityRouteServer(t)
			actor := createAccount(t, database, "unassigned", []user.Role{role})
			Register(server, New(database, mentoring.LoadCapabilityAssignments))
			session := capabilitySession(t, server, actor, "authenticated", time.Now())
			capabilities := readCapabilities(t, capabilityGET(server, session), actor)
			for action, item := range capabilities {
				assert.Equal(t, []string{}, item.Scope.CourseIDs, action)
				assert.Equal(t, []string{}, item.Scope.StudentIDs, action)
				assert.Equal(t, []mentoring.CapabilityAssignment{}, item.Scope.CourseStudents, action)
			}
			for _, action := range []string{"courses.supervise", "students.manage", "students.ban",
				"students.unban", "learning.participate", "mentoring.fulfill"} {
				assert.False(t, capabilities[action].Available, action)
			}
			assert.True(t, capabilities["profile.view"].Available)
			assert.True(t, capabilities["profile.view"].Scope.OwnResource)
		})
	}
}

func TestCapabilitiesRejectRestrictedStages(t *testing.T) {
	for _, test := range []struct {
		name               string
		stage              string
		mustChangePassword bool
	}{
		{name: "mfa alone", stage: "mfa"},
		{name: "mfa before password replacement", stage: "mfa", mustChangePassword: true},
		{name: "password replacement", stage: "password-change", mustChangePassword: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, database := capabilityRouteServer(t)
			actor := createAccount(t, database, "restricted", []user.Role{user.Student})
			_, err := database.Exec("UPDATE users SET must_change_password = ? WHERE id = ?",
				test.mustChangePassword, actor)
			require.NoError(t, err)
			Register(server, New(database, mentoring.LoadCapabilityAssignments))
			session := capabilitySession(t, server, actor, test.stage, time.Now())
			response := capabilityGET(server, session)
			conformance.Error(t, response, http.StatusForbidden, "auth_password_change_required")
			assert.NotContains(t, response.Body.String(), "account-capabilities")
		})
	}
}

func TestMentorScopePreservesPairsAndReflectsRemoval(t *testing.T) {
	server, database := capabilityRouteServer(t)
	actor := createAccount(t, database, "mentor", []user.Role{user.Mentor})
	other := createAccount(t, database, "other-mentor", []user.Role{user.Mentor})
	first := createAccount(t, database, "first-student", []user.Role{user.Student})
	second := createAccount(t, database, "second-student", []user.Role{user.Student})
	for _, courseID := range []string{"cou_first", "cou_second"} {
		_, err := database.Exec(`INSERT INTO courses (id, name, name_normalized, is_active, created_at)
			VALUES (?, ?, ?, 1, '2026-09-25T00:00:00.000000Z')`, courseID, courseID, courseID)
		require.NoError(t, err)
		for _, mentorID := range []string{actor, other} {
			_, err = database.Exec(`INSERT INTO course_mentors (course_id, mentor_user_id, assigned_at)
				VALUES (?, ?, '2026-09-25T00:00:00.000000Z')`, courseID, mentorID)
			require.NoError(t, err)
		}
		for _, studentID := range []string{first, second} {
			_, err = database.Exec(`INSERT INTO course_students (id, course_id, student_user_id, joined_at)
				VALUES (?, ?, ?, '2026-09-25T00:00:00.000000Z')`, courseID+studentID, courseID, studentID)
			require.NoError(t, err)
			mentorID := other
			if (courseID == "cou_first" && studentID == first) || (courseID == "cou_second" && studentID == second) {
				mentorID = actor
			}
			_, err = database.Exec(`INSERT INTO mentor_assignments
				(course_id, student_user_id, mentor_user_id, assigned_at)
				VALUES (?, ?, ?, '2026-09-25T00:00:00.000000Z')`, courseID, studentID, mentorID)
			require.NoError(t, err)
		}
	}
	Register(server, New(database, mentoring.LoadCapabilityAssignments))
	session := capabilitySession(t, server, actor, "authenticated", time.Now())
	want := []mentoring.CapabilityAssignment{
		{CourseID: "cou_first", StudentID: first}, {CourseID: "cou_second", StudentID: second},
	}
	for remaining := len(want); remaining >= 0; remaining-- {
		capabilities := readCapabilities(t, capabilityGET(server, session), actor)
		assert.Equal(t, want[:remaining], capabilities["mentoring.fulfill"].Scope.CourseStudents)
		assert.Equal(t, remaining > 0, capabilities["mentoring.fulfill"].Available)
		for action, item := range capabilities {
			assert.Empty(t, item.Scope.CourseIDs, action)
			assert.Empty(t, item.Scope.StudentIDs, action)
			assert.False(t, item.Scope.Global, action)
			if action != "mentoring.fulfill" {
				assert.Empty(t, item.Scope.CourseStudents, action)
			}
		}
		if remaining > 0 {
			_, err := database.Exec(`DELETE FROM mentor_assignments
				WHERE mentor_user_id = ? AND course_id = ? AND student_user_id = ?`,
				actor, want[remaining-1].CourseID, want[remaining-1].StudentID)
			require.NoError(t, err)
		}
	}
}

func TestCapabilitiesFailureReturnsNoPartialScope(t *testing.T) {
	server, database := capabilityRouteServer(t)
	actor := createAccount(t, database, "unavailable", []user.Role{user.Supervisor})
	student := createAccount(t, database, "scoped-student", []user.Role{user.Student})
	const courseID = "cou_failure_scope"
	_, err := database.Exec(`INSERT INTO courses (id, name, name_normalized, is_active, created_at)
		VALUES (?, 'Failure scope', 'failure scope', 1, '2026-09-25T00:00:00.000000Z')`, courseID)
	require.NoError(t, err)
	_, err = database.Exec(`INSERT INTO course_supervisors (course_id, supervisor_user_id, assigned_at)
		VALUES (?, ?, '2026-09-25T00:00:00.000000Z')`, courseID, actor)
	require.NoError(t, err)
	_, err = database.Exec(`INSERT INTO course_students (id, course_id, student_user_id, joined_at)
		VALUES ('cst_failure_scope', ?, ?, '2026-09-25T00:00:00.000000Z')`, courseID, student)
	require.NoError(t, err)
	Register(server, New(database, mentoring.LoadCapabilityAssignments))
	session := capabilitySession(t, server, actor, "authenticated", time.Now())
	capabilities := readCapabilities(t, capabilityGET(server, session), actor)
	require.Equal(t, []string{courseID}, capabilities["courses.supervise"].Scope.CourseIDs)
	require.Equal(t, []string{student}, capabilities["students.manage"].Scope.StudentIDs)
	_, err = database.Exec("DROP TABLE mentor_assignments")
	require.NoError(t, err)
	response := capabilityGET(server, session)
	conformance.Error(t, response, http.StatusInternalServerError, "internal_error")
	assert.NotContains(t, response.Body.String(), "account-capabilities")
	assert.NotContains(t, response.Body.String(), "mentor_assignments")
	assert.NotContains(t, response.Body.String(), courseID)
	assert.NotContains(t, response.Body.String(), student)
	assert.NotContains(t, response.Body.String(), actor)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &document))
	assert.NotContains(t, document, "data")
}

func readCapabilities(t *testing.T, response *httptest.ResponseRecorder, actorID string) map[string]Capability {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "application/vnd.api+json", response.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var document struct {
		Data struct {
			Type       string
			ID         string
			Attributes struct{ Capabilities []Capability }
		}
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &document))
	assert.Equal(t, "account-capabilities", document.Data.Type)
	assert.Equal(t, actorID, document.Data.ID)
	require.Len(t, document.Data.Attributes.Capabilities, 14)
	byAction := make(map[string]Capability)
	for _, item := range document.Data.Attributes.Capabilities {
		require.NotContains(t, byAction, item.Action, "duplicate capability action")
		byAction[item.Action] = item
	}
	require.Len(t, byAction, 14)
	return byAction
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

func capabilitySession(t *testing.T, server *httpserver.Server, actorID, stage string, startedAt time.Time) []*http.Cookie {
	t.Helper()
	server.Echo.GET("/issue-capability-session", func(c *echo.Context) error {
		if stage == "mfa" {
			return server.StartMFASession(c, actorID, 1, "mfc_00000000-0000-4000-8000-000000000001", startedAt)
		}
		return server.StartSession(c, actorID, 1, stage, startedAt)
	})
	response := httptest.NewRecorder()
	server.Echo.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"http://mia.test/issue-capability-session", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
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
