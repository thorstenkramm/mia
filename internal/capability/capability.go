// Package capability assembles current-account navigation guidance from feature-owner APIs.
package capability

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/course"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/mentoring"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

// MentorScope loads the mentoring owner's student assignment scope.
type MentorScope func(context.Context, miSQLite.Querier, string) ([]mentoring.CapabilityAssignment, error)

// Service is safe for concurrent use after construction.
type Service struct {
	database    *sql.DB
	mentorScope MentorScope
}

func New(database *sql.DB, mentorScope MentorScope) *Service {
	return &Service{database: database, mentorScope: mentorScope}
}

type Scope struct {
	Global         bool                             `json:"global"`
	CourseIDs      []string                         `json:"course_ids"`
	StudentIDs     []string                         `json:"student_ids"`
	CourseStudents []mentoring.CapabilityAssignment `json:"course_students"`
	OwnResource    bool                             `json:"own_resource"`
}

type Capability struct {
	Action    string `json:"action"`
	Available bool   `json:"available"`
	Scope     Scope  `json:"scope"`
}

func (service *Service) Current(ctx context.Context, actorID string) ([]Capability, error) {
	var capabilities []Capability
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var loadErr error
		capabilities, loadErr = service.current(ctx, tx, actorID)
		return loadErr
	})
	return capabilities, err
}

func (service *Service) current(ctx context.Context, query miSQLite.Querier,
	actorID string) ([]Capability, error) {
	roles, err := user.Roles(ctx, query, actorID)
	if err != nil {
		return nil, err
	}
	courseScope, err := course.LoadCapabilityScope(ctx, query, actorID)
	if err != nil {
		return nil, err
	}
	managedStudents := make([]string, 0, len(courseScope.ManagedStudentIDs))
	bannedStudents := make([]string, 0, len(courseScope.ManagedStudentIDs))
	for _, studentID := range courseScope.ManagedStudentIDs {
		studentOnly, banned, err := user.StudentOnlyState(ctx, query, studentID)
		if err != nil {
			return nil, err
		}
		if studentOnly {
			managedStudents = append(managedStudents, studentID)
			if banned {
				bannedStudents = append(bannedStudents, studentID)
			}
		}
	}
	mentorAssignments := []mentoring.CapabilityAssignment{}
	if service.mentorScope != nil {
		mentorAssignments, err = service.mentorScope(ctx, query, actorID)
		if err != nil {
			return nil, err
		}
	}
	has := func(wanted user.Role) bool {
		for _, role := range roles {
			if role == wanted {
				return true
			}
		}
		return false
	}
	admin, supervisor, staff := has(user.Administrator), has(user.Supervisor),
		has(user.Administrator) || has(user.Supervisor) || has(user.Mentor)
	capabilities := []Capability{
		capability("profile.view", Scope{OwnResource: true}),
		capabilityIf("profile.edit", staff, Scope{OwnResource: staff}),
		capabilityIf("accounts.administer", admin, Scope{Global: admin}),
		capabilityIf("audit.view", admin, Scope{Global: admin}),
		capabilityIf("jobs.view", admin, Scope{Global: admin}),
		capabilityIf("courses.create", admin, Scope{Global: admin}),
		capabilityIf("course_records.manage", admin, Scope{Global: admin}),
		capabilityIf("course_supervisors.manage", admin, Scope{Global: admin}),
		capabilityIf("courses.supervise", supervisor && len(courseScope.SupervisedCourseIDs) > 0,
			Scope{CourseIDs: courseScope.SupervisedCourseIDs}),
		capabilityIf("students.manage", supervisor && len(managedStudents) > 0,
			Scope{StudentIDs: managedStudents}),
		capabilityIf("students.ban", supervisor && len(managedStudents) > 0,
			Scope{StudentIDs: managedStudents}),
		capabilityIf("students.unban", supervisor && len(bannedStudents) > 0,
			Scope{StudentIDs: bannedStudents}),
		capabilityIf("learning.participate", len(courseScope.JoinedCourseIDs) > 0,
			Scope{CourseIDs: courseScope.JoinedCourseIDs}),
		capabilityIf("mentoring.fulfill", len(mentorAssignments) > 0,
			Scope{CourseStudents: mentorAssignments}),
	}
	return capabilities, nil
}

func capability(action string, scope Scope) Capability {
	return Capability{Action: action, Available: true, Scope: normalized(scope)}
}

func capabilityIf(action string, available bool, scope Scope) Capability {
	return Capability{Action: action, Available: available, Scope: normalized(scope)}
}

func normalized(scope Scope) Scope {
	if scope.CourseIDs == nil {
		scope.CourseIDs = []string{}
	}
	if scope.StudentIDs == nil {
		scope.StudentIDs = []string{}
	}
	if scope.CourseStudents == nil {
		scope.CourseStudents = []mentoring.CapabilityAssignment{}
	}
	return scope
}

func Register(server *httpserver.Server, service *Service) {
	server.AuthenticatedGET("/api/v1/users/me/capabilities", handler(service))
}

func handler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		capabilities, err := service.Current(c.Request().Context(), actorID)
		if err != nil {
			return fmt.Errorf("load current capabilities: %w", err)
		}
		return httpserver.Resource(c, http.StatusOK, "account-capabilities", actorID,
			map[string]any{"capabilities": capabilities})
	}
}
