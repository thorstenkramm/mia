// Command mia runs MIA's server and local operator commands.
package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	osuser "os/user"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/auth"
	"github.com/thorstenkramm/mia/internal/config"
	"github.com/thorstenkramm/mia/internal/course"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/invitation"
	"github.com/thorstenkramm/mia/internal/jobs"
	"github.com/thorstenkramm/mia/internal/lifecycle"
	"github.com/thorstenkramm/mia/internal/lock"
	"github.com/thorstenkramm/mia/internal/logging"
	"github.com/thorstenkramm/mia/internal/material"
	"github.com/thorstenkramm/mia/internal/provider/mistral"
	"github.com/thorstenkramm/mia/internal/provider/openai"
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
	command := &cobra.Command{Use: "bootstrap-admin", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) (returnErr error) {
		input, err := readBootstrapInput(command.Flags(), term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stdout.Fd())))
		if err != nil {
			return err
		}
		// jscpd:ignore-start
		// Bootstrap input is intentionally collected before offline database setup.
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
		// jscpd:ignore-end
		if err := miSQLite.WithTx(command.Context(), database, func(transaction *sql.Tx) error {
			return bootstrapAdministrator(command.Context(), transaction, input)
		}); err != nil {
			return err
		}
		_, err = fmt.Fprintf(command.OutOrStdout(), "User %s has been inserted into %s\n", input.username,
			miSQLite.DatabasePath(configuration.Main.DataDir))
		return err
	}}
	command.Flags().String("username", "", "administrator username for noninteractive bootstrap")
	command.Flags().String("email", "", "administrator email for noninteractive bootstrap")
	command.Flags().String("language", "", "administrator language for noninteractive bootstrap")
	command.Flags().String("country", "", "administrator country for noninteractive bootstrap")
	command.Flags().String("time-zone", "", "administrator time zone for noninteractive bootstrap")
	command.Flags().String("password-file", "", "one-line administrator password file for noninteractive bootstrap")
	return command
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

var bootstrapFlags = []string{"username", "email", "language", "country", "time-zone", "password-file"}

func readBootstrapInput(flags *pflag.FlagSet, stdinTerminal, stdoutTerminal bool) (bootstrapInput, error) {
	interactive, modeErr := bootstrapInteractiveMode(flags, stdinTerminal, stdoutTerminal)
	if modeErr != nil {
		return bootstrapInput{}, modeErr
	}
	if !interactive {
		return readBootstrapFlagInput(flags)
	}
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
	result.passwordHash, err = readBootstrapPassword(promptMaskedPassword, func(message string) error {
		_, err := fmt.Fprintln(os.Stdout, message)
		return err
	})
	return result, err
}

func readBootstrapPassword(read func(string) ([]byte, error), report func(string) error) (string, error) {
	for {
		password, err := read("Password: ")
		if err != nil {
			if errors.Is(err, identity.ErrPasswordTooLongBytes) {
				if err := reportBootstrapPasswordPolicyFailure(report, err); err != nil {
					return "", err
				}
				continue
			}
			return "", err
		}
		confirmation, err := read("Password confirmation: ")
		if err != nil {
			if errors.Is(err, identity.ErrPasswordTooLongBytes) {
				if err := reportBootstrapPasswordPolicyFailure(report, err); err != nil {
					return "", err
				}
				continue
			}
			return "", err
		}
		if !bytes.Equal(password, confirmation) {
			if err := report("Password confirmation does not match. Please try again."); err != nil {
				return "", fmt.Errorf("write password mismatch: %w", err)
			}
			continue
		}
		hash, err := identity.Password(string(password))
		if err == nil {
			return hash, nil
		}
		if !errors.Is(err, identity.ErrInvalidPassword) {
			return "", err
		}
		if err := reportBootstrapPasswordPolicyFailure(report, err); err != nil {
			return "", err
		}
	}
}

func reportBootstrapPasswordPolicyFailure(report func(string) error, policyErr error) error {
	if err := report("Password rejected: " + passwordPolicyMessage(policyErr) + ". Please try again."); err != nil {
		return fmt.Errorf("write password policy failure: %w", err)
	}
	return nil
}

func bootstrapInteractiveMode(flags *pflag.FlagSet, stdinTerminal, stdoutTerminal bool) (bool, error) {
	flagsProvided := false
	for _, name := range bootstrapFlags {
		if flags.Changed(name) {
			flagsProvided = true
			break
		}
	}
	if flagsProvided {
		if stdinTerminal || stdoutTerminal {
			return false, errors.New("bootstrap-admin flags require noninteractive input and output")
		}
		for _, name := range bootstrapFlags {
			value, err := flags.GetString(name)
			if err != nil {
				return false, fmt.Errorf("read --%s: %w", name, err)
			}
			if !flags.Changed(name) || value == "" {
				return false, fmt.Errorf("noninteractive bootstrap requires --%s", name)
			}
		}
		return false, nil
	}
	if !stdinTerminal || !stdoutTerminal {
		return false, errors.New("bootstrap-admin requires an interactive terminal or complete noninteractive flags")
	}
	return true, nil
}

func readBootstrapFlagInput(flags *pflag.FlagSet) (bootstrapInput, error) {
	var input bootstrapInput
	values := []*string{&input.username, &input.email, &input.language, &input.country, &input.timeZone}
	for index, name := range bootstrapFlags[:5] {
		value, err := flags.GetString(name)
		if err != nil {
			return input, fmt.Errorf("read --%s: %w", name, err)
		}
		*values[index] = value
	}
	path, err := flags.GetString("password-file")
	if err != nil {
		return input, fmt.Errorf("read --password-file: %w", err)
	}
	password, err := readPasswordFile(path)
	if err != nil {
		return input, err
	}
	input.passwordHash, err = identity.Password(string(password))
	return input, err
}

// readPasswordFile accepts exactly one non-empty line and keeps every password byte except its terminal line ending.
func readPasswordFile(path string) (value []byte, returnErr error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat password file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("password file is not regular")
	}
	descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open password file: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		if err := syscall.Close(descriptor); err != nil {
			return nil, fmt.Errorf("close password file descriptor: %w", err)
		}
		return nil, errors.New("create password file handle")
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close password file: %w", err))
		}
	}()
	info, err = file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat password file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("password file is not regular")
	}
	value, err = io.ReadAll(io.LimitReader(file, 515))
	if err != nil {
		return nil, fmt.Errorf("read password file: %w", err)
	}
	if len(value) == 515 {
		return nil, errors.New("password file exceeds the maximum password length")
	}
	if bytes.HasSuffix(value, []byte("\r\n")) {
		value = value[:len(value)-2]
	} else if bytes.HasSuffix(value, []byte("\n")) {
		value = value[:len(value)-1]
	}
	if len(value) == 0 {
		return nil, errors.New("password file is empty")
	}
	if bytes.ContainsAny(value, "\r\n") {
		return nil, errors.New("password file must contain exactly one line")
	}
	return value, nil
}

func promptMaskedPassword(label string) ([]byte, error) {
	if _, err := fmt.Fprint(os.Stdout, label); err != nil {
		return nil, fmt.Errorf("write password prompt: %w", err)
	}
	value, err := readMaskedPassword(int(os.Stdin.Fd()), os.Stdout)
	if _, newlineErr := fmt.Fprintln(os.Stdout); newlineErr != nil {
		return nil, fmt.Errorf("write password prompt newline: %w", newlineErr)
	}
	return value, err
}

// readMaskedPassword uses raw terminal input and restores terminal state before returning.
func readMaskedPassword(fd int, output io.Writer) (value []byte, returnErr error) {
	terminalState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("set password terminal mode: %w", err)
	}
	defer func() {
		if err := term.Restore(fd, terminalState); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("restore password terminal mode: %w", err))
		}
	}()
	state := maskedPasswordState{}
	for {
		var input [1]byte
		if _, err := os.Stdin.Read(input[:]); err != nil {
			return nil, fmt.Errorf("read password: %w", err)
		}
		feedback, complete, err := state.accept(input[0])
		if err != nil {
			return nil, err
		}
		if feedback != "" {
			if _, err := fmt.Fprint(output, feedback); err != nil {
				return nil, fmt.Errorf("write password feedback: %w", err)
			}
		}
		if complete {
			if state.overflow > 0 {
				return state.value, identity.ErrPasswordTooLongBytes
			}
			return state.value, nil
		}
	}
}

// maskedPasswordState bounds retained input while tracking terminal feedback
// separately from the exact password bytes passed to password validation.
type maskedPasswordState struct {
	value    []byte
	masked   int
	overflow int
}

func (state *maskedPasswordState) accept(input byte) (feedback string, complete bool, err error) {
	switch input {
	case '\r', '\n':
		return "", true, nil
	case 3:
		return "", false, errors.New("password input interrupted")
	case 4:
		return "", false, io.EOF
	case 8, 127:
		if state.overflow > 0 {
			state.overflow--
			return "", false, nil
		}
		if len(state.value) == 0 {
			return "", false, nil
		}
		_, size := utf8.DecodeLastRune(state.value)
		state.value = state.value[:len(state.value)-size]
		if passwordRuneCount(state.value) < state.masked {
			state.masked--
			return "\b \b", false, nil
		}
		return "", false, nil
	default:
		if len(state.value) >= 513 {
			state.overflow++
			return "", false, nil
		}
		state.value = append(state.value, input)
		count := passwordRuneCount(state.value)
		if count > state.masked {
			state.masked = count
			return "*", false, nil
		}
		return "", false, nil
	}
}

func passwordRuneCount(value []byte) int {
	count := 0
	for len(value) > 0 && utf8.FullRune(value) {
		_, size := utf8.DecodeRune(value)
		value = value[size:]
		count++
	}
	return count
}

func passwordPolicyMessage(err error) string {
	switch {
	case errors.Is(err, identity.ErrPasswordInvalidUTF8):
		return "it must be valid UTF-8"
	case errors.Is(err, identity.ErrPasswordTooLongBytes):
		return "it must be at most 512 bytes"
	case errors.Is(err, identity.ErrPasswordTooShort):
		return "it must contain at least 12 Unicode code points"
	case errors.Is(err, identity.ErrPasswordTooLong):
		return "it must contain at most 128 Unicode code points"
	case errors.Is(err, identity.ErrPasswordCommon):
		return "it is commonly used"
	default:
		return "it does not meet the password policy"
	}
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
		if err := material.EnsureInstructions(configuration.Main.DataDir); err != nil {
			return err
		}
		materialService := material.NewService(database, configuration.Main.DataDir, material.Limits{
			MaxFileBytes:     int64(configuration.Uploads.MaxFileSizeMiB) << 20,
			MaxMaterialBytes: int64(configuration.Uploads.MaxMaterialSizeMiB) << 20,
			MaxPages:         configuration.Uploads.MaxMaterialPages, MaxFiles: configuration.Uploads.MaxMaterialFiles,
			MaxImageMegapixels: configuration.Uploads.MaxImageMegapixels,
		}, mistral.New(mistral.Options{APIKey: configuration.Mistral.APIKey}), openai.New(openai.Options{
			APIKey: configuration.OpenAI.APIKey, Model: configuration.OpenAI.JobModel,
		}), nil, logger.Slog())
		if err := materialService.Reconcile(command.Context()); err != nil {
			return err
		}
		lifecycleRegistry.RegisterCourse(materialService)
		lifecycleRegistry.RegisterStudentCourse(materialService)
		lifecycleRegistry.RegisterAccount(materialService)
		oversight := jobs.NewOversight(database, func(ctx context.Context, query miSQLite.Querier, actorID string) (bool, error) {
			return user.HasRole(ctx, query, actorID, user.Administrator)
		})
		worker := jobs.New(database, logger.Slog())
		worker.Register("material-extraction", material.NewExtractionHandler(materialService))
		worker.Register("material-summary", material.NewSummaryHandler(materialService))
		if err := worker.Recover(command.Context()); err != nil {
			return err
		}
		jobs.Register(server, oversight)
		material.Register(server, materialService, oversight)
		course.Register(server, course.NewService(database, configuration.Main.DataDir, lifecycleRegistry,
			material.MaterialReady, nil,
			nil, auth.InvalidateSecurityArtifacts, logger.Slog()))
		worker.Start(command.Context())
		defer func() {
			shutdown, cancel := contextWithTimeout(command.Context(), 30*time.Second)
			defer cancel()
			returnErr = errors.Join(returnErr, worker.Stop(shutdown))
		}()
		return serve(command.Context(), configuration.HTTP.Listen, configuration.HTTP.SocketGroup, server.Echo,
			worker.BeginShutdown, logger)
	}}
}

func serve(parent context.Context, listen, socketGroup string, handler http.Handler, beginShutdown func(),
	logger *logging.Logger) (returnErr error) {
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
			if beginShutdown != nil {
				beginShutdown()
			}
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
