package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/spf13/pflag"
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
	shutdownStarted := false
	if err := serve(context, "127.0.0.1:0", "", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), func() {
		shutdownStarted = true
	}, logger); err != nil {
		t.Fatalf("serve shutdown error = %v", err)
	}
	if !shutdownStarted {
		t.Fatal("serve did not begin worker shutdown before HTTP shutdown")
	}
}

func TestBootstrapAdminRejectsNonTerminalInput(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	command := newRootCommand()
	command.SetArgs([]string{"--main-data-dir", dataDir, "bootstrap-admin"})
	if err := command.Execute(); err == nil || err.Error() != "bootstrap-admin requires an interactive terminal or complete noninteractive flags" {
		t.Fatalf("bootstrap non-terminal error = %v", err)
	}
}

func TestBootstrapInteractiveModeRejectsPartialAndMixedFlags(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	for _, name := range bootstrapFlags {
		flags.String(name, "", "")
	}
	if err := flags.Set("username", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrapInteractiveMode(flags, false, false); err == nil || !strings.Contains(err.Error(), "--email") {
		t.Fatalf("partial mode error = %v", err)
	}
	if _, err := bootstrapInteractiveMode(flags, true, true); err == nil || !strings.Contains(err.Error(), "noninteractive") {
		t.Fatalf("mixed mode error = %v", err)
	}
}

func TestReadPasswordFileAcceptsOneTerminalLineEnding(t *testing.T) {
	for name, suffix := range map[string]string{"LF": "\n", "CRLF": "\r\n"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "password")
			if err := os.WriteFile(path, []byte("  twelve chars  "+suffix), 0o600); err != nil {
				t.Fatal(err)
			}
			value, err := readPasswordFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(value) != len("  twelve chars  ") || value[0] != ' ' || value[len(value)-1] != ' ' {
				t.Fatal("password-file whitespace was not retained")
			}
		})
	}
}

func TestReadPasswordFileRejectsInvalidInput(t *testing.T) {
	directory := t.TempDir()
	for name, content := range map[string][]byte{
		"empty":       {},
		"second line": []byte("twelve chars\n\n"),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, name)
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readPasswordFile(path); err == nil {
				t.Fatal("readPasswordFile accepted invalid input")
			}
		})
	}
	if _, err := readPasswordFile(directory); err == nil {
		t.Fatal("readPasswordFile accepted a directory")
	}
	fifoPath := filepath.Join(directory, "password-fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPasswordFile(fifoPath); err == nil {
		t.Fatal("readPasswordFile accepted a FIFO")
	}
}

func TestReadBootstrapPasswordReportsEveryPolicyFailure(t *testing.T) {
	for name, test := range map[string]struct {
		password string
		message  string
	}{
		"invalid UTF-8":  {string([]byte{0xff}), "it must be valid UTF-8"},
		"too short":      {"short", "it must contain at least 12 Unicode code points"},
		"too many runes": {strings.Repeat("a", 129), "it must contain at most 128 Unicode code points"},
		"too many bytes": {strings.Repeat("a", 513), "it must be at most 512 bytes"},
		"common":         {"passwordpassword", "it is commonly used"},
	} {
		t.Run(name, func(t *testing.T) {
			passwords := [][]byte{[]byte(test.password), []byte(test.password), []byte("twelve chars"), []byte("twelve chars")}
			var reports []string
			hash, err := readBootstrapPassword(func(string) ([]byte, error) {
				value := passwords[0]
				passwords = passwords[1:]
				return value, nil
			}, func(message string) error {
				reports = append(reports, message)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(passwords) != 0 || len(reports) != 1 || !strings.Contains(reports[0], test.message) {
				t.Fatalf("bootstrap password reports = %v", reports)
			}
			valid, err := identity.VerifyPassword("twelve chars", hash)
			if err != nil || !valid {
				t.Fatalf("bootstrap password hash valid = %v, %v", valid, err)
			}
		})
	}
}

func TestMaskedPasswordStatePreservesInputAndFeedback(t *testing.T) {
	state := maskedPasswordState{}
	var feedback strings.Builder
	input := append([]byte("ab"), []byte("界")...)
	for _, value := range input {
		output, complete, err := state.accept(value)
		if err != nil || complete {
			t.Fatalf("state acceptance = %q, %v, %v", output, complete, err)
		}
		feedback.WriteString(output)
	}
	output, complete, err := state.accept(127)
	if err != nil || complete {
		t.Fatalf("backspace acceptance = %q, %v, %v", output, complete, err)
	}
	feedback.WriteString(output)
	if got, want := feedback.String(), "***\b \b"; got != want {
		t.Fatalf("feedback = %q, want %q", got, want)
	}
	if got, want := string(state.value), "ab"; got != want {
		t.Fatalf("preserved bytes = %q, want %q", got, want)
	}
	if _, complete, err := state.accept('\n'); err != nil || !complete {
		t.Fatalf("completion = %v, %v", complete, err)
	}
}

func TestMaskedPasswordStateBoundsRetainedInput(t *testing.T) {
	state := maskedPasswordState{}
	for range 514 {
		if _, complete, err := state.accept('a'); err != nil || complete {
			t.Fatalf("state acceptance = %v, %v", complete, err)
		}
	}
	if len(state.value) != 513 || state.overflow != 1 {
		t.Fatalf("bounded input = %d bytes with overflow %d", len(state.value), state.overflow)
	}
}

func TestBootstrapAdminCreatesAdministratorNoninteractively(t *testing.T) {
	directory := t.TempDir()
	dataDir := filepath.Join(directory, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "mia.toml")
	if err := os.WriteFile(configPath, []byte("[main]\ndata_dir = \""+dataDir+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	passwordPath := filepath.Join(directory, "password")
	password := "  twelve chars  "
	if err := os.WriteFile(passwordPath, []byte(password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := &bytes.Buffer{}
	command := newRootCommand()
	command.SetOut(output)
	command.SetArgs([]string{"--config", configPath, "bootstrap-admin", "--username", "admin", "--email", "admin@localhost.de", "--language", "en", "--country", "DE", "--time-zone", "UTC", "--password-file", passwordPath})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "User admin has been inserted into "+miSQLite.DatabasePath(dataDir)+"\n"; got != want {
		t.Fatalf("bootstrap output = %q, want %q", got, want)
	}
	database, err := miSQLite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	if exists, err := user.HasAdministrator(context.Background(), database); err != nil || !exists {
		t.Fatalf("administrator exists = %v, %v", exists, err)
	}
	var hash string
	if err := database.QueryRowContext(context.Background(), "SELECT password_hash FROM users WHERE username = ?", "admin").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	valid, err := identity.VerifyPassword(password, hash)
	if err != nil || !valid {
		t.Fatalf("exact password hash valid = %v, %v", valid, err)
	}
	trimmedValid, err := identity.VerifyPassword(strings.TrimSpace(password), hash)
	if err != nil || trimmedValid {
		t.Fatalf("trimmed password hash valid = %v, %v", trimmedValid, err)
	}
}

func TestBootstrapAdminValidatesDataDirectoryBeforeReadingPasswordFile(t *testing.T) {
	directory := t.TempDir()
	passwordPath := filepath.Join(directory, "unreadable-password-file")
	for name, test := range map[string]struct {
		args       []string
		diagnostic string
	}{
		"missing": {diagnostic: "main.data_dir must be configured"},
		"invalid": {
			args:       []string{"--main-data-dir", "relative-data-dir"},
			diagnostic: "main.data_dir must be an absolute path",
		},
	} {
		t.Run(name, func(t *testing.T) {
			command := newRootCommand()
			args := append([]string{}, test.args...)
			args = append(args, "bootstrap-admin", "--username", "admin", "--email", "admin@localhost.de", "--language", "en", "--country", "DE", "--time-zone", "UTC", "--password-file", passwordPath)
			command.SetArgs(args)
			err := command.Execute()
			if err == nil || !strings.Contains(err.Error(), test.diagnostic) {
				t.Fatalf("bootstrap error = %v", err)
			}
			if strings.Contains(err.Error(), "password file") {
				t.Fatalf("bootstrap read password file before validating configuration: %v", err)
			}
		})
	}
}
