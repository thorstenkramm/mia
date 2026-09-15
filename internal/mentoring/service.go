package mentoring

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/course"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

const (
	maxTopicRunes        = 4_000
	maxTopicBytes        = 16 << 10
	maxResponseRunes     = 8_000
	maxResponseBytes     = 32 << 10
	maxInstructionsRunes = 4_000
	maxInstructionsBytes = 16 << 10
	maxURLBytes          = 2_048
	reasonCanceled       = "cancel" + "led"
)

// Service is safe for concurrent use; it keeps no mutable request state.
type Service struct {
	database *sql.DB
	now      func() time.Time
	logger   *slog.Logger
	profiles *user.Service
}

// CapabilityAssignment is one course-and-student scope that must not be widened into independent lists.
type CapabilityAssignment struct {
	CourseID  string `json:"course_id"`
	StudentID string `json:"student_id"`
}

// LoadCapabilityAssignments returns the mentor's current paired assignment scope.
func LoadCapabilityAssignments(ctx context.Context, query miSQLite.Querier,
	actorID string) ([]CapabilityAssignment, error) {
	rows, err := query.QueryContext(ctx, `SELECT course_id, student_user_id FROM mentor_assignments
		WHERE mentor_user_id = ? ORDER BY course_id, student_user_id`, actorID)
	if err != nil {
		return nil, fmt.Errorf("load mentoring capability scope: %w", err)
	}
	assignments := make([]CapabilityAssignment, 0)
	for rows.Next() {
		var assignment CapabilityAssignment
		if err := rows.Scan(&assignment.CourseID, &assignment.StudentID); err != nil {
			return nil, errors.Join(fmt.Errorf("scan mentoring capability scope: %w", err), rows.Close())
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("iterate mentoring capability scope: %w", err), rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close mentoring capability scope: %w", err)
	}
	return assignments, nil
}

func NewService(database *sql.DB, profiles *user.Service, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{database: database, now: time.Now, logger: logger, profiles: profiles}
}

// AuditMutationDenied records a content-free domain-level denial without changing the API response on audit failure.
func (service *Service) AuditMutationDenied(ctx context.Context, actorID, outcome string) {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		return audit.WriteWithMetadata(ctx, tx, audit.ActionMentoringMutationDenied, actorID, "",
			audit.Metadata{OutcomeCode: outcome})
	})
	if err != nil {
		service.logger.WarnContext(ctx, "audit denied mentoring mutation", "error", err)
	}
}

// Available reports the current student-facing suggestion/request gate for tutor context.
func (service *Service) Available(ctx context.Context, query miSQLite.Querier, courseID, studentID string) (bool, error) {
	member, err := course.ActiveStudentMembership(ctx, query, courseID, studentID)
	if err != nil || !member {
		return false, err
	}
	allowed, err := user.MentoringRequestsAllowed(ctx, query, studentID)
	if err != nil || !allowed {
		return false, err
	}
	return studentHasMentor(ctx, query, courseID, studentID)
}

// RequestEligibility atomically projects the student-visible creation gate without disclosing which gate is closed.
func (service *Service) RequestEligibility(ctx context.Context, courseID, studentID string) (RequestEligibility, error) {
	result := RequestEligibility{ID: eligibilityID("request", courseID, studentID), State: "access-lost"}
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		result.CheckedAt = service.now()
		var active, allowed, hasMentor int
		err := tx.QueryRowContext(ctx, `SELECT c.is_active, u.mentoring_requests_allowed,
			EXISTS(SELECT 1 FROM mentor_assignments ma WHERE ma.course_id = c.id AND ma.student_user_id = u.id)
			FROM courses c JOIN course_students cs ON cs.course_id = c.id
			JOIN users u ON u.id = cs.student_user_id WHERE c.id = ? AND u.id = ?`, courseID, studentID).
			Scan(&active, &allowed, &hasMentor)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load mentoring request eligibility: %w", err)
		}
		result.State = "disabled"
		if active != 0 && allowed != 0 && hasMentor != 0 {
			result.State = "allowed"
		}
		return nil
	})
	if err != nil {
		return RequestEligibility{}, errors.Join(ErrStateUnavailable,
			fmt.Errorf("project request eligibility: %w", err))
	}
	return result, nil
}

func (service *Service) AssignCourseMentor(ctx context.Context, courseID, mentorID, actorID string) error {
	return miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := course.RequireAssignedSupervisor(ctx, tx, courseID, actorID); err != nil {
			return ErrNotFound
		}
		eligible, err := user.HasRole(ctx, tx, mentorID, user.Mentor)
		if err != nil {
			return err
		}
		if !eligible {
			return ErrNotFound
		}
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO course_mentors
			(course_id, mentor_user_id, assigned_at, assigned_by) VALUES (?, ?, ?, ?)`, courseID, mentorID,
			instant(service.now()), actorID)
		if err != nil {
			return fmt.Errorf("assign course mentor: %w", err)
		}
		return auditIfChanged(ctx, tx, result, audit.ActionMentoringCourseMentorAssigned, actorID, mentorID,
			audit.Metadata{CourseID: courseID})
	})
}

func (service *Service) AssignStudentMentor(ctx context.Context, courseID, studentID, mentorID, actorID string) error {
	return miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := course.RequireAssignedSupervisor(ctx, tx, courseID, actorID); err != nil {
			return ErrNotFound
		}
		member, err := course.StudentMembership(ctx, tx, courseID, studentID)
		if err != nil {
			return err
		}
		courseMentor, err := courseMentorExists(ctx, tx, courseID, mentorID)
		if err != nil {
			return err
		}
		if !member || !courseMentor {
			return ErrNotFound
		}
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mentor_assignments
			(course_id, student_user_id, mentor_user_id, assigned_at, assigned_by) VALUES (?, ?, ?, ?, ?)`,
			courseID, studentID, mentorID, instant(service.now()), actorID)
		if err != nil {
			return fmt.Errorf("assign student mentor: %w", err)
		}
		return auditIfChanged(ctx, tx, result, audit.ActionMentoringStudentMentorAssigned, actorID, studentID,
			audit.Metadata{CourseID: courseID, MentorID: mentorID})
	})
}

func (service *Service) ReviewCourseMentorRemoval(ctx context.Context, courseID, mentorID,
	actorID string) (RemovalReview, error) {
	return service.reviewRemoval(ctx, courseID, "", mentorID, actorID)
}

func (service *Service) RemoveCourseMentor(ctx context.Context, courseID, mentorID, actorID, etag string) error {
	return miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		review, err := service.removalReview(ctx, tx, courseID, "", mentorID, actorID)
		if err != nil {
			return err
		}
		if etag == "" {
			return ErrPreconditionRequired
		}
		if etag != review.ETag {
			return ErrPreconditionFailed
		}
		triaged, err := triage(ctx, tx, "course_id = ? AND mentor_user_id = ?", courseID, mentorID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM course_mentors WHERE course_id = ? AND mentor_user_id = ?",
			courseID, mentorID); err != nil {
			return fmt.Errorf("remove course mentor: %w", err)
		}
		if err := auditTriaged(ctx, tx, triaged, actorID, courseID, mentorID); err != nil {
			return err
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionMentoringCourseMentorRemoved, actorID, mentorID,
			audit.Metadata{CourseID: courseID})
	})
}

func (service *Service) ReviewStudentMentorRemoval(ctx context.Context, courseID, studentID, mentorID,
	actorID string) (RemovalReview, error) {
	return service.reviewRemoval(ctx, courseID, studentID, mentorID, actorID)
}

func (service *Service) RemoveStudentMentor(ctx context.Context, courseID, studentID, mentorID, actorID,
	etag string) error {
	return miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		review, err := service.removalReview(ctx, tx, courseID, studentID, mentorID, actorID)
		if err != nil {
			return err
		}
		if etag == "" {
			return ErrPreconditionRequired
		}
		if etag != review.ETag {
			return ErrPreconditionFailed
		}
		triaged, err := triage(ctx, tx, "course_id = ? AND student_user_id = ? AND mentor_user_id = ?",
			courseID, studentID, mentorID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM mentor_assignments
			WHERE course_id = ? AND student_user_id = ? AND mentor_user_id = ?`, courseID, studentID, mentorID); err != nil {
			return fmt.Errorf("remove student mentor: %w", err)
		}
		if err := auditTriaged(ctx, tx, triaged, actorID, courseID, mentorID); err != nil {
			return err
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionMentoringStudentMentorRemoved, actorID, studentID,
			audit.Metadata{CourseID: courseID, MentorID: mentorID})
	})
}

func (service *Service) ListCourseMentors(ctx context.Context, courseID, actorID string,
	input ListInput) (AssignmentListResult, error) {
	if !validPage(input) {
		return AssignmentListResult{}, ErrInvalid
	}
	if err := course.RequireAssignedSupervisor(ctx, service.database, courseID, actorID); err != nil {
		return AssignmentListResult{}, ErrNotFound
	}
	return listAssignments(ctx, service.database, `SELECT course_id, '', mentor_user_id, assigned_at,
		COALESCE(assigned_by, '') FROM course_mentors WHERE course_id = ? ORDER BY mentor_user_id LIMIT ? OFFSET ?`,
		input, courseID)
}

func (service *Service) ListStudentMentors(ctx context.Context, courseID, studentID, actorID string,
	input ListInput) (AssignmentListResult, error) {
	if !validPage(input) {
		return AssignmentListResult{}, ErrInvalid
	}
	if err := course.RequireAssignedSupervisor(ctx, service.database, courseID, actorID); err != nil {
		return AssignmentListResult{}, ErrNotFound
	}
	return listAssignments(ctx, service.database, `SELECT course_id, student_user_id, mentor_user_id, assigned_at,
		COALESCE(assigned_by, '') FROM mentor_assignments WHERE course_id = ? AND student_user_id = ?
		ORDER BY mentor_user_id LIMIT ? OFFSET ?`, input, courseID, studentID)
}

func (service *Service) Create(ctx context.Context, input CreateInput) (Session, error) {
	topic, err := normalizedRequired(input.Topic, maxTopicRunes, maxTopicBytes)
	if err != nil {
		return Session{}, ErrInvalid
	}
	var created Session
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		member, err := course.ActiveStudentMembership(ctx, tx, input.CourseID, input.StudentID)
		if err != nil {
			return err
		}
		allowed, err := user.MentoringRequestsAllowed(ctx, tx, input.StudentID)
		if err != nil {
			return err
		}
		hasMentor, err := studentHasMentor(ctx, tx, input.CourseID, input.StudentID)
		if err != nil {
			return err
		}
		if !member || !allowed || !hasMentor {
			return ErrUnavailable
		}
		created = Session{ID: "ms_" + uuid.NewString(), CourseID: input.CourseID, StudentID: input.StudentID,
			Topic: topic, ProposedFor: input.ProposedFor, CreatedAt: service.now()}
		_, err = tx.ExecContext(ctx, `INSERT INTO mentoring_sessions
			(id, student_user_id, course_id, topic, proposed_for, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			created.ID, created.StudentID, created.CourseID, created.Topic, nullableTime(created.ProposedFor),
			instant(created.CreatedAt))
		if err != nil {
			return fmt.Errorf("create mentoring session: %w", err)
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionMentoringSessionRequested, input.StudentID, input.StudentID,
			audit.Metadata{CourseID: input.CourseID, MentoringSessionID: created.ID})
	})
	if err != nil {
		return Session{}, err
	}
	return service.Get(ctx, created.ID, input.StudentID)
}

func (service *Service) Get(ctx context.Context, id, actorID string) (Session, error) {
	var value Session
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		loaded, err := loadAuthorizedSession(ctx, tx, id, actorID)
		if err != nil {
			return err
		}
		loaded.CompletionEligibility, err = service.completionEligibility(ctx, tx, loaded, actorID)
		value = loaded
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Session{}, errors.Join(ErrStateUnavailable, fmt.Errorf("project completion eligibility: %w", err))
	}
	return value, err
}

func (service *Service) CompletionEligibility(ctx context.Context, id, actorID string) (CompletionEligibility, error) {
	var result CompletionEligibility
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		value, err := loadAuthorizedSession(ctx, tx, id, actorID)
		if err != nil {
			return err
		}
		result, err = service.completionEligibility(ctx, tx, value, actorID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return CompletionEligibility{}, ErrNotFound
	}
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, sql.ErrNoRows) {
		return CompletionEligibility{}, errors.Join(ErrStateUnavailable,
			fmt.Errorf("project completion eligibility: %w", err))
	}
	return result, err
}

func (service *Service) completionEligibility(ctx context.Context, query miSQLite.Querier, value Session,
	actorID string) (CompletionEligibility, error) {
	now := service.now()
	result := CompletionEligibility{ID: eligibilityID("completion", value.ID, actorID), State: "not-allowed",
		CheckedAt: now}
	if value.ClosedAt != nil {
		result.State = "terminal"
		return result, nil
	}
	if value.MentorID == "" || value.MentorID != actorID {
		return result, nil
	}
	assigned, err := studentMentorExists(ctx, query, value.CourseID, value.StudentID, actorID)
	if err != nil {
		return CompletionEligibility{}, err
	}
	if !assigned {
		return CompletionEligibility{}, ErrNotFound
	}
	if value.ScheduledFor == nil {
		return result, nil
	}
	if value.ScheduledFor.After(now) {
		instant := *value.ScheduledFor
		result.RecheckAfter = &instant
		return result, nil
	}
	result.State = "allowed"
	return result, nil
}

// MentorStudentProfile returns minimal identity only while the mentor has the explicit course-student assignment.
func (service *Service) MentorStudentProfile(ctx context.Context, courseID, studentID,
	mentorID string) (user.MinimalProfile, bool, error) {
	if service.profiles == nil {
		return user.MinimalProfile{}, false, errors.New("mentor student profile service is not configured")
	}
	var profile user.MinimalProfile
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		assigned, err := studentMentorExists(ctx, tx, courseID, studentID, mentorID)
		if err != nil {
			return err
		}
		if !assigned {
			return ErrNotFound
		}
		profile, err = user.LoadMinimalProfile(ctx, tx, studentID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return user.MinimalProfile{}, false, err
	}
	return profile, service.profiles.HasAvatar(studentID), nil
}

// MentorStudentAvatar returns the assigned student's avatar while the explicit assignment remains current.
func (service *Service) MentorStudentAvatar(ctx context.Context, courseID, studentID,
	mentorID string) ([]byte, error) {
	if service.profiles == nil {
		return nil, errors.New("mentor student profile service is not configured")
	}
	var data []byte
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		assigned, err := studentMentorExists(ctx, tx, courseID, studentID, mentorID)
		if err != nil {
			return err
		}
		if !assigned {
			return ErrNotFound
		}
		avatar, err := service.profiles.Avatar(ctx, studentID)
		if errors.Is(err, user.ErrAvatarNotFound) {
			return ErrNotFound
		}
		data = avatar
		return err
	})
	return data, err
}

func (service *Service) List(ctx context.Context, courseID, actorID string, input ListInput) (SessionListResult, error) {
	if !validPage(input) {
		return SessionListResult{}, ErrInvalid
	}
	var result SessionListResult
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var err error
		result, err = service.listSessions(ctx, tx, courseID, actorID, input)
		return err
	})
	return result, err
}

func (service *Service) listSessions(ctx context.Context, query miSQLite.Querier, courseID, actorID string,
	input ListInput) (SessionListResult, error) {
	student, supervisor, err := course.MentoringScope(ctx, query, courseID, actorID)
	if err != nil {
		return SessionListResult{}, err
	}
	mentor, err := user.HasRole(ctx, query, actorID, user.Mentor)
	if err != nil {
		return SessionListResult{}, err
	}
	if !student && !supervisor && !mentor {
		return SessionListResult{}, ErrNotFound
	}
	rows, err := query.QueryContext(ctx, sessionSelect+` WHERE course_id = ? AND
		(? OR student_user_id = ? OR (mentor_user_id = ? AND EXISTS(SELECT 1 FROM mentor_assignments
		WHERE course_id = mentoring_sessions.course_id AND student_user_id = mentoring_sessions.student_user_id
		AND mentor_user_id = ?))) ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		courseID, supervisor, actorID, actorID, actorID, input.Limit+1, input.Offset)
	if err != nil {
		return SessionListResult{}, fmt.Errorf("list mentoring sessions: %w", err)
	}
	values, err := scanSessions(rows)
	if err != nil {
		return SessionListResult{}, err
	}
	hasMore := len(values) > input.Limit
	if hasMore {
		values = values[:input.Limit]
	}
	for index := range values {
		values[index].CompletionEligibility, err = service.completionEligibility(ctx, query, values[index], actorID)
		if err != nil {
			return SessionListResult{}, errors.Join(ErrStateUnavailable,
				fmt.Errorf("project listed session completion eligibility: %w", err))
		}
	}
	return SessionListResult{Sessions: values, HasMore: hasMore}, nil
}

func (service *Service) Update(ctx context.Context, id string, input UpdateInput) (Session, error) {
	if !validUpdate(input) {
		return Session{}, ErrInvalid
	}
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		value, err := loadSession(ctx, tx, id)
		if err != nil {
			return ErrNotFound
		}
		scope, err := service.actorScope(ctx, tx, id, input.ActorID)
		if err != nil || !scope.visible {
			return ErrNotFound
		}
		if value.ClosedAt != nil {
			return ErrInvalidState
		}
		switch {
		case input.MentorID.Set:
			return service.assignSessionMentor(ctx, tx, value, input)
		case input.ClosureReason != "":
			return service.closeSession(ctx, tx, value, input, scope)
		default:
			return service.updateSessionDetails(ctx, tx, value, input, scope)
		}
	})
	if err != nil {
		return Session{}, err
	}
	return service.Get(ctx, id, input.ActorID)
}

type actorScope struct{ student, supervisor, mentor, visible bool }

func (service *Service) actorScope(ctx context.Context, query miSQLite.Querier, id, actorID string) (actorScope, error) {
	value, err := loadSession(ctx, query, id)
	if err != nil {
		return actorScope{}, err
	}
	student, supervisor, err := course.MentoringScope(ctx, query, value.CourseID, actorID)
	if err != nil {
		return actorScope{}, err
	}
	scope := actorScope{student: student && value.StudentID == actorID, supervisor: supervisor,
		mentor: value.MentorID != "" && value.MentorID == actorID}
	scope.visible = scope.student || scope.supervisor || scope.mentor
	return scope, nil
}

func (service *Service) assignSessionMentor(ctx context.Context, tx *sql.Tx, value Session, input UpdateInput) error {
	if err := course.RequireAssignedSupervisor(ctx, tx, value.CourseID, input.ActorID); err != nil {
		return ErrNotFound
	}
	if input.MentorID.Value == nil {
		return ErrNotFound
	}
	exists, err := studentMentorExists(ctx, tx, value.CourseID, value.StudentID, *input.MentorID.Value)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, "UPDATE mentoring_sessions SET mentor_user_id = ? WHERE id = ? AND closed_at IS NULL",
		*input.MentorID.Value, value.ID); err != nil {
		return fmt.Errorf("assign mentoring session: %w", err)
	}
	return audit.WriteWithMetadata(ctx, tx, audit.ActionMentoringSessionAssigned, input.ActorID, value.StudentID,
		audit.Metadata{CourseID: value.CourseID, MentoringSessionID: value.ID, MentorID: *input.MentorID.Value})
}

func (service *Service) updateSessionDetails(ctx context.Context, tx *sql.Tx, value Session, input UpdateInput,
	scope actorScope) error {
	if value.MentorID == "" || !scope.mentor {
		if !scope.student || !onlyStudentReschedule(input) || value.ScheduledFor == nil {
			return ErrNotFound
		}
	}
	now := service.now()
	if value.ScheduledFor != nil && !value.ScheduledFor.After(now) {
		return ErrInvalidState
	}
	response, instructions, meetingURL := value.Response, value.MeetingInstructions, value.MeetingURL
	respondedAt, respondedBy := value.RespondedAt, value.RespondedBy
	if input.Response.Set {
		if !scope.mentor || value.Response != "" || input.Response.Value == nil {
			return ErrInvalidState
		}
		var err error
		response, err = normalizedRequired(*input.Response.Value, maxResponseRunes, maxResponseBytes)
		if err != nil {
			return ErrInvalid
		}
		at := now
		respondedAt, respondedBy = &at, input.ActorID
	}
	var err error
	if input.MeetingInstructions.Set {
		instructions, err = normalizedOptional(input.MeetingInstructions, maxInstructionsRunes, maxInstructionsBytes)
		if err != nil {
			return ErrInvalid
		}
	}
	if input.MeetingURL.Set {
		meetingURL, err = validMeetingURL(input.MeetingURL)
		if err != nil {
			return ErrInvalid
		}
	}
	scheduled := value.ScheduledFor
	if input.ScheduledFor.Set {
		if input.ScheduledFor.Value == nil || !input.ScheduledFor.Value.After(now) {
			return ErrInvalidState
		}
		scheduled = input.ScheduledFor.Value
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mentoring_sessions SET response = ?, responded_at = ?, responded_by = ?,
		scheduled_for = ?, meeting_instructions = ?, meeting_url = ? WHERE id = ? AND closed_at IS NULL`,
		nullable(response), nullableTime(respondedAt), nullable(respondedBy), nullableTime(scheduled),
		nullable(instructions), nullable(meetingURL), value.ID); err != nil {
		return fmt.Errorf("update mentoring details: %w", err)
	}
	action := audit.ActionMentoringSessionUpdated
	if input.ScheduledFor.Set && value.ScheduledFor != nil {
		action = audit.ActionMentoringSessionRescheduled
	} else if input.Response.Set {
		action = audit.ActionMentoringSessionResponded
	} else if input.ScheduledFor.Set {
		action = audit.ActionMentoringSessionScheduled
	}
	return audit.WriteWithMetadata(ctx, tx, action, input.ActorID, value.StudentID,
		rescheduleMetadata(value, input, action))
}

func rescheduleMetadata(value Session, input UpdateInput, action audit.Action) audit.Metadata {
	metadata := audit.Metadata{CourseID: value.CourseID, MentoringSessionID: value.ID}
	if action == audit.ActionMentoringSessionRescheduled {
		metadata.PreviousScheduledAt = instant(*value.ScheduledFor)
		metadata.NewScheduledAt = instant(*input.ScheduledFor.Value)
	}
	return metadata
}

func (service *Service) closeSession(ctx context.Context, tx *sql.Tx, value Session, input UpdateInput,
	scope actorScope) error {
	now := service.now()
	switch input.ClosureReason {
	case reasonCanceled:
		if value.ScheduledFor == nil {
			if !scope.student && !scope.supervisor {
				return ErrNotFound
			}
		} else if !value.ScheduledFor.After(now) || !scope.student && !scope.mentor {
			return ErrInvalidState
		}
	case "completed":
		assigned, err := studentMentorExists(ctx, tx, value.CourseID, value.StudentID, input.ActorID)
		if err != nil {
			return err
		}
		if !scope.mentor || !assigned || value.ScheduledFor == nil || value.ScheduledFor.After(now) {
			return ErrInvalidState
		}
	default:
		return ErrInvalid
	}
	result, err := tx.ExecContext(ctx, `UPDATE mentoring_sessions SET closed_at = ?, closed_by = ?, closure_reason = ?
		WHERE id = ? AND closed_at IS NULL`, instant(now), input.ActorID, input.ClosureReason, value.ID)
	if err != nil {
		return fmt.Errorf("close mentoring session: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return ErrInvalidState
	}
	action := audit.ActionMentoringSessionCancelled
	if input.ClosureReason == "completed" {
		action = audit.ActionMentoringSessionCompleted
	}
	return audit.WriteWithMetadata(ctx, tx, action, input.ActorID, value.StudentID,
		audit.Metadata{CourseID: value.CourseID, MentoringSessionID: value.ID})
}

func (service *Service) DeleteCourseData(ctx context.Context, query miSQLite.Querier, courseID string) error {
	_, err := query.ExecContext(ctx, "DELETE FROM mentoring_sessions WHERE course_id = ?", courseID)
	return err
}

func (service *Service) DeleteStudentCourseData(ctx context.Context, query miSQLite.Querier, courseID,
	studentID string) error {
	if _, err := query.ExecContext(ctx, "DELETE FROM mentoring_sessions WHERE course_id = ? AND student_user_id = ?",
		courseID, studentID); err != nil {
		return err
	}
	_, err := query.ExecContext(ctx, "DELETE FROM mentor_assignments WHERE course_id = ? AND student_user_id = ?",
		courseID, studentID)
	return err
}

func (service *Service) CourseDeletionImpact(ctx context.Context, query miSQLite.Querier,
	courseID string) ([]string, error) {
	return mentoringDeletionImpact(ctx, query, courseID, "", false)
}

func (service *Service) StudentCourseDeletionImpact(ctx context.Context, query miSQLite.Querier, courseID,
	studentID string) ([]string, error) {
	return mentoringDeletionImpact(ctx, query, courseID, studentID, true)
}

func mentoringDeletionImpact(ctx context.Context, query miSQLite.Querier, courseID, studentID string,
	studentOnly bool) ([]string, error) {
	condition, args := "course_id = ?", []any{courseID}
	if studentOnly {
		condition, args = "course_id = ? AND student_user_id = ?", []any{courseID, studentID}
	}
	statements := []struct{ prefix, query string }{
		{"session:", "SELECT id FROM mentoring_sessions WHERE " + condition + " ORDER BY id"},
		{"assignment:", "SELECT course_id || ':' || student_user_id || ':' || mentor_user_id FROM mentor_assignments WHERE " +
			condition + " ORDER BY course_id, student_user_id, mentor_user_id"},
	}
	if !studentOnly {
		statements = append(statements, struct{ prefix, query string }{"course-mentor:",
			"SELECT course_id || ':' || mentor_user_id FROM course_mentors WHERE course_id = ? ORDER BY mentor_user_id"})
	}
	var result []string
	for _, statement := range statements {
		rows, err := query.QueryContext(ctx, statement.query, args...)
		if err != nil {
			return nil, fmt.Errorf("list mentoring deletion impact: %w", err)
		}
		ids, err := miSQLite.ScanStrings(rows)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			result = append(result, statement.prefix+id)
		}
	}
	return result, nil
}

func (service *Service) DeleteAccountData(ctx context.Context, query miSQLite.Querier, accountID string) error {
	if _, err := triage(ctx, query, "mentor_user_id = ?", accountID); err != nil {
		return err
	}
	if _, err := query.ExecContext(ctx, "DELETE FROM course_mentors WHERE mentor_user_id = ?", accountID); err != nil {
		return err
	}
	if _, err := query.ExecContext(ctx, "DELETE FROM mentoring_sessions WHERE student_user_id = ?", accountID); err != nil {
		return err
	}
	_, err := query.ExecContext(ctx, `UPDATE mentoring_sessions SET responded_by = CASE WHEN responded_by = ? THEN NULL
		ELSE responded_by END, closed_by = CASE WHEN closed_by = ? THEN NULL ELSE closed_by END`, accountID, accountID)
	return err
}

type triagedSession struct{ id, studentID string }

func triage(ctx context.Context, query miSQLite.Querier, condition string, args ...any) ([]triagedSession, error) {
	rows, err := query.QueryContext(ctx, `SELECT id, student_user_id FROM mentoring_sessions
		WHERE closed_at IS NULL AND `+condition, args...)
	if err != nil {
		return nil, fmt.Errorf("list mentoring work for triage: %w", err)
	}
	var affected []triagedSession
	for rows.Next() {
		var value triagedSession
		if err := rows.Scan(&value.id, &value.studentID); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		affected = append(affected, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	_, err = query.ExecContext(ctx, `UPDATE mentoring_sessions SET mentor_user_id = NULL, proposed_for = NULL,
		scheduled_for = NULL, meeting_instructions = NULL, meeting_url = NULL WHERE closed_at IS NULL AND `+condition, args...)
	if err != nil {
		return nil, fmt.Errorf("return mentoring work to triage: %w", err)
	}
	return affected, nil
}

func auditTriaged(ctx context.Context, query miSQLite.Querier, affected []triagedSession, actorID, courseID,
	mentorID string) error {
	for _, value := range affected {
		if err := audit.WriteWithMetadata(ctx, query, audit.ActionMentoringSessionTriaged, actorID, value.studentID,
			audit.Metadata{CourseID: courseID, MentoringSessionID: value.id, MentorID: mentorID}); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) reviewRemoval(ctx context.Context, courseID, studentID, mentorID,
	actorID string) (RemovalReview, error) {
	var review RemovalReview
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var err error
		review, err = service.removalReview(ctx, tx, courseID, studentID, mentorID, actorID)
		return err
	})
	return review, err
}

type removalSnapshot struct {
	CourseID, StudentID, MentorID, AssignedAt, AssignedBy string
	OpenWork                                              []removalWorkSnapshot
}

type removalWorkSnapshot struct {
	ID, ProposedFor, ScheduledFor, MeetingInstructions, MeetingURL, Response, RespondedBy string
}

// removalReview authorizes and hashes every current fact that changes the reviewed removal consequences.
func (service *Service) removalReview(ctx context.Context, query miSQLite.Querier, courseID, studentID, mentorID,
	actorID string) (RemovalReview, error) {
	if err := course.RequireAssignedSupervisor(ctx, query, courseID, actorID); err != nil {
		return RemovalReview{}, ErrNotFound
	}
	snapshot := removalSnapshot{CourseID: courseID, StudentID: studentID, MentorID: mentorID,
		OpenWork: make([]removalWorkSnapshot, 0)}
	var assignedAt string
	var assignedBy sql.NullString
	statement := `SELECT assigned_at, assigned_by FROM course_mentors WHERE course_id = ? AND mentor_user_id = ?`
	args := []any{courseID, mentorID}
	condition := "course_id = ? AND mentor_user_id = ?"
	if studentID != "" {
		statement = `SELECT assigned_at, assigned_by FROM mentor_assignments
			WHERE course_id = ? AND student_user_id = ? AND mentor_user_id = ?`
		args = []any{courseID, studentID, mentorID}
		condition = "course_id = ? AND student_user_id = ? AND mentor_user_id = ?"
	}
	if err := query.QueryRowContext(ctx, statement, args...).Scan(&assignedAt, &assignedBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RemovalReview{}, ErrNotFound
		}
		return RemovalReview{}, fmt.Errorf("load mentor removal assignment: %w", err)
	}
	snapshot.AssignedAt, snapshot.AssignedBy = assignedAt, assignedBy.String
	rows, err := query.QueryContext(ctx, `SELECT id, COALESCE(proposed_for, ''), COALESCE(scheduled_for, ''),
		COALESCE(meeting_instructions, ''), COALESCE(meeting_url, ''), COALESCE(response, ''),
		COALESCE(responded_by, '') FROM mentoring_sessions WHERE closed_at IS NULL AND `+condition+` ORDER BY id`, args...)
	if err != nil {
		return RemovalReview{}, fmt.Errorf("load mentor removal work: %w", err)
	}
	for rows.Next() {
		var work removalWorkSnapshot
		if err := rows.Scan(&work.ID, &work.ProposedFor, &work.ScheduledFor, &work.MeetingInstructions,
			&work.MeetingURL, &work.Response, &work.RespondedBy); err != nil {
			return RemovalReview{}, errors.Join(fmt.Errorf("scan mentor removal work: %w", err), rows.Close())
		}
		snapshot.OpenWork = append(snapshot.OpenWork, work)
	}
	if err := rows.Err(); err != nil {
		return RemovalReview{}, errors.Join(fmt.Errorf("iterate mentor removal work: %w", err), rows.Close())
	}
	if err := rows.Close(); err != nil {
		return RemovalReview{}, fmt.Errorf("close mentor removal work: %w", err)
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return RemovalReview{}, fmt.Errorf("encode mentor removal review: %w", err)
	}
	digest := sha256.Sum256(canonical)
	token := hex.EncodeToString(digest[:])
	scope := "course"
	if studentID != "" {
		scope = "student"
	}
	return RemovalReview{ID: "mrr_" + token[:32], ETag: `"` + token + `"`, Scope: scope,
		AffectedOpenWork: len(snapshot.OpenWork), CheckedAt: service.now()}, nil
}

func eligibilityID(kind string, values ...string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + strings.Join(values, "\x00")))
	return "mel_" + hex.EncodeToString(digest[:])[:32]
}

const sessionSelect = `SELECT id, student_user_id, course_id, COALESCE(mentor_user_id, ''), topic,
	COALESCE(response, ''), responded_at, COALESCE(responded_by, ''), proposed_for, scheduled_for,
	COALESCE(meeting_instructions, ''), COALESCE(meeting_url, ''), created_at, closed_at,
	COALESCE(closed_by, ''), COALESCE(closure_reason, '') FROM mentoring_sessions`

type scanner interface{ Scan(...any) error }

func loadSession(ctx context.Context, query miSQLite.Querier, id string) (Session, error) {
	value, err := scanSession(query.QueryRowContext(ctx, sessionSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return value, err
}

// loadAuthorizedSession combines visibility and loading in one statement so assignment changes cannot race a second read.
func loadAuthorizedSession(ctx context.Context, query miSQLite.Querier, id, actorID string) (Session, error) {
	return scanSession(query.QueryRowContext(ctx, sessionSelect+` WHERE id = ? AND (student_user_id = ?
		OR (mentor_user_id = ? AND EXISTS(SELECT 1 FROM mentor_assignments
		WHERE course_id = mentoring_sessions.course_id AND student_user_id = mentoring_sessions.student_user_id
		AND mentor_user_id = ?)) OR EXISTS(SELECT 1 FROM course_supervisors
		WHERE course_id = mentoring_sessions.course_id AND supervisor_user_id = ?))`,
		id, actorID, actorID, actorID, actorID))
}

func scanSession(row scanner) (Session, error) {
	var value Session
	var created string
	var responded, proposed, scheduled, closed sql.NullString
	err := row.Scan(&value.ID, &value.StudentID, &value.CourseID, &value.MentorID, &value.Topic, &value.Response,
		&responded, &value.RespondedBy, &proposed, &scheduled, &value.MeetingInstructions, &value.MeetingURL,
		&created, &closed, &value.ClosedBy, &value.ClosureReason)
	if err != nil {
		return Session{}, err
	}
	if value.CreatedAt, err = parseInstant(created); err != nil {
		return Session{}, err
	}
	for source, destination := range map[sql.NullString]**time.Time{responded: &value.RespondedAt,
		proposed: &value.ProposedFor, scheduled: &value.ScheduledFor, closed: &value.ClosedAt} {
		if source.Valid {
			parsed, parseErr := parseInstant(source.String)
			if parseErr != nil {
				return Session{}, parseErr
			}
			*destination = &parsed
		}
	}
	return value, nil
}

func scanSessions(rows *sql.Rows) ([]Session, error) {
	var values []Session
	for rows.Next() {
		value, err := scanSession(rows)
		if err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return values, nil
}

func listAssignments(ctx context.Context, query miSQLite.Querier, statement string, input ListInput,
	args ...any) (AssignmentListResult, error) {
	args = append(args, input.Limit+1, input.Offset)
	rows, err := query.QueryContext(ctx, statement, args...)
	if err != nil {
		return AssignmentListResult{}, err
	}
	var values []Assignment
	for rows.Next() {
		var value Assignment
		var at string
		if err := rows.Scan(&value.CourseID, &value.StudentID, &value.MentorID, &at, &value.AssignedBy); err != nil {
			return AssignmentListResult{}, errors.Join(err, rows.Close())
		}
		value.AssignedAt, err = parseInstant(at)
		if err != nil {
			return AssignmentListResult{}, errors.Join(err, rows.Close())
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return AssignmentListResult{}, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return AssignmentListResult{}, err
	}
	hasMore := len(values) > input.Limit
	if hasMore {
		values = values[:input.Limit]
	}
	return AssignmentListResult{Assignments: values, HasMore: hasMore}, nil
}

func auditIfChanged(ctx context.Context, query miSQLite.Querier, result sql.Result, action audit.Action,
	actorID, subjectID string, metadata audit.Metadata) error {
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	return audit.WriteWithMetadata(ctx, query, action, actorID, subjectID, metadata)
}

func courseMentorExists(ctx context.Context, query miSQLite.Querier, courseID, mentorID string) (bool, error) {
	var exists int
	err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM course_mentors
		WHERE course_id = ? AND mentor_user_id = ?)`, courseID, mentorID).Scan(&exists)
	return exists != 0, err
}

func studentMentorExists(ctx context.Context, query miSQLite.Querier, courseID, studentID,
	mentorID string) (bool, error) {
	var exists int
	err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mentor_assignments WHERE course_id = ?
		AND student_user_id = ? AND mentor_user_id = ?)`, courseID, studentID, mentorID).Scan(&exists)
	return exists != 0, err
}

func studentHasMentor(ctx context.Context, query miSQLite.Querier, courseID, studentID string) (bool, error) {
	var exists int
	err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mentor_assignments
		WHERE course_id = ? AND student_user_id = ?)`, courseID, studentID).Scan(&exists)
	return exists != 0, err
}

func validUpdate(input UpdateInput) bool {
	actions := 0
	if input.MentorID.Set {
		actions++
	}
	if input.ClosureReason != "" {
		actions++
	}
	if input.Response.Set || input.ScheduledFor.Set || input.MeetingInstructions.Set || input.MeetingURL.Set {
		actions++
	}
	return actions == 1
}

func onlyStudentReschedule(input UpdateInput) bool {
	return input.ScheduledFor.Set && !input.Response.Set && !input.MeetingInstructions.Set && !input.MeetingURL.Set
}

func validPage(input ListInput) bool {
	return input.Limit > 0 && input.Limit <= 100 && input.Offset >= 0 && input.Offset <= 10_000
}

func normalizedRequired(value string, maxRunes, maxBytes int) (string, error) {
	result, err := normalized(value, maxRunes, maxBytes)
	if err != nil || result == "" {
		return "", ErrInvalid
	}
	return result, nil
}

func normalizedOptional(value OptionalString, maxRunes, maxBytes int) (string, error) {
	if value.Value == nil {
		return "", nil
	}
	return normalized(*value.Value, maxRunes, maxBytes)
}

func normalized(value string, maxRunes, maxBytes int) (string, error) {
	if !utf8.ValidString(value) || len(value) > maxBytes {
		return "", ErrInvalid
	}
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"))
	if utf8.RuneCountInString(value) > maxRunes {
		return "", ErrInvalid
	}
	for _, char := range value {
		if char == 0 || char < 32 && char != '\t' && char != '\n' {
			return "", ErrInvalid
		}
	}
	return value, nil
}

func validMeetingURL(value OptionalString) (string, error) {
	if value.Value == nil || *value.Value == "" {
		return "", nil
	}
	raw := strings.TrimSpace(*value.Value)
	parsed, err := url.Parse(raw)
	if err != nil || len(raw) > maxURLBytes || !utf8.ValidString(raw) || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil {
		return "", ErrInvalid
	}
	return raw, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return instant(*value)
}

func instant(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000Z") }

func parseInstant(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02T15:04:05.000000Z", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored mentoring instant: %w", err)
	}
	return parsed, nil
}
