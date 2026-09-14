package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabasePathUsesTheFixedDatabaseName(t *testing.T) {
	directory := t.TempDir()
	if got, want := DatabasePath(directory), filepath.Join(directory, "mia.sqlite3"); got != want {
		t.Fatalf("DatabasePath() = %q, want %q", got, want)
	}
}

func TestOpenAppliesEmbeddedMigrations(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	var name string
	if err := database.QueryRowContext(context.Background(), "SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'kernel_metadata'").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "kernel_metadata" {
		t.Fatalf("migration table = %q", name)
	}
}

func TestMFAFactorMigrationBackfillsOpaqueRequiredID(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.ExecContext(context.Background(), `
		INSERT INTO users (id, username, username_key, password_hash, preferred_language, country, time_zone, created_at)
		VALUES ('u_legacy', 'legacy', 'legacy', 'hash', 'en', 'DE', 'UTC', '2026-09-14T00:00:00.000000Z');
		DROP TABLE mfa_factors;
		CREATE TABLE mfa_factors (
			user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			method TEXT NOT NULL CHECK (method IN ('totp', 'sms')),
			totp_secret BLOB, sms_destination TEXT, last_totp_step INTEGER, created_at TEXT NOT NULL,
			CHECK ((method = 'totp' AND totp_secret IS NOT NULL AND sms_destination IS NULL)
				OR (method = 'sms' AND totp_secret IS NULL AND sms_destination IS NOT NULL)));
		INSERT INTO mfa_factors (user_id, method, totp_secret, created_at)
		VALUES ('u_legacy', 'totp', zeroblob(20), '2026-09-14T00:00:00.000000Z');
		DROP TRIGGER tutor_responses_retry_request_pair_insert;
		DROP TRIGGER tutor_responses_retry_request_pair_update;
		DROP INDEX tutor_responses_retry_request_idx;
		ALTER TABLE tutor_responses DROP COLUMN retry_request_digest;
		ALTER TABLE tutor_responses DROP COLUMN retry_request_id;
		UPDATE schema_migrations SET version = 17, dirty = 0;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	var id string
	if err := database.QueryRowContext(context.Background(),
		"SELECT id FROM mfa_factors WHERE user_id = 'u_legacy'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if len(id) != 40 || id[:4] != "mff_" {
		t.Fatalf("backfilled factor ID = %q", id)
	}
	if _, err := database.ExecContext(context.Background(),
		"UPDATE mfa_factors SET id = NULL WHERE user_id = 'u_legacy'"); err == nil {
		t.Fatal("migrated factor ID accepted null")
	}
}

func TestOpenRejectsInsecureRestoredDatabase(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	path := directory + "/mia.sqlite3"
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory); err == nil {
		t.Fatal("Open accepted insecure restored database")
	}
}

func TestOpenRejectsDirtyAndNewerSchema(t *testing.T) {
	for name, statement := range map[string]string{"dirty": "UPDATE schema_migrations SET dirty = 1", "newer": "UPDATE schema_migrations SET version = 20"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			database, err := Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.ExecContext(context.Background(), statement); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(directory); err == nil {
				t.Fatal("Open accepted invalid schema state")
			}
		})
	}
}

func TestWithTxRollsBackPanic(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := database.ExecContext(context.Background(), "CREATE TABLE transaction_test (value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("WithTx did not preserve panic")
			}
		}()
		if err := WithTx(context.Background(), database, func(query *sql.Tx) error {
			if _, err := query.ExecContext(context.Background(), "INSERT INTO transaction_test VALUES (1)"); err != nil {
				t.Fatal(err)
			}
			panic("test panic")
		}); err != nil {
			t.Fatal(err)
		}
	}()
	var count int
	if err := database.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM transaction_test").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("panic transaction committed %d rows", count)
	}
}
