// Package auth implements password login and browser-session stages.
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/provider/sms"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

const cookieName = "__Host-mia_session"

type recoveryMailer interface {
	SendPasswordRecovery(context.Context, string, string) error
}

// Register attaches auth routes using the configured recovery-mail adapter.
func Register(server *httpserver.Server, routes httpserver.AuthRouteRegistrar, database *sql.DB, publicURL string, deliveries *DeliveryManager, smsSender sms.Sender) {
	server.Echo.POST("/api/v1/auth/login", login(server, database, smsSender))
	server.Echo.POST("/api/v1/auth/password-recovery-requests", passwordRecoveryRequest(server, database, publicURL, deliveries))
	server.Echo.POST("/api/v1/auth/password-resets", passwordReset(server, database))
	routes.POST("/api/v1/auth/logout", "any", logout(server, database))
	routes.POST("/api/v1/auth/password-changes", "password-change", changePassword(server, database))
	routes.POST("/api/v1/auth/mfa-challenges/:id/verifications", "mfa", verifyChallenge(server, database))
	routes.POST("/api/v1/auth/mfa-challenges/:id/recovery-code-consumptions", "mfa", consumeChallengeRecoveryCode(server, database))
	routes.POST("/api/v1/auth/mfa-challenges/:id/resends", "mfa", resendChallenge(server, database, smsSender))
	routes.POST("/api/v1/users/me/mfa-enrollments", "authenticated", startEnrollment(server, database, publicURL, smsSender))
	routes.POST("/api/v1/users/me/mfa-enrollments/:id/verifications", "authenticated", verifyEnrollment(server, database))
	routes.POST("/api/v1/users/me/mfa-enrollments/:id/resends", "authenticated", resendEnrollment(server, database, smsSender))
	routes.DELETE("/api/v1/users/me/mfa-enrollments/:id", "authenticated", cancelOrDisableMFA(server, database))
	routes.POST("/api/v1/auth/mfa-management-proofs", "authenticated", createManagementProof(server, database))
}

type credentials struct {
	Data struct {
		Type       string `json:"type"`
		Attributes struct {
			Username *string `json:"username"`
			Password *string `json:"password"`
		} `json:"attributes"`
	} `json:"data"`
}

func login(server *httpserver.Server, database *sql.DB, smsSender sms.Sender) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var request credentials
		if err := decode(c, &request); err != nil {
			return loginDecodeError(c, server, database, err)
		}
		if request.Data.Type != "login-attempts" || request.Data.Attributes.Username == nil || request.Data.Attributes.Password == nil || *request.Data.Attributes.Username == "" {
			return loginFailure(c, server, database, "", "", httpserver.CodeInvalidRequest)
		}
		if result := server.CheckLogin(c, *request.Data.Attributes.Username, false); !result.Allowed {
			return loginThrottled(c, database, result)
		}
		account, err := user.FindForLogin(c.Request().Context(), database, *request.Data.Attributes.Username)
		if errors.Is(err, sql.ErrNoRows) || account.Banned {
			identity.DummyPasswordWork(*request.Data.Attributes.Password)
			return loginFailure(c, server, database, *request.Data.Attributes.Username, account.ID, httpserver.CodeInvalidCredentials)
		}
		if errors.Is(err, identity.ErrInvalidUsername) {
			identity.DummyPasswordWork(*request.Data.Attributes.Password)
			return loginFailure(c, server, database, *request.Data.Attributes.Username, "", httpserver.CodeInvalidCredentials)
		}
		if err != nil {
			return err
		}
		matches, err := identity.VerifyPassword(*request.Data.Attributes.Password, account.PasswordHash)
		if err != nil {
			return err
		}
		if !matches {
			return loginFailure(c, server, database, *request.Data.Attributes.Username, account.ID, httpserver.CodeInvalidCredentials)
		}
		var method, destination string
		err = database.QueryRowContext(c.Request().Context(), "SELECT method, COALESCE(sms_destination, '') FROM mfa_factors WHERE user_id = ?", account.ID).Scan(&method, &destination)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load MFA factor: %w", err)
		}
		stage := "authenticated"
		challengeID := ""
		if err == nil {
			challengeID = "mfc_" + uuid.NewString()
			var smsCode string
			if method == "sms" {
				if !sms.Available(smsSender) {
					return httpserver.NewError(httpserver.CodeMFAUnavailable)
				}
				smsCode, err = newSMSCode()
				if err != nil {
					return err
				}
			}
			if err = miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
				if method == "sms" {
					if _, reserveErr := sms.Reserve(c.Request().Context(), tx, account.ID, destination, time.Now()); reserveErr != nil {
						return reserveErr
					}
				}
				_, err := tx.ExecContext(c.Request().Context(), "INSERT INTO mfa_challenges (id, user_id, method, sms_code, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)", challengeID, account.ID, method, nullable(smsCode), instant(time.Now().Add(30*time.Minute)), instant(time.Now()))
				if err != nil {
					return err
				}
				return audit.Write(c.Request().Context(), tx, audit.ActionAuthMFAChallengeCreated, account.ID, account.ID)
			}); err != nil {
				if errors.Is(err, sms.ErrUnavailable) {
					return httpserver.NewError(httpserver.CodeMFAUnavailable)
				}
				if errors.Is(err, sms.ErrRateLimited) {
					return httpserver.NewError(httpserver.CodeRateLimited)
				}
				return fmt.Errorf("create MFA challenge: %w", err)
			}
			if method == "sms" {
				if err := smsSender.Send(c.Request().Context(), destination, smsCode); err != nil {
					return mfaDeliveryFailure(c.Request().Context(), database, account.ID)
				}
			}
			stage = "mfa"
		} else if account.MustChangePassword {
			stage = "password-change"
		}
		if err := miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			return audit.Write(c.Request().Context(), tx, audit.ActionAuthSessionLoggedIn, account.ID, account.ID)
		}); err != nil {
			return err
		}
		if stage == "mfa" {
			err = server.StartMFASession(c, account.ID, account.SecurityGeneration, challengeID, time.Now())
		} else {
			err = server.StartSession(c, account.ID, account.SecurityGeneration, stage, time.Now())
		}
		if err != nil {
			return err
		}
		httpserver.RotateCSRF(c)
		return sessionResponseWithChallenge(c, account.ID, stage, challengeID)
	}
}

func loginFailure(c *echo.Context, server *httpserver.Server, database *sql.DB, username, subjectID string, code httpserver.Code) error {
	result := server.RecordLoginFailure(c, username)
	if err := miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
		action := audit.ActionAuthSessionFailed
		if !result.Allowed || result.Delay > 0 {
			action = audit.ActionAuthSessionThrottled
		}
		return audit.Write(c.Request().Context(), tx, action, "", subjectID)
	}); err != nil {
		return err
	}
	if !result.Allowed || result.Delay > 0 {
		return throttled(c, result)
	}
	return httpserver.NewError(code)
}

func loginThrottled(c *echo.Context, database *sql.DB, result httpserver.Result) error {
	if err := miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
		return audit.Write(c.Request().Context(), tx, audit.ActionAuthSessionThrottled, "", "")
	}); err != nil {
		return err
	}
	return throttled(c, result)
}

func throttled(c *echo.Context, result httpserver.Result) error {
	if result.RetryAfter > 0 {
		c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(result.RetryAfter.Seconds()+.999))))
	} else if result.Delay > 0 {
		c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(result.Delay.Seconds()+.999))))
	}
	return httpserver.NewError(httpserver.CodeLoginThrottled)
}

func logout(server *httpserver.Server, database *sql.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, ok := c.Get("mia.auth.user_id").(string)
		if !ok || accountID == "" {
			return httpserver.NewError(httpserver.CodeUnauthenticated)
		}
		if err := miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			return audit.Write(c.Request().Context(), tx, audit.ActionAuthSessionLoggedOut, accountID, accountID)
		}); err != nil {
			return err
		}
		if err := server.EndSession(c); err != nil {
			return err
		}
		httpserver.RotateCSRF(c)
		return c.NoContent(http.StatusNoContent)
	}
}

func changePassword(server *httpserver.Server, database *sql.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, ok := c.Get("mia.auth.user_id").(string)
		if !ok || accountID == "" {
			return httpserver.NewError(httpserver.CodeUnauthenticated)
		}
		var request struct {
			Data struct {
				Type       string `json:"type"`
				Attributes struct {
					Password     string `json:"password"`
					Confirmation string `json:"password_confirmation"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := decode(c, &request); err != nil || request.Data.Type != "password-changes" || request.Data.Attributes.Password != request.Data.Attributes.Confirmation {
			if err != nil {
				return decodeErrorCode(err)
			}
			return httpserver.NewError(httpserver.CodeInvalidPassword)
		}
		hash, err := identity.Password(request.Data.Attributes.Password)
		if err != nil {
			return httpserver.NewError(httpserver.CodeInvalidPassword)
		}
		if err := miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			if err := user.ChangePassword(c.Request().Context(), tx, accountID, hash); err != nil {
				return err
			}
			if err := invalidateMFAArtifacts(c.Request().Context(), tx, accountID); err != nil {
				return err
			}
			return audit.Write(c.Request().Context(), tx, audit.ActionAuthPasswordChanged, accountID, accountID)
		}); err != nil {
			return err
		}
		if err := server.TransitionSession(c, "authenticated", time.Now()); err != nil {
			return err
		}
		httpserver.RotateCSRF(c)
		return sessionResponse(c, accountID, "authenticated")
	}
}

func passwordRecoveryRequest(server *httpserver.Server, database *sql.DB, publicURL string, deliveries *DeliveryManager) echo.HandlerFunc {
	return func(c *echo.Context) error {
		limit := server.CheckRecoveryIP(c)
		if limit.Transitioned {
			if err := writeAudit(c.Request().Context(), database, audit.ActionAuthPasswordRecoveryThrottled, ""); err != nil {
				return err
			}
		}
		if !limit.Allowed {
			return c.NoContent(http.StatusNoContent)
		}
		var request struct {
			Data struct {
				Type       string `json:"type"`
				Attributes struct {
					Username string `json:"username"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := decode(c, &request); err != nil {
			return decodeErrorCode(err)
		}
		if request.Data.Type != "password-recovery-requests" {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		limit = server.CheckRecoveryIdentifier(request.Data.Attributes.Username)
		if limit.Transitioned {
			if err := writeAudit(c.Request().Context(), database, audit.ActionAuthPasswordRecoveryThrottled, ""); err != nil {
				return err
			}
		}
		if !limit.Allowed {
			return c.NoContent(http.StatusNoContent)
		}
		account, err := user.FindStaffForRecovery(c.Request().Context(), database, request.Data.Attributes.Username)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) || errors.Is(err, identity.ErrInvalidUsername) {
				return c.NoContent(http.StatusNoContent)
			}
			return err
		}
		token := uuid.NewString()
		digest := sha256.Sum256([]byte(token))
		challengeID := "prc_" + uuid.NewString()
		now := time.Now().UTC()
		if err := miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(c.Request().Context(), `INSERT INTO password_reset_challenges
				(id, user_id, token_digest, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`, challengeID, account.ID,
				digest[:], instant(now.Add(30*time.Minute)), instant(now))
			if err != nil {
				return fmt.Errorf("create password reset challenge: %w", err)
			}
			return audit.Write(c.Request().Context(), tx, audit.ActionAuthPasswordRecoveryRequested, "", account.ID)
		}); err != nil {
			return err
		}
		// Admission fails only during shutdown. The undelivered challenge expires
		// on its own and the response stays indistinguishable from delivery.
		deliveries.Admit(c.Request().Context(), account.ID, account.Email, publicURL+"/password-reset#token="+token, challengeID)
		return c.NoContent(http.StatusNoContent)
	}
}

func passwordReset(server *httpserver.Server, database *sql.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var request struct {
			Data struct {
				Type       string `json:"type"`
				Attributes struct {
					Token        string `json:"token"`
					Password     string `json:"password"`
					Confirmation string `json:"password_confirmation"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := decode(c, &request); err != nil {
			return decodeErrorCode(err)
		}
		if request.Data.Type != "password-resets" {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		if limit := server.CheckReset(c, request.Data.Attributes.Token); limit.Transitioned || !limit.Allowed {
			if limit.Transitioned {
				if err := writeAudit(c.Request().Context(), database, audit.ActionAuthPasswordResetThrottled, ""); err != nil {
					return err
				}
			}
			if !limit.Allowed {
				return httpserver.NewError(httpserver.CodeInvalidResetToken)
			}
		}
		parsed, err := uuid.Parse(request.Data.Attributes.Token)
		if err != nil || parsed.Version() != 4 || parsed.String() != request.Data.Attributes.Token {
			return httpserver.NewError(httpserver.CodeInvalidResetToken)
		}
		digest := sha256.Sum256([]byte(request.Data.Attributes.Token))
		var challengeID, accountID, expires string
		var consumed any
		err = database.QueryRowContext(c.Request().Context(), `SELECT id, user_id, expires_at, consumed_at
			FROM password_reset_challenges WHERE token_digest = ?`, digest[:]).Scan(&challengeID, &accountID, &expires, &consumed)
		if errors.Is(err, sql.ErrNoRows) {
			return invalidResetToken(c, database)
		}
		if err != nil {
			return fmt.Errorf("load password reset challenge: %w", err)
		}
		live, err := beforeExpiry(expires)
		if err != nil {
			return err
		}
		if consumed != nil || !live {
			return invalidResetToken(c, database)
		}
		eligible, err := user.IsEligibleForRecovery(c.Request().Context(), database, accountID)
		if err != nil {
			return err
		}
		if !eligible {
			return invalidResetToken(c, database)
		}
		var hash string
		if request.Data.Attributes.Password == request.Data.Attributes.Confirmation {
			hash, err = identity.Password(request.Data.Attributes.Password)
		}
		if err != nil || request.Data.Attributes.Password != request.Data.Attributes.Confirmation {
			if auditErr := writeAudit(c.Request().Context(), database, audit.ActionAuthPasswordResetFailed, ""); auditErr != nil {
				return auditErr
			}
			return httpserver.NewError(httpserver.CodeInvalidPassword)
		}
		err = miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			result, err := tx.ExecContext(c.Request().Context(), "UPDATE password_reset_challenges SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL AND expires_at > ?", instant(time.Now()), challengeID, instant(time.Now()))
			if err != nil {
				return fmt.Errorf("consume password reset challenge: %w", err)
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("count consumed password reset challenge: %w", err)
			}
			if rows != 1 {
				return errInvalidResetToken
			}
			eligible, err := user.IsEligibleForRecovery(c.Request().Context(), tx, accountID)
			if err != nil {
				return err
			}
			if !eligible {
				return errInvalidResetToken
			}
			if err := user.ChangePassword(c.Request().Context(), tx, accountID, hash); err != nil {
				return err
			}
			if err := invalidateMFAArtifacts(c.Request().Context(), tx, accountID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(c.Request().Context(), "UPDATE password_reset_challenges SET consumed_at = ? WHERE user_id = ? AND consumed_at IS NULL", instant(time.Now()), accountID); err != nil {
				return fmt.Errorf("invalidate password reset challenges: %w", err)
			}
			return audit.Write(c.Request().Context(), tx, audit.ActionAuthPasswordReset, accountID, accountID)
		})
		if errors.Is(err, errInvalidResetToken) {
			if auditErr := writeAudit(c.Request().Context(), database, audit.ActionAuthPasswordResetFailed, ""); auditErr != nil {
				return auditErr
			}
			return httpserver.NewError(httpserver.CodeInvalidResetToken)
		}
		if err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

var errInvalidResetToken = errors.New("invalid password reset token")

func writeAudit(ctx context.Context, database *sql.DB, action audit.Action, subjectID string) error {
	return miSQLite.WithTx(ctx, database, func(tx *sql.Tx) error { return audit.Write(ctx, tx, action, "", subjectID) })
}

func mfaDeliveryFailure(ctx context.Context, database *sql.DB, accountID string) error {
	if err := writeAudit(ctx, database, audit.ActionAuthMFADeliveryFailed, accountID); err != nil {
		return err
	}
	return httpserver.NewError(httpserver.CodeMFAUnavailable)
}

func beforeExpiry(value string) (bool, error) {
	expires, err := time.Parse("2006-01-02T15:04:05.000000Z", value)
	if err != nil {
		return false, fmt.Errorf("parse password reset challenge expiry: %w", err)
	}
	return time.Now().Before(expires), nil
}

func invalidResetToken(c *echo.Context, database *sql.DB) error {
	if err := writeAudit(c.Request().Context(), database, audit.ActionAuthPasswordResetFailed, ""); err != nil {
		return err
	}
	return httpserver.NewError(httpserver.CodeInvalidResetToken)
}

func instant(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000Z") }

func decode(c *echo.Context, destination any) error {
	const maximumRequestBody = 1 << 20
	mediaType, parameters, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil || mediaType != "application/vnd.api+json" || len(parameters) != 0 {
		return errUnsupportedMediaType
	}
	if c.Request().ContentLength > maximumRequestBody {
		return errRequestTooLarge
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maximumRequestBody)
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errRequestTooLarge
		}
		return err
	}
	if !validJSONUnicode(body) {
		return errors.New("invalid JSON Unicode")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errRequestTooLarge
		}
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON input")
	}
	return nil
}

// validJSONUnicode rejects malformed UTF-8 and unpaired UTF-16 surrogate
// escapes before encoding/json can replace them with U+FFFD.
func validJSONUnicode(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	inString := false
	for index := 0; index < len(body); index++ {
		if !inString {
			if body[index] == '"' {
				inString = true
			}
			continue
		}
		if body[index] == '"' {
			inString = false
			continue
		}
		if body[index] != '\\' {
			continue
		}
		index++
		if index >= len(body) {
			return false
		}
		if body[index] != 'u' {
			continue
		}
		if index+4 >= len(body) {
			return false
		}
		value, ok := unicodeEscape(body[index+1 : index+5])
		if !ok {
			return false
		}
		index += 4
		if value >= 0xD800 && value <= 0xDBFF {
			if index+6 >= len(body) || body[index+1] != '\\' || body[index+2] != 'u' {
				return false
			}
			low, ok := unicodeEscape(body[index+3 : index+7])
			if !ok || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			index += 6
		} else if value >= 0xDC00 && value <= 0xDFFF {
			return false
		}
	}
	return !inString
}

func unicodeEscape(value []byte) (rune, bool) {
	if len(value) != 4 {
		return 0, false
	}
	var result rune
	for _, character := range value {
		result <<= 4
		switch {
		case character >= '0' && character <= '9':
			result += rune(character - '0')
		case character >= 'a' && character <= 'f':
			result += rune(character-'a') + 10
		case character >= 'A' && character <= 'F':
			result += rune(character-'A') + 10
		default:
			return 0, false
		}
	}
	return result, true
}

var (
	errRequestTooLarge      = errors.New("auth request body too large")
	errUnsupportedMediaType = errors.New("unsupported auth request media type")
)

func loginDecodeError(c *echo.Context, server *httpserver.Server, database *sql.DB, err error) error {
	if errors.Is(err, errRequestTooLarge) || errors.Is(err, errUnsupportedMediaType) {
		return decodeErrorCode(err)
	}
	return loginFailure(c, server, database, "", "", httpserver.CodeInvalidRequest)
}

func decodeErrorCode(err error) error {
	if errors.Is(err, errRequestTooLarge) {
		return httpserver.NewError(httpserver.CodeRequestTooLarge)
	}
	if errors.Is(err, errUnsupportedMediaType) {
		return httpserver.NewError(httpserver.CodeUnsupportedMediaType)
	}
	return httpserver.NewError(httpserver.CodeInvalidRequest)
}

func sessionResponse(c *echo.Context, id, stage string) error {
	return sessionResponseWithChallenge(c, id, stage, "")
}

func sessionResponseWithChallenge(c *echo.Context, id, stage, challengeID string) error {
	c.Response().Header().Set(echo.HeaderContentType, "application/vnd.api+json")
	attributes := map[string]string{"stage": stage}
	if challengeID != "" {
		attributes["mfa_challenge_id"] = challengeID
	}
	return c.JSON(http.StatusOK, map[string]any{"data": map[string]any{"type": "auth-sessions", "id": id, "attributes": attributes}})
}

func authenticatedUser(c *echo.Context) (string, error) {
	id, ok := c.Get("mia.auth.user_id").(string)
	if !ok || id == "" {
		return "", httpserver.NewError(httpserver.CodeUnauthenticated)
	}
	return id, nil
}

func verifyChallenge(server *httpserver.Server, database *sql.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		if !mfaChallengeMatches(c) {
			return httpserver.NewError(httpserver.CodeMFAChallengeExpired)
		}
		if limit := server.CheckMFA(c, accountID); !limit.Allowed {
			return mfaThrottled(c, database, accountID, audit.ActionAuthMFAChallengeThrottled, limit)
		}
		var request mfaCodeRequest
		if err := decode(c, &request); err != nil {
			return decodeErrorCode(err)
		}
		if request.Data.Type != "mfa-verifications" {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		code, transition, err := verifyLiveChallenge(c.Request().Context(), database, accountID, c.Param("id"), request.Data.Attributes.Code)
		if err != nil {
			return err
		}
		if code != "" {
			return httpserver.NewError(code)
		}
		if err := server.TransitionSession(c, transition, time.Now()); err != nil {
			return err
		}
		httpserver.RotateCSRF(c)
		return sessionResponse(c, accountID, transition)
	}
}

func consumeChallengeRecoveryCode(server *httpserver.Server, database *sql.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		if !mfaChallengeMatches(c) {
			return httpserver.NewError(httpserver.CodeMFAChallengeExpired)
		}
		if limit := server.CheckMFA(c, accountID); !limit.Allowed {
			return mfaThrottled(c, database, accountID, audit.ActionAuthMFAChallengeThrottled, limit)
		}
		var request mfaCodeRequest
		if err := decode(c, &request); err != nil {
			return decodeErrorCode(err)
		}
		if request.Data.Type != "mfa-recovery-code-consumptions" {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		digest, err := recoveryDigest(request.Data.Attributes.Code)
		if err != nil {
			if auditErr := miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
				return recordChallengeFailure(c.Request().Context(), tx, accountID, c.Param("id"), audit.ActionAuthMFAChallengeFailed)
			}); auditErr != nil && !errors.Is(auditErr, errChallengeExpired) {
				return auditErr
			}
			return httpserver.NewError(httpserver.CodeInvalidRecoveryCode)
		}
		transition := "authenticated"
		resultCode := httpserver.Code("")
		err = miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			profile, err := user.LoadMFAProfile(c.Request().Context(), tx, accountID)
			if err != nil {
				return err
			}
			result, err := tx.ExecContext(c.Request().Context(), "UPDATE mfa_recovery_codes SET consumed_at = ? WHERE user_id = ? AND digest = ? AND consumed_at IS NULL", instant(time.Now()), accountID, digest[:])
			if err != nil {
				return err
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if rows != 1 {
				resultCode = httpserver.CodeInvalidRecoveryCode
				return recordChallengeFailure(c.Request().Context(), tx, accountID, c.Param("id"), audit.ActionAuthMFAChallengeFailed)
			}
			result, err = tx.ExecContext(c.Request().Context(), "UPDATE mfa_challenges SET consumed_at = ? WHERE id = ? AND user_id = ? AND consumed_at IS NULL AND expires_at > ?", instant(time.Now()), c.Param("id"), accountID, instant(time.Now()))
			if err != nil {
				return err
			}
			rows, err = result.RowsAffected()
			if err != nil {
				return err
			}
			if rows != 1 {
				return errChallengeExpired
			}
			if profile.MustChangePassword {
				transition = "password-change"
			}
			return audit.Write(c.Request().Context(), tx, audit.ActionAuthMFARecoveryCodeUsed, accountID, accountID)
		})
		if errors.Is(err, errChallengeExpired) {
			return httpserver.NewError(httpserver.CodeMFAChallengeExpired)
		}
		if err != nil {
			return err
		}
		if resultCode != "" {
			return httpserver.NewError(resultCode)
		}
		if err := server.TransitionSession(c, transition, time.Now()); err != nil {
			return err
		}
		httpserver.RotateCSRF(c)
		return sessionResponse(c, accountID, transition)
	}
}

func resendChallenge(_ *httpserver.Server, database *sql.DB, sender sms.Sender) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if !sms.Available(sender) {
			return httpserver.NewError(httpserver.CodeMFAUnavailable)
		}
		accountID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		if !mfaChallengeMatches(c) {
			return httpserver.NewError(httpserver.CodeMFAChallengeExpired)
		}
		var destination, code string
		err = miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			var method, expiry string
			if err := tx.QueryRowContext(c.Request().Context(), `SELECT method, sms_code, expires_at FROM mfa_challenges
				WHERE id = ? AND user_id = ? AND consumed_at IS NULL`, c.Param("id"), accountID).Scan(&method, &code, &expiry); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return errChallengeExpired
				}
				return err
			}
			live, err := beforeExpiry(expiry)
			if err != nil || !live {
				return errChallengeExpired
			}
			if method != "sms" {
				return errInvalidMFAValue
			}
			if err := tx.QueryRowContext(c.Request().Context(), "SELECT sms_destination FROM mfa_factors WHERE user_id = ? AND method = 'sms'", accountID).Scan(&destination); err != nil {
				return err
			}
			_, err = sms.Reserve(c.Request().Context(), tx, accountID, destination, time.Now())
			return err
		})
		if errors.Is(err, sms.ErrUnavailable) {
			return httpserver.NewError(httpserver.CodeMFAUnavailable)
		}
		if errors.Is(err, sms.ErrRateLimited) {
			return httpserver.NewError(httpserver.CodeRateLimited)
		}
		if errors.Is(err, errChallengeExpired) {
			return httpserver.NewError(httpserver.CodeMFAChallengeExpired)
		}
		if errors.Is(err, errInvalidMFAValue) {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		if err != nil {
			return err
		}
		if err := sender.Send(c.Request().Context(), destination, code); err != nil {
			return mfaDeliveryFailure(c.Request().Context(), database, accountID)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

type mfaCodeRequest struct {
	Data struct {
		Type       string `json:"type"`
		Attributes struct {
			Code string `json:"code"`
		} `json:"attributes"`
	} `json:"data"`
}

var (
	errChallengeExpired = errors.New("MFA challenge expired")
)

func mfaChallengeMatches(c *echo.Context) bool {
	id, ok := c.Get("mia.auth.mfa_challenge_id").(string)
	return ok && id != "" && id == c.Param("id")
}

func verifyLiveChallenge(ctx context.Context, database *sql.DB, accountID, challengeID, submitted string) (httpserver.Code, string, error) {
	transition := "authenticated"
	resultCode := httpserver.Code("")
	err := miSQLite.WithTx(ctx, database, func(tx *sql.Tx) error {
		var method, expiry, smsCode string
		err := tx.QueryRowContext(ctx, "SELECT method, COALESCE(sms_code, ''), expires_at FROM mfa_challenges WHERE id = ? AND user_id = ? AND consumed_at IS NULL", challengeID, accountID).Scan(&method, &smsCode, &expiry)
		if errors.Is(err, sql.ErrNoRows) {
			return errChallengeExpired
		}
		if err != nil {
			return err
		}
		live, err := beforeExpiry(expiry)
		if err != nil {
			return err
		}
		if !live {
			return errChallengeExpired
		}
		var step int64
		valid := false
		var secret []byte
		var last sql.NullInt64
		if method == "totp" {
			if err := tx.QueryRowContext(ctx, "SELECT totp_secret, last_totp_step FROM mfa_factors WHERE user_id = ? AND method = 'totp'", accountID).Scan(&secret, &last); err != nil {
				return err
			}
			step, valid = verifyTOTP(secret, submitted, time.Now())
		} else {
			valid = len(submitted) == 6 && subtle.ConstantTimeCompare([]byte(smsCode), []byte(submitted)) == 1
		}
		if !valid {
			resultCode = httpserver.CodeInvalidMFACode
			return recordChallengeFailure(ctx, tx, accountID, challengeID, audit.ActionAuthMFAChallengeFailed)
		}
		if method == "totp" && last.Valid && step <= last.Int64 {
			resultCode = httpserver.CodeMFAStepUsed
			return audit.Write(ctx, tx, audit.ActionAuthMFAChallengeReplayed, accountID, accountID)
		}
		if method == "totp" {
			result, err := tx.ExecContext(ctx, "UPDATE mfa_factors SET last_totp_step = ? WHERE user_id = ? AND (last_totp_step IS NULL OR last_totp_step < ?)", step, accountID, step)
			if err != nil {
				return err
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if rows != 1 {
				resultCode = httpserver.CodeMFAStepUsed
				return audit.Write(ctx, tx, audit.ActionAuthMFAChallengeReplayed, accountID, accountID)
			}
		}
		result, err := tx.ExecContext(ctx, "UPDATE mfa_challenges SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL", instant(time.Now()), challengeID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return errChallengeExpired
		}
		profile, err := user.LoadMFAProfile(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if profile.MustChangePassword {
			transition = "password-change"
		}
		return audit.Write(ctx, tx, audit.ActionAuthMFAChallengeVerified, accountID, accountID)
	})
	if errors.Is(err, errChallengeExpired) {
		return httpserver.CodeMFAChallengeExpired, "", nil
	}
	if err != nil {
		return "", "", err
	}
	return resultCode, transition, nil
}

func startEnrollment(_ *httpserver.Server, database *sql.DB, publicURL string, sender sms.Sender) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		var request struct {
			Data struct {
				Type       string `json:"type"`
				Attributes struct {
					Method string `json:"method"`
					Proof  string `json:"proof"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := decode(c, &request); err != nil {
			return decodeErrorCode(err)
		}
		if request.Data.Type != "mfa-enrollments" || (request.Data.Attributes.Method != "totp" && request.Data.Attributes.Method != "sms") {
			return httpserver.NewError(httpserver.CodeMFAUnavailable)
		}
		method := request.Data.Attributes.Method
		if method == "sms" && !sms.Available(sender) {
			return httpserver.NewError(httpserver.CodeMFAUnavailable)
		}
		var secret []byte
		var smsCode string
		if method == "totp" {
			secret, err = newTOTPSecret()
		} else {
			smsCode, err = newSMSCode()
		}
		if err != nil {
			return err
		}
		id := "mfe_" + uuid.NewString()
		now := time.Now()
		var username, destination string
		err = miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			profile, err := user.LoadMFAProfile(c.Request().Context(), tx, accountID)
			if err != nil {
				return err
			}
			username, destination = profile.Username, profile.VerifiedMobile
			if method == "sms" && destination == "" {
				return errSMSDestinationRequired
			}
			var active int
			if err := tx.QueryRowContext(c.Request().Context(), "SELECT EXISTS(SELECT 1 FROM mfa_factors WHERE user_id = ?)", accountID).Scan(&active); err != nil {
				return err
			}
			var proofDigest any
			if active != 0 {
				digest, valid := validProof(c.Request().Context(), tx, accountID, request.Data.Attributes.Proof)
				if !valid {
					return errProofRequired
				}
				proofDigest = digest[:]
			}
			// The unique enrollment row must not let an expired enrollment block a new attempt.
			if _, err := tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_enrollments WHERE user_id = ? AND expires_at <= ?", accountID, instant(now)); err != nil {
				return err
			}
			if method == "sms" {
				if _, err := sms.Reserve(c.Request().Context(), tx, accountID, destination, now); err != nil {
					return err
				}
			}
			_, err = tx.ExecContext(c.Request().Context(), `INSERT INTO mfa_enrollments
				(id, user_id, method, totp_secret, sms_destination, sms_code, proof_digest, expires_at, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, accountID, method, nullableBytes(secret), nullable(destination), nullable(smsCode), proofDigest, instant(now.Add(30*time.Minute)), instant(now))
			return err
		})
		if errors.Is(err, errProofRequired) {
			return httpserver.NewError(httpserver.CodeMFAProofRequired)
		}
		if errors.Is(err, errSMSDestinationRequired) || errors.Is(err, sms.ErrUnavailable) {
			return httpserver.NewError(httpserver.CodeMFAUnavailable)
		}
		if errors.Is(err, sms.ErrRateLimited) {
			return httpserver.NewError(httpserver.CodeRateLimited)
		}
		if err != nil {
			return fmt.Errorf("start MFA enrollment: %w", err)
		}
		if method == "sms" {
			if err := sender.Send(c.Request().Context(), destination, smsCode); err != nil {
				return mfaDeliveryFailure(c.Request().Context(), database, accountID)
			}
			return resource(c, http.StatusCreated, "mfa-enrollments", id, map[string]string{"method": "sms"})
		}
		label := "MIA (" + strings.TrimPrefix(strings.TrimPrefix(publicURL, "https://"), "http://") + "):" + username
		uri := "otpauth://totp/" + strings.ReplaceAll(label, " ", "%20") + "?secret=" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret) + "&issuer=MIA"
		return resource(c, http.StatusCreated, "mfa-enrollments", id, map[string]string{"method": "totp", "provisioning_uri": uri})
	}
}

var errSMSDestinationRequired = errors.New("verified mobile is required for SMS MFA")

func verifyEnrollment(server *httpserver.Server, database *sql.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		if limit := server.CheckMFA(c, accountID); !limit.Allowed {
			return mfaThrottled(c, database, accountID, audit.ActionAuthMFAEnrollmentThrottled, limit)
		}
		var request mfaCodeRequest
		if err := decode(c, &request); err != nil {
			return decodeErrorCode(err)
		}
		if request.Data.Type != "mfa-enrollment-verifications" {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		var codes []string
		resultCode := httpserver.Code("")
		err = miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			var secret []byte
			var destination, smsCode, method, expiry string
			var proofDigest []byte
			err := tx.QueryRowContext(c.Request().Context(), `SELECT method, totp_secret, COALESCE(sms_destination, ''), COALESCE(sms_code, ''), expires_at, proof_digest
				FROM mfa_enrollments WHERE id = ? AND user_id = ?`, c.Param("id"), accountID).Scan(&method, &secret, &destination, &smsCode, &expiry, &proofDigest)
			if errors.Is(err, sql.ErrNoRows) {
				return errChallengeExpired
			}
			if err != nil {
				return err
			}
			live, err := beforeExpiry(expiry)
			if err != nil {
				return err
			}
			if !live {
				return errChallengeExpired
			}
			step, valid := verifyTOTP(secret, request.Data.Attributes.Code, time.Now())
			if method == "sms" {
				valid = len(request.Data.Attributes.Code) == 6 && subtle.ConstantTimeCompare([]byte(smsCode), []byte(request.Data.Attributes.Code)) == 1
			}
			if !valid {
				resultCode = httpserver.CodeInvalidMFACode
				result, err := tx.ExecContext(c.Request().Context(), "UPDATE mfa_enrollments SET failures = failures + 1 WHERE id = ? AND user_id = ? AND failures < 5", c.Param("id"), accountID)
				if err != nil {
					return err
				}
				rows, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if rows != 1 {
					return errChallengeExpired
				}
				if _, err = tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_enrollments WHERE id = ? AND failures >= 5", c.Param("id")); err != nil {
					return err
				}
				return audit.Write(c.Request().Context(), tx, audit.ActionAuthMFAEnrollmentFailed, accountID, accountID)
			}
			var digests [][32]byte
			codes, digests, err = newRecoveryCodes()
			if err != nil {
				return err
			}
			if proofDigest != nil {
				result, err := tx.ExecContext(c.Request().Context(), "UPDATE mfa_management_proofs SET consumed_at = ? WHERE user_id = ? AND token_digest = ? AND consumed_at IS NULL AND expires_at > ?", instant(time.Now()), accountID, proofDigest, instant(time.Now()))
				if err != nil {
					return err
				}
				rows, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if rows != 1 {
					return errProofRequired
				}
			}
			_, err = tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_factors WHERE user_id = ?", accountID)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(c.Request().Context(), "INSERT INTO mfa_factors (user_id, method, totp_secret, sms_destination, last_totp_step, created_at) VALUES (?, ?, ?, ?, ?, ?)", accountID, method, nullableBytes(secret), nullable(destination), nullableStep(method, step), instant(time.Now()))
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_recovery_codes WHERE user_id = ?", accountID)
			if err != nil {
				return err
			}
			for _, digest := range digests {
				if _, err = tx.ExecContext(c.Request().Context(), "INSERT INTO mfa_recovery_codes (id, user_id, digest, created_at) VALUES (?, ?, ?, ?)", "mrc_"+uuid.NewString(), accountID, digest[:], instant(time.Now())); err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_enrollments WHERE id = ?", c.Param("id")); err != nil {
				return err
			}
			if _, err = tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_challenges WHERE user_id = ?", accountID); err != nil {
				return err
			}
			if _, err = tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_management_proofs WHERE user_id = ?", accountID); err != nil {
				return err
			}
			return audit.Write(c.Request().Context(), tx, audit.ActionAuthMFAEnrolled, accountID, accountID)
		})
		if errors.Is(err, errChallengeExpired) {
			return httpserver.NewError(httpserver.CodeMFAChallengeExpired)
		}
		if errors.Is(err, errProofRequired) {
			return httpserver.NewError(httpserver.CodeMFAProofRequired)
		}
		if err != nil {
			return fmt.Errorf("verify MFA enrollment: %w", err)
		}
		if resultCode != "" {
			return httpserver.NewError(resultCode)
		}
		return resource(c, http.StatusOK, "mfa-recovery-codes", c.Param("id"), map[string]any{"recovery_codes": codes})
	}
}

func resendEnrollment(_ *httpserver.Server, database *sql.DB, sender sms.Sender) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if !sms.Available(sender) {
			return httpserver.NewError(httpserver.CodeMFAUnavailable)
		}
		accountID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		var destination, code string
		err = miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			var method, expiry string
			if err := tx.QueryRowContext(c.Request().Context(), `SELECT method, sms_destination, sms_code, expires_at FROM mfa_enrollments WHERE id = ? AND user_id = ?`, c.Param("id"), accountID).Scan(&method, &destination, &code, &expiry); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return errChallengeExpired
				}
				return err
			}
			live, err := beforeExpiry(expiry)
			if err != nil || !live {
				return errChallengeExpired
			}
			if method != "sms" {
				return errInvalidMFAValue
			}
			_, err = sms.Reserve(c.Request().Context(), tx, accountID, destination, time.Now())
			return err
		})
		if errors.Is(err, sms.ErrRateLimited) {
			return httpserver.NewError(httpserver.CodeRateLimited)
		}
		if errors.Is(err, errChallengeExpired) {
			return httpserver.NewError(httpserver.CodeMFAChallengeExpired)
		}
		if errors.Is(err, errInvalidMFAValue) {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		if err != nil {
			return err
		}
		if err := sender.Send(c.Request().Context(), destination, code); err != nil {
			return mfaDeliveryFailure(c.Request().Context(), database, accountID)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func createManagementProof(server *httpserver.Server, database *sql.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		if limit := server.CheckMFA(c, accountID); !limit.Allowed {
			return mfaThrottled(c, database, accountID, audit.ActionAuthMFAChallengeThrottled, limit)
		}
		var request struct {
			Data struct {
				Type       string `json:"type"`
				Attributes struct {
					Password string `json:"password"`
					Code     string `json:"code"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := decode(c, &request); err != nil {
			return decodeErrorCode(err)
		}
		if request.Data.Type != "mfa-management-proofs" {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		token := uuid.NewString()
		digest := sha256.Sum256([]byte(token))
		resultCode := httpserver.Code("")
		err = miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			profile, err := user.LoadMFAProfile(c.Request().Context(), tx, accountID)
			if err != nil {
				return err
			}
			matches, err := identity.VerifyPassword(request.Data.Attributes.Password, profile.PasswordHash)
			if err != nil {
				return err
			}
			if !matches {
				resultCode = httpserver.CodeInvalidMFACode
				return audit.Write(c.Request().Context(), tx, audit.ActionAuthMFAChallengeFailed, accountID, accountID)
			}
			var method string
			var secret []byte
			var last sql.NullInt64
			if err := tx.QueryRowContext(c.Request().Context(), "SELECT method, totp_secret, last_totp_step FROM mfa_factors WHERE user_id = ?", accountID).Scan(&method, &secret, &last); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					resultCode = httpserver.CodeMFARequired
					return nil
				}
				return err
			}
			if recovery, digestErr := recoveryDigest(request.Data.Attributes.Code); digestErr == nil {
				result, err := tx.ExecContext(c.Request().Context(), "UPDATE mfa_recovery_codes SET consumed_at = ? WHERE user_id = ? AND digest = ? AND consumed_at IS NULL", instant(time.Now()), accountID, recovery[:])
				if err != nil {
					return err
				}
				rows, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if rows == 1 {
					goto createProof
				}
			}
			if method == "totp" {
				step, valid := verifyTOTP(secret, request.Data.Attributes.Code, time.Now())
				if !valid {
					resultCode = httpserver.CodeInvalidMFACode
					return audit.Write(c.Request().Context(), tx, audit.ActionAuthMFAChallengeFailed, accountID, accountID)
				}
				result, err := tx.ExecContext(c.Request().Context(), "UPDATE mfa_factors SET last_totp_step = ? WHERE user_id = ? AND (last_totp_step IS NULL OR last_totp_step < ?)", step, accountID, step)
				if err != nil {
					return err
				}
				rows, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if rows != 1 {
					resultCode = httpserver.CodeMFAStepUsed
					return audit.Write(c.Request().Context(), tx, audit.ActionAuthMFAChallengeReplayed, accountID, accountID)
				}
			} else {
				result, err := tx.ExecContext(c.Request().Context(), `UPDATE mfa_challenges SET consumed_at = ?
					WHERE id = (SELECT id FROM mfa_challenges WHERE user_id = ? AND method = 'sms' AND sms_code = ?
					AND consumed_at IS NULL AND expires_at > ? ORDER BY created_at DESC LIMIT 1)`, instant(time.Now()), accountID,
					request.Data.Attributes.Code, instant(time.Now()))
				if err != nil {
					return err
				}
				rows, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if rows == 1 {
					goto createProof
				}
				resultCode = httpserver.CodeInvalidMFACode
				return audit.Write(c.Request().Context(), tx, audit.ActionAuthMFAChallengeFailed, accountID, accountID)
			}
		createProof:
			_, err = tx.ExecContext(c.Request().Context(), "INSERT INTO mfa_management_proofs (id, user_id, token_digest, expires_at, created_at) VALUES (?, ?, ?, ?, ?)", "mfp_"+uuid.NewString(), accountID, digest[:], instant(time.Now().Add(5*time.Minute)), instant(time.Now()))
			return err
		})
		if err != nil {
			return err
		}
		if resultCode != "" {
			return httpserver.NewError(resultCode)
		}
		return resource(c, http.StatusCreated, "mfa-management-proofs", "mfp", map[string]string{"proof": token})
	}
}

func cancelOrDisableMFA(_ *httpserver.Server, database *sql.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		proof := c.Request().Header.Get("X-MFA-Management-Proof")
		err = miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			result, err := tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_enrollments WHERE id = ? AND user_id = ?", c.Param("id"), accountID)
			if err != nil {
				return err
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if rows == 1 {
				return nil
			}
			if !consumeProof(c.Request().Context(), tx, accountID, proof) {
				return errProofRequired
			}
			_, err = tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_factors WHERE user_id = ?", accountID)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_recovery_codes WHERE user_id = ?", accountID)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_challenges WHERE user_id = ?", accountID)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(c.Request().Context(), "DELETE FROM mfa_management_proofs WHERE user_id = ?", accountID); err != nil {
				return err
			}
			return audit.Write(c.Request().Context(), tx, audit.ActionAuthMFADisabled, accountID, accountID)
		})
		if errors.Is(err, errProofRequired) {
			return httpserver.NewError(httpserver.CodeMFAProofRequired)
		}
		if err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

var errProofRequired = errors.New("MFA management proof required")

func validProof(ctx context.Context, tx *sql.Tx, accountID, proof string) ([32]byte, bool) {
	if parsed, err := uuid.Parse(proof); err != nil || parsed.Version() != 4 || parsed.String() != proof {
		return [32]byte{}, false
	}
	digest := sha256.Sum256([]byte(proof))
	var exists int
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM mfa_management_proofs WHERE user_id = ? AND token_digest = ? AND consumed_at IS NULL AND expires_at > ?)", accountID, digest[:], instant(time.Now())).Scan(&exists)
	return digest, err == nil && exists != 0
}

// consumeProof marks an unexpired digest-only proof used in the caller transaction.
func consumeProof(ctx context.Context, tx *sql.Tx, accountID, proof string) bool {
	if parsed, err := uuid.Parse(proof); err != nil || parsed.Version() != 4 || parsed.String() != proof {
		return false
	}
	digest := sha256.Sum256([]byte(proof))
	result, err := tx.ExecContext(ctx, "UPDATE mfa_management_proofs SET consumed_at = ? WHERE user_id = ? AND token_digest = ? AND consumed_at IS NULL AND expires_at > ?", instant(time.Now()), accountID, digest[:], instant(time.Now()))
	if err != nil {
		return false
	}
	rows, err := result.RowsAffected()
	return err == nil && rows == 1
}

func resource(c *echo.Context, status int, typ, id string, attributes any) error {
	c.Response().Header().Set(echo.HeaderContentType, "application/vnd.api+json")
	return c.JSON(status, map[string]any{"data": map[string]any{"type": typ, "id": id, "attributes": attributes}})
}

// recordChallengeFailure persists every unsuccessful challenge submission. The
// caller returns a normal domain result after the transaction commits.
func recordChallengeFailure(ctx context.Context, tx *sql.Tx, accountID, challengeID string, action audit.Action) error {
	result, err := tx.ExecContext(ctx, "UPDATE mfa_challenges SET failures = failures + 1 WHERE id = ? AND user_id = ? AND failures < 5", challengeID, accountID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errChallengeExpired
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM mfa_challenges WHERE id = ? AND user_id = ? AND failures >= 5", challengeID, accountID); err != nil {
		return err
	}
	return audit.Write(ctx, tx, action, accountID, accountID)
}

func mfaThrottled(c *echo.Context, database *sql.DB, accountID string, action audit.Action, limit httpserver.Result) error {
	if err := writeAudit(c.Request().Context(), database, action, accountID); err != nil {
		return err
	}
	if limit.RetryAfter > 0 {
		c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(limit.RetryAfter.Seconds()+.999))))
	}
	return httpserver.NewError(httpserver.CodeRateLimited)
}

func newSMSCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("generate SMS code: %w", err)
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullableStep(method string, step int64) any {
	if method != "totp" {
		return nil
	}
	return step
}

func invalidateMFAArtifacts(ctx context.Context, tx *sql.Tx, accountID string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM mfa_challenges WHERE user_id = ?", accountID); err != nil {
		return fmt.Errorf("invalidate MFA challenges: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM mfa_management_proofs WHERE user_id = ?", accountID); err != nil {
		return fmt.Errorf("invalidate MFA proofs: %w", err)
	}
	return nil
}
