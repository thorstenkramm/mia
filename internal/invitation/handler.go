package invitation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/user"
)

// instantFormat is the RFC 3339 format with exactly 6 fractional digits as required by the API.
const instantFormat = "2006-01-02T15:04:05.000000Z"

// Register attaches invitation routes to the server.
func Register(server *httpserver.Server, service *Service, publicURL string, deliveries *DeliveryManager) {
	server.AuthenticatedPOST("/api/v1/invitations", createHandler(server, service, publicURL, deliveries))
	server.AuthenticatedGET("/api/v1/invitations", listHandler(service))
	server.AuthenticatedGET("/api/v1/invitations/:id", getHandler(service))
	server.AuthenticatedDELETE("/api/v1/invitations/:id", deleteHandler(service))
	server.AuthenticatedPOST("/api/v1/invitations/:id/resends", resendHandler(server, service, publicURL, deliveries))
	server.Echo.POST("/api/v1/invitation-previews", previewHandler(server, service))
	server.Echo.POST("/api/v1/invitation-acceptances", acceptHandler(server, service))
}

type createRequest struct {
	Data struct {
		Type       string `json:"type"`
		Attributes struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"attributes"`
	} `json:"data"`
}

func createHandler(_ *httpserver.Server, service *Service, publicURL string, deliveries *DeliveryManager) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		var request createRequest
		if err := decode(c, &request); err != nil {
			return decodeErrorCode(err)
		}
		if request.Data.Type != "invitations" {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		role := Role(request.Data.Attributes.Role)
		if role != RoleAdministrator && role != RoleSupervisor && role != RoleMentor {
			return httpserver.NewError(httpserver.CodeInvitationRoleUnauthorized)
		}
		invitation, token, err := service.Create(c.Request().Context(), CreateInput{
			Email:     request.Data.Attributes.Email,
			Role:      role,
			InviterID: actorID,
		})
		if errors.Is(err, identity.ErrInvalidEmail) {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		if errors.Is(err, ErrInvitationEmailExists) {
			return httpserver.NewError(httpserver.CodeInvitationEmailRegistered)
		}
		if errors.Is(err, ErrInvitationPendingExists) {
			return httpserver.NewError(httpserver.CodeInvitationEmailRegistered)
		}
		if errors.Is(err, ErrInvitationRoleInvalid) {
			// Audit denied mutation without revealing sensitive data.
			service.AuditCreationDenied(c.Request().Context(), actorID, string(role))
			return httpserver.NewError(httpserver.CodeInvitationRoleUnauthorized)
		}
		if err != nil {
			return err
		}
		link := publicURL + "/invitation#token=" + token
		if !deliveries.Admit(c.Request().Context(), invitation.ID, invitation.Email, string(invitation.Role), link, invitation.TokenGeneration) {
			service.logger.WarnContext(c.Request().Context(), "delivery admission failed", "invitation_id", invitation.ID)
		}
		return invitationResource(c, http.StatusCreated, invitation)
	}
}

const (
	defaultPageLimit = 25
	maxPageLimit     = 100
	maxPageOffset    = 10000
)

var allowedListParams = map[string]bool{
	"page[limit]":  true,
	"page[offset]": true,
}

func listHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		// Reject unknown query parameters.
		for key := range c.QueryParams() {
			if !allowedListParams[key] {
				return httpserver.NewError(httpserver.CodeInvalidRequest)
			}
		}
		// Pagination parameters with API conventions (Finding 7: reject malformed values).
		// Check if parameters are present in query string (not just empty string default).
		queryParams := c.QueryParams()
		offsetParam := queryParams.Get("page[offset]")
		var offset int
		_, offsetPresent := queryParams["page[offset]"]
		if offsetPresent && offsetParam != "" {
			var parseErr error
			offset, parseErr = strconv.Atoi(offsetParam)
			if parseErr != nil {
				return httpserver.NewError(httpserver.CodeInvalidRequest)
			}
		} else if offsetPresent && offsetParam == "" {
			// Present but empty is invalid.
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		if offset < 0 || offset > maxPageOffset {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		limitParam := queryParams.Get("page[limit]")
		var limit int
		_, limitPresent := queryParams["page[limit]"]
		if limitPresent && limitParam != "" {
			var parseErr error
			limit, parseErr = strconv.Atoi(limitParam)
			if parseErr != nil {
				return httpserver.NewError(httpserver.CodeInvalidRequest)
			}
		} else if limitPresent && limitParam == "" {
			// Present but empty is invalid.
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		} else {
			limit = defaultPageLimit
		}
		if limit <= 0 || limit > maxPageLimit {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		result, err := service.List(c.Request().Context(), actorID, ListInput{Offset: offset, PageSize: limit})
		if errors.Is(err, ErrInvitationListUnauthorized) {
			return httpserver.NewError(httpserver.CodeInvitationListUnauthorized)
		}
		if err != nil {
			return err
		}
		return invitationCollection(c, result, offset, limit)
	}
}

func getHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		invitation, err := service.Get(c.Request().Context(), c.Param("id"), actorID)
		if errors.Is(err, ErrInvitationNotFound) {
			return httpserver.NewError(httpserver.CodeInvitationNotFound)
		}
		if err != nil {
			return err
		}
		return invitationResource(c, http.StatusOK, invitation)
	}
}

func deleteHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		err = service.Delete(c.Request().Context(), c.Param("id"), actorID)
		if errors.Is(err, ErrInvitationNotFound) {
			// Audit denied mutation without revealing existence.
			service.AuditRevocationDenied(c.Request().Context(), actorID)
			return httpserver.NewError(httpserver.CodeInvitationNotFound)
		}
		if errors.Is(err, ErrInvitationNotPending) {
			return httpserver.NewError(httpserver.CodeInvitationInvalid)
		}
		if err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func resendHandler(_ *httpserver.Server, service *Service, publicURL string, deliveries *DeliveryManager) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		invitation, token, err := service.Resend(c.Request().Context(), c.Param("id"), actorID)
		if errors.Is(err, ErrInvitationNotFound) {
			// Audit denied mutation without revealing existence.
			service.AuditResendDenied(c.Request().Context(), actorID)
			return httpserver.NewError(httpserver.CodeInvitationNotFound)
		}
		if errors.Is(err, ErrInvitationNotPending) {
			return httpserver.NewError(httpserver.CodeInvitationInvalid)
		}
		if err != nil {
			return err
		}
		link := publicURL + "/invitation#token=" + token
		if !deliveries.Admit(c.Request().Context(), invitation.ID, invitation.Email, string(invitation.Role), link, invitation.TokenGeneration) {
			service.logger.WarnContext(c.Request().Context(), "delivery admission failed", "invitation_id", invitation.ID)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

type previewRequest struct {
	Data struct {
		Type       string `json:"type"`
		Attributes struct {
			Token string `json:"token"`
		} `json:"attributes"`
	} `json:"data"`
}

func previewHandler(server *httpserver.Server, service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var request previewRequest
		if err := decode(c, &request); err != nil {
			return httpserver.NewError(httpserver.CodeInvitationInvalid)
		}
		if request.Data.Type != "invitation-previews" {
			return httpserver.NewError(httpserver.CodeInvitationInvalid)
		}
		// Apply layered rate limits: IP and token-digest.
		if result := server.CheckInvitationPreview(c, request.Data.Attributes.Token); !result.Allowed {
			c.Response().Header().Set("Retry-After", retryAfterSeconds(result.RetryAfter))
			return httpserver.NewError(httpserver.CodeRateLimited)
		}
		role, err := service.Preview(c.Request().Context(), request.Data.Attributes.Token)
		if errors.Is(err, ErrInvitationInvalid) {
			return httpserver.NewError(httpserver.CodeInvitationInvalid)
		}
		if err != nil {
			return err
		}
		c.Response().Header().Set(echo.HeaderContentType, "application/vnd.api+json")
		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"type":       "invitation-previews",
				"id":         "preview",
				"attributes": map[string]string{"platform": "mia", "role": string(role)},
			},
		})
	}
}

type acceptRequest struct {
	Data struct {
		Type       string `json:"type"`
		Attributes struct {
			Token                string `json:"token"`
			Username             string `json:"username"`
			Password             string `json:"password"`
			PasswordConfirmation string `json:"password_confirmation"`
			Language             string `json:"language"`
			Country              string `json:"country"`
			TimeZone             string `json:"time_zone"`
		} `json:"attributes"`
	} `json:"data"`
}

func acceptHandler(server *httpserver.Server, service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var request acceptRequest
		if err := decode(c, &request); err != nil {
			return httpserver.NewError(httpserver.CodeInvitationInvalid)
		}
		if request.Data.Type != "invitation-acceptances" {
			return httpserver.NewError(httpserver.CodeInvitationInvalid)
		}
		// Apply layered rate limits: IP and token-digest.
		if result := server.CheckInvitationAccept(c, request.Data.Attributes.Token); !result.Allowed {
			c.Response().Header().Set("Retry-After", retryAfterSeconds(result.RetryAfter))
			return httpserver.NewError(httpserver.CodeRateLimited)
		}
		attrs := request.Data.Attributes
		// Validate token is pending BEFORE checking password/profile validity.
		// Non-pending tokens must return the same generic error to avoid leaking state.
		if err := service.IsPending(c.Request().Context(), attrs.Token); err != nil {
			return httpserver.NewError(httpserver.CodeInvitationInvalid)
		}
		if attrs.Password != attrs.PasswordConfirmation {
			return httpserver.NewError(httpserver.CodeInvalidPassword)
		}
		hash, err := identity.Password(attrs.Password)
		if err != nil {
			return httpserver.NewError(httpserver.CodeInvalidPassword)
		}
		account, err := service.Accept(c.Request().Context(), AcceptInput{
			Token:        attrs.Token,
			Username:     attrs.Username,
			PasswordHash: hash,
			Language:     attrs.Language,
			Country:      attrs.Country,
			TimeZone:     attrs.TimeZone,
		})
		if errors.Is(err, ErrInvitationInvalid) {
			return httpserver.NewError(httpserver.CodeInvitationInvalid)
		}
		if errors.Is(err, ErrInvitationEmailExists) {
			return httpserver.NewError(httpserver.CodeInvitationEmailRegistered)
		}
		// Finding 4: Map username uniqueness violation to client error.
		if errors.Is(err, user.ErrUsernameTaken) {
			return httpserver.NewError(httpserver.CodeUsernameTaken)
		}
		if errors.Is(err, identity.ErrInvalidUsername) {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		if errors.Is(err, identity.ErrInvalidEmail) {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		// Map identity validation errors from user.Create to proper client errors.
		if isIdentityValidationError(err) {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		if err != nil {
			return err
		}
		c.Response().Header().Set(echo.HeaderContentType, "application/vnd.api+json")
		return c.JSON(http.StatusCreated, map[string]any{
			"data": map[string]any{
				"type":       "users",
				"id":         account.ID,
				"attributes": map[string]any{},
			},
		})
	}
}

// isIdentityValidationError checks if the error is an identity validation error
// from user.Create for language, country, or timezone.
func isIdentityValidationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "invalid language") ||
		strings.Contains(msg, "invalid country") ||
		strings.Contains(msg, "invalid time zone")
}

func authenticatedUser(c *echo.Context) (string, error) {
	id, ok := c.Get("mia.auth.user_id").(string)
	if !ok || id == "" {
		return "", httpserver.NewError(httpserver.CodeUnauthenticated)
	}
	return id, nil
}

func invitationResource(c *echo.Context, status int, inv Invitation) error {
	c.Response().Header().Set(echo.HeaderContentType, "application/vnd.api+json")
	attrs := map[string]any{
		"email":      inv.Email,
		"role":       string(inv.Role),
		"status":     string(inv.Status),
		"created_at": inv.CreatedAt.Format(instantFormat),
		"updated_at": inv.UpdatedAt.Format(instantFormat),
	}
	if inv.FailureCode != nil {
		attrs["failure_code"] = *inv.FailureCode
	}
	return c.JSON(status, map[string]any{
		"data": map[string]any{
			"type":       "invitations",
			"id":         inv.ID,
			"attributes": attrs,
		},
	})
}

func invitationCollection(c *echo.Context, result ListResult, offset, limit int) error {
	c.Response().Header().Set(echo.HeaderContentType, "application/vnd.api+json")
	data := make([]map[string]any, 0, len(result.Invitations))
	for _, inv := range result.Invitations {
		attrs := map[string]any{
			"email":      inv.Email,
			"role":       string(inv.Role),
			"status":     string(inv.Status),
			"created_at": inv.CreatedAt.Format(instantFormat),
			"updated_at": inv.UpdatedAt.Format(instantFormat),
		}
		if inv.FailureCode != nil {
			attrs["failure_code"] = *inv.FailureCode
		}
		data = append(data, map[string]any{
			"type":       "invitations",
			"id":         inv.ID,
			"attributes": attrs,
		})
	}
	// Build navigation links per API conventions.
	links := map[string]any{
		"self": "/api/v1/invitations?page[limit]=" + strconv.Itoa(limit) + "&page[offset]=" + strconv.Itoa(offset),
	}
	if offset > 0 {
		prevOffset := offset - limit
		if prevOffset < 0 {
			prevOffset = 0
		}
		links["prev"] = "/api/v1/invitations?page[limit]=" + strconv.Itoa(limit) + "&page[offset]=" + strconv.Itoa(prevOffset)
	}
	if result.HasMore {
		nextOffset := offset + limit
		links["next"] = "/api/v1/invitations?page[limit]=" + strconv.Itoa(limit) + "&page[offset]=" + strconv.Itoa(nextOffset)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"data":  data,
		"links": links,
		"meta":  map[string]any{"offset": offset, "limit": limit},
	})
}

var (
	errRequestTooLarge      = errors.New("request body too large")
	errUnsupportedMediaType = errors.New("unsupported request media type")
)

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

func decodeErrorCode(err error) error {
	if errors.Is(err, errRequestTooLarge) {
		return httpserver.NewError(httpserver.CodeRequestTooLarge)
	}
	if errors.Is(err, errUnsupportedMediaType) {
		return httpserver.NewError(httpserver.CodeUnsupportedMediaType)
	}
	return httpserver.NewError(httpserver.CodeInvalidRequest)
}

func retryAfterSeconds(duration time.Duration) string {
	seconds := int(duration.Seconds() + 0.999)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
