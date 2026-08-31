package user_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/identity"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestBootstrapCreationAndAuditAreAtomic(t *testing.T) {
	database := openDatabase(t)
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	err = miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		account, err := user.Create(context.Background(), tx, user.CreateInput{
			Username: "admin", Email: "admin@example.test", PasswordHash: hash,
			Language: "en", Country: "DE", TimeZone: "UTC", EmailVerified: true,
			Roles: []user.Role{user.Administrator},
		})
		if err != nil {
			return err
		}
		return audit.Write(context.Background(), tx, audit.ActionOperatorAdministratorBootstrapped, "", account.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	var users, events int
	if err := database.QueryRow("SELECT COUNT(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = ?", audit.ActionOperatorAdministratorBootstrapped).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if users != 1 || events != 1 {
		t.Fatalf("users/events = %d/%d", users, events)
	}
}

func TestBootstrapPreconditionAndRollback(t *testing.T) {
	database := openDatabase(t)
	createStaff(t, database, "admin", []user.Role{user.Administrator})
	if exists, err := user.HasAdministrator(context.Background(), database); err != nil || !exists {
		t.Fatalf("administrator precondition = %v, %v", exists, err)
	}
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		_, err := user.Create(context.Background(), tx, user.CreateInput{Username: "rollback", PasswordHash: "hash", Language: "en", Country: "DE", TimeZone: "UTC", Roles: []user.Role{user.Student}})
		if err != nil {
			return err
		}
		return audit.Write(context.Background(), tx, audit.Action("audit.invalid.action"), "", "")
	}); err == nil {
		t.Fatal("bootstrap transaction accepted an audit failure")
	}
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM users WHERE username_key = 'rollback'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed bootstrap retained an account")
	}
}

func TestCreatedUserIDUsesPrefixedUUIDv4(t *testing.T) {
	database := openDatabase(t)
	account := createStudent(t, database, "student")
	if len(account.ID) < 3 || account.ID[:2] != "u_" {
		t.Fatalf("user ID prefix = %q", account.ID)
	}
	parsed, err := uuid.Parse(account.ID[2:])
	if err != nil || parsed.Version() != 4 {
		t.Fatalf("user ID = %q", account.ID)
	}
}

func TestGrantRoleIsIdempotentAndSupervisorAlsoGetsStudent(t *testing.T) {
	database := openDatabase(t)
	account := createStaff(t, database, "mentor", []user.Role{user.Mentor})
	administrator := createStaff(t, database, "admin", []user.Role{user.Administrator})
	for range 2 {
		if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
			return user.GrantRole(context.Background(), tx, account.ID, user.Supervisor, administrator.ID)
		}); err != nil {
			t.Fatal(err)
		}
	}
	var roles int
	if err := database.QueryRow("SELECT COUNT(*) FROM user_roles WHERE user_id = ? AND role IN ('supervisor', 'student')", account.ID).Scan(&roles); err != nil {
		t.Fatal(err)
	}
	if roles != 2 {
		t.Fatalf("supervisor/student roles = %d", roles)
	}
}

func TestAccountCreationAndRoleGrantsRejectUnsupportedActorsAndShapes(t *testing.T) {
	database := openDatabase(t)
	student := createStudent(t, database, "student")
	mentor := createStaff(t, database, "mentor", []user.Role{user.Mentor})
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		return user.GrantRole(context.Background(), tx, student.ID, user.Administrator, mentor.ID)
	}); err == nil {
		t.Fatal("mentor granted administrator role")
	}
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		_, err := user.Create(context.Background(), tx, user.CreateInput{Username: "invalid", Language: "en", Country: "DE", TimeZone: "UTC", Roles: []user.Role{user.Administrator}})
		return err
	}); err == nil {
		t.Fatal("staff account without verified email was created")
	}
}

func TestRoleGrantRejectsBannedAndUnverifiedRecipients(t *testing.T) {
	database := openDatabase(t)
	administrator := createStaff(t, database, "admin", []user.Role{user.Administrator})
	for name, prepare := range map[string]func(user.Account){
		"unverified": func(user.Account) {},
		"banned": func(account user.Account) {
			if _, err := database.Exec("UPDATE users SET is_banned = 1 WHERE id = ?", account.ID); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			recipient := createStaff(t, database, name, []user.Role{user.Mentor})
			if name == "unverified" {
				if _, err := database.Exec("UPDATE users SET email_verified_at = NULL WHERE id = ?", recipient.ID); err != nil {
					t.Fatal(err)
				}
			}
			prepare(recipient)
			if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
				return user.GrantRole(context.Background(), tx, recipient.ID, user.Supervisor, administrator.ID)
			}); err == nil {
				t.Fatal("invalid role recipient accepted")
			}
		})
	}
}

func openDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := miSQLite.Open(t.TempDir())
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

func createStaff(t *testing.T, database *sql.DB, username string, roles []user.Role) user.Account {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{
			Username: username, Email: username + "@example.test", PasswordHash: hash,
			Language: "en", Country: "DE", TimeZone: "UTC", EmailVerified: true, Roles: roles,
		})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return account
}

func createStudent(t *testing.T, database *sql.DB, username string) user.Account {
	t.Helper()
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{Username: username, PasswordHash: hash, Language: "en", Country: "DE", TimeZone: "UTC", Roles: []user.Role{user.Student}})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	return account
}
