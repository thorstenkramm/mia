// Package tutoring owns tutoring sessions, chat history, tutor responses, and session summaries.
package tutoring

import (
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("tutoring session not found")
	ErrInvalid         = errors.New("invalid tutoring request")
	ErrConflict        = errors.New("tutoring request conflicts with retained history")
	ErrActiveSession   = errors.New("student already has an active tutoring session")
	ErrWorkBusy        = errors.New("tutoring response slots are full")
	ErrInvalidState    = errors.New("tutoring state does not allow action")
	ErrSummaryMissing  = errors.New("session summary is not available")
	ErrProviderFailure = errors.New("tutor provider failed")
)

type Session struct {
	ID, CourseID, StudentID, State                 string
	Summary, FollowUp, SummarySource, SummaryActor string
	StartedAt, LastActivityAt                      time.Time
	CompletedAt, SummaryUpdatedAt                  *time.Time
	SelectedMaterialIDs                            []string
}

type Message struct {
	ID, SessionID, Content string
	Sequence               int
	CreatedAt              time.Time
}

type Response struct {
	ID, SessionID, MessageID, RetryOfID, State, Content, FailureCode string
	Attempt                                                          int
	CreatedAt                                                        time.Time
	StartedAt, FinishedAt                                            *time.Time
}

type MessageResult struct {
	Message  Message
	Response Response
	Replay   bool
}

type Event struct {
	Type, Content, State, Text, Code string
}

type StartInput struct {
	CourseID, StudentID, RequestID string
	SelectedMaterialIDs            []string
}

type SubmitInput struct {
	SessionID, StudentID, RequestID, Content string
}

type ListInput struct{ Limit, Offset int }
type ListResult struct {
	Sessions []Session
	HasMore  bool
}

type UsedMaterial struct {
	ID, Name, Kind, Scope string
}
