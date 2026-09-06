package user_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/course"
	"github.com/thorstenkramm/mia/internal/lifecycle"
	"github.com/thorstenkramm/mia/internal/mentoring"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestAccountDeletionCascadesAndDeidentifiesAuditHistory(t *testing.T) {
	database := openDatabase(t)
	administrator := createStaff(t, database, "deleting-admin", []user.Role{user.Administrator})
	student := createStudent(t, database, "deleted-student")
	registry := &lifecycle.Registry{}
	courseService := course.NewService(database, t.TempDir(), registry, nil, nil, nil, nil, nil)
	registry.RegisterAccount(courseService)
	registry.RegisterAccount(lifecycle.AccountFunc(func(ctx context.Context, query miSQLite.Querier,
		accountID string) error {
		_, err := query.ExecContext(ctx, "DELETE FROM deletion_marker WHERE account_id = ?", accountID)
		return err
	}))
	if _, err := database.Exec("CREATE TABLE deletion_marker (account_id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO deletion_marker (account_id) VALUES (?)", student.ID); err != nil {
		t.Fatal(err)
	}
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		if err := audit.Write(context.Background(), tx, audit.ActionUserProfileUpdated, student.ID,
			administrator.ID); err != nil {
			return err
		}
		return audit.WriteWithMetadata(context.Background(), tx, audit.ActionMentoringSessionAssigned,
			administrator.ID, student.ID, audit.Metadata{MentorID: student.ID})
	}); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	avatarDir := filepath.Join(dataDir, "users", student.ID)
	if err := os.MkdirAll(avatarDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(avatarDir, "avatar.png"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := user.NewDeletionService(database, dataDir, registry, nil)
	if err := service.Delete(context.Background(), administrator.ID, student.ID); err != nil {
		t.Fatal(err)
	}
	for table, column := range map[string]string{"users": "id", "deletion_marker": "account_id"} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+column+" = ?", student.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained deleted account", table)
		}
	}
	if _, err := os.Stat(avatarDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted account files still exist: %v", err)
	}
	var directReferences, fingerprintCount, distinctFingerprints, metadataReferences int
	if err := database.QueryRow(`SELECT
		COUNT(*) FILTER (WHERE actor_user_id = ? OR subject_user_id = ?),
		COUNT(*) FILTER (WHERE actor_fingerprint IS NOT NULL OR subject_fingerprint IS NOT NULL),
		COUNT(DISTINCT fingerprint),
		COUNT(*) FILTER (WHERE json_extract(metadata, '$.mentor_id') = ?)
		FROM audit_events LEFT JOIN (
			SELECT actor_fingerprint AS fingerprint FROM audit_events WHERE actor_fingerprint IS NOT NULL
			UNION ALL SELECT subject_fingerprint FROM audit_events WHERE subject_fingerprint IS NOT NULL
			UNION ALL SELECT json_extract(metadata, '$.mentor_fingerprint') FROM audit_events
				WHERE json_extract(metadata, '$.mentor_fingerprint') IS NOT NULL
		) ON 1 = 1`, student.ID, student.ID, student.ID).
		Scan(&directReferences, &fingerprintCount, &distinctFingerprints, &metadataReferences); err != nil {
		t.Fatal(err)
	}
	if directReferences != 0 || fingerprintCount == 0 || distinctFingerprints != 1 || metadataReferences != 0 {
		t.Fatalf("audit de-identification = direct %d fingerprints %d distinct %d metadata refs %d",
			directReferences, fingerprintCount, distinctFingerprints, metadataReferences)
	}
}

func TestAccountDeletionSafeguardsStaffRelationships(t *testing.T) {
	database := openDatabase(t)
	administrator := createStaff(t, database, "only-admin", []user.Role{user.Administrator})
	registry := &lifecycle.Registry{}
	service := user.NewDeletionService(database, t.TempDir(), registry, nil)
	if err := service.Delete(context.Background(), administrator.ID, administrator.ID); !errors.Is(err,
		user.ErrLastAdministrator) {
		t.Fatalf("self deletion error = %v", err)
	}
	otherAdministrator := createStaff(t, database, "other-admin", []user.Role{user.Administrator})
	if err := service.Delete(context.Background(), otherAdministrator.ID, administrator.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), otherAdministrator.ID, otherAdministrator.ID); !errors.Is(err,
		user.ErrLastAdministrator) {
		t.Fatalf("remaining administrator self deletion error = %v", err)
	}

	supervisor := createStaff(t, database, "sole-supervisor", []user.Role{user.Supervisor})
	courseService := course.NewService(database, t.TempDir(), registry, nil, nil, nil, nil, nil)
	registry.RegisterAccount(courseService)
	if _, err := database.Exec(`INSERT INTO courses (id, name, name_normalized, is_active, created_at)
		VALUES ('cou_delete_guard', 'Guarded', 'guarded', 0, '2026-09-06T00:00:00.000000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO course_supervisors
		(course_id, supervisor_user_id, assigned_at) VALUES ('cou_delete_guard', ?,
		'2026-09-06T00:00:00.000000Z')`, supervisor.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), otherAdministrator.ID, supervisor.ID); !errors.Is(err,
		user.ErrSoleSupervisor) {
		t.Fatalf("sole supervisor deletion error = %v", err)
	}
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", supervisor.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("protected supervisor count/error = %d/%v", count, err)
	}
}

func TestStaffDeletionTriagesOpenMentoringWork(t *testing.T) {
	database := openDatabase(t)
	administrator := createStaff(t, database, "triage-admin", []user.Role{user.Administrator})
	supervisor := createStaff(t, database, "triage-supervisor", []user.Role{user.Supervisor})
	mentor := createStaff(t, database, "deleted-mentor", []user.Role{user.Mentor})
	student := createStudent(t, database, "triage-student")
	registry := &lifecycle.Registry{}
	registry.RegisterAccount(mentoring.NewService(database, nil, nil))
	registry.RegisterAccount(course.NewService(database, t.TempDir(), registry, nil, nil, nil, nil, nil))
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO courses (id, name, name_normalized, is_active, created_at)
			VALUES ('cou_triage', 'Triage', 'triage', 1, '2026-09-06T00:00:00.000000Z')`, nil},
		{`INSERT INTO course_supervisors (course_id, supervisor_user_id, assigned_at)
			VALUES ('cou_triage', ?, '2026-09-06T00:00:00.000000Z')`, []any{supervisor.ID}},
		{`INSERT INTO course_students (id, course_id, student_user_id, joined_at)
			VALUES ('cst_triage', 'cou_triage', ?, '2026-09-06T00:00:00.000000Z')`, []any{student.ID}},
		{`INSERT INTO course_mentors (course_id, mentor_user_id, assigned_at)
			VALUES ('cou_triage', ?, '2026-09-06T00:00:00.000000Z')`, []any{mentor.ID}},
		{`INSERT INTO mentor_assignments (course_id, student_user_id, mentor_user_id, assigned_at)
			VALUES ('cou_triage', ?, ?, '2026-09-06T00:00:00.000000Z')`, []any{student.ID, mentor.ID}},
		{`INSERT INTO mentoring_sessions
			(id, student_user_id, course_id, mentor_user_id, topic, response, responded_at, responded_by,
			 proposed_for, scheduled_for, meeting_instructions, meeting_url, created_at)
			VALUES ('ms_triage', ?, 'cou_triage', ?, 'topic', 'preserved response',
			'2026-09-06T00:01:00.000000Z', ?, '2026-09-07T00:00:00.000000Z',
			'2026-09-08T00:00:00.000000Z', 'instructions', 'https://example.test/meet',
			'2026-09-06T00:00:00.000000Z')`, []any{student.ID, mentor.ID, mentor.ID}},
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	service := user.NewDeletionService(database, t.TempDir(), registry, nil)
	if err := service.Delete(context.Background(), administrator.ID, mentor.ID); err != nil {
		t.Fatal(err)
	}
	var currentMentor, respondedBy, proposedFor, scheduledFor, instructions, meetingURL sql.NullString
	var response string
	if err := database.QueryRow(`SELECT mentor_user_id, responded_by, proposed_for, scheduled_for,
		meeting_instructions, meeting_url, response FROM mentoring_sessions WHERE id = 'ms_triage'`).
		Scan(&currentMentor, &respondedBy, &proposedFor, &scheduledFor, &instructions, &meetingURL, &response); err != nil {
		t.Fatal(err)
	}
	if currentMentor.Valid || respondedBy.Valid || proposedFor.Valid || scheduledFor.Valid || instructions.Valid ||
		meetingURL.Valid || response != "preserved response" {
		t.Fatalf("triaged work retained assignment/schedule or lost response: mentor=%v responded_by=%v response=%q",
			currentMentor, respondedBy, response)
	}
}

func TestAccountDeletionRollsBackEveryLifecycleOwner(t *testing.T) {
	database := openDatabase(t)
	administrator := createStaff(t, database, "rollback-admin", []user.Role{user.Administrator})
	student := createStudent(t, database, "rollback-student")
	if _, err := database.Exec("CREATE TABLE deletion_rollback_marker (account_id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO deletion_rollback_marker (account_id) VALUES (?)", student.ID); err != nil {
		t.Fatal(err)
	}
	registry := &lifecycle.Registry{}
	registry.RegisterAccount(lifecycle.AccountFunc(func(ctx context.Context, query miSQLite.Querier,
		accountID string) error {
		_, err := query.ExecContext(ctx, "DELETE FROM deletion_rollback_marker WHERE account_id = ?", accountID)
		return err
	}))
	registry.RegisterAccount(lifecycle.AccountFunc(func(context.Context, miSQLite.Querier, string) error {
		return errors.New("injected lifecycle failure")
	}))
	service := user.NewDeletionService(database, t.TempDir(), registry, nil)
	if err := service.Delete(context.Background(), administrator.ID, student.ID); err == nil {
		t.Fatal("account deletion succeeded despite lifecycle failure")
	}
	for table, column := range map[string]string{"users": "id", "deletion_rollback_marker": "account_id"} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+column+" = ?", student.ID).
			Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s rollback count/error = %d/%v", table, count, err)
		}
	}
}
