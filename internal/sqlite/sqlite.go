// Package sqlite opens MIA's SQLite database and applies embedded migrations.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/thorstenkramm/mia/migrations"
	_ "modernc.org/sqlite"
)

// Querier is the single persistence contract used by kernel and feature owner
// APIs. Both *sql.DB and *sql.Tx satisfy it.
type Querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// WithTx executes fn in one transaction, rolling back on any returned error and
// preserving that error's identity.
func WithTx(ctx context.Context, database *sql.DB, fn func(*sql.Tx) error) (returnErr error) {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if err := transaction.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			rollbackErr := fmt.Errorf("rollback transaction: %w", err)
			if returnErr == nil {
				returnErr = rollbackErr
			} else {
				returnErr = errors.Join(returnErr, rollbackErr)
			}
		}
	}()
	if err := fn(transaction); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

// Open opens the fixed data-directory database, configures every connection,
// and applies all embedded forward-only migrations.
func Open(dataDir string) (*sql.DB, error) {
	path := filepath.Join(dataDir, "mia.sqlite3")
	if err := validateExistingFiles(path); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	db, err := openDatabase(dsn)
	if err != nil {
		return nil, err
	}
	if err := migrateUp(db); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close migration database: %w", closeErr))
		}
		return nil, err
	}
	if err := secureFiles(path); err != nil {
		return nil, err
	}
	db, err = openDatabase(dsn)
	if err != nil {
		return nil, err
	}
	if err := secureFiles(path); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close database after secure-file failure: %w", closeErr))
		}
		return nil, err
	}
	return db, nil
}

func validateExistingFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Lstat(candidate); err == nil {
			if err := validatePrivateFile(candidate); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat database file: %w", err)
		}
	}
	return nil
}
func secureFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("stat database file: %w", err)
		}
		if err := os.Chmod(candidate, 0o600); err != nil {
			return fmt.Errorf("set database file mode: %w", err)
		}
		if err := validatePrivateFile(candidate); err != nil {
			return err
		}
	}
	return nil
}
func validatePrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat private file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("private database file has unsafe type or permissions")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("private database file has untrusted owner")
	}
	return nil
}

func openDatabase(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("ping database: %w", err), fmt.Errorf("close database after ping failure: %w", closeErr))
		}
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

func migrateUp(db *sql.DB) (returnErr error) {
	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("create migration database driver: %w", err)
	}
	migrator, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer func() {
		sourceErr, databaseErr := migrator.Close()
		if sourceErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close migration source: %w", sourceErr))
		}
		if databaseErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close migration database: %w", databaseErr))
		}
	}()
	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
