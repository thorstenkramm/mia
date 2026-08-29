// Command mia runs MIA's server and local operator commands.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/thorstenkramm/mia/internal/config"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/lock"
	"github.com/thorstenkramm/mia/internal/logging"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	command := &cobra.Command{Use: "mia", SilenceUsage: true}
	config.AddFlags(command.PersistentFlags())
	command.AddCommand(newServeCommand(), unavailableOfflineCommand("bootstrap-admin"), unavailableOfflineCommand("reset-admin-mfa"))
	return command
}

func newServeCommand() *cobra.Command {
	return &cobra.Command{Use: "serve", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) (returnErr error) {
		configuration, err := config.Load(command.Flags(), true)
		if err != nil {
			return err
		}
		logger, err := logging.New(configuration.Log.File, configuration.Log.Level, configuration.Log.Format)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := logger.Close(); closeErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close logger: %w", closeErr))
			}
		}()
		dataLock, err := lock.Acquire(configuration.Main.DataDir)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := dataLock.Close(); closeErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("release data directory lock: %w", closeErr))
			}
		}()
		database, err := miSQLite.Open(configuration.Main.DataDir)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := database.Close(); closeErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close database: %w", closeErr))
			}
		}()
		server, err := httpserver.New(httpserver.Options{DataDir: configuration.Main.DataDir, DocRoot: configuration.Main.DocRoot, TrustedProxyCIDRs: configuration.HTTP.TrustedProxyCIDRs, Logger: logger.Slog()})
		if err != nil {
			return err
		}
		return serve(command.Context(), configuration.HTTP.Listen, configuration.HTTP.SocketGroup, server.Echo, logger)
	}}
}

func unavailableOfflineCommand(name string) *cobra.Command {
	return &cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		return errors.New(name + " is unavailable until the authentication slice is installed")
	}}
}

func serve(parent context.Context, listen, socketGroup string, handler http.Handler, logger *logging.Logger) (returnErr error) {
	listener, err := listenSocket(listen, socketGroup)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			returnErr = errors.Join(returnErr, fmt.Errorf("close listener: %w", closeErr))
		}
	}()
	if strings.HasPrefix(listen, "unix:") {
		path := strings.TrimPrefix(listen, "unix:")
		created, err := listenerSocketInfo(listener)
		if err != nil {
			return fmt.Errorf("stat created Unix socket: %w", err)
		}
		defer func() {
			if removeErr := removeCreatedSocket(path, created); removeErr != nil {
				returnErr = errors.Join(returnErr, removeErr)
			}
		}()
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	context, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	for {
		select {
		case err := <-errCh:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return fmt.Errorf("serve HTTP: %w", err)
		case <-context.Done():
			shutdown, cancel := contextWithTimeout(context, 30*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdown); err != nil {
				return fmt.Errorf("shut down HTTP server: %w", err)
			}
			return nil
		case <-hup:
			if err := logger.Reopen(); err != nil {
				return err
			}
		}
	}
}

func listenerSocketInfo(listener net.Listener) (os.FileInfo, error) {
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		return nil, errors.New("listener is not a Unix socket")
	}
	file, err := unixListener.File()
	if err != nil {
		return nil, fmt.Errorf("duplicate Unix socket descriptor: %w", err)
	}
	info, err := file.Stat()
	closeErr := file.Close()
	if err != nil {
		return nil, fmt.Errorf("stat Unix socket descriptor: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close Unix socket descriptor: %w", closeErr)
	}
	return info, nil
}

func contextWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), timeout)
}

func listenSocket(value, socketGroup string) (net.Listener, error) {
	if strings.HasPrefix(value, "unix:") {
		path := strings.TrimPrefix(value, "unix:")
		if err := removeStaleSocket(path); err != nil {
			return nil, err
		}
		listener, err := net.Listen("unix", path)
		if err != nil {
			return nil, err
		}
		groupID, err := socketGroupID(socketGroup)
		if err != nil {
			return nil, closeAndRemoveSocket(listener, path, err)
		}
		if err := os.Chmod(path, 0o660); err != nil {
			return nil, closeAndRemoveSocket(listener, path, fmt.Errorf("set Unix socket mode: %w", err))
		}
		if err := os.Chown(path, -1, groupID); err != nil {
			return nil, closeAndRemoveSocket(listener, path, fmt.Errorf("set Unix socket group: %w", err))
		}
		return listener, nil
	}
	return net.Listen("tcp", value)
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat Unix socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("unix listener path exists and is not a socket")
	}
	connection, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err == nil {
		activeErr := errors.New("unix listener is already active")
		if closeErr := connection.Close(); closeErr != nil {
			return errors.Join(activeErr, fmt.Errorf("close Unix probe connection: %w", closeErr))
		}
		return activeErr
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("cannot determine whether Unix socket is active: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
		return errors.New("stale Unix socket has an untrusted owner")
	}
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("re-stat stale Unix socket: %w", err)
	}
	if !os.SameFile(info, current) {
		return errors.New("unix socket changed while checking activity")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale Unix socket: %w", err)
	}
	return nil
}

// removeCreatedSocket removes the listener path only when it still identifies
// the socket bound by this process, avoiding unlinking a replacement socket.
func removeCreatedSocket(path string, created os.FileInfo) error {
	current, err := os.Lstat(path)
	if err == nil && os.SameFile(created, current) {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove created Unix socket: %w", err)
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat created Unix socket: %w", err)
	}
	return nil
}

func closeAndRemoveSocket(listener net.Listener, path string, primary error) error {
	if err := listener.Close(); err != nil {
		primary = errors.Join(primary, fmt.Errorf("close Unix socket: %w", err))
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		primary = errors.Join(primary, fmt.Errorf("remove Unix socket: %w", err))
	}
	return primary
}

func socketGroupID(name string) (int, error) {
	if name == "" {
		return os.Getgid(), nil
	}
	group, err := user.LookupGroup(name)
	if err != nil {
		return 0, fmt.Errorf("look up Unix socket group: %w", err)
	}
	id, err := strconv.Atoi(group.Gid)
	if err != nil {
		return 0, fmt.Errorf("parse Unix socket group: %w", err)
	}
	return id, nil
}
