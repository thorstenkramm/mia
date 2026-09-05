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
	for name, statement := range map[string]string{"dirty": "UPDATE schema_migrations SET dirty = 1", "newer": "UPDATE schema_migrations SET version = 15"} {
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
