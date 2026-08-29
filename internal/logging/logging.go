// Package logging configures MIA's shared structured logger.
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Logger owns MIA's single structured logger and is safe for concurrent use.
type Logger struct {
	path   string
	file   *os.File
	writer *reopenWriter
	logger *slog.Logger
	mu     sync.Mutex
}

// reopenWriter keeps the handler's output stable while its destination changes.
type reopenWriter struct {
	mu     sync.RWMutex
	output io.Writer
}

func (writer *reopenWriter) Write(data []byte) (int, error) {
	writer.mu.RLock()
	defer writer.mu.RUnlock()
	return writer.output.Write(data)
}
func (writer *reopenWriter) replace(output io.Writer) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.output = output
}

// New constructs the configured shared logger.
func New(path, level, format string) (*Logger, error) {
	parsed := new(slog.Level)
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("parse log level: %w", err)
	}
	writer := &reopenWriter{output: os.Stderr}
	options := &slog.HandlerOptions{Level: *parsed}
	var handler slog.Handler = slog.NewJSONHandler(writer, options)
	if format == "text" {
		handler = slog.NewTextHandler(writer, options)
	}
	logger := &Logger{path: path, writer: writer, logger: slog.New(handler)}
	if err := logger.Reopen(); err != nil {
		return nil, err
	}
	return logger, nil
}

// Slog returns the stable process-wide logger.
func (logger *Logger) Slog() *slog.Logger { return logger.logger }

// Reopen closes and reopens the configured log file. Standard error is retained
// when no log file is configured.
func (logger *Logger) Reopen() error {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if logger.path == "" {
		return nil
	}
	fd, err := unix.Open(logger.path, unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o640)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	next := os.NewFile(uintptr(fd), logger.path)
	if err := next.Chmod(0o640); err != nil {
		if closeErr := next.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("set log file mode: %w", err), fmt.Errorf("close log file: %w", closeErr))
		}
		return fmt.Errorf("set log file mode: %w", err)
	}
	if err := validateLogFile(next); err != nil {
		if closeErr := next.Close(); closeErr != nil {
			return errors.Join(err, fmt.Errorf("close log file: %w", closeErr))
		}
		return err
	}
	logger.writer.replace(next)
	previous := logger.file
	logger.file = next
	if previous != nil {
		if err := previous.Close(); err != nil {
			return fmt.Errorf("close prior log file: %w", err)
		}
	}
	return nil
}

func validateLogFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat log file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o640 {
		return fmt.Errorf("log file has unsafe type or permissions")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("log file has an untrusted owner")
	}
	return nil
}

// Close closes a configured log file.
func (logger *Logger) Close() error {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if logger.file == nil {
		return nil
	}
	logger.writer.replace(io.Discard)
	err := logger.file.Close()
	logger.file = nil
	return err
}
