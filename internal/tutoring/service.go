package tutoring

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/course"
	"github.com/thorstenkramm/mia/internal/jobs"
	"github.com/thorstenkramm/mia/internal/material"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

const storedInstant = "2006-01-02T15:04:05.000000Z"

// Service is safe for concurrent use after construction.
type Service struct {
	database              *sql.DB
	materials             *material.Service
	logger                *slog.Logger
	manager               *Manager
	mentoringAvailability func(context.Context, miSQLite.Querier, string, string) (bool, error)
}

func NewService(database *sql.DB, materials *material.Service, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{database: database, materials: materials, logger: logger}
}

func (service *Service) SetManager(manager *Manager) { service.manager = manager }

// SetMentoringAvailability injects the mentoring owner's last-resource gate without reversing package ownership.
func (service *Service) SetMentoringAvailability(
	availability func(context.Context, miSQLite.Querier, string, string) (bool, error),
) {
	service.mentoringAvailability = availability
}

func (service *Service) Start(ctx context.Context, input StartInput) (Session, bool, error) {
	requestID, ok := canonicalUUID(input.RequestID)
	if !ok || input.CourseID == "" || input.StudentID == "" || service.materials == nil {
		return Session{}, false, ErrInvalid
	}
	selected := append([]string(nil), input.SelectedMaterialIDs...)
	sort.Strings(selected)
	for index, id := range selected {
		if id == "" || index > 0 && id == selected[index-1] {
			return Session{}, false, ErrInvalid
		}
	}
	digest, err := bodyDigest(struct {
		CourseID  string   `json:"course_id"`
		Materials []string `json:"materials"`
	}{input.CourseID, selected})
	if err != nil {
		return Session{}, false, err
	}
	var created Session
	replay := false
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		existing, existingDigest, err := loadSessionByRequest(ctx, tx, input.StudentID, requestID)
		if err == nil {
			if existing.State != "active" || !equalDigest(existingDigest, digest) {
				return ErrConflict
			}
			created, replay = existing, true
			return loadSelected(ctx, tx, &created)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tutoring_sessions
			WHERE student_user_id = ? AND state = 'active')`, input.StudentID).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return ErrActiveSession
		}
		courseContext, err := course.LoadTutorContext(ctx, tx, input.CourseID, input.StudentID)
		if err != nil {
			return ErrNotFound
		}
		if !courseContext.Active {
			return ErrInvalidState
		}
		ready, err := material.MaterialReady(ctx, tx, input.CourseID)
		if err != nil {
			return err
		}
		if !ready {
			return ErrInvalidState
		}
		if _, err := service.materials.SelectForTutor(ctx, tx, input.CourseID, input.StudentID, selected); err != nil {
			return ErrNotFound
		}
		now := time.Now()
		created = Session{ID: "ts_" + uuid.NewString(), CourseID: input.CourseID, StudentID: input.StudentID,
			State: "active", StartedAt: now, LastActivityAt: now, SelectedMaterialIDs: selected}
		_, err = tx.ExecContext(ctx, `INSERT INTO tutoring_sessions
			(id, course_id, student_user_id, client_request_id, request_digest, started_at, last_activity_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, created.ID, created.CourseID, created.StudentID, requestID, digest[:],
			instant(now), instant(now))
		if err != nil {
			if strings.Contains(err.Error(), "tutoring_sessions_one_active_student_idx") ||
				strings.Contains(err.Error(), "UNIQUE constraint failed: tutoring_sessions.student_user_id") {
				return ErrActiveSession
			}
			return fmt.Errorf("insert tutoring session: %w", err)
		}
		for _, materialID := range selected {
			if _, err := tx.ExecContext(ctx, `INSERT INTO session_material_selections
				(tutoring_session_id, material_id, selected_at) VALUES (?, ?, ?)`, created.ID, materialID,
				instant(now)); err != nil {
				return fmt.Errorf("select tutoring material: %w", err)
			}
		}
		return nil
	})
	return created, replay, err
}

func (service *Service) Get(ctx context.Context, sessionID, actorID string) (Session, error) {
	session, role, err := loadScopedSession(ctx, service.database, sessionID, actorID)
	if err != nil {
		return Session{}, err
	}
	if role == "supervisor" && session.State == "active" {
		session.Summary, session.FollowUp, session.SummarySource, session.SummaryActor = "", "", "", ""
		return session, nil
	}
	err = loadSelected(ctx, service.database, &session)
	return session, err
}

// DiscoverActive returns the caller's single active session across all courses from one database snapshot.
func (service *Service) DiscoverActive(ctx context.Context, actorID string) (*ActiveSession, error) {
	var discovered *ActiveSession
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var student int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_roles
			WHERE user_id = ? AND role = 'student')`, actorID).Scan(&student); err != nil {
			return fmt.Errorf("authorize active tutoring session discovery: %w", err)
		}
		if student == 0 {
			return ErrUnauthorized
		}
		value, err := loadActiveSession(ctx, tx, actorID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("discover active tutoring session: %w", err)
		}
		discovered = &value
		return nil
	})
	if err != nil && !errors.Is(err, ErrUnauthorized) {
		return nil, fmt.Errorf("%w: %w", ErrDiscoveryUnavailable, err)
	}
	return discovered, err
}

func (service *Service) List(ctx context.Context, courseID, actorID string, input ListInput) (ListResult, error) {
	if input.Limit < 1 || input.Limit > 100 || input.Offset < 0 || input.Offset > 10_000 {
		return ListResult{}, ErrInvalid
	}
	student, supervisor, err := course.TutoringScope(ctx, service.database, courseID, actorID)
	if err != nil {
		return ListResult{}, err
	}
	if !student && !supervisor {
		return ListResult{}, ErrNotFound
	}
	query := sessionSelect + ` WHERE s.course_id = ? AND s.student_user_id = ?
		ORDER BY s.started_at DESC, s.id DESC LIMIT ? OFFSET ?`
	args := []any{courseID, actorID, input.Limit + 1, input.Offset}
	if supervisor {
		query = sessionSelect + ` WHERE s.course_id = ? ORDER BY s.started_at DESC, s.id DESC LIMIT ? OFFSET ?`
		args = []any{courseID, input.Limit + 1, input.Offset}
	}
	rows, err := service.database.QueryContext(ctx, query, args...)
	if err != nil {
		return ListResult{}, err
	}
	var sessions []Session
	for rows.Next() {
		value, err := scanSession(rows)
		if err != nil {
			return ListResult{}, errors.Join(err, rows.Close())
		}
		if supervisor && value.State == "active" {
			value.Summary, value.FollowUp, value.SummarySource, value.SummaryActor = "", "", "", ""
		}
		sessions = append(sessions, value)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return ListResult{}, err
	}
	hasMore := len(sessions) > input.Limit
	if hasMore {
		sessions = sessions[:input.Limit]
	}
	return ListResult{Sessions: sessions, HasMore: hasMore}, nil
}

// CurrentWork returns session state, response slots, and action eligibility from one database snapshot.
func (service *Service) CurrentWork(ctx context.Context, sessionID, actorID string, input WorkQuery) (CurrentWork, error) {
	if input.MessageRequestID != "" {
		if _, ok := canonicalUUID(input.MessageRequestID); !ok {
			return CurrentWork{}, ErrInvalid
		}
	}
	if input.ResponseID != "" && (len(input.ResponseID) > 128 || !utf8.ValidString(input.ResponseID)) {
		return CurrentWork{}, ErrInvalid
	}
	var work CurrentWork
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var sessionState string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM tutoring_sessions
			WHERE id = ? AND student_user_id = ?`, sessionID, actorID).Scan(&sessionState); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		work.SessionID, work.SessionState = sessionID, sessionState
		generating, err := loadWorkResponse(ctx, tx, sessionID, "generating")
		if err != nil {
			return err
		}
		work.Generating = generating
		queued, err := loadQueuedWork(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		work.Queued = queued
		latest, err := loadLatestWorkResponse(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		work.LatestResponse = latest
		retryable, err := loadLatestRetryableResponse(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if input.MessageRequestID != "" {
			message, _, response, err := loadMessageByRequest(ctx, tx, sessionID, input.MessageRequestID)
			if err == nil {
				work.ReconciledMessage = &MessageResult{Message: message, Response: response, Replay: true}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if input.ResponseID != "" {
			response, err := loadResponseInSession(ctx, tx, sessionID, input.ResponseID)
			if err == nil {
				work.ReconciledResponse = &response
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		setWorkStateAndActions(&work, retryable)
		return nil
	})
	return work, err
}

func loadLatestRetryableResponse(ctx context.Context, query miSQLite.Querier, sessionID string) (*Response, error) {
	row := query.QueryRowContext(ctx, `SELECT r.id, r.session_id, r.student_message_id,
		COALESCE(r.retry_of_response_id, ''), r.attempt, r.state, r.content, COALESCE(r.failure_code, ''),
		r.created_at, r.started_at, r.finished_at FROM tutor_responses r
		JOIN student_messages m ON m.id = r.student_message_id WHERE r.session_id = ?
		AND r.attempt = (SELECT MAX(last.attempt) FROM tutor_responses last
			WHERE last.student_message_id = r.student_message_id)
		AND r.state IN ('failed', 'interrupted') ORDER BY m.sequence DESC LIMIT 1`, sessionID)
	return scanOptionalResponse(row)
}

func loadWorkResponse(ctx context.Context, query miSQLite.Querier, sessionID, state string) (*Response, error) {
	row := query.QueryRowContext(ctx, `SELECT id, session_id, student_message_id,
		COALESCE(retry_of_response_id, ''), attempt, state, content, COALESCE(failure_code, ''),
		created_at, started_at, finished_at FROM tutor_responses WHERE session_id = ? AND state = ?`, sessionID, state)
	return scanOptionalResponse(row)
}

func loadQueuedWork(ctx context.Context, query miSQLite.Querier, sessionID string) (*MessageResult, error) {
	row := query.QueryRowContext(ctx, `SELECT m.id, m.tutoring_session_id, m.sequence, m.content, m.created_at,
		r.id, r.session_id, r.student_message_id, COALESCE(r.retry_of_response_id, ''), r.attempt,
		r.state, r.content, COALESCE(r.failure_code, ''), r.created_at, r.started_at, r.finished_at
		FROM tutor_responses r JOIN student_messages m ON m.id = r.student_message_id
		WHERE r.session_id = ? AND r.state = 'queued'`, sessionID)
	message, response, err := scanMessageResponse(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &MessageResult{Message: message, Response: response}, nil
}

func loadLatestWorkResponse(ctx context.Context, query miSQLite.Querier, sessionID string) (*Response, error) {
	row := query.QueryRowContext(ctx, `SELECT r.id, r.session_id, r.student_message_id,
		COALESCE(r.retry_of_response_id, ''), r.attempt, r.state, r.content, COALESCE(r.failure_code, ''),
		r.created_at, r.started_at, r.finished_at FROM tutor_responses r
		JOIN student_messages m ON m.id = r.student_message_id WHERE r.session_id = ?
		ORDER BY m.sequence DESC, r.attempt DESC LIMIT 1`, sessionID)
	return scanOptionalResponse(row)
}

func scanOptionalResponse(row scanner) (*Response, error) {
	response, err := scanResponse(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func loadResponseInSession(ctx context.Context, query miSQLite.Querier, sessionID, responseID string) (Response, error) {
	row := query.QueryRowContext(ctx, `SELECT id, session_id, student_message_id,
		COALESCE(retry_of_response_id, ''), attempt, state, content, COALESCE(failure_code, ''),
		created_at, started_at, finished_at FROM tutor_responses WHERE session_id = ? AND id = ?`, sessionID, responseID)
	return scanResponse(row)
}

func setWorkStateAndActions(work *CurrentWork, retryable *Response) {
	work.Actions.StopResponseIDs = []string{}
	work.State = "idle"
	if work.SessionState == "completed" {
		work.State = "completed"
		return
	}
	work.RemainingQueueCapacity = 1
	if work.Generating != nil {
		work.State = "generating"
		work.Actions.StopResponseIDs = append(work.Actions.StopResponseIDs, work.Generating.ID)
		work.Actions.Reconnect = true
		work.Actions.ReconnectResponseID = work.Generating.ID
	}
	if work.Queued != nil {
		work.RemainingQueueCapacity = 0
		work.Actions.StopResponseIDs = append(work.Actions.StopResponseIDs, work.Queued.Response.ID)
		if work.Generating == nil {
			work.State = "queued"
			work.Actions.Reconnect = true
			work.Actions.ReconnectResponseID = work.Queued.Response.ID
		} else {
			work.State = "generating_and_queued"
		}
	}
	work.Actions.Stop = len(work.Actions.StopResponseIDs) != 0
	work.Actions.Submit = work.Generating == nil && work.Queued == nil
	work.Actions.Queue = work.Generating != nil && work.Queued == nil
	work.Actions.Finish = work.Actions.Submit
	if work.Actions.Submit && retryable != nil {
		work.Actions.Retry = true
		work.Actions.RetryResponseID = retryable.ID
	}
}

func (service *Service) Submit(ctx context.Context, input SubmitInput) (MessageResult, error) {
	requestID, ok := canonicalUUID(input.RequestID)
	if !ok || !validMessage(input.Content) {
		return MessageResult{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(input.Content))
	var result MessageResult
	shouldDispatch := false
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := lockSessionForMessage(ctx, tx, input.SessionID, input.StudentID); err != nil {
			return err
		}
		existing, existingDigest, response, err := loadMessageByRequest(ctx, tx, input.SessionID, requestID)
		if err == nil {
			if !equalDigest(existingDigest, digest) {
				return ErrConflict
			}
			if existing.SessionID != input.SessionID {
				return ErrNotFound
			}
			var owner string
			if err := tx.QueryRowContext(ctx, "SELECT student_user_id FROM tutoring_sessions WHERE id = ?",
				input.SessionID).Scan(&owner); err != nil || owner != input.StudentID {
				return ErrNotFound
			}
			result = MessageResult{Message: existing, Response: response, RequestID: requestID, Replay: true}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM tutoring_sessions WHERE id = ? AND student_user_id = ?`,
			input.SessionID, input.StudentID).Scan(&state); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if state != "active" {
			return ErrInvalidState
		}
		var generating, queued, sequence int
		if err := tx.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM tutor_responses WHERE session_id = ? AND state = 'generating'),
			(SELECT COUNT(*) FROM tutor_responses WHERE session_id = ? AND state = 'queued'),
			COALESCE((SELECT MAX(sequence) FROM student_messages WHERE tutoring_session_id = ?), 0)`, input.SessionID,
			input.SessionID, input.SessionID).Scan(&generating, &queued, &sequence); err != nil {
			return err
		}
		if queued != 0 {
			return ErrWorkBusy
		}
		now := time.Now()
		message := Message{ID: "msg_" + uuid.NewString(), SessionID: input.SessionID, Content: input.Content,
			Sequence: sequence + 1, CreatedAt: now}
		newResponse := Response{ID: "rsp_" + uuid.NewString(), SessionID: input.SessionID, MessageID: message.ID,
			Attempt: 1, State: "queued", CreatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO student_messages
			(id, tutoring_session_id, sequence, content, client_request_id, request_digest, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, message.ID, message.SessionID, message.Sequence, message.Content,
			requestID, digest[:], instant(now)); err != nil {
			if isWorkSlotConstraint(err) {
				return ErrWorkBusy
			}
			return fmt.Errorf("insert tutoring message: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tutor_responses
			(id, session_id, student_message_id, attempt, state, created_at) VALUES (?, ?, ?, 1, 'queued', ?)`,
			newResponse.ID, newResponse.SessionID, newResponse.MessageID, instant(now)); err != nil {
			if isWorkSlotConstraint(err) {
				return ErrWorkBusy
			}
			return fmt.Errorf("insert tutor response: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE tutoring_sessions SET last_activity_at = ? WHERE id = ?",
			instant(now), input.SessionID); err != nil {
			return err
		}
		result, shouldDispatch = MessageResult{Message: message, Response: newResponse, RequestID: requestID}, generating == 0
		return nil
	})
	if err == nil && shouldDispatch && service.manager != nil {
		service.manager.Dispatch(result.Response.ID)
	}
	return result, err
}

func (service *Service) Retry(ctx context.Context, input RetryInput) (ResponseResult, error) {
	requestID, ok := canonicalUUID(input.RequestID)
	if !ok {
		return ResponseResult{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(input.MessageID))
	var result ResponseResult
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := lockSessionForRetry(ctx, tx, input.MessageID, input.StudentID); err != nil {
			return err
		}
		original, err := loadLastResponseForMessage(ctx, tx, input.MessageID)
		if err != nil {
			return ErrNotFound
		}
		var state, owner string
		if err := tx.QueryRowContext(ctx, `SELECT state, student_user_id FROM tutoring_sessions WHERE id = ?`,
			original.SessionID).Scan(&state, &owner); err != nil || owner != input.StudentID {
			return ErrNotFound
		}
		existing, existingDigest, err := loadRetryByRequest(ctx, tx, original.SessionID, requestID)
		if err == nil {
			if !equalDigest(existingDigest, digest) {
				return ErrConflict
			}
			result = ResponseResult{Response: existing, Replay: true}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if state != "active" {
			return ErrInvalidState
		}
		var busy, attempt int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(attempt), 0) FROM tutor_responses
			WHERE student_message_id = ?`, original.MessageID).Scan(&busy, &attempt); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tutor_responses WHERE session_id = ?
			AND state IN ('queued', 'generating')`, original.SessionID).Scan(&busy); err != nil || busy != 0 {
			if err != nil {
				return err
			}
			return ErrWorkBusy
		}
		if original.State != "failed" && original.State != "interrupted" {
			return ErrInvalidState
		}
		created := Response{ID: "rsp_" + uuid.NewString(), SessionID: original.SessionID, MessageID: original.MessageID,
			RetryOfID: original.ID, Attempt: attempt + 1, State: "queued", CreatedAt: time.Now()}
		_, err = tx.ExecContext(ctx, `INSERT INTO tutor_responses
			(id, session_id, student_message_id, retry_of_response_id, attempt, state, created_at,
			retry_request_id, retry_request_digest) VALUES (?, ?, ?, ?, ?, 'queued', ?, ?, ?)`, created.ID,
			created.SessionID, created.MessageID, original.ID, created.Attempt, instant(created.CreatedAt), requestID, digest[:])
		if isWorkSlotConstraint(err) {
			return ErrWorkBusy
		}
		if err != nil {
			return fmt.Errorf("insert tutor response retry: %w", err)
		}
		result.Response = created
		return nil
	})
	if err == nil && !result.Replay && service.manager != nil {
		service.manager.Dispatch(result.Response.ID)
	}
	return result, err
}

func (service *Service) Interrupt(ctx context.Context, responseID, actorID string) error {
	response, err := loadResponseScopedOwner(ctx, service.database, responseID, actorID)
	if err != nil {
		return err
	}
	if response.State == "queued" {
		result, err := service.database.ExecContext(ctx, `UPDATE tutor_responses SET state = 'interrupted',
			finished_at = ? WHERE id = ? AND state = 'queued'`, instant(time.Now()), responseID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrInvalidState
		}
		if service.manager != nil {
			service.manager.queuedInterrupted(responseID)
		}
		return nil
	}
	if response.State != "generating" || service.manager == nil {
		return ErrInvalidState
	}
	return service.manager.Interrupt(responseID)
}

// InterruptAndCurrentWork binds a Stop result to its immutable response and the following persisted work snapshot.
func (service *Service) InterruptAndCurrentWork(ctx context.Context, responseID, actorID string) (CurrentWork, error) {
	if err := service.Interrupt(ctx, responseID, actorID); err != nil {
		return CurrentWork{}, err
	}
	response, err := loadResponseScopedOwner(ctx, service.database, responseID, actorID)
	if err != nil {
		return CurrentWork{}, err
	}
	return service.CurrentWork(ctx, response.SessionID, actorID, WorkQuery{ResponseID: response.ID})
}

func (service *Service) Complete(ctx context.Context, sessionID, actorID string) (Session, error) {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var courseID, state string
		if err := tx.QueryRowContext(ctx, `SELECT course_id, state FROM tutoring_sessions
			WHERE id = ? AND student_user_id = ?`, sessionID, actorID).Scan(&courseID, &state); err != nil {
			return ErrNotFound
		}
		if state != "active" {
			return ErrInvalidState
		}
		var busy int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tutor_responses WHERE session_id = ?
			AND state IN ('queued', 'generating')`, sessionID).Scan(&busy); err != nil {
			return err
		}
		if busy != 0 {
			return ErrWorkBusy
		}
		now := instant(time.Now())
		if _, err := tx.ExecContext(ctx, `UPDATE tutoring_sessions SET state = 'completed', completed_at = ?,
			completed_by = ?, last_activity_at = ? WHERE id = ? AND state = 'active'`, now, actorID, now, sessionID); err != nil {
			return err
		}
		_, err := jobs.Enqueue(ctx, tx, "tutoring-session-summary", "tutoring-session", sessionID, courseID, actorID)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	return service.Get(ctx, sessionID, actorID)
}

func (service *Service) CorrectSummary(ctx context.Context, sessionID, actorID, summary, followUp string) (Session, error) {
	if !validCorrection(summary) || !validCorrection(followUp) || summary == "" {
		return Session{}, ErrInvalid
	}
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var courseID, state string
		err := tx.QueryRowContext(ctx, `SELECT s.course_id, s.state FROM tutoring_sessions s JOIN course_supervisors cs
			ON cs.course_id = s.course_id WHERE s.id = ? AND cs.supervisor_user_id = ?`,
			sessionID, actorID).Scan(&courseID, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state != "completed" {
			return ErrInvalidState
		}
		now := instant(time.Now())
		if _, err := tx.ExecContext(ctx, `UPDATE tutoring_sessions SET summary = ?, follow_up = ?,
			summary_source = 'supervisor', summary_updated_at = ?, summary_updated_by = ? WHERE id = ?`,
			summary, followUp, now, actorID, sessionID); err != nil {
			return err
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionTutoringSummaryCorrected, actorID, "",
			audit.Metadata{CourseID: courseID})
	})
	if err != nil {
		return Session{}, err
	}
	return service.Get(ctx, sessionID, actorID)
}

func (service *Service) RegenerateSummary(ctx context.Context, sessionID, actorID string) error {
	return miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var courseID, ownerID, state string
		var summary sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT s.course_id, s.student_user_id, s.state, s.summary
			FROM tutoring_sessions s JOIN course_supervisors cs ON cs.course_id = s.course_id
			WHERE s.id = ? AND cs.supervisor_user_id = ?`, sessionID, actorID).
			Scan(&courseID, &ownerID, &state, &summary)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state != "completed" || summary.Valid {
			return ErrInvalidState
		}
		var active, failed int
		if err := tx.QueryRowContext(ctx, `SELECT
			COUNT(*) FILTER (WHERE state IN ('queued', 'running')),
			COUNT(*) FILTER (WHERE state = 'failed') FROM jobs
			WHERE type = 'tutoring-session-summary' AND subject_id = ?`, sessionID).Scan(&active, &failed); err != nil {
			return err
		}
		if active != 0 || failed == 0 {
			return ErrInvalidState
		}
		_, err = jobs.Enqueue(ctx, tx, "tutoring-session-summary", "tutoring-session", sessionID, courseID, ownerID)
		return err
	})
}

func (service *Service) Messages(ctx context.Context, sessionID, actorID string,
	input ListInput) ([]MessageResult, bool, error) {
	if input.Limit < 1 || input.Limit > 100 || input.Offset < 0 || input.Offset > 10_000 {
		return nil, false, ErrInvalid
	}
	session, role, err := loadScopedSession(ctx, service.database, sessionID, actorID)
	if err != nil || role == "supervisor" && session.State != "completed" {
		return nil, false, ErrNotFound
	}
	rows, err := service.database.QueryContext(ctx, `SELECT m.id, m.tutoring_session_id, m.sequence, m.content,
		m.created_at, r.id, r.session_id, r.student_message_id, COALESCE(r.retry_of_response_id, ''), r.attempt,
		r.state, r.content, COALESCE(r.failure_code, ''), r.created_at, r.started_at, r.finished_at
		FROM student_messages m JOIN tutor_responses r ON r.student_message_id = m.id
		WHERE m.tutoring_session_id = ? ORDER BY m.sequence, r.attempt LIMIT ? OFFSET ?`, sessionID,
		input.Limit+1, input.Offset)
	if err != nil {
		return nil, false, err
	}
	var result []MessageResult
	for rows.Next() {
		message, response, err := scanMessageResponse(rows)
		if err != nil {
			return nil, false, errors.Join(err, rows.Close())
		}
		result = append(result, MessageResult{Message: message, Response: response})
	}
	if err := rows.Err(); err != nil {
		return nil, false, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	hasMore := len(result) > input.Limit
	if hasMore {
		result = result[:input.Limit]
	}
	return result, hasMore, nil
}

func (service *Service) Materials(ctx context.Context, sessionID, actorID string,
	input ListInput) ([]UsedMaterial, bool, error) {
	if input.Limit < 1 || input.Limit > 100 || input.Offset < 0 || input.Offset > 10_000 {
		return nil, false, ErrInvalid
	}
	session, role, err := loadScopedSession(ctx, service.database, sessionID, actorID)
	if err != nil || role == "supervisor" && session.State != "completed" {
		return nil, false, ErrNotFound
	}
	rows, err := service.database.QueryContext(ctx, `SELECT DISTINCT m.id, m.name, m.kind, m.scope, m.name_normalized
		FROM material_retrievals used JOIN materials m ON m.id = used.material_id
		WHERE used.tutoring_session_id = ? ORDER BY m.name_normalized, m.id LIMIT ? OFFSET ?`, sessionID,
		input.Limit+1, input.Offset)
	if err != nil {
		return nil, false, err
	}
	var values []UsedMaterial
	for rows.Next() {
		var value UsedMaterial
		var normalizedName string
		if err := rows.Scan(&value.ID, &value.Name, &value.Kind, &value.Scope, &normalizedName); err != nil {
			return nil, false, errors.Join(err, rows.Close())
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	hasMore := len(values) > input.Limit
	if hasMore {
		values = values[:input.Limit]
	}
	return values, hasMore, nil
}

func (service *Service) ActiveInCourse(ctx context.Context, query miSQLite.Querier, courseID string) (bool, error) {
	var active int
	err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tutoring_sessions
		WHERE course_id = ? AND state = 'active')`, courseID).Scan(&active)
	return active != 0, err
}

func (service *Service) StudentActiveInCourse(ctx context.Context, query miSQLite.Querier, courseID,
	studentID string) (bool, error) {
	var active int
	err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tutoring_sessions
		WHERE course_id = ? AND student_user_id = ? AND state = 'active')`, courseID, studentID).Scan(&active)
	return active != 0, err
}

// StudentActive reports whether the student has an active tutoring session in any course.
func (service *Service) StudentActive(ctx context.Context, query miSQLite.Querier, studentID string) (bool, error) {
	var active int
	if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tutoring_sessions
		WHERE student_user_id = ? AND state = 'active')`, studentID).Scan(&active); err != nil {
		return false, fmt.Errorf("check active tutoring session for student: %w", err)
	}
	return active != 0, nil
}

func (service *Service) MaterialSelectedByActive(ctx context.Context, query miSQLite.Querier,
	materialID string) (bool, error) {
	var selected int
	err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM session_material_selections sm
		JOIN tutoring_sessions s ON s.id = sm.tutoring_session_id WHERE sm.material_id = ? AND s.state = 'active')`,
		materialID).Scan(&selected)
	return selected != 0, err
}

// LoadCompletedResponseForOwner returns speech-eligible response text without exposing other students' resources.
func LoadCompletedResponseForOwner(ctx context.Context, query miSQLite.Querier, responseID,
	studentID string) (CompletedResponse, error) {
	var response CompletedResponse
	err := query.QueryRowContext(ctx, `SELECT r.id, r.content FROM tutor_responses r
		JOIN tutoring_sessions s ON s.id = r.session_id
		WHERE r.id = ? AND r.state = 'completed' AND s.student_user_id = ?`, responseID, studentID).
		Scan(&response.ID, &response.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return CompletedResponse{}, ErrNotFound
	}
	if err != nil {
		return CompletedResponse{}, fmt.Errorf("load speech source response: %w", err)
	}
	return response, nil
}

// ResponseIDsForCourse returns tutoring-owned response identifiers for lifecycle deletion.
func ResponseIDsForCourse(ctx context.Context, query miSQLite.Querier, courseID string) ([]string, error) {
	return responseIDs(ctx, query, "s.course_id = ?", courseID)
}

// ResponseIDsForStudentCourse returns tutoring-owned response identifiers for lifecycle deletion.
func ResponseIDsForStudentCourse(ctx context.Context, query miSQLite.Querier, courseID,
	studentID string) ([]string, error) {
	return responseIDs(ctx, query, "s.course_id = ? AND s.student_user_id = ?", courseID, studentID)
}

// ResponseIDsForAccount returns tutoring-owned response identifiers for lifecycle deletion.
func ResponseIDsForAccount(ctx context.Context, query miSQLite.Querier, accountID string) ([]string, error) {
	return responseIDs(ctx, query, "s.student_user_id = ?", accountID)
}

func responseIDs(ctx context.Context, query miSQLite.Querier, condition string, args ...any) ([]string, error) {
	rows, err := query.QueryContext(ctx, `SELECT r.id FROM tutor_responses r JOIN tutoring_sessions s
		ON s.id = r.session_id WHERE `+condition+` ORDER BY r.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list tutoring response identifiers: %w", err)
	}
	return miSQLite.ScanStrings(rows)
}

func (service *Service) DeleteCourseData(ctx context.Context, query miSQLite.Querier, courseID string) error {
	return service.deleteSessions(ctx, query, "course_id = ?", courseID)
}
func (service *Service) DeleteStudentCourseData(ctx context.Context, query miSQLite.Querier, courseID,
	studentID string) error {
	return service.deleteSessions(ctx, query, "course_id = ? AND student_user_id = ?", courseID, studentID)
}

func (service *Service) CourseDeletionImpact(ctx context.Context, query miSQLite.Querier,
	courseID string) ([]string, error) {
	return tutoringDeletionImpact(ctx, query, "s.course_id = ?", courseID)
}

func (service *Service) StudentCourseDeletionImpact(ctx context.Context, query miSQLite.Querier, courseID,
	studentID string) ([]string, error) {
	return tutoringDeletionImpact(ctx, query, "s.course_id = ? AND s.student_user_id = ?", courseID, studentID)
}

func tutoringDeletionImpact(ctx context.Context, query miSQLite.Querier, condition string,
	args ...any) ([]string, error) {
	statements := []struct {
		prefix, query string
	}{
		{"session:", "SELECT s.id FROM tutoring_sessions s WHERE " + condition + " ORDER BY s.id"},
		{"selection:", `SELECT sm.tutoring_session_id || ':' || sm.material_id FROM session_material_selections sm
			JOIN tutoring_sessions s ON s.id = sm.tutoring_session_id WHERE ` + condition +
			" ORDER BY sm.tutoring_session_id, sm.material_id"},
		{"message:", `SELECT m.id FROM student_messages m JOIN tutoring_sessions s ON s.id = m.tutoring_session_id
			WHERE ` + condition + " ORDER BY m.id"},
		{"response:", `SELECT r.id FROM tutor_responses r JOIN tutoring_sessions s ON s.id = r.session_id
			WHERE ` + condition + " ORDER BY r.id"},
	}
	var result, sessionIDs []string
	for index, statement := range statements {
		rows, err := query.QueryContext(ctx, statement.query, args...)
		if err != nil {
			return nil, fmt.Errorf("list tutoring deletion impact: %w", err)
		}
		ids, err := miSQLite.ScanStrings(rows)
		if err != nil {
			return nil, err
		}
		if index == 0 {
			sessionIDs = append(sessionIDs, ids...)
		}
		for _, id := range ids {
			result = append(result, statement.prefix+id)
		}
	}
	jobImpact, err := jobs.DeletionImpact(ctx, query, sessionIDs)
	if err != nil {
		return nil, err
	}
	return append(result, jobImpact...), nil
}
func (service *Service) DeleteAccountData(ctx context.Context, query miSQLite.Querier, accountID string) error {
	return service.deleteSessions(ctx, query, "student_user_id = ?", accountID)
}

func (service *Service) deleteSessions(ctx context.Context, query miSQLite.Querier, condition string, args ...any) error {
	rows, err := query.QueryContext(ctx, "SELECT id FROM tutoring_sessions WHERE "+condition, args...)
	if err != nil {
		return err
	}
	ids, err := miSQLite.ScanStrings(rows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := jobs.DeleteBySubject(ctx, query, "tutoring-session", id); err != nil {
			return err
		}
	}
	_, err = query.ExecContext(ctx, "DELETE FROM tutoring_sessions WHERE "+condition, args...)
	return err
}

func validMessage(value string) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 8_000 && len(value) <= 32<<10
}

// lockSessionForMessage obtains SQLite's writer reservation before reading response slots.
// This makes distinct message submissions re-evaluate the queue after earlier writers commit.
func lockSessionForMessage(ctx context.Context, tx *sql.Tx, sessionID, studentID string) error {
	result, err := tx.ExecContext(ctx, `UPDATE tutoring_sessions SET last_activity_at = last_activity_at
		WHERE id = ? AND student_user_id = ?`, sessionID, studentID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

// lockSessionForRetry serializes retries with every response-slot mutation in the owning session.
func lockSessionForRetry(ctx context.Context, tx *sql.Tx, messageID, studentID string) error {
	result, err := tx.ExecContext(ctx, `UPDATE tutoring_sessions SET last_activity_at = last_activity_at
		WHERE student_user_id = ? AND id = (SELECT session_id FROM tutor_responses
		WHERE student_message_id = ? ORDER BY attempt DESC LIMIT 1)`, studentID, messageID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func isWorkSlotConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed: tutor_responses.session_id") ||
		strings.Contains(message, "UNIQUE constraint failed: tutor_responses.student_message_id, tutor_responses.attempt") ||
		strings.Contains(message, "UNIQUE constraint failed: student_messages.tutoring_session_id, student_messages.sequence")
}
func validCorrection(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 16_000 && len(value) <= 64<<10
}
func canonicalUUID(value string) (string, bool) {
	parsed, err := uuid.Parse(value)
	return value, err == nil && parsed.Version() == 4 && parsed.Variant() == uuid.RFC4122 && value == parsed.String()
}
func bodyDigest(value any) ([32]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode tutoring request digest: %w", err)
	}
	return sha256.Sum256(encoded), nil
}
func equalDigest(stored []byte, value [32]byte) bool {
	return len(stored) == len(value) && string(stored) == string(value[:])
}
func instant(value time.Time) string { return value.UTC().Format(storedInstant) }
