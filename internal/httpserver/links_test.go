package httpserver

import (
	"reflect"
	"testing"
)

func TestCollectionLinks(t *testing.T) {
	cases := []struct {
		name    string
		page    Page
		hasMore bool
		want    map[string]string
	}{
		{
			name: "first page with more", page: Page{Limit: 25, Offset: 0}, hasMore: true,
			want: map[string]string{"next": "/api/v1/things?page%5Blimit%5D=25&page%5Boffset%5D=25"},
		},
		{
			name: "middle page", page: Page{Limit: 25, Offset: 25}, hasMore: true,
			want: map[string]string{
				"prev": "/api/v1/things?page%5Blimit%5D=25&page%5Boffset%5D=0",
				"next": "/api/v1/things?page%5Blimit%5D=25&page%5Boffset%5D=50",
			},
		},
		{
			name: "last page", page: Page{Limit: 25, Offset: 50}, hasMore: false,
			want: map[string]string{"prev": "/api/v1/things?page%5Blimit%5D=25&page%5Boffset%5D=25"},
		},
		{
			name: "single or empty page", page: Page{Limit: 25, Offset: 0}, hasMore: false,
			want: nil,
		},
		{
			name: "previous offset clamps to zero", page: Page{Limit: 25, Offset: 10}, hasMore: false,
			want: map[string]string{"prev": "/api/v1/things?page%5Blimit%5D=25&page%5Boffset%5D=0"},
		},
		{
			name: "maximum offset omits unusable next", page: Page{Limit: 25, Offset: 10000}, hasMore: true,
			want: map[string]string{"prev": "/api/v1/things?page%5Blimit%5D=25&page%5Boffset%5D=9975"},
		},
		{
			name: "near boundary permits maximum next", page: Page{Limit: 25, Offset: 9975}, hasMore: true,
			want: map[string]string{
				"prev": "/api/v1/things?page%5Blimit%5D=25&page%5Boffset%5D=9950",
				"next": "/api/v1/things?page%5Blimit%5D=25&page%5Boffset%5D=10000",
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := CollectionLinks("/api/v1/things", testCase.page, testCase.hasMore)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("links = %v, want %v", got, testCase.want)
			}
		})
	}
}
