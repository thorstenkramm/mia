package user

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
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
