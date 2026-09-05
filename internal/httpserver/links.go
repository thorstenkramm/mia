package httpserver

import (
	"net/url"
	"strconv"
)

// CollectionLinks builds the JSON:API prev and next navigation links for one
// paginated collection page from its canonical path. Absent links are omitted,
// and only the canonical pagination parameters are preserved. A nil result
// means the page has no navigation links.
func CollectionLinks(path string, page Page, hasMore bool) map[string]string {
	links := make(map[string]string, 2)
	if page.Offset > 0 {
		links["prev"] = paginationURL(path, page.Limit, max(0, page.Offset-page.Limit))
	}
	if hasMore && page.Offset <= MaxPageOffset-page.Limit {
		links["next"] = paginationURL(path, page.Limit, page.Offset+page.Limit)
	}
	if len(links) == 0 {
		return nil
	}
	return links
}

func paginationURL(path string, limit, offset int) string {
	values := url.Values{
		"page[limit]":  []string{strconv.Itoa(limit)},
		"page[offset]": []string{strconv.Itoa(offset)},
	}
	return path + "?" + values.Encode()
}
