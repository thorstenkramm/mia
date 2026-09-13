// Package httpserver provides MIA's shared Echo HTTP kernel.
package httpserver

import (
	"context"
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

// ErrIdentityNotFound tells authenticated-route middleware that the cookie's account was deleted.
var ErrIdentityNotFound = errors.New("identity not found")

// Options configures the HTTP kernel.
type Options struct {
	DataDir           string
	DocRoot           string
	TrustedProxyCIDRs []string
	Logger            *slog.Logger
	CookiePolicy      CookiePolicy
}

// CookiePolicy defines the fixed cookie names and transport flag for one server.
type CookiePolicy struct {
	SessionName string
	CSRFName    string
	Secure      bool
}

// Server is safe for concurrent use after construction and route
// registration. Register every route during single-threaded startup wiring
// before the server starts handling requests.
type Server struct {
	Echo           *echo.Echo
	Sessions       *sessions.CookieStore
	identityLoader IdentityLoader
	limiter        *Limiter
	resolver       *ClientIPResolver
	routes         map[string]routeRegistration
	cookiePolicy   CookiePolicy
}

// AuthRouteRegistrar is a wiring-issued capability for auth's restricted routes.
// A Server does not expose a method to obtain one.
type AuthRouteRegistrar struct{ server *Server }

// CheckLogin enforces MIA's layered login limits using trusted client IP resolution.
func (server *Server) CheckLogin(c *echo.Context, username string, failed bool) Result {
	now := time.Now()
	ip := server.resolver.Resolve(c.Request())
	key, err := UsernameKey(username)
	if err != nil {
		key = "username:invalid"
	}
	ipResult := server.limiter.Check(LimitLoginIP, ip, now)
	userResult := server.limiter.Check(LimitLoginUsername, key, now)
	if failed {
		ipResult = server.limiter.RecordFailure(LimitLoginIP, ip, now)
		userResult = server.limiter.RecordFailure(LimitLoginUsername, key, now)
	}
	if !ipResult.Allowed && !userResult.Allowed {
		if userResult.RetryAfter > ipResult.RetryAfter {
			return userResult
		}
		return ipResult
	}
	if !ipResult.Allowed {
		return ipResult
	}
	return userResult
}

// RecordLoginFailure records the account and IP failure limits after a failed
// login attempt. Its result includes the applicable progressive delay or block.
func (server *Server) RecordLoginFailure(c *echo.Context, username string) Result {
	return server.CheckLogin(c, username, true)
}

// CheckRecoveryIP consumes the recovery source-IP limit before parsing input.
func (server *Server) CheckRecoveryIP(c *echo.Context) Result {
	return server.limiter.Check(LimitRecoveryIP, server.resolver.Resolve(c.Request()), time.Now())
}

// CheckRecoveryIdentifier consumes the recovery identifier limit after parsing.
func (server *Server) CheckRecoveryIdentifier(username string) Result {
	key, err := UsernameKey(username)
	if err != nil {
		key = "username:invalid"
	}
	return server.limiter.Check(LimitRecoveryIdentifier, key, time.Now())
}

// CheckReset consumes both fixed reset submission dimensions.
func (server *Server) CheckReset(c *echo.Context, token string) Result {
	key, err := TokenKey(token)
	if err != nil {
		key = "token:invalid"
	}
	now := time.Now()
	ip := server.limiter.Check(LimitResetIP, server.resolver.Resolve(c.Request()), now)
	challenge := server.limiter.Check(LimitResetToken, key, now)
	if !ip.Allowed {
		return ip
	}
	return challenge
}

// CheckMFA consumes MFA verification limits for both account and source IP.
func (server *Server) CheckMFA(c *echo.Context, accountID string) Result {
	key, err := AccountKey(accountID)
	if err != nil {
		key = "account:invalid"
	}
	now := time.Now()
	ip := server.limiter.Check(LimitMFAIP, server.resolver.Resolve(c.Request()), now)
	account := server.limiter.Check(LimitMFAAccount, key, now)
	if !ip.Allowed {
		return ip
	}
	return account
}

// CheckInvitationPreview consumes layered IP and token-digest limits.
func (server *Server) CheckInvitationPreview(c *echo.Context, token string) Result {
	return server.checkInvitation(c, token)
}

// CheckInvitationAccept consumes layered IP and token-digest limits.
func (server *Server) CheckInvitationAccept(c *echo.Context, token string) Result {
	return server.checkInvitation(c, token)
}

func (server *Server) checkInvitation(c *echo.Context, token string) Result {
	key, err := TokenKey(token)
	if err != nil {
		key = "token:invalid"
	}
	now := time.Now()
	ip := server.limiter.Check(LimitInvitationIP, server.resolver.Resolve(c.Request()), now)
	challenge := server.limiter.Check(LimitInvitationToken, key, now)
	if !ip.Allowed {
		return ip
	}
	return challenge
}

// IdentityState is the database-authoritative state needed to validate a browser session.
type IdentityState struct {
	SecurityGeneration         int64
	MustChangePassword, Banned bool
}

// IdentityLoader reloads account state for every protected request.
type IdentityLoader func(context.Context, string) (IdentityState, error)

// RotateCSRF invalidates the current browser CSRF token after an auth boundary.
func (server *Server) RotateCSRF(c *echo.Context) {
	server.setCSRFCookie(c, randomToken(32))
}

// SessionCookieName returns this server's immutable session cookie name.
func (server *Server) SessionCookieName() string { return server.cookiePolicy.SessionName }

// CSRFCookieName returns this server's immutable CSRF cookie name.
func (server *Server) CSRFCookieName() string { return server.cookiePolicy.CSRFName }

// PrivateAvatarPNG writes a normalized private avatar with shared download-safety headers.
func PrivateAvatarPNG(c *echo.Context, data []byte) error {
	header := c.Response().Header()
	header.Set(echo.HeaderContentType, "image/png")
	header.Set("Content-Disposition", `inline; filename="avatar.png"`)
	header.Set("X-Content-Type-Options", "nosniff")
	return c.Blob(http.StatusOK, "image/png", data)
}

// SetIdentityLoader configures the sole identity reload path for authenticated routes.
func (server *Server) SetIdentityLoader(loader IdentityLoader) { server.identityLoader = loader }

// AuthenticatedPOST registers an ordinary protected POST. Ordinary routes always require a full session.
func (server *Server) AuthenticatedPOST(path string, next echo.HandlerFunc) {
	server.AuthenticatedRoute(http.MethodPost, path, RepresentationJSONAPI, next)
}

// AuthenticatedGET registers an ordinary protected GET. Ordinary routes always require a full session.
func (server *Server) AuthenticatedGET(path string, next echo.HandlerFunc) {
	server.AuthenticatedRoute(http.MethodGet, path, RepresentationJSONAPI, next)
}

// AuthenticatedPATCH registers an ordinary protected PATCH.
func (server *Server) AuthenticatedPATCH(path string, next echo.HandlerFunc) {
	server.AuthenticatedRoute(http.MethodPatch, path, RepresentationJSONAPI, next)
}

// AuthenticatedPUT registers an ordinary protected PUT.
func (server *Server) AuthenticatedPUT(path string, next echo.HandlerFunc) {
	server.AuthenticatedRoute(http.MethodPut, path, RepresentationJSONAPI, next)
}

// AuthenticatedDELETE registers an ordinary protected DELETE. Ordinary routes always require a full session.
func (server *Server) AuthenticatedDELETE(path string, next echo.HandlerFunc) {
	server.AuthenticatedRoute(http.MethodDelete, path, RepresentationJSONAPI, next)
}

// POST registers an auth route and centrally validates its allowed login stage.
func (registrar AuthRouteRegistrar) POST(path, stage string, next echo.HandlerFunc) {
	if stage != "authenticated" && stage != "mfa" && stage != "password-change" && stage != "any" {
		panic("invalid auth route stage")
	}
	registrar.server.classify(http.MethodPost, path, stageClass(stage), RepresentationJSONAPI)
	registrar.server.authenticatedPOST(path, stage, next)
}

// DELETE registers an auth route and centrally validates its allowed login stage.
func (registrar AuthRouteRegistrar) DELETE(path, stage string, next echo.HandlerFunc) {
	if stage != "authenticated" && stage != "mfa" && stage != "password-change" && stage != "any" {
		panic("invalid auth route stage")
	}
	registrar.server.classify(http.MethodDelete, path, stageClass(stage), RepresentationJSONAPI)
	registrar.server.authenticatedDELETE(path, stage, next)
}

// StartSession creates a new login-stage cookie. It is used after successful
// login and stage transitions, each of which starts with a fresh CSRF token.
func (server *Server) StartSession(c *echo.Context, userID string, generation int64, stage string, now time.Time) error {
	return saveSession(c, server.Sessions, server.cookiePolicy, userID, generation, stage, "", now)
}

// StartMFASession creates the restricted MFA stage bound to one server-side challenge.
func (server *Server) StartMFASession(c *echo.Context, userID string, generation int64, challengeID string, now time.Time) error {
	return saveSession(c, server.Sessions, server.cookiePolicy, userID, generation, "mfa", challengeID, now)
}

// TransitionSession moves the validated current session to another stage.
func (server *Server) TransitionSession(c *echo.Context, stage string, now time.Time) error {
	userID, userOK := c.Get("mia.auth.user_id").(string)
	generation, generationOK := c.Get("mia.auth.security_generation").(int64)
	if !userOK || !generationOK || userID == "" || generation == 0 {
		return NewError(CodeUnauthenticated)
	}
	return saveSession(c, server.Sessions, server.cookiePolicy, userID, generation, stage, "", now)
}

// EndSession clears the validated current browser session.
func (server *Server) EndSession(c *echo.Context) error {
	c.Set("mia.auth.end_session", true)
	return clearSession(c, server.Sessions, server.cookiePolicy)
}

func (server *Server) authenticatedPOST(path, stage string, next echo.HandlerFunc) {
	server.authenticated(path, stage, next, server.Echo.POST)
}

func (server *Server) authenticatedDELETE(path, stage string, next echo.HandlerFunc) {
	server.authenticated(path, stage, next, server.Echo.DELETE)
}

func (server *Server) authenticated(path, stage string, next echo.HandlerFunc, register func(string, echo.HandlerFunc, ...echo.MiddlewareFunc) echo.RouteInfo) {
	register(path, func(c *echo.Context) error {
		if server.identityLoader == nil {
			return NewError(CodeInternalError)
		}
		session, err := server.Sessions.Get(c.Request(), server.cookiePolicy.SessionName)
		if err != nil {
			return server.clearedStageError(c, stage)
		}
		userID, userOK := session.Values["user_id"].(string)
		current, stageOK := session.Values["stage"].(string)
		generation, generationOK := session.Values["security_generation"].(int64)
		absolute, absoluteOK := session.Values["expires_at"].(int64)
		idle, idleOK := session.Values["idle_until"].(int64)
		if !userOK || !stageOK || !generationOK || !absoluteOK || !idleOK || time.Now().After(time.Unix(absolute, 0)) || time.Now().After(time.Unix(idle, 0)) {
			return server.clearedStageError(c, stage)
		}
		state, err := server.identityLoader(c.Request().Context(), userID)
		if err != nil {
			if errors.Is(err, ErrIdentityNotFound) {
				return server.clearedStageError(c, stage)
			}
			return err
		}
		if state.Banned || state.SecurityGeneration != generation {
			return server.clearedStageError(c, stage)
		}
		if current == "password-change" && !state.MustChangePassword {
			if err := clearSession(c, server.Sessions, server.cookiePolicy); err != nil {
				return err
			}
			return NewError(CodePasswordChangeRequired)
		}
		// MFA always precedes password replacement. A password gate therefore must
		// not block the challenge-bound MFA actions that advance to that gate.
		if state.MustChangePassword && stage != "password-change" && stage != "any" && (stage != "mfa" || current != "mfa") {
			return NewError(CodePasswordChangeRequired)
		}
		if stage != "any" && current != stage && (stage != "password-change" || !state.MustChangePassword || current != "authenticated") {
			return NewError(CodePasswordChangeRequired)
		}
		c.Set("mia.auth.user_id", userID)
		c.Set("mia.auth.security_generation", generation)
		c.Set("mia.auth.stage", current)
		if challengeID, ok := session.Values["mfa_challenge_id"].(string); ok {
			c.Set("mia.auth.mfa_challenge_id", challengeID)
		}
		response, unwrapErr := echo.UnwrapResponse(c.Response())
		if unwrapErr != nil {
			return fmt.Errorf("unwrap authenticated response: %w", unwrapErr)
		}
		response.Before(func() {
			if current == "authenticated" && stage == "authenticated" && c.Get("mia.auth.end_session") != true && response.Status >= http.StatusOK && response.Status < http.StatusMultipleChoices {
				if err := refreshSession(c, server.Sessions, server.cookiePolicy, session, time.Now()); err != nil {
					c.Logger().Error("refresh authenticated session", "error", err)
				}
			}
		})
		return next(c)
	})
}

func (server *Server) clearedStageError(c *echo.Context, stage string) error {
	if err := clearSession(c, server.Sessions, server.cookiePolicy); err != nil {
		return err
	}
	if stage == "password-change" {
		return NewError(CodePasswordChangeRequired)
	}
	return NewError(CodeUnauthenticated)
}

func saveSession(c *echo.Context, store *sessions.CookieStore, policy CookiePolicy, userID string, generation int64, stage, challengeID string, now time.Time) error {
	session, err := store.New(c.Request(), policy.SessionName)
	if err != nil {
		return err
	}
	expires := now.Add(12 * time.Hour)
	if stage != "authenticated" {
		expires = now.Add(30 * time.Minute)
	}
	session.Values["user_id"] = userID
	session.Values["stage"] = stage
	session.Values["security_generation"] = generation
	if challengeID != "" {
		session.Values["mfa_challenge_id"] = challengeID
	}
	session.Values["expires_at"] = expires.Unix()
	idleUntil := now.Add(30 * time.Minute)
	if idleUntil.After(expires) {
		idleUntil = expires
	}
	session.Values["idle_until"] = idleUntil.Unix()
	session.Options = &sessions.Options{Path: "/", MaxAge: int(time.Until(expires).Seconds()), Secure: policy.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	return store.Save(c.Request(), c.Response(), session)
}

func refreshSession(c *echo.Context, store *sessions.CookieStore, policy CookiePolicy, session *sessions.Session, now time.Time) error {
	absolute, ok := session.Values["expires_at"].(int64)
	if !ok || !now.Before(time.Unix(absolute, 0)) {
		return errors.New("authenticated session has invalid absolute expiry")
	}
	idleUntil := now.Add(30 * time.Minute)
	absoluteExpiry := time.Unix(absolute, 0)
	if idleUntil.After(absoluteExpiry) {
		idleUntil = absoluteExpiry
	}
	session.Values["idle_until"] = idleUntil.Unix()
	session.Options = &sessions.Options{Path: "/", MaxAge: int(time.Unix(absolute, 0).Sub(now).Seconds()), Secure: policy.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	return store.Save(c.Request(), c.Response(), session)
}

func clearSession(c *echo.Context, store *sessions.CookieStore, policy CookiePolicy) error {
	session, err := store.Get(c.Request(), policy.SessionName)
	if err != nil {
		session = sessions.NewSession(store, policy.SessionName)
	}
	session.Options = &sessions.Options{Path: "/", MaxAge: -1, Secure: policy.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	if err := store.Save(c.Request(), c.Response(), session); err != nil {
		return fmt.Errorf("clear auth session: %w", err)
	}
	return nil
}

// New builds the shared HTTP kernel. Feature routes are deliberately registered
// by their owning vertical slice, not here.
func New(options Options) (*Server, AuthRouteRegistrar, error) {
	store, err := loadSessionStore(options.DataDir)
	if err != nil {
		return nil, AuthRouteRegistrar{}, err
	}
	resolver, err := NewClientIPResolver(options.TrustedProxyCIDRs)
	if err != nil {
		return nil, AuthRouteRegistrar{}, err
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	application := echo.New()
	// Route c.Logger() to the configured logger so error records reach the
	// operator's log file, level, and format instead of Echo's own default.
	application.Logger = logger
	application.HTTPErrorHandler = errorHandler
	application.GET("/*", staticHandler(options.DocRoot))
	application.HEAD("/*", staticHandler(options.DocRoot))
	limiter := NewLimiter(50_000)
	policy, err := resolveCookiePolicy(options.CookiePolicy)
	if err != nil {
		return nil, AuthRouteRegistrar{}, err
	}
	server := &Server{Echo: application, Sessions: store, limiter: limiter, resolver: resolver, routes: make(map[string]routeRegistration), cookiePolicy: policy}
	application.Use(apiResponsePolicy, recoverMiddleware(logger), requestMiddleware(logger), securityHeaders,
		server.rateLimitMiddleware, server.csrfMiddleware, server.acceptMiddleware)
	return server, AuthRouteRegistrar{server: server}, nil
}

// apiResponseWriter enforces the protected-response policy at the transport
// boundary, after Echo has run every Before callback.
type apiResponseWriter struct {
	http.ResponseWriter
}

func (writer *apiResponseWriter) WriteHeader(statusCode int) {
	writer.protect()
	writer.ResponseWriter.WriteHeader(statusCode)
}

func (writer *apiResponseWriter) Write(body []byte) (int, error) {
	writer.protect()
	return writer.ResponseWriter.Write(body)
}

func (writer *apiResponseWriter) FlushError() error {
	writer.protect()
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *apiResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *apiResponseWriter) protect() {
	writer.Header().Set(echo.HeaderCacheControl, "no-store")
}

// apiResponsePolicy installs the protected-response writer before any API
// handler or error mapper can commit headers.
func apiResponsePolicy(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if !isAPIPath(c.Request().URL.Path) {
			return next(c)
		}
		response, err := echo.UnwrapResponse(c.Response())
		if err != nil {
			return fmt.Errorf("unwrap API response: %w", err)
		}
		response.Header().Set(echo.HeaderCacheControl, "no-store")
		response.ResponseWriter = &apiResponseWriter{ResponseWriter: response.ResponseWriter}
		return next(c)
	}
}

func resolveCookiePolicy(policy CookiePolicy) (CookiePolicy, error) {
	production := CookiePolicy{SessionName: "__Host-mia_session", CSRFName: "__Host-mia_csrf", Secure: true}
	local := CookiePolicy{SessionName: "mia_session", CSRFName: "mia_csrf"}
	if policy == (CookiePolicy{}) || policy == production {
		return production, nil
	}
	if policy == local {
		return local, nil
	}
	return CookiePolicy{}, errors.New("invalid cookie policy")
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
func (server *Server) csrfMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if !isAPIPath(c.Request().URL.Path) {
			return next(c)
		}
		var cookie *http.Cookie
		for _, candidate := range c.Request().Cookies() {
			if candidate.Name != server.cookiePolicy.CSRFName {
				continue
			}
			if cookie != nil {
				return NewError(CodeCSRFInvalid)
			}
			cookie = candidate
		}
		if cookie == nil {
			token := randomToken(32)
			server.setCSRFCookie(c, token)
			cookie = &http.Cookie{Value: token}
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

func (server *Server) setCSRFCookie(c *echo.Context, token string) {
	http.SetCookie(c.Response(), &http.Cookie{Name: server.cookiePolicy.CSRFName, Value: token, Path: "/", Secure: server.cookiePolicy.Secure, SameSite: http.SameSiteLaxMode})
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
