package invitation

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/user"
)

// Register attaches invitation routes to the server.
func Register(server *httpserver.Server, service *Service, publicURL string, deliveries *DeliveryManager) {
	server.AuthenticatedPOST("/api/v1/invitations", createHandler(server, service, publicURL, deliveries))
	server.AuthenticatedGET("/api/v1/invitations", listHandler(service))
	server.AuthenticatedGET("/api/v1/invitations/:id", getHandler(service))
	server.AuthenticatedDELETE("/api/v1/invitations/:id", deleteHandler(service))
	server.AuthenticatedPOST("/api/v1/invitations/:id/resends", resendHandler(server, service, publicURL, deliveries))
	server.AuthenticationSensitive(http.MethodPost, "/api/v1/invitation-previews", previewHandler(server, service))
	server.AuthenticationSensitive(http.MethodPost, "/api/v1/invitation-acceptances", acceptHandler(server, service))
}

type createRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
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
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "invitations" || request.Data.ID != "" {
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

func listHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		page, err := httpserver.ParsePagination(c.QueryParams())
		if err != nil {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		result, err := service.List(c.Request().Context(), actorID, ListInput{Offset: page.Offset, PageSize: page.Limit})
		if errors.Is(err, ErrInvitationListUnauthorized) {
			return httpserver.NewError(httpserver.CodeInvitationListUnauthorized)
		}
		if err != nil {
			return err
		}
		return invitationCollection(c, result, page)
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
		ID         string `json:"id"`
		Attributes struct {
			Token string `json:"token"`
		} `json:"attributes"`
	} `json:"data"`
}

func previewHandler(server *httpserver.Server, service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var request previewRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "invitation-previews" || request.Data.ID != "" {
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
		ID         string `json:"id"`
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
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "invitation-acceptances" || request.Data.ID != "" {
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
	return errors.Is(err, identity.ErrInvalidCountry) ||
		strings.Contains(msg, "invalid language") ||
		strings.Contains(msg, "invalid time zone")
}

func authenticatedUser(c *echo.Context) (string, error) {
	return httpserver.AuthenticatedUser(c)
}

func invitationResource(c *echo.Context, status int, inv Invitation) error {
	c.Response().Header().Set(echo.HeaderContentType, "application/vnd.api+json")
	return c.JSON(status, map[string]any{
		"data": map[string]any{
			"type":       "invitations",
			"id":         inv.ID,
			"attributes": invitationAttributes(inv),
		},
	})
}

func invitationAttributes(inv Invitation) map[string]any {
	attrs := map[string]any{
		"email":      inv.Email,
		"role":       string(inv.Role),
		"status":     string(inv.Status),
		"created_at": httpserver.FormatInstant(inv.CreatedAt),
		"updated_at": httpserver.FormatInstant(inv.UpdatedAt),
	}
	if inv.FailureCode != nil {
		attrs["failure_code"] = *inv.FailureCode
	}
	return attrs
}

func invitationCollection(c *echo.Context, result ListResult, page httpserver.Page) error {
	data := make([]map[string]any, 0, len(result.Invitations))
	for _, inv := range result.Invitations {
		data = append(data, map[string]any{
			"type":       "invitations",
			"id":         inv.ID,
			"attributes": invitationAttributes(inv),
		})
	}
	return httpserver.Collection(c, data, page, result.HasMore)
}

func retryAfterSeconds(duration time.Duration) string {
	seconds := int(duration.Seconds() + 0.999)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
