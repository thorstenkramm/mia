package lock

import (
	"errors"
	"strings"
	"testing"
)

func TestAcquireRejectsContention(t *testing.T) {
	directory := t.TempDir()
	first, err := Acquire(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
	}()
	second, err := Acquire(directory)
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("second Acquire error = %v", err)
	}
	if second != nil {
		t.Fatal("second Acquire returned a lock")
	}
	// Without the directory an operator cannot tell which instance is running,
	// and "locked" alone reads like a stale file waiting to be deleted.
	if !strings.Contains(err.Error(), directory) {
		t.Errorf("error %q does not name the data directory", err)
	}
	if !strings.Contains(err.Error(), "another mia process") {
		t.Errorf("error %q does not explain the cause", err)
	}
}
