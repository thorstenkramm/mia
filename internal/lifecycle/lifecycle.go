// Package lifecycle provides the deletion inversion registry required by AD-14.
package lifecycle

import (
	"context"
	"errors"

	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

// CourseDeleter removes one feature's course-scoped data in the caller's transaction.
type CourseDeleter interface {
	DeleteCourseData(context.Context, miSQLite.Querier, string) error
}

// AccountDeleter removes or de-identifies one feature's account-scoped data in the caller's transaction.
type AccountDeleter interface {
	DeleteAccountData(context.Context, miSQLite.Querier, string) error
}

// Registry is configured during process wiring and then used by deleting transactions.
// It is not safe for concurrent registration; registrations must finish before serving.
type Registry struct {
	course  []CourseDeleter
	account []AccountDeleter
}

// RegisterCourse appends a course-scoped owner in deterministic invocation order.
func (registry *Registry) RegisterCourse(deleter CourseDeleter) {
	if deleter == nil {
		panic("nil course lifecycle deleter")
	}
	registry.course = append(registry.course, deleter)
}

// RegisterAccount appends an account-scoped owner in deterministic invocation order.
func (registry *Registry) RegisterAccount(deleter AccountDeleter) {
	if deleter == nil {
		panic("nil account lifecycle deleter")
	}
	registry.account = append(registry.account, deleter)
}

// DeleteCourseData invokes all registered owners in the deleting transaction.
func (registry *Registry) DeleteCourseData(ctx context.Context, query miSQLite.Querier, courseID string) error {
	for _, deleter := range registry.course {
		if err := deleter.DeleteCourseData(ctx, query, courseID); err != nil {
			return err
		}
	}
	return nil
}

// DeleteAccountData invokes all registered owners in the deleting transaction.
func (registry *Registry) DeleteAccountData(ctx context.Context, query miSQLite.Querier, accountID string) error {
	for _, deleter := range registry.account {
		if err := deleter.DeleteAccountData(ctx, query, accountID); err != nil {
			return err
		}
	}
	return nil
}

// CourseFunc adapts a function to CourseDeleter.
type CourseFunc func(context.Context, miSQLite.Querier, string) error

func (function CourseFunc) DeleteCourseData(ctx context.Context, query miSQLite.Querier, id string) error {
	if function == nil {
		return errors.New("nil course lifecycle function")
	}
	return function(ctx, query, id)
}

// AccountFunc adapts a function to AccountDeleter.
type AccountFunc func(context.Context, miSQLite.Querier, string) error

func (function AccountFunc) DeleteAccountData(ctx context.Context, query miSQLite.Querier, id string) error {
	if function == nil {
		return errors.New("nil account lifecycle function")
	}
	return function(ctx, query, id)
}
