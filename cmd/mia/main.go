// Command mia runs MIA's server and local operator commands.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	osuser "os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/auth"
	"github.com/thorstenkramm/mia/internal/config"
	"github.com/thorstenkramm/mia/internal/course"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/invitation"
	"github.com/thorstenkramm/mia/internal/lifecycle"
	"github.com/thorstenkramm/mia/internal/lock"
	"github.com/thorstenkramm/mia/internal/logging"
	"github.com/thorstenkramm/mia/internal/provider/sms"
	"github.com/thorstenkramm/mia/internal/provider/smtp"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
	"golang.org/x/term"
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
	command.AddCommand(newServeCommand(), newBootstrapAdminCommand(), newResetAdminMFACommand())
	return command
}

func newBootstrapAdminCommand() *cobra.Command {
	return &cobra.Command{Use: "bootstrap-admin", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) (returnErr error) {
		if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
			return errors.New("bootstrap-admin requires an interactive terminal")
		}
		input, err := readBootstrapInput()
		if err != nil {
			return err
		}
		configuration, err := config.Load(command.Flags(), false)
		if err != nil {
			return err
		}
		dataLock, err := lock.Acquire(configuration.Main.DataDir)
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, dataLock.Close()) }()
		database, err := miSQLite.Open(configuration.Main.DataDir)
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, database.Close()) }()
		return miSQLite.WithTx(command.Context(), database, func(transaction *sql.Tx) error { return bootstrapAdministrator(command.Context(), transaction, input) })
	}}
}

func newResetAdminMFACommand() *cobra.Command {
	return &cobra.Command{Use: "reset-admin-mfa", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) (returnErr error) {
		if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
			return errors.New("reset-admin-mfa requires an interactive terminal")
		}
		configuration, err := config.Load(command.Flags(), false)
		if err != nil {
			return err
		}
		dataLock, err := lock.Acquire(configuration.Main.DataDir)
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, dataLock.Close()) }()
		database, err := miSQLite.Open(configuration.Main.DataDir)
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, database.Close()) }()
		administrator, err := user.SoleAdministrator(command.Context(), database)
		if err != nil {
			return err
		}
		hasMFA, err := auth.HasMFA(command.Context(), database, administrator.ID)
		if err != nil {
			return err
		}
		if !hasMFA {
			return errors.New("sole administrator has no MFA state to reset")
		}
		if _, err := fmt.Fprintf(os.Stdout, "Reset MFA for administrator %s. Type the exact username to continue: ", administrator.Username); err != nil {
			return err
		}
		value, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return fmt.Errorf("read administrator confirmation: %w", err)
		}
		if strings.TrimSpace(value) != administrator.Username {
			return errors.New("administrator confirmation does not match")
		}
		return miSQLite.WithTx(command.Context(), database, func(tx *sql.Tx) error {
			administrator, err := user.SoleAdministrator(command.Context(), tx)
			if err != nil {
				return err
			}
			hasMFA, err := auth.HasMFA(command.Context(), tx, administrator.ID)
			if err != nil {
				return err
			}
			if !hasMFA {
				return errors.New("sole administrator has no MFA state to reset")
			}
			if err := auth.ResetMFA(command.Context(), tx, administrator.ID); err != nil {
				return err
			}
			return audit.Write(command.Context(), tx, audit.ActionOperatorAdministratorMFAReset, "", administrator.ID)
		})
	}}
}

func bootstrapAdministrator(ctx context.Context, transaction *sql.Tx, input bootstrapInput) error {
	exists, err := user.HasAdministrator(ctx, transaction)
	if err != nil {
		return err
	}
	if exists {
		return user.ErrAdministratorExists
	}
	account, err := user.Create(ctx, transaction, user.CreateInput{Username: input.username, Email: input.email, PasswordHash: input.passwordHash, Language: input.language, Country: input.country, TimeZone: input.timeZone, EmailVerified: true, Roles: []user.Role{user.Administrator}})
	if err != nil {
		return err
	}
	return audit.Write(ctx, transaction, audit.ActionOperatorAdministratorBootstrapped, "", account.ID)
}

type bootstrapInput struct{ username, email, language, country, timeZone, passwordHash string }

func readBootstrapInput() (bootstrapInput, error) {
	reader := bufio.NewReader(os.Stdin)
	read := func(label string) (string, error) {
		if _, err := fmt.Fprint(os.Stdout, label+": "); err != nil {
			return "", fmt.Errorf("write %s prompt: %w", strings.ToLower(label), err)
		}
		value, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read %s: %w", strings.ToLower(label), err)
		}
		return strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r"), nil
	}
	var result bootstrapInput
	var err error
	if result.username, err = read("Username"); err != nil {
		return result, err
	}
	if result.email, err = read("Email"); err != nil {
		return result, err
	}
	if result.language, err = read("Language"); err != nil {
		return result, err
	}
	if result.country, err = read("Country"); err != nil {
		return result, err
	}
	if result.timeZone, err = read("Time zone"); err != nil {
		return result, err
	}
	if _, err := fmt.Fprint(os.Stdout, "Password: "); err != nil {
		return result, fmt.Errorf("write password prompt: %w", err)
	}
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return result, fmt.Errorf("read password: %w", err)
	}
	if _, err := fmt.Fprint(os.Stdout, "\nPassword confirmation: "); err != nil {
		return result, fmt.Errorf("write password confirmation prompt: %w", err)
	}
	confirmation, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return result, fmt.Errorf("read password confirmation: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout); err != nil {
		return result, fmt.Errorf("write prompt newline: %w", err)
	}
	if string(password) != string(confirmation) {
		return result, errors.New("password confirmation does not match")
	}
	if result.passwordHash, err = identity.Password(string(password)); err != nil {
		return result, err
	}
	return result, nil
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
		server, authRoutes, err := httpserver.New(httpserver.Options{DataDir: configuration.Main.DataDir, DocRoot: configuration.Main.DocRoot, TrustedProxyCIDRs: configuration.HTTP.TrustedProxyCIDRs, Logger: logger.Slog()})
		if err != nil {
			return err
		}
		server.SetIdentityLoader(func(ctx context.Context, id string) (httpserver.IdentityState, error) {
			account, err := user.LoadSecurityState(ctx, database, id)
			if errors.Is(err, sql.ErrNoRows) {
				return httpserver.IdentityState{}, httpserver.ErrIdentityNotFound
			}
			return httpserver.IdentityState{SecurityGeneration: account.SecurityGeneration, MustChangePassword: account.MustChangePassword, Banned: account.Banned}, err
		})
		recoveryDeliveries := auth.NewDeliveryManager(database, smtp.New(configuration, logger.Slog()), logger.Slog())
		defer recoveryDeliveries.Close()
		smsSender := sms.New(sms.ClientOptions{Username: configuration.ClickSend.Username,
			APIKey: configuration.ClickSend.APIKey, SenderID: configuration.ClickSend.SenderID})
		auth.Register(server, authRoutes, database, configuration.Main.PublicURL, recoveryDeliveries, smsSender)
		user.RegisterProfileRoutes(server,
			user.NewService(database, configuration.Main.DataDir, smsSender, auth.InvalidatePendingSMS))

		invitationService := invitation.NewService(database, logger.Slog())
		invitationDeliveries := invitation.NewDeliveryManager(invitationService, database, smtp.New(configuration, logger.Slog()), logger.Slog())
		defer invitationDeliveries.Close()
		invitation.Register(server, invitationService, configuration.Main.PublicURL, invitationDeliveries)
		invitation.RegisterRoleRoutes(server, database, logger.Slog())
		lifecycleRegistry := &lifecycle.Registry{}
		course.Register(server, course.NewService(database, configuration.Main.DataDir, lifecycleRegistry, nil, nil,
			nil, auth.InvalidateSecurityArtifacts, logger.Slog()))
		return serve(command.Context(), configuration.HTTP.Listen, configuration.HTTP.SocketGroup, server.Echo, logger)
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
	group, err := osuser.LookupGroup(name)
	if err != nil {
		return 0, fmt.Errorf("look up Unix socket group: %w", err)
	}
	id, err := strconv.Atoi(group.Gid)
	if err != nil {
		return 0, fmt.Errorf("parse Unix socket group: %w", err)
	}
	return id, nil
}
