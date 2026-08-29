package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReopenKeepsLoggerDestinationLive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mia.log")
	logger, err := New(path, "info", "text")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := logger.Close(); err != nil {
			t.Error(err)
		}
	}()
	logger.Slog().Info("before")
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := logger.Reopen(); err != nil {
		t.Fatal(err)
	}
	logger.Slog().Info("after")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "after") {
		t.Fatalf("reopened log = %q", content)
	}
}

func TestReopenCorrectsExistingLogPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mia.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	logger, err := New(path, "info", "json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := logger.Close(); err != nil {
			t.Error(err)
		}
	}()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("log permissions = %04o", info.Mode().Perm())
	}
}
