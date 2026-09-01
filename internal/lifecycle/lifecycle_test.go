package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

func TestRegistryInvokesDeletersInRegistrationOrderAndStopsOnFailure(t *testing.T) {
	registry := &Registry{}
	var calls []string
	registry.RegisterCourse(CourseFunc(func(context.Context, miSQLite.Querier, string) error {
		calls = append(calls, "first")
		return nil
	}))
	registry.RegisterCourse(CourseFunc(func(context.Context, miSQLite.Querier, string) error {
		calls = append(calls, "second")
		return errors.New("stop")
	}))
	registry.RegisterCourse(CourseFunc(func(context.Context, miSQLite.Querier, string) error {
		calls = append(calls, "third")
		return nil
	}))
	err := registry.DeleteCourseData(context.Background(), (*sql.Tx)(nil), "cou_test")
	if err == nil || !reflect.DeepEqual(calls, []string{"first", "second"}) {
		t.Fatalf("DeleteCourseData error/calls = %v/%v", err, calls)
	}
}

func TestRegistryInvokesAccountDeleters(t *testing.T) {
	registry := &Registry{}
	called := false
	registry.RegisterAccount(AccountFunc(func(_ context.Context, _ miSQLite.Querier, id string) error {
		called = id == "u_test"
		return nil
	}))
	if err := registry.DeleteAccountData(context.Background(), (*sql.Tx)(nil), "u_test"); err != nil || !called {
		t.Fatalf("DeleteAccountData error/called = %v/%v", err, called)
	}
}
