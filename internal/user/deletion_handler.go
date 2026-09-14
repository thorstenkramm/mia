package user

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
)

// RegisterDeletionRoutes attaches administrator account deletion.
func RegisterDeletionRoutes(server *httpserver.Server, service *DeletionService) {
	if service.soleSupervisor == nil {
		panic("account deletion routes require the course sole-supervisor check")
	}
	server.AuthenticatedDELETE("/api/v1/users/:id", deleteAccountHandler(service))
}

func deleteAccountHandler(service *DeletionService) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := currentUser(c)
		if err != nil {
			return err
		}
		deleteErr := service.DeleteReviewed(c.Request().Context(), actorID, c.Param("id"),
			c.Request().Header.Get("If-Match"))
		if deleteErr != nil {
			code, outcome := deletionError(deleteErr)
			if outcome != "" {
				service.AuditDeletionDenied(c.Request().Context(), actorID, outcome)
			}
			if code != "" {
				return httpserver.NewError(code)
			}
			return deleteErr
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func deletionError(err error) (httpserver.Code, string) {
	switch {
	case errors.Is(err, ErrDeletionUnauthorized):
		return httpserver.CodeUserDeletionUnauthorized, "user_deletion_unauthorized"
	case errors.Is(err, ErrDeletionNotFound):
		return httpserver.CodeUserNotFound, "user_not_found"
	case errors.Is(err, ErrLastAdministrator):
		return httpserver.CodeUserLastAdministrator, "user_last_administrator"
	case errors.Is(err, ErrSoleSupervisor):
		return httpserver.CodeUserSoleSupervisor, "user_sole_supervisor"
	case errors.Is(err, ErrAccountPreconditionRequired):
		return httpserver.CodeUserAccountPreconditionRequired, "user_account_precondition_required"
	case errors.Is(err, ErrAccountPreconditionFailed):
		return httpserver.CodeUserAccountPreconditionFailed, "user_account_precondition_failed"
	default:
		return "", ""
	}
}
