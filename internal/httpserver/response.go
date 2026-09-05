package httpserver

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// AuthenticatedUser returns the identity installed by authenticated-route middleware.
func AuthenticatedUser(c *echo.Context) (string, error) {
	id, ok := c.Get("mia.auth.user_id").(string)
	if !ok || id == "" {
		return "", NewError(CodeUnauthenticated)
	}
	return id, nil
}

// JSONAPI writes a JSON:API document with the required response media type.
func JSONAPI(c *echo.Context, status int, document any) error {
	c.Response().Header().Set(echo.HeaderContentType, "application/vnd.api+json")
	return c.JSON(status, document)
}

// Resource writes a JSON:API resource document.
func Resource(c *echo.Context, status int, resourceType, id string, attributes any) error {
	return JSONAPI(c, status, map[string]any{"data": map[string]any{
		"type": resourceType, "id": id, "attributes": attributes,
	}})
}

// Collection writes a paginated JSON:API collection with navigation links.
func Collection(c *echo.Context, data []map[string]any, page Page, hasMore bool) error {
	document := map[string]any{"data": data, "meta": map[string]any{"has_more": hasMore}}
	if links := CollectionLinks(c.Request().URL.Path, page, hasMore); links != nil {
		document["links"] = links
	}
	return JSONAPI(c, http.StatusOK, document)
}
