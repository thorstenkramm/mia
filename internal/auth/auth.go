// Package auth implements password login and browser-session stages.
package auth

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/identity"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

const cookieName = "__Host-mia_session"

// Register attaches the first auth slice's routes to the API server.
func Register(server *httpserver.Server, routes httpserver.AuthRouteRegistrar, database *sql.DB) {
	server.Echo.POST("/api/v1/auth/login", login(server, database))
	routes.POST("/api/v1/auth/logout", "any", logout(server, database))
	routes.POST("/api/v1/auth/password-changes", "password-change", changePassword(server, database))
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

func login(server *httpserver.Server, database *sql.DB) echo.HandlerFunc {
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
		stage := "authenticated"
		if account.MustChangePassword {
			stage = "password-change"
		}
		if err := miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			return audit.Write(c.Request().Context(), tx, audit.ActionAuthSessionLoggedIn, account.ID, account.ID)
		}); err != nil {
			return err
		}
		if err := server.StartSession(c, account.ID, account.SecurityGeneration, stage, time.Now()); err != nil {
			return err
		}
		httpserver.RotateCSRF(c)
		return sessionResponse(c, account.ID, stage)
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
	c.Response().Header().Set(echo.HeaderContentType, "application/vnd.api+json")
	return c.JSON(http.StatusOK, map[string]any{"data": map[string]any{"type": "auth-sessions", "id": id, "attributes": map[string]string{"stage": stage}}})
}
