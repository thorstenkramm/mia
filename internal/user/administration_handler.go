package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/httpserver"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

var accountFilters = map[string]bool{
	"filter[username]": true, "filter[account_class]": true,
	"filter[role]": true, "filter[state]": true,
}

// RegisterAdministrationRoutes attaches administrator account discovery.
func RegisterAdministrationRoutes(server *httpserver.Server, databaseService *DeletionService) {
	if databaseService.soleSupervisor == nil {
		panic("account administration routes require the course sole-supervisor check")
	}
	server.AuthenticatedGET("/api/v1/users", listAccountsHandler(databaseService))
	server.AuthenticatedGET("/api/v1/users/:id", getAccountHandler(databaseService))
	server.AuthenticatedPOST("/api/v1/mentor-role-target-preflights", mentorTargetPreflightHandler(server,
		databaseService))
}

type mentorTargetPreflightRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			UserID json.RawMessage `json:"user_id"`
		} `json:"attributes"`
	} `json:"data"`
}

func mentorTargetPreflightHandler(server *httpserver.Server, service *DeletionService) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := currentUser(c)
		if err != nil {
			return err
		}
		supervisor, err := HasRole(c.Request().Context(), service.database, actorID, Supervisor)
		if err != nil {
			return err
		}
		if !supervisor {
			return httpserver.NewError(httpserver.CodeUserRoleUnauthorized)
		}
		var request mentorTargetPreflightRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "mentor-role-target-preflights" || request.Data.ID != "" {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		set, userID, err := httpserver.OptionalStringAttribute(request.Data.Attributes.UserID)
		if err != nil || !set || userID == nil || !validUserID(*userID) {
			return httpserver.NewError(httpserver.CodeInvalidRequest)
		}
		if result := server.CheckMentorTarget(c, actorID); !result.Allowed {
			if err := auditMentorTargetThrottle(c.Request().Context(), service.database, actorID); err != nil {
				return err
			}
			return mentorTargetUnavailable()
		}
		target, err := service.ResolveMentorTarget(c.Request().Context(), actorID, *userID)
		if err != nil {
			if errors.Is(err, ErrMentorTargetUnavailable) {
				return mentorTargetUnavailable()
			}
			if errors.Is(err, ErrRoleActorUnauthorized) {
				return httpserver.NewError(httpserver.CodeUserRoleUnauthorized)
			}
			return err
		}
		c.Response().Header().Set("ETag", target.ETag)
		return httpserver.Resource(c, http.StatusOK, "mentor-role-targets", target.ID, map[string]any{
			"username": target.Username, "display_name": target.DisplayName, "grant_state": target.GrantState,
		})
	}
}

func mentorTargetUnavailable() error {
	return httpserver.NewError(httpserver.CodeUserMentorTargetUnavailable)
}

func auditMentorTargetThrottle(ctx context.Context, database *sql.DB, actorID string) error {
	err := miSQLite.WithTx(ctx, database, func(tx *sql.Tx) error {
		return audit.Write(ctx, tx, audit.ActionUserMentorTargetThrottled, actorID, "")
	})
	if err != nil {
		return fmt.Errorf("audit mentor target throttle: %w", err)
	}
	return nil
}

func listAccountsHandler(service *DeletionService) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := currentUser(c)
		if err != nil {
			return err
		}
		page, filters, err := httpserver.ParsePaginationWithFilters(c.QueryParams(), accountFilters)
		if err != nil {
			return httpserver.NewError(httpserver.CodeUserAccountQueryInvalid)
		}
		result, err := service.ListAccounts(c.Request().Context(), actorID, AccountFilter{
			Username: filters["filter[username]"], Class: filters["filter[account_class]"],
			Role: filters["filter[role]"], State: filters["filter[state]"], Limit: page.Limit, Offset: page.Offset,
		})
		if err != nil {
			return administrationError(err)
		}
		data := make([]map[string]any, 0, len(result.Accounts))
		for _, account := range result.Accounts {
			data = append(data, administrationResource(account, false))
		}
		return httpserver.CollectionWithFilters(c, data, page, result.HasMore, filters)
	}
}

func getAccountHandler(service *DeletionService) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := currentUser(c)
		if err != nil {
			return err
		}
		account, err := service.GetAccount(c.Request().Context(), actorID, c.Param("id"))
		if err != nil {
			return administrationError(err)
		}
		c.Response().Header().Set("ETag", account.ETag)
		return httpserver.Resource(c, http.StatusOK, "administration-accounts", account.ID,
			administrationAttributes(account, true))
	}
}

func administrationResource(account AdministrationAccount, detail bool) map[string]any {
	return map[string]any{"type": "administration-accounts", "id": account.ID,
		"attributes": administrationAttributes(account, detail)}
}

func administrationAttributes(account AdministrationAccount, detail bool) map[string]any {
	attributes := map[string]any{"username": account.Username, "account_class": account.Class,
		"account_state": account.State, "permanent_roles": account.Roles}
	if detail {
		attributes["actions"] = account.Actions
	}
	return attributes
}

func administrationError(err error) error {
	switch {
	case errors.Is(err, ErrAdministrationUnauthorized):
		return httpserver.NewError(httpserver.CodeUserAdministrationUnauthorized)
	case errors.Is(err, ErrAccountQueryInvalid):
		return httpserver.NewError(httpserver.CodeUserAccountQueryInvalid)
	case errors.Is(err, ErrDeletionNotFound):
		return httpserver.NewError(httpserver.CodeUserNotFound)
	default:
		return err
	}
}
