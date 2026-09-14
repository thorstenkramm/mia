package httpserver

import (
	"errors"
	"net/url"
	"strconv"
)

// Shared offset-pagination bounds for every paginated collection.
const (
	DefaultPageLimit = 25
	MaxPageLimit     = 100
	MaxPageOffset    = 10_000
)

// ErrInvalidPagination reports rejected pagination input. Callers translate it
// into their endpoint's documented validation error.
var ErrInvalidPagination = errors.New("invalid pagination parameters")

// Page is a validated offset-pagination window.
type Page struct {
	Limit, Offset int
}

// ParsePagination validates the page[limit] and page[offset] query parameters
// with defaults 25 and 0. It rejects duplicate, unknown, empty, non-integer,
// negative, and out-of-range parameters.
func ParsePagination(values url.Values) (Page, error) {
	page, _, err := ParsePaginationWithFilters(values, nil)
	return page, err
}

// ParsePaginationWithFilters validates pagination plus a route-owned allowlist of
// single, non-empty exact-match filters. Returned filters preserve navigation.
func ParsePaginationWithFilters(values url.Values, allowedFilters map[string]bool) (Page, map[string]string, error) {
	page := Page{Limit: DefaultPageLimit}
	filters := make(map[string]string)
	for key, entries := range values {
		if key != "page[limit]" && key != "page[offset]" {
			if !allowedFilters[key] || len(entries) != 1 || entries[0] == "" {
				return Page{}, nil, ErrInvalidPagination
			}
			filters[key] = entries[0]
			continue
		}
		if len(entries) != 1 || entries[0] == "" {
			return Page{}, nil, ErrInvalidPagination
		}
		value, err := strconv.Atoi(entries[0])
		if err != nil {
			return Page{}, nil, ErrInvalidPagination
		}
		if key == "page[limit]" {
			page.Limit = value
		} else {
			page.Offset = value
		}
	}
	if page.Limit < 1 || page.Limit > MaxPageLimit || page.Offset < 0 || page.Offset > MaxPageOffset {
		return Page{}, nil, ErrInvalidPagination
	}
	return page, filters, nil
}
