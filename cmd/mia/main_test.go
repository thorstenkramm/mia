package main

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/logging"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestListenSocketRejectsActiveUnixSocketAndSetsPermissions(t *testing.T) {
	file, err := os.CreateTemp("/tmp", "mia-sock-")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	active, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := active.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	}()
	if _, err := listenSocket("unix:"+path, ""); err == nil {
		t.Fatal("listenSocket accepted an active socket")
	}
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := listenSocket("unix:"+path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	}()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("Unix socket permissions = %04o", info.Mode().Perm())
	}
}

func TestBootstrapAdministratorRejectsExistingAdministratorWithoutWrites(t *testing.T) {
	database, err := miSQLite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		_, err := user.Create(context.Background(), tx, user.CreateInput{Username: "admin", Email: "admin@example.test", PasswordHash: hash, Language: "en", Country: "DE", TimeZone: "UTC", EmailVerified: true, Roles: []user.Role{user.Administrator}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var beforeUsers, beforeRoles, beforeAudits int
	if err := database.QueryRow("SELECT COUNT(*) FROM users").Scan(&beforeUsers); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM user_roles").Scan(&beforeRoles); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&beforeAudits); err != nil {
		t.Fatal(err)
	}
	err = miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		return bootstrapAdministrator(context.Background(), tx, bootstrapInput{username: "other", email: "other@example.test", passwordHash: hash, language: "en", country: "DE", timeZone: "UTC"})
	})
	if !errors.Is(err, user.ErrAdministratorExists) {
		t.Fatalf("bootstrap error = %v", err)
	}
	var users, roles, audits int
	if err := database.QueryRow("SELECT COUNT(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM user_roles").Scan(&roles); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if users != beforeUsers || roles != beforeRoles || audits != beforeAudits {
		t.Fatalf("writes changed: %d/%d/%d", users, roles, audits)
	}
}

func TestServeGracefulShutdownDoesNotReturnClosedListenerError(t *testing.T) {
	logger, err := logging.New("", "error", "json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := logger.Close(); err != nil {
			t.Error(err)
		}
	}()
	context, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serve(context, "127.0.0.1:0", "", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), logger); err != nil {
		t.Fatalf("serve shutdown error = %v", err)
	}
}

func TestBootstrapAdminRejectsNonTerminalInput(t *testing.T) {
	command := newRootCommand()
	command.SetArgs([]string{"bootstrap-admin"})
	if err := command.Execute(); err == nil || err.Error() != "bootstrap-admin requires an interactive terminal" {
		t.Fatalf("bootstrap non-terminal error = %v", err)
	}
}
