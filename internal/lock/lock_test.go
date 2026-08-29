package lock

import (
	"errors"
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
	if _, err := Acquire(directory); !errors.Is(err, ErrHeld) {
		t.Fatalf("second Acquire error = %v", err)
	}
}
