package user

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
)

// RegisterDeletionRoutes attaches administrator account deletion.
func RegisterDeletionRoutes(server *httpserver.Server, service *DeletionService) {
	server.AuthenticatedDELETE("/api/v1/users/:id", deleteAccountHandler(service))
}

func deleteAccountHandler(service *DeletionService) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := currentUser(c)
		if err != nil {
			return err
		}
		if err := service.Delete(c.Request().Context(), actorID, c.Param("id")); err != nil {
			code, outcome := deletionError(err)
			if outcome != "" {
				service.AuditDeletionDenied(c.Request().Context(), actorID, outcome)
			}
			if code != "" {
				return httpserver.NewError(code)
			}
			return err
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
	default:
		return "", ""
	}
}
