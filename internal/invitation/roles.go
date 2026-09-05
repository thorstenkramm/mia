package invitation

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/httpserver"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

// roleLogger is injected during route registration.
var roleLogger *slog.Logger

// RegisterRoleRoutes attaches direct role grant routes to the server.
func RegisterRoleRoutes(server *httpserver.Server, database *sql.DB, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	roleLogger = logger
	server.AuthenticatedPOST("/api/v1/users/:id/roles", grantRoleHandler(database))
}

type grantRoleRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Role string `json:"role"`
		} `json:"attributes"`
	} `json:"data"`
}

func grantRoleHandler(database *sql.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := authenticatedUser(c)
		if err != nil {
			return err
		}
		targetID := c.Param("id")
		var request grantRoleRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "user-roles" || request.Data.ID != "" {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		role := user.Role(request.Data.Attributes.Role)
		if role != user.Administrator && role != user.Supervisor && role != user.Mentor {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		err = miSQLite.WithTx(c.Request().Context(), database, func(tx *sql.Tx) error {
			if err := user.GrantRole(c.Request().Context(), tx, targetID, role, actorID); err != nil {
				return err
			}
			return audit.WriteWithMetadata(c.Request().Context(), tx, audit.ActionUserUserRoleGranted, actorID, targetID, audit.Metadata{Role: string(role)})
		})
		if err != nil {
			if isNotFoundError(err) {
				// Audit denied mutation without revealing existence (no targetID in audit).
				auditDeniedRoleGrant(c, database, actorID, string(role))
				return httpserver.NewError(httpserver.CodeUserNotFound)
			}
			if isUnauthorizedError(err) {
				// Audit denied mutation without revealing existence.
				auditDeniedRoleGrant(c, database, actorID, string(role))
				return httpserver.NewError(httpserver.CodeUserRoleUnauthorized)
			}
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// auditDeniedRoleGrant records a denied role grant attempt without revealing target existence.
// Fire-and-forget: audit failure does not change the API response for denied mutations.
func auditDeniedRoleGrant(c *echo.Context, database *sql.DB, actorID, role string) {
	ctx := c.Request().Context()
	err := miSQLite.WithTx(ctx, database, func(tx *sql.Tx) error {
		// Do not include targetID to avoid revealing existence.
		return audit.WriteWithMetadata(ctx, tx, audit.ActionUserUserRoleGrantDenied, actorID, "", audit.Metadata{Role: role})
	})
	if err != nil {
		roleLogger.ErrorContext(ctx, "audit write failed", "action", audit.ActionUserUserRoleGrantDenied, "error", err)
	}
}

func isNotFoundError(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, user.ErrRoleRecipientIneligible)
}

func isUnauthorizedError(err error) bool {
	return errors.Is(err, user.ErrRoleActorUnauthorized) || errors.Is(err, user.ErrRoleInvalid)
}
