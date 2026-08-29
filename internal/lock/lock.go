// Package lock provides the data-directory process lock.
package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var ErrHeld = errors.New("data directory is locked")

// Lock holds an exclusive, non-blocking data-directory lock.
type Lock struct {
	file *os.File
}

// Acquire obtains the exclusive lock at dataDir/mia.lock.
func Acquire(dataDir string) (*Lock, error) {
	path := filepath.Join(dataDir, "mia.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("set lock file mode: %w", err), fmt.Errorf("close lock file: %w", closeErr))
		}
		return nil, fmt.Errorf("set lock file mode: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("acquire lock: %w", err), fmt.Errorf("close lock file: %w", closeErr))
		}
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	return &Lock{file: file}, nil
}

// Close releases the OS lock.
func (lock *Lock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	if err := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN); err != nil {
		if closeErr := lock.file.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("release lock: %w", err), fmt.Errorf("close lock file: %w", closeErr))
		}
		return fmt.Errorf("release lock: %w", err)
	}
	if err := lock.file.Close(); err != nil {
		return fmt.Errorf("close lock file: %w", err)
	}
	lock.file = nil
	return nil
}
