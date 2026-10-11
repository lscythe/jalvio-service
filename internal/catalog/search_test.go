package catalog_test

import (
	"reflect"
	"testing"

	"github.com/lscythe/jalvio-service/internal/catalog"
)

func route(id string) catalog.Result { return catalog.Result{Kind: catalog.RouteResult, ID: id} }
func stop(id string) catalog.Result  { return catalog.Result{Kind: catalog.StopResult, ID: id} }

func TestSearch(t *testing.T) {
	c := parseFixture(t)
	bundaranHI := &catalog.Point{Lat: -6.193, Lon: 106.823}

	tests := []struct {
		name  string
		query string
		near  *catalog.Point
		limit int
		want  []catalog.Result
	}{
		{
			name:  "route code ignores case and punctuation",
			query: "jak18",
			want:  []catalog.Result{route("JAK.18")},
		},
		{
			name:  "route code with a space",
			query: "JAK 18",
			want:  []catalog.Result{route("JAK.18")},
		},
		{
			name:  "stop code",
			query: "knb",
			want:  []catalog.Result{stop("B00003P")},
		},
		{
			name:  "name prefix ranks above word prefix",
			query: "kuningan",
			want:  []catalog.Result{stop("B00003P"), route("JAK.18")},
		},
		{
			name:  "exact name ranks above prefix, even when farther",
			query: "kota",
			near:  bundaranHI,
			want:  []catalog.Result{stop("H00003P"), stop("B00004P"), route("1")},
		},
		{
			name:  "routes with the same match quality sort by code",
			query: "jak",
			want:  []catalog.Result{route("JAK.10A"), route("JAK.18")},
		},
		{
			name:  "substring",
			query: "alibat",
			want:  []catalog.Result{stop("B00001P"), route("JAK.18")},
		},
		{
			name:  "platforms are grouped under their station",
			query: "Blok-M",
			want:  []catalog.Result{stop("H00001P"), route("1")},
		},
		{
			name:  "ties break by name",
			query: "b",
			want: []catalog.Result{
				stop("H00001P"), stop("H00002P"), route("1"), stop("B00003P"), stop("B00001P"), route("JAK.10A"), route("JAK.18"),
			},
		},
		{
			name:  "ties break by distance when near is given",
			query: "b",
			near:  bundaranHI,
			want: []catalog.Result{
				stop("H00002P"), stop("H00001P"), route("1"), stop("B00003P"), stop("B00001P"), route("JAK.10A"), route("JAK.18"),
			},
		},
		{
			name:  "limit",
			query: "b",
			limit: 2,
			want:  []catalog.Result{stop("H00001P"), stop("H00002P")},
		},
		{
			name:  "non-positive limit",
			query: "b",
			limit: -1,
			want:  nil,
		},
		{
			name:  "blank query",
			query: "  - ",
			want:  nil,
		},
		{
			name:  "no match",
			query: "monas",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit := tt.limit
			if limit == 0 {
				limit = 10
			}
			got := c.Search(tt.query, tt.near, limit)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Search(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}
