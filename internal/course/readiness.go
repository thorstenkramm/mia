package course

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/identity"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

const (
	ReadinessInactiveIncomplete  = "inactive_incomplete"
	ReadinessInactiveActivatable = "inactive_activatable"
	ReadinessActiveAccepting     = "active_accepting"
	ReadinessActiveNotAccepting  = "active_not_accepting"
)

type ActionEligibility struct {
	Eligible     bool
	Blockers     []string
	Consequences []string
}

type ReadinessBlocker struct {
	ID   string
	Link *string
}

type Readiness struct {
	State           string
	Activation      ActionEligibility
	NewSession      ActionEligibility
	VisibleBlockers []ReadinessBlocker
}

type SupervisorAssignment struct {
	CourseID, SupervisorID string
	Action                 ActionEligibility
	ETag                   string
}

type MembershipReview struct {
	Membership Membership
	Action     ActionEligibility
	ETag       string
}

func (service *Service) courseSnapshot(ctx context.Context, query miSQLite.Querier, courseID, actorID string) (Course, error) {
	value, err := loadScoped(ctx, query, courseID, actorID, false)
	if err != nil {
		return Course{}, err
	}
	value.ViewerID = actorID
	value.ViewerIsAdministrator, err = user.HasRole(ctx, query, actorID, user.Administrator)
	if err != nil {
		return Course{}, fmt.Errorf("load course viewer administrator role: %w", err)
	}
	value.ViewerIsStudent, value.ViewerIsSupervisor, err = TutoringScope(ctx, query, courseID, actorID)
	if err != nil {
		return Course{}, err
	}
	value.SupervisorIDs, err = supervisorIDs(ctx, query, courseID)
	if err != nil {
		return Course{}, err
	}
	value.HasLogo = service.logoExists(courseID)
	value.Readiness, err = service.evaluateReadiness(ctx, query, value)
	if err != nil {
		return Course{}, err
	}
	activeSessions, err := service.hasActiveSessions(ctx, query, courseID)
	if err != nil {
		return Course{}, err
	}
	deleteBlockers := []string{}
	if !value.ViewerIsAdministrator {
		deleteBlockers = append(deleteBlockers, "administrator_required")
	}
	if value.Active {
		deleteBlockers = append(deleteBlockers, "course_active")
	}
	if activeSessions {
		deleteBlockers = append(deleteBlockers, "active_tutoring_session")
	}
	value.DeleteAction = action(deleteBlockers, []string{"delete_course_scoped_data", "preserve_accounts"})
	impact, err := service.courseDeletionImpact(ctx, query, courseID)
	if err != nil {
		return Course{}, err
	}
	value.ETag = courseETag(value, actorID, activeSessions, impact)
	return value, nil
}

func (service *Service) evaluateReadiness(ctx context.Context, query miSQLite.Querier, value Course) (Readiness, error) {
	blockers := []string{}
	if value.LearningGoals == nil || strings.TrimSpace(*value.LearningGoals) == "" {
		blockers = append(blockers, "learning_goals")
	}
	if value.Instructions == nil || strings.TrimSpace(*value.Instructions) == "" {
		blockers = append(blockers, "ai_tutor_instructions")
	}
	if value.Language == nil {
		blockers = append(blockers, "language")
	} else if _, err := identity.Language(*value.Language); err != nil {
		blockers = append(blockers, "language")
	}
	if len(value.SupervisorIDs) == 0 {
		blockers = append(blockers, "supervisor_presence")
	}
	materialReady, err := service.hasReadyMaterial(ctx, query, value.ID)
	if err != nil {
		return Readiness{}, err
	}
	if !materialReady {
		blockers = append(blockers, "qualifying_material")
	}
	state := ReadinessInactiveIncomplete
	if value.Active && len(blockers) == 0 {
		state = ReadinessActiveAccepting
	} else if value.Active {
		state = ReadinessActiveNotAccepting
	} else if len(blockers) == 0 {
		state = ReadinessInactiveActivatable
	}
	activationBlockers := append([]string(nil), blockers...)
	if value.Active {
		activationBlockers = append(activationBlockers, "course_already_active")
	}
	if !value.ViewerIsSupervisor {
		activationBlockers = append(activationBlockers, "assigned_supervisor_required")
	}
	sessionBlockers := []string{}
	if !value.Active {
		sessionBlockers = append(sessionBlockers, "course_inactive")
	} else if len(blockers) != 0 {
		if value.ViewerIsSupervisor {
			sessionBlockers = append(sessionBlockers, blockers...)
		} else {
			sessionBlockers = append(sessionBlockers, "course_not_ready")
		}
	}
	if !value.ViewerIsStudent {
		sessionBlockers = append(sessionBlockers, "course_membership_required")
	} else if service.studentAnyActive != nil {
		active, checkErr := service.studentAnyActive(ctx, query, value.ViewerID)
		if checkErr != nil {
			return Readiness{}, fmt.Errorf("%w: tutoring dependency", ErrReadinessUnavailable)
		}
		if active {
			sessionBlockers = append(sessionBlockers, "active_tutoring_session")
		}
	}
	visible := []ReadinessBlocker{}
	if value.ViewerIsSupervisor {
		for _, blocker := range blockers {
			link := readinessLink(value.ID, blocker)
			visible = append(visible, ReadinessBlocker{ID: blocker, Link: link})
		}
	}
	return Readiness{State: state, Activation: action(activationBlockers, []string{"activate_course"}),
		NewSession: action(sessionBlockers, []string{"start_tutoring_session"}), VisibleBlockers: visible}, nil
}

func readinessLink(courseID, blocker string) *string {
	var value string
	switch blocker {
	case "learning_goals", "ai_tutor_instructions", "language":
		value = "/api/v1/courses/" + courseID
	case "supervisor_presence":
		value = "/api/v1/courses/" + courseID + "/supervisors"
	case "qualifying_material":
		value = "/api/v1/courses/" + courseID + "/materials"
	default:
		return nil
	}
	return &value
}

func action(blockers, consequences []string) ActionEligibility {
	if blockers == nil {
		blockers = []string{}
	}
	if consequences == nil {
		consequences = []string{}
	}
	return ActionEligibility{Eligible: len(blockers) == 0, Blockers: blockers, Consequences: consequences}
}

func (service *Service) hasReadyMaterial(ctx context.Context, query miSQLite.Querier, courseID string) (bool, error) {
	if service.materialReady == nil {
		return false, nil
	}
	ready, err := service.materialReady(ctx, query, courseID)
	if err != nil {
		return false, fmt.Errorf("%w: material dependency", ErrReadinessUnavailable)
	}
	return ready, nil
}

func (service *Service) hasActiveSessions(ctx context.Context, query miSQLite.Querier, courseID string) (bool, error) {
	if service.activeSessions == nil {
		return false, nil
	}
	active, err := service.activeSessions(ctx, query, courseID)
	if err != nil {
		return false, fmt.Errorf("%w: tutoring dependency", ErrReadinessUnavailable)
	}
	return active, nil
}

func courseETag(value Course, actorID string, activeSessions bool, impact []string) string {
	supervisors := value.SupervisorIDs
	description, curriculum := value.Description, value.Curriculum
	learningGoals, instructions, language := value.LearningGoals, value.Instructions, value.Language
	if value.ViewerIsStudent && !value.ViewerIsSupervisor && !value.ViewerIsAdministrator {
		supervisors = []string{}
		description, curriculum, learningGoals, instructions, language = nil, nil, nil, nil, nil
	}
	reviewed := struct {
		ID, ActorID, Name, State, CreatedAt, ActivatedAt, DeactivatedAt, UpdatedAt string
		Description, Curriculum, LearningGoals, Instructions, Language             *string
		HasLogo, ActiveSessions                                                    bool
		Readiness                                                                  Readiness
		Delete                                                                     ActionEligibility
		Supervisors, DeletionImpact                                                []string
	}{ID: value.ID, ActorID: actorID, Name: value.Name, State: map[bool]string{false: "inactive", true: "active"}[value.Active],
		CreatedAt: instant(value.CreatedAt), Description: description, Curriculum: curriculum,
		LearningGoals: learningGoals, Instructions: instructions, Language: language,
		HasLogo: value.HasLogo, ActiveSessions: activeSessions, Readiness: value.Readiness, Delete: value.DeleteAction,
		Supervisors: supervisors, DeletionImpact: impact}
	if value.ActivatedAt != nil {
		reviewed.ActivatedAt = instant(*value.ActivatedAt)
	}
	if value.DeactivatedAt != nil {
		reviewed.DeactivatedAt = instant(*value.DeactivatedAt)
	}
	if value.UpdatedAt != nil {
		reviewed.UpdatedAt = instant(*value.UpdatedAt)
	}
	raw, err := json.Marshal(reviewed)
	if err != nil {
		panic("marshal canonical course review: " + err.Error())
	}
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("\"%x\"", digest)
}

func assignmentETag(courseID, supervisorID, actorID string, count int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", courseID, supervisorID, actorID, count)))
	return fmt.Sprintf("\"%x\"", digest)
}

func membershipETag(value Membership, actorID string, active bool, impact []string) string {
	raw, err := json.Marshal(struct {
		Membership Membership
		ActorID    string
		Active     bool
		Impact     []string
	}{value, actorID, active, impact})
	if err != nil {
		panic("marshal canonical membership review: " + err.Error())
	}
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("\"%x\"", digest)
}

func (service *Service) SupervisorAssignment(ctx context.Context, courseID, supervisorID, actorID string) (SupervisorAssignment, error) {
	var snapshot SupervisorAssignment
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var err error
		snapshot, err = service.supervisorAssignmentSnapshot(ctx, tx, courseID, supervisorID, actorID)
		return err
	})
	return snapshot, err
}

func (service *Service) supervisorAssignmentSnapshot(ctx context.Context, query miSQLite.Querier, courseID, supervisorID, actorID string) (SupervisorAssignment, error) {
	if err := requireAdministrator(ctx, query, actorID); err != nil {
		return SupervisorAssignment{}, err
	}
	if _, err := loadScoped(ctx, query, courseID, actorID, false); err != nil {
		return SupervisorAssignment{}, err
	}
	var count, assigned int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*), EXISTS(SELECT 1 FROM course_supervisors
		WHERE course_id = ? AND supervisor_user_id = ?) FROM course_supervisors WHERE course_id = ?`,
		courseID, supervisorID, courseID).Scan(&count, &assigned); err != nil {
		return SupervisorAssignment{}, err
	}
	if assigned == 0 {
		return SupervisorAssignment{}, ErrNotFound
	}
	blockers := []string{}
	if count <= 1 {
		blockers = append(blockers, "last_supervisor")
	}
	return SupervisorAssignment{CourseID: courseID, SupervisorID: supervisorID,
		Action: action(blockers, []string{"remove_supervisor_assignment"}),
		ETag:   assignmentETag(courseID, supervisorID, actorID, count)}, nil
}

func (service *Service) StudentMembership(ctx context.Context, courseID, studentID, actorID string) (MembershipReview, error) {
	var snapshot MembershipReview
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var err error
		snapshot, err = service.studentMembershipSnapshot(ctx, tx, courseID, studentID, actorID)
		return err
	})
	return snapshot, err
}

func (service *Service) studentMembershipSnapshot(ctx context.Context, query miSQLite.Querier, courseID, studentID, actorID string) (MembershipReview, error) {
	if err := requireAssignedSupervisor(ctx, query, courseID, actorID, false); err != nil {
		return MembershipReview{}, err
	}
	value, err := loadMembership(ctx, query, courseID, studentID)
	if err != nil {
		return MembershipReview{}, err
	}
	active := false
	if service.studentActiveSessions != nil {
		active, err = service.studentActiveSessions(ctx, query, courseID, studentID)
		if err != nil {
			return MembershipReview{}, fmt.Errorf("%w: tutoring dependency", ErrReadinessUnavailable)
		}
	}
	blockers := []string{}
	if active {
		blockers = append(blockers, "active_tutoring_session")
	}
	impact, err := service.studentCourseDeletionImpact(ctx, query, courseID, studentID)
	if err != nil {
		return MembershipReview{}, err
	}
	return MembershipReview{Membership: value, Action: action(blockers,
		[]string{"delete_student_course_data", "preserve_account", "preserve_other_course_data"}),
		ETag: membershipETag(value, actorID, active, impact)}, nil
}

func (service *Service) courseDeletionImpact(ctx context.Context, query miSQLite.Querier,
	courseID string) ([]string, error) {
	rows, err := query.QueryContext(ctx, `SELECT id || ':' || student_user_id || ':' || joined_at
		FROM course_students WHERE course_id = ? ORDER BY id`, courseID)
	if err != nil {
		return nil, fmt.Errorf("list course-owned deletion impact: %w", err)
	}
	impact, err := miSQLite.ScanStrings(rows)
	if err != nil {
		return nil, err
	}
	registered, err := service.lifecycle.CourseDeletionImpact(ctx, query, courseID)
	if err != nil {
		return nil, fmt.Errorf("load registered course deletion impact: %w", err)
	}
	audits, err := audit.CourseDeletionImpact(ctx, query, courseID)
	if err != nil {
		return nil, err
	}
	return append(append(impact, registered...), audits...), nil
}

func (service *Service) studentCourseDeletionImpact(ctx context.Context, query miSQLite.Querier, courseID,
	studentID string) ([]string, error) {
	registered, err := service.lifecycle.StudentCourseDeletionImpact(ctx, query, courseID, studentID)
	if err != nil {
		return nil, fmt.Errorf("load registered membership deletion impact: %w", err)
	}
	audits, err := audit.StudentCourseDeletionImpact(ctx, query, courseID, studentID)
	if err != nil {
		return nil, err
	}
	return append(registered, audits...), nil
}

func checkPrecondition(expected, actual string) error {
	if expected == "" {
		return ErrPreconditionRequired
	}
	if expected != actual {
		return ErrPreconditionFailed
	}
	return nil
}

func (service *Service) RemoveSupervisorReviewed(ctx context.Context, courseID, supervisorID, actorID, expected string) error {
	return miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		snapshot, err := service.supervisorAssignmentSnapshot(ctx, tx, courseID, supervisorID, actorID)
		if err != nil {
			return err
		}
		if err := checkPrecondition(expected, snapshot.ETag); err != nil {
			return err
		}
		if !snapshot.Action.Eligible {
			return ErrLastSupervisor
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM course_supervisors WHERE course_id = ? AND supervisor_user_id = ?`, courseID, supervisorID); err != nil {
			return err
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionCourseSupervisorRemoved, actorID, supervisorID,
			audit.Metadata{CourseID: courseID})
	})
}

func (service *Service) RemoveStudentReviewed(ctx context.Context, courseID, studentID, actorID, expected string) error {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		snapshot, err := service.studentMembershipSnapshot(ctx, tx, courseID, studentID, actorID)
		if err != nil {
			return err
		}
		if err := checkPrecondition(expected, snapshot.ETag); err != nil {
			return err
		}
		if !snapshot.Action.Eligible {
			return ErrActiveSession
		}
		if err := service.lifecycle.DeleteStudentCourseData(ctx, tx, courseID, studentID); err != nil {
			return err
		}
		if err := audit.ReplaceStudentCourseHistoryWithRemoval(ctx, tx, courseID, studentID, actorID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM course_students WHERE course_id = ? AND student_user_id = ?", courseID, studentID)
		return err
	})
	if err == nil {
		service.cleanupStudentCourseData(ctx, courseID, studentID)
	}
	return err
}

func (service *Service) DeleteReviewed(ctx context.Context, courseID, actorID, expected string) error {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireAdministrator(ctx, tx, actorID); err != nil {
			return err
		}
		snapshot, err := service.courseSnapshot(ctx, tx, courseID, actorID)
		if err != nil {
			return err
		}
		if err := checkPrecondition(expected, snapshot.ETag); err != nil {
			return err
		}
		if !snapshot.DeleteAction.Eligible {
			if snapshot.Active {
				return ErrInvalidState
			}
			return ErrActiveSession
		}
		if err := service.lifecycle.DeleteCourseData(ctx, tx, courseID); err != nil {
			return err
		}
		if err := audit.ReplaceCourseHistoryWithDeletion(ctx, tx, courseID, actorID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM courses WHERE id = ?", courseID)
		return err
	})
	if err == nil {
		service.cleanupDeletedCourse(ctx, courseID)
	}
	return err
}

func (service *Service) cleanupDeletedCourse(ctx context.Context, courseID string) {
	if cleanupErr := service.lifecycle.CleanupCourseData(ctx, courseID); cleanupErr != nil {
		service.logger.WarnContext(ctx, "clean deleted course files", "course_id", courseID, "error", cleanupErr)
	}
	service.logoMu.Lock()
	defer service.logoMu.Unlock()
	path, err := service.logoPath(courseID)
	if err == nil {
		if err := os.RemoveAll(filepath.Dir(path)); err != nil {
			service.logger.WarnContext(ctx, "remove deleted course logo", "course_id", courseID, "error", err)
		}
	}
}
