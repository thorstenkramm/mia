package filepublish

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceCommitAndRollbackPreservePrivateAtomicState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users", "u_test", "avatar.png")
	change, err := Replace(path, []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Finish(true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("published mode = %o", info.Mode().Perm())
	}
	change, err = Replace(path, []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Finish(false); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "first" {
		t.Fatalf("rolled-back contents = %q", contents)
	}
}

func TestRemoveIsIdempotentAndRollbackRestores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "avatar.png")
	if err := os.WriteFile(path, []byte("avatar"), 0o600); err != nil {
		t.Fatal(err)
	}
	change, err := Remove(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Finish(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	change, err = Remove(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Finish(true); err != nil {
		t.Fatal(err)
	}
	change, err = Remove(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Finish(true); err != nil {
		t.Fatal(err)
	}
}
