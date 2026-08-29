package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
)

// Code identifies a registered, stable MIA domain error.
type Code string

const (
	CodeCSRFInvalid      Code = "csrf_invalid"
	CodeInternalError    Code = "internal_error"
	CodeMethodNotAllowed Code = "method_not_allowed"
	CodeNotFound         Code = "not_found"
	CodeRateLimited      Code = "rate_limited"
)

type definition struct {
	status        int
	title, detail string
}

var errorRegistry = map[Code]definition{
	CodeCSRFInvalid:      {http.StatusForbidden, "Forbidden", "The request could not be completed."},
	CodeInternalError:    {http.StatusInternalServerError, "Internal Server Error", "The request could not be completed."},
	CodeMethodNotAllowed: {http.StatusMethodNotAllowed, "Method Not Allowed", "The request method is not supported."},
	CodeNotFound:         {http.StatusNotFound, "Not Found", "The requested resource was not found."},
	CodeRateLimited:      {http.StatusTooManyRequests, "Too Many Requests", "The request could not be completed."},
}

// Error is a registered domain error translated centrally to a JSON:API response.
type Error struct{ code Code }

func (err *Error) Error() string { return string(err.code) }

// NewError returns an error from the shared registry. Codes can only be added
// by extending the registry in this package.
func NewError(code Code) *Error {
	if _, ok := errorRegistry[code]; !ok {
		return &Error{code: CodeInternalError}
	}
	return &Error{code: code}
}

func errorHandler(c *echo.Context, err error) {
	code := CodeInternalError
	directStatus := 0
	var domain *Error
	if errors.As(err, &domain) {
		code = domain.code
	} else {
		var httpError *echo.HTTPError
		if errors.As(err, &httpError) {
			code, directStatus = frameworkError(httpError.Code)
		} else {
			var statusCoder echo.HTTPStatusCoder
			if errors.As(err, &statusCoder) {
				code, directStatus = frameworkError(statusCoder.StatusCode())
			}
		}
	}
	problem := errorRegistry[code]
	if !isAPIPath(c.Request().URL.Path) {
		if directStatus != 0 {
			if writeErr := c.NoContent(directStatus); writeErr != nil {
				c.Logger().Error("write HTTP error response", "error", writeErr)
			}
			return
		}
		if writeErr := c.NoContent(problem.status); writeErr != nil {
			c.Logger().Error("write HTTP error response", "error", writeErr)
		}
		return
	}
	c.Response().Header().Set(echo.HeaderContentType, jsonAPI)
	if writeErr := c.JSON(problem.status, map[string]any{"errors": []map[string]string{{"status": strconv.Itoa(problem.status), "code": string(code), "title": problem.title, "detail": problem.detail}}}); writeErr != nil {
		c.Logger().Error("write HTTP error response", "error", writeErr)
	}
}

func frameworkError(status int) (Code, int) {
	switch status {
	case http.StatusNotFound:
		return CodeNotFound, 0
	case http.StatusMethodNotAllowed:
		return CodeMethodNotAllowed, 0
	default:
		return CodeInternalError, status
	}
}

func isAPIPath(path string) bool { return path == "/api" || strings.HasPrefix(path, "/api/") }
