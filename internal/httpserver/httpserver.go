// Package httpserver provides MIA's shared Echo HTTP kernel.
package httpserver

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v5"
	"golang.org/x/sys/unix"
)

const jsonAPI = "application/vnd.api+json"

var (
	errStaticUnsafe     = errors.New("unsafe static path")
	errStaticDirectory  = errors.New("static directory")
	errStaticSymlinkOut = errors.New("static symlink escapes document root")
)

// Options configures the HTTP kernel.
type Options struct {
	DataDir           string
	DocRoot           string
	TrustedProxyCIDRs []string
	Logger            *slog.Logger
}

// Server is safe for concurrent use after construction.
type Server struct {
	Echo     *echo.Echo
	Sessions *sessions.CookieStore
}

// New builds the shared HTTP kernel. Feature routes are deliberately registered
// by their owning vertical slice, not here.
func New(options Options) (*Server, error) {
	store, err := loadSessionStore(options.DataDir)
	if err != nil {
		return nil, err
	}
	resolver, err := NewClientIPResolver(options.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	application := echo.New()
	application.HTTPErrorHandler = errorHandler
	application.Use(recoverMiddleware(logger), requestMiddleware(logger), securityHeaders, rateLimitMiddleware(NewLimiter(50_000), resolver), csrfMiddleware)
	application.GET("/*", staticHandler(options.DocRoot))
	application.HEAD("/*", staticHandler(options.DocRoot))
	return &Server{Echo: application, Sessions: store}, nil
}

func loadSessionStore(dataDir string) (*sessions.CookieStore, error) {
	path := filepath.Join(dataDir, "session.key")
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 64)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate session key: %w", err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create session key: %w", err)
		}
		if _, err := file.Write(key); err != nil {
			if closeErr := file.Close(); closeErr != nil {
				return nil, errors.Join(fmt.Errorf("write session key: %w", err), fmt.Errorf("close session key: %w", closeErr))
			}
			return nil, fmt.Errorf("write session key: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("write session key: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read session key: %w", err)
	}
	if len(key) != 64 {
		return nil, errors.New("session key must be 64 bytes")
	}
	if err := validateSessionKey(path); err != nil {
		return nil, err
	}
	return sessions.NewCookieStore(key[:32], key[32:]), nil
}

func validateSessionKey(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat session key: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("session key has unsafe type or permissions")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("session key has untrusted owner")
	}
	return nil
}

func recoverMiddleware(logger *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) (err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error("panic while serving request", "request_id", c.Response().Header().Get(echo.HeaderXRequestID))
					err = NewError(CodeInternalError)
				}
			}()
			return next(c)
		}
	}
}

func requestMiddleware(logger *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			requestID := randomToken(18)
			c.Response().Header().Set(echo.HeaderXRequestID, requestID)
			started := time.Now()
			err := next(c)
			logger.Info("http request", "request_id", requestID, "method", c.Request().Method, "path", c.Request().URL.Path, "duration_ms", time.Since(started).Milliseconds())
			return err
		}
	}
}

func securityHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		header := c.Response().Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		return next(c)
	}
}

// csrfMiddleware applies same-origin CSRF protection to unsafe requests and
// supplies a host-only token cookie for the frontend.
func csrfMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if !isAPIPath(c.Request().URL.Path) {
			return next(c)
		}
		cookie, err := c.Request().Cookie("__Host-mia_csrf")
		if errors.Is(err, http.ErrNoCookie) {
			token := randomToken(32)
			http.SetCookie(c.Response(), &http.Cookie{Name: "__Host-mia_csrf", Value: token, Path: "/", Secure: true, SameSite: http.SameSiteLaxMode})
			cookie = &http.Cookie{Value: token}
		} else if err != nil {
			return NewError(CodeCSRFInvalid)
		}
		if isSafeMethod(c.Request().Method) {
			return next(c)
		}
		if c.Request().Header.Get("Sec-Fetch-Site") == "cross-site" || cookie == nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(c.Request().Header.Get("X-CSRF-Token"))) != 1 {
			return NewError(CodeCSRFInvalid)
		}
		return next(c)
	}
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}
func randomToken(size int) string {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		panic("cryptographic randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(bytes)
}

func staticHandler(root string) echo.HandlerFunc {
	return func(c *echo.Context) error {
		name := strings.TrimPrefix(c.Request().URL.Path, "/")
		if c.Request().URL.Path == "/api" || strings.HasPrefix(name, "api/") {
			return NewError(CodeNotFound)
		}
		if name == "" {
			name = "index.html"
		}
		file, err := staticFile(root, name)
		if errors.Is(err, fs.ErrNotExist) {
			name = "index.html"
			file, err = staticFile(root, "index.html")
		}
		if err != nil {
			return NewError(CodeNotFound)
		}
		defer func() {
			if closeErr := file.Close(); closeErr != nil {
				c.Logger().Error("close static file", "error", closeErr)
			}
		}()
		info, err := file.Stat()
		if err != nil {
			return fmt.Errorf("stat static file: %w", err)
		}
		return c.Stream(http.StatusOK, mimeType(name, info), file)
	}
}

// staticFile validates a static request path and rejects symlink targets that
// would escape the configured document root.
func staticFile(root, name string) (file *os.File, returnErr error) {
	if !fs.ValidPath(name) || hasDotPath(name) {
		return nil, errStaticUnsafe
	}
	anchoredRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	resolvedTarget, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return nil, err
	}
	if resolvedTarget != anchoredRoot && !strings.HasPrefix(resolvedTarget, anchoredRoot+string(os.PathSeparator)) {
		return nil, errStaticSymlinkOut
	}
	relativeTarget, err := filepath.Rel(anchoredRoot, resolvedTarget)
	if err != nil || relativeTarget == "." || !fs.ValidPath(filepath.ToSlash(relativeTarget)) {
		return nil, errStaticUnsafe
	}
	parts := strings.Split(filepath.ToSlash(relativeTarget), "/")
	rootFD, err := unix.Open(anchoredRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	directoryFD := rootFD
	defer func() {
		var cleanupErr error
		if directoryFD != rootFD {
			if closeErr := unix.Close(directoryFD); closeErr != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close static directory descriptor: %w", closeErr))
			}
		}
		if closeErr := unix.Close(rootFD); closeErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close static root descriptor: %w", closeErr))
		}
		if cleanupErr != nil {
			if file != nil {
				if closeErr := file.Close(); closeErr != nil {
					cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close static file: %w", closeErr))
				}
				file = nil
			}
			returnErr = errors.Join(returnErr, cleanupErr)
		}
	}()
	for _, part := range parts[:len(parts)-1] {
		nextFD, err := unix.Openat(directoryFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		if directoryFD != rootFD {
			if closeErr := unix.Close(directoryFD); closeErr != nil {
				if nextCloseErr := unix.Close(nextFD); nextCloseErr != nil {
					closeErr = errors.Join(closeErr, fmt.Errorf("close next static directory descriptor: %w", nextCloseErr))
				}
				return nil, fmt.Errorf("close static directory descriptor: %w", closeErr)
			}
			directoryFD = rootFD
		}
		directoryFD = nextFD
	}
	fd, err := unix.Openat(directoryFD, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file = os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		if closeErr := file.Close(); closeErr != nil {
			return nil, errors.Join(errStaticDirectory, fmt.Errorf("close static file: %w", closeErr))
		}
		return nil, errStaticDirectory
	}
	return file, nil
}

func hasDotPath(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}
func mimeType(name string, info fs.FileInfo) string {
	if value := mime.TypeByExtension(filepath.Ext(name)); value != "" {
		return value
	}
	return "application/octet-stream"
}
