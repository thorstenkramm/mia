package course

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/lifecycle"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestCreateIsAtomicAndUsesNormalizedGlobalName(t *testing.T) {
	database := courseDatabase(t)
	admin := createAccount(t, database, "admin", user.Administrator)
	supervisor := createAccount(t, database, "supervisor", user.Supervisor)
	service := NewService(database, t.TempDir(), nil, nil, nil, nil)

	_, err := service.Create(context.Background(), CreateInput{ActorID: admin, SupervisorIDs: []string{supervisor, "u_missing"},
		Fields: preparedFields("History")})
	if !errors.Is(err, ErrSupervisorInvalid) {
		t.Fatalf("Create invalid supervisor error = %v", err)
	}
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM courses").Scan(&count); err != nil || count != 0 {
		t.Fatalf("courses after rolled-back creation = %d, %v", count, err)
	}

	created, err := service.Create(context.Background(), CreateInput{ActorID: admin, SupervisorIDs: []string{supervisor},
		Fields: preparedFields("  History  ")})
	if err != nil || created.Active || created.Name != "History" || len(created.SupervisorIDs) != 1 {
		t.Fatalf("created course = %#v, %v", created, err)
	}
	_, err = service.Create(context.Background(), CreateInput{ActorID: admin, SupervisorIDs: []string{supervisor},
		Fields: preparedFields("HISTORY")})
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("normalized duplicate error = %v", err)
	}
	renamed := "World History"
	if _, err := service.Update(context.Background(), created.ID, supervisor,
		Fields{Name: OptionalString{Set: true, Value: &renamed}}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("supervisor rename error = %v", err)
	}
	unchanged, err := service.Get(context.Background(), created.ID, supervisor)
	if err != nil || unchanged.Name != "History" {
		t.Fatalf("course after denied supervisor rename = %#v, %v", unchanged, err)
	}
	updated, err := service.Update(context.Background(), created.ID, admin,
		Fields{Name: OptionalString{Set: true, Value: &renamed}})
	if err != nil || updated.Name != renamed {
		t.Fatalf("administrator rename = %#v, %v", updated, err)
	}
}

func TestActivationRequiresMaterialAndAssignedSupervisor(t *testing.T) {
	database := courseDatabase(t)
	admin := createAccount(t, database, "admin", user.Administrator)
	supervisor := createAccount(t, database, "supervisor", user.Supervisor)
	blocked := NewService(database, t.TempDir(), nil, nil, nil, nil)
	created, err := blocked.Create(context.Background(), CreateInput{ActorID: admin, SupervisorIDs: []string{supervisor},
		Fields: preparedFields("Math")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocked.Activate(context.Background(), created.ID, supervisor); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("activation without material error = %v", err)
	}
	ready := NewService(database, t.TempDir(), nil, func(context.Context, miSQLite.Querier, string) (bool, error) {
		return true, nil
	}, nil, nil)
	active, err := ready.Activate(context.Background(), created.ID, supervisor)
	if err != nil || !active.Active {
		t.Fatalf("activation = %#v, %v", active, err)
	}
	if _, err := ready.Update(context.Background(), created.ID, supervisor,
		Fields{Language: OptionalString{Set: true}}); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("clearing active-course language error = %v", err)
	}
	unchanged, err := ready.Get(context.Background(), created.ID, supervisor)
	if err != nil || unchanged.Language == nil {
		t.Fatalf("active-course requirement rollback = %#v, %v", unchanged, err)
	}
	if _, err := ready.Deactivate(context.Background(), created.ID, admin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unassigned administrator deactivation error = %v", err)
	}
	deactivated, err := ready.Deactivate(context.Background(), created.ID, supervisor)
	if err != nil || deactivated.Active {
		t.Fatalf("deactivation = %#v, %v", deactivated, err)
	}
}

func TestSupervisorInvariantAndLifecycleDeletionTransaction(t *testing.T) {
	database := courseDatabase(t)
	admin := createAccount(t, database, "admin", user.Administrator)
	first := createAccount(t, database, "first", user.Supervisor)
	second := createAccount(t, database, "second", user.Supervisor)
	registry := &lifecycle.Registry{}
	if _, err := database.Exec("CREATE TABLE deletion_markers (course_id TEXT)"); err != nil {
		t.Fatal(err)
	}
	fail := true
	registry.RegisterCourse(lifecycle.CourseFunc(func(ctx context.Context, query miSQLite.Querier, courseID string) error {
		if _, err := query.ExecContext(ctx, "INSERT INTO deletion_markers(course_id) VALUES (?)", courseID); err != nil {
			return err
		}
		if fail {
			return errors.New("injected deletion failure")
		}
		return nil
	}))
	service := NewService(database, t.TempDir(), registry, nil, nil, nil)
	created, err := service.Create(context.Background(), CreateInput{ActorID: admin, SupervisorIDs: []string{first},
		Fields: preparedFields("Science")})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveSupervisor(context.Background(), created.ID, first, admin); !errors.Is(err, ErrLastSupervisor) {
		t.Fatalf("last supervisor removal error = %v", err)
	}
	if err := service.AssignSupervisor(context.Background(), created.ID, second, admin); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveSupervisor(context.Background(), created.ID, first, admin); err != nil {
		t.Fatal(err)
	}
	var courseAuditsBefore int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE course_id = ?", created.ID).
		Scan(&courseAuditsBefore); err != nil || courseAuditsBefore < 1 {
		t.Fatalf("course audits before deletion = %d, %v", courseAuditsBefore, err)
	}
	if err := service.Delete(context.Background(), created.ID, admin); err == nil {
		t.Fatal("Delete succeeded despite lifecycle failure")
	}
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM courses WHERE id = ?", created.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("course after rolled-back delete = %d, %v", count, err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM deletion_markers").Scan(&count); err != nil || count != 0 {
		t.Fatalf("marker after rolled-back delete = %d, %v", count, err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE course_id = ?", created.ID).
		Scan(&count); err != nil || count != courseAuditsBefore {
		t.Fatalf("course audits after rolled-back delete = %d, %v", count, err)
	}
	fail = false
	if err := service.Delete(context.Background(), created.ID, admin); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil || count != 3 {
		t.Fatalf("accounts after course deletion = %d, %v", count, err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE course_id = ?", created.ID).
		Scan(&count); err != nil || count != 0 {
		t.Fatalf("course-scoped audits after deletion = %d, %v", count, err)
	}
	var actor string
	var subject, metadata, courseID sql.NullString
	var subjectType, fingerprint string
	err = database.QueryRow(`SELECT actor_user_id, subject_user_id, metadata, course_id, subject_type,
		subject_fingerprint FROM audit_events WHERE action = 'course.course.deleted'`).
		Scan(&actor, &subject, &metadata, &courseID, &subjectType, &fingerprint)
	parsedFingerprint, parseErr := uuid.Parse(fingerprint)
	if err != nil || actor != admin || subject.Valid || metadata.Valid || courseID.Valid || subjectType != "course" ||
		parseErr != nil || parsedFingerprint.Version() != 4 {
		t.Fatalf("retained deletion audit = actor=%q subject=%v metadata=%v course=%v type=%q fingerprint=%q, %v/%v",
			actor, subject, metadata, courseID, subjectType, fingerprint, err, parseErr)
	}
}

func TestDeletionRejectsActiveSession(t *testing.T) {
	database := courseDatabase(t)
	admin := createAccount(t, database, "admin", user.Administrator)
	supervisor := createAccount(t, database, "supervisor", user.Supervisor)
	service := NewService(database, t.TempDir(), nil, nil,
		func(context.Context, miSQLite.Querier, string) (bool, error) { return true, nil }, nil)
	created, err := service.Create(context.Background(), CreateInput{ActorID: admin, SupervisorIDs: []string{supervisor},
		Fields: preparedFields("Geography")})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), created.ID, admin); !errors.Is(err, ErrActiveSession) {
		t.Fatalf("active-session deletion error = %v", err)
	}
}

func courseDatabase(t *testing.T) *sql.DB {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := miSQLite.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	return database
}

func createAccount(t *testing.T, database *sql.DB, username string, role user.Role) string {
	t.Helper()
	var account user.Account
	err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		input := user.CreateInput{Username: username, PasswordHash: "test-hash", Language: "en", Country: "DE",
			TimeZone: "UTC", Roles: []user.Role{role}}
		if role != user.Student {
			input.Email, input.EmailVerified = username+"@example.test", true
		}
		var err error
		account, err = user.Create(context.Background(), tx, input)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return account.ID
}

func preparedFields(name string) Fields {
	goals, instructions, language := "Understand the topic", "Guide with questions", "en"
	return Fields{Name: OptionalString{Set: true, Value: &name}, LearningGoals: OptionalString{Set: true, Value: &goals},
		Instructions: OptionalString{Set: true, Value: &instructions}, Language: OptionalString{Set: true, Value: &language}}
}
