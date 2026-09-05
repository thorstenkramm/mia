package httpserver

import (
	"errors"
	"net/url"
	"testing"
)

func TestParsePagination(t *testing.T) {
	cases := []struct {
		name, query string
		want        Page
		wantErr     bool
	}{
		{name: "defaults", query: "", want: Page{Limit: 25}},
		{name: "explicit values", query: "page[limit]=50&page[offset]=100", want: Page{Limit: 50, Offset: 100}},
		{name: "minimum limit", query: "page[limit]=1", want: Page{Limit: 1}},
		{name: "maximum limit", query: "page[limit]=100", want: Page{Limit: 100}},
		{name: "zero offset", query: "page[offset]=0", want: Page{Limit: 25}},
		{name: "maximum offset", query: "page[offset]=10000", want: Page{Limit: 25, Offset: 10_000}},
		{name: "zero limit", query: "page[limit]=0", wantErr: true},
		{name: "limit above maximum", query: "page[limit]=101", wantErr: true},
		{name: "negative limit", query: "page[limit]=-1", wantErr: true},
		{name: "offset above maximum", query: "page[offset]=10001", wantErr: true},
		{name: "negative offset", query: "page[offset]=-1", wantErr: true},
		{name: "duplicate limit", query: "page[limit]=1&page[limit]=2", wantErr: true},
		{name: "duplicate offset", query: "page[offset]=1&page[offset]=2", wantErr: true},
		{name: "unknown parameter", query: "filter=x", wantErr: true},
		{name: "unknown page parameter", query: "page[size]=10", wantErr: true},
		{name: "empty limit", query: "page[limit]=", wantErr: true},
		{name: "empty offset", query: "page[offset]=", wantErr: true},
		{name: "non-integer limit", query: "page[limit]=abc", wantErr: true},
		{name: "decimal limit", query: "page[limit]=1.5", wantErr: true},
		{name: "non-integer offset", query: "page[offset]=abc", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			values, err := url.ParseQuery(testCase.query)
			if err != nil {
				t.Fatal(err)
			}
			page, err := ParsePagination(values)
			if testCase.wantErr {
				if !errors.Is(err, ErrInvalidPagination) {
					t.Fatalf("error = %v, want ErrInvalidPagination", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if page != testCase.want {
				t.Fatalf("page = %+v, want %+v", page, testCase.want)
			}
		})
	}
}
