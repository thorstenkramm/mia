// Package filepublish atomically publishes private data-directory files.
package filepublish

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Change represents one already-published filesystem change. Finish must be
// called exactly once: commit keeps the change and rollback restores its prior state.
type Change struct {
	destination string
	backup      string
	hadOld      bool
	finished    bool
}

// Replace writes data through a same-directory mode-0600 temporary file, syncs
// it, and atomically renames it over destination. Parent directories are 0700.
func Replace(destination string, data []byte) (published *Change, returnErr error) {
	directory := filepath.Dir(destination)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create publication directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("secure publication directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".mia-publish-*")
	if err != nil {
		return nil, fmt.Errorf("create publication temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				returnErr = errors.Join(returnErr, fmt.Errorf("remove publication temporary file: %w", err))
			}
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return nil, closeAfter(temporary, fmt.Errorf("secure publication temporary file: %w", err))
	}
	if _, err := temporary.Write(data); err != nil {
		return nil, closeAfter(temporary, fmt.Errorf("write publication temporary file: %w", err))
	}
	if err := temporary.Sync(); err != nil {
		return nil, closeAfter(temporary, fmt.Errorf("sync publication temporary file: %w", err))
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close publication temporary file: %w", err)
	}
	change := &Change{destination: destination}
	if _, err := os.Lstat(destination); err == nil {
		backup, createErr := os.CreateTemp(directory, ".mia-backup-*")
		if createErr != nil {
			return nil, fmt.Errorf("reserve publication backup: %w", createErr)
		}
		change.backup = backup.Name()
		if closeErr := backup.Close(); closeErr != nil {
			return nil, fmt.Errorf("close publication backup placeholder: %w", closeErr)
		}
		if removeErr := os.Remove(change.backup); removeErr != nil {
			return nil, fmt.Errorf("remove publication backup placeholder: %w", removeErr)
		}
		if linkErr := os.Link(destination, change.backup); linkErr != nil {
			return nil, fmt.Errorf("preserve previous published file: %w", linkErr)
		}
		change.hadOld = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect published file: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		publishErr := fmt.Errorf("publish file: %w", err)
		if change.backup != "" {
			if removeErr := os.Remove(change.backup); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				publishErr = errors.Join(publishErr, fmt.Errorf("remove publication backup: %w", removeErr))
			}
		}
		return nil, publishErr
	}
	removeTemporary = false
	if err := syncDirectory(directory); err != nil {
		return nil, errors.Join(err, change.Finish(false))
	}
	return change, nil
}

// Remove makes destination absent while retaining enough state to roll back.
func Remove(destination string) (*Change, error) {
	change := &Change{destination: destination}
	if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
		return change, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect file for removal: %w", err)
	}
	directory := filepath.Dir(destination)
	backup, err := os.CreateTemp(directory, ".mia-remove-*")
	if err != nil {
		return nil, fmt.Errorf("reserve removal backup: %w", err)
	}
	change.backup = backup.Name()
	if err := backup.Close(); err != nil {
		return nil, fmt.Errorf("close removal backup placeholder: %w", err)
	}
	if err := os.Remove(change.backup); err != nil {
		return nil, fmt.Errorf("remove removal backup placeholder: %w", err)
	}
	if err := os.Rename(destination, change.backup); err != nil {
		return nil, fmt.Errorf("stage file removal: %w", err)
	}
	change.hadOld = true
	if err := syncDirectory(directory); err != nil {
		return nil, errors.Join(err, change.Finish(false))
	}
	return change, nil
}

func closeAfter(file *os.File, cause error) error {
	if err := file.Close(); err != nil {
		return errors.Join(cause, fmt.Errorf("close publication temporary file: %w", err))
	}
	return cause
}

// Finish commits or rolls back a published change.
func (change *Change) Finish(commit bool) error {
	if change == nil || change.finished {
		return errors.New("publication change already finished")
	}
	change.finished = true
	if commit {
		if change.backup != "" {
			if err := os.Remove(change.backup); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove publication backup: %w", err)
			}
		}
		return syncDirectory(filepath.Dir(change.destination))
	}
	if change.hadOld {
		if err := os.Rename(change.backup, change.destination); err != nil {
			return fmt.Errorf("restore previous published file: %w", err)
		}
	} else if err := os.Remove(change.destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove rolled-back published file: %w", err)
	}
	return syncDirectory(filepath.Dir(change.destination))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open publication directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		if closeErr != nil {
			return errors.Join(fmt.Errorf("sync publication directory: %w", syncErr),
				fmt.Errorf("close publication directory: %w", closeErr))
		}
		return fmt.Errorf("sync publication directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close publication directory: %w", closeErr)
	}
	return nil
}
