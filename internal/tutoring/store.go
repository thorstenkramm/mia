package tutoring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

type scanner interface{ Scan(...any) error }

const sessionColumns = `s.id, s.course_id, s.student_user_id, s.state, COALESCE(s.summary, ''),
	COALESCE(s.follow_up, ''), COALESCE(s.summary_source, ''), COALESCE(s.summary_updated_by, ''),
	s.started_at, s.last_activity_at, s.completed_at, s.summary_updated_at`

const sessionSelect = `SELECT ` + sessionColumns + ` FROM tutoring_sessions s`

func loadSessionByRequest(ctx context.Context, query miSQLite.Querier, studentID,
	requestID string) (Session, []byte, error) {
	row := query.QueryRowContext(ctx, `SELECT `+sessionColumns+`, s.request_digest FROM tutoring_sessions s
		WHERE s.student_user_id = ? AND s.client_request_id = ?`,
		studentID, requestID)
	value, err := scanSessionWithDigest(row)
	return value.session, value.digest, err
}

func loadActiveSession(ctx context.Context, query miSQLite.Querier, studentID string) (ActiveSession, error) {
	var value ActiveSession
	var started, activity string
	err := query.QueryRowContext(ctx, `SELECT s.id, s.course_id, c.name, s.state, s.started_at, s.last_activity_at
		FROM tutoring_sessions s JOIN courses c ON c.id = s.course_id
		WHERE s.student_user_id = ? AND s.state = 'active'`, studentID).
		Scan(&value.ID, &value.CourseID, &value.CourseName, &value.State, &started, &activity)
	if err != nil {
		return ActiveSession{}, err
	}
	value.StartedAt, err = parseInstant(started)
	if err == nil {
		value.LastActivityAt, err = parseInstant(activity)
	}
	return value, err
}

type sessionDigest struct {
	session Session
	digest  []byte
}

func scanSessionWithDigest(row scanner) (sessionDigest, error) {
	var value Session
	var started, activity string
	var completed, updated sql.NullString
	var digest []byte
	err := row.Scan(&value.ID, &value.CourseID, &value.StudentID, &value.State, &value.Summary, &value.FollowUp,
		&value.SummarySource, &value.SummaryActor, &started, &activity, &completed, &updated, &digest)
	if err == nil {
		err = parseSessionTimes(&value, started, activity, completed, updated)
	}
	return sessionDigest{session: value, digest: digest}, err
}

func scanSession(row scanner) (Session, error) {
	var value Session
	var started, activity string
	var completed, updated sql.NullString
	err := row.Scan(&value.ID, &value.CourseID, &value.StudentID, &value.State, &value.Summary, &value.FollowUp,
		&value.SummarySource, &value.SummaryActor, &started, &activity, &completed, &updated)
	if err == nil {
		err = parseSessionTimes(&value, started, activity, completed, updated)
	}
	return value, err
}

func parseSessionTimes(value *Session, started, activity string, completed, updated sql.NullString) error {
	var err error
	if value.StartedAt, err = parseInstant(started); err != nil {
		return err
	}
	if value.LastActivityAt, err = parseInstant(activity); err != nil {
		return err
	}
	if value.CompletedAt, err = parseOptionalInstant(completed); err != nil {
		return err
	}
	value.SummaryUpdatedAt, err = parseOptionalInstant(updated)
	return err
}

func loadScopedSession(ctx context.Context, query miSQLite.Querier, sessionID,
	actorID string) (Session, string, error) {
	row := query.QueryRowContext(ctx, `SELECT `+sessionColumns+`,
		CASE WHEN s.student_user_id = ? THEN 'owner' ELSE 'supervisor' END
		FROM tutoring_sessions s WHERE s.id = ? AND (s.student_user_id = ? OR EXISTS(SELECT 1 FROM course_supervisors cs
		WHERE cs.course_id = s.course_id AND cs.supervisor_user_id = ?))`, actorID, sessionID, actorID, actorID)
	var value Session
	var started, activity, role string
	var completed, updated sql.NullString
	err := row.Scan(&value.ID, &value.CourseID, &value.StudentID, &value.State, &value.Summary, &value.FollowUp,
		&value.SummarySource, &value.SummaryActor, &started, &activity, &completed, &updated, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, "", ErrNotFound
	}
	if err != nil {
		return Session{}, "", err
	}
	if err := parseSessionTimes(&value, started, activity, completed, updated); err != nil {
		return Session{}, "", err
	}
	return value, role, nil
}

func loadSelected(ctx context.Context, query miSQLite.Querier, session *Session) error {
	rows, err := query.QueryContext(ctx, `SELECT material_id FROM session_material_selections
		WHERE tutoring_session_id = ? ORDER BY material_id`, session.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return errors.Join(err, rows.Close())
		}
		session.SelectedMaterialIDs = append(session.SelectedMaterialIDs, id)
	}
	if err := rows.Err(); err != nil {
		return errors.Join(err, rows.Close())
	}
	return rows.Close()
}

func loadMessageByRequest(ctx context.Context, query miSQLite.Querier, sessionID,
	requestID string) (Message, []byte, Response, error) {
	row := query.QueryRowContext(ctx, `SELECT m.id, m.tutoring_session_id, m.sequence, m.content, m.created_at, m.request_digest,
		r.id, r.session_id, r.student_message_id, COALESCE(r.retry_of_response_id, ''), r.attempt, r.state, r.content,
		COALESCE(r.failure_code, ''), r.created_at, r.started_at, r.finished_at
		FROM student_messages m JOIN tutor_responses r ON r.student_message_id = m.id AND r.attempt = 1
		WHERE m.tutoring_session_id = ? AND m.client_request_id = ?`, sessionID, requestID)
	var message Message
	var response Response
	var messageCreated, responseCreated string
	var digest []byte
	var responseStarted, responseFinished sql.NullString
	err := row.Scan(&message.ID, &message.SessionID, &message.Sequence, &message.Content, &messageCreated, &digest,
		&response.ID, &response.SessionID, &response.MessageID, &response.RetryOfID, &response.Attempt, &response.State,
		&response.Content, &response.FailureCode, &responseCreated, &responseStarted, &responseFinished)
	if err != nil {
		return Message{}, nil, Response{}, err
	}
	message.CreatedAt, err = parseInstant(messageCreated)
	if err == nil {
		response.CreatedAt, err = parseInstant(responseCreated)
	}
	if err == nil {
		response.StartedAt, err = parseOptionalInstant(responseStarted)
	}
	if err == nil {
		response.FinishedAt, err = parseOptionalInstant(responseFinished)
	}
	return message, digest, response, err
}

func loadResponse(ctx context.Context, query miSQLite.Querier, responseID string) (Response, error) {
	row := query.QueryRowContext(ctx, `SELECT id, session_id, student_message_id, COALESCE(retry_of_response_id, ''), attempt,
		state, content, COALESCE(failure_code, ''), created_at, started_at, finished_at FROM tutor_responses WHERE id = ?`,
		responseID)
	response, err := scanResponse(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Response{}, ErrNotFound
	}
	return response, err
}

func loadLastResponseForMessage(ctx context.Context, query miSQLite.Querier, messageID string) (Response, error) {
	row := query.QueryRowContext(ctx, `SELECT id, session_id, student_message_id, COALESCE(retry_of_response_id, ''),
		attempt, state, content, COALESCE(failure_code, ''), created_at, started_at, finished_at
		FROM tutor_responses WHERE student_message_id = ? ORDER BY attempt DESC LIMIT 1`, messageID)
	response, err := scanResponse(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Response{}, ErrNotFound
	}
	return response, err
}

func loadResponseScopedOwner(ctx context.Context, query miSQLite.Querier, responseID,
	actorID string) (Response, error) {
	var allowed int
	if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tutor_responses r JOIN tutoring_sessions s
		ON s.id = r.session_id WHERE r.id = ? AND s.student_user_id = ?)`, responseID, actorID).Scan(&allowed); err != nil {
		return Response{}, err
	}
	if allowed == 0 {
		return Response{}, ErrNotFound
	}
	return loadResponse(ctx, query, responseID)
}

func scanResponse(row scanner) (Response, error) {
	var response Response
	var created string
	var started, finished sql.NullString
	err := row.Scan(&response.ID, &response.SessionID, &response.MessageID, &response.RetryOfID, &response.Attempt,
		&response.State, &response.Content, &response.FailureCode, &created, &started, &finished)
	if err != nil {
		return Response{}, err
	}
	response.CreatedAt, err = parseInstant(created)
	if err == nil {
		response.StartedAt, err = parseOptionalInstant(started)
	}
	if err == nil {
		response.FinishedAt, err = parseOptionalInstant(finished)
	}
	return response, err
}

func scanMessageResponse(row scanner) (Message, Response, error) {
	var message Message
	var response Response
	var messageCreated, responseCreated string
	var started, finished sql.NullString
	err := row.Scan(&message.ID, &message.SessionID, &message.Sequence, &message.Content, &messageCreated,
		&response.ID, &response.SessionID, &response.MessageID, &response.RetryOfID, &response.Attempt, &response.State,
		&response.Content, &response.FailureCode, &responseCreated, &started, &finished)
	if err != nil {
		return Message{}, Response{}, err
	}
	message.CreatedAt, err = parseInstant(messageCreated)
	if err == nil {
		response.CreatedAt, err = parseInstant(responseCreated)
	}
	if err == nil {
		response.StartedAt, err = parseOptionalInstant(started)
	}
	if err == nil {
		response.FinishedAt, err = parseOptionalInstant(finished)
	}
	return message, response, err
}

func parseInstant(value string) (time.Time, error) {
	parsed, err := time.Parse(storedInstant, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored tutoring instant: %w", err)
	}
	return parsed, nil
}

func parseOptionalInstant(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseInstant(value.String)
	return &parsed, err
}
