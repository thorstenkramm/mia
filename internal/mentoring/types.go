// Package mentoring owns mentor assignments and external mentoring-session workflow.
package mentoring

import (
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("mentoring resource not found")
	ErrInvalid      = errors.New("invalid mentoring request")
	ErrInvalidState = errors.New("mentoring state does not allow action")
	ErrUnavailable  = errors.New("new mentoring request unavailable")
)

type Assignment struct {
	CourseID, StudentID, MentorID, AssignedBy string
	AssignedAt                                time.Time
}

type Session struct {
	ID, StudentID, CourseID, MentorID, Topic         string
	Response, RespondedBy                            string
	MeetingInstructions, MeetingURL                  string
	ClosureReason, ClosedBy                          string
	CreatedAt                                        time.Time
	RespondedAt, ProposedFor, ScheduledFor, ClosedAt *time.Time
}

type CreateInput struct {
	CourseID, StudentID, Topic string
	ProposedFor                *time.Time
}

type OptionalString struct {
	Set   bool
	Value *string
}

type OptionalTime struct {
	Set   bool
	Value *time.Time
}

type UpdateInput struct {
	MentorID, Response, MeetingInstructions, MeetingURL OptionalString
	ScheduledFor                                        OptionalTime
	ClosureReason                                       string
	ActorID                                             string
}

type ListInput struct{ Limit, Offset int }

type SessionListResult struct {
	Sessions []Session
	HasMore  bool
}

type AssignmentListResult struct {
	Assignments []Assignment
	HasMore     bool
}
