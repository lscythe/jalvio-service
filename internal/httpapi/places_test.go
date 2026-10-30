package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/httpapi"
	"github.com/lscythe/jalvio-service/internal/places"
)

var grandIndonesia = places.Place{
	ID: "osm:W123", Name: "Grand Indonesia", Label: "Jalan M.H. Thamrin, Jakarta", Kind: "mall",
	Position: geo.Point{Lat: -6.195, Lon: 106.82},
}

type searchCall struct {
	area  geo.BBox
	text  string
	near  *geo.Point
	limit int
}

type fakePlaces struct {
	found []places.Place
	err   error
	calls *[]searchCall
}

func (f fakePlaces) Search(_ context.Context, area geo.BBox, text string, near *geo.Point, limit int) ([]places.Place, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, searchCall{area, text, near, limit})
	}
	if len(text) < 3 {
		return nil, places.ErrQueryTooShort
	}
	return f.found, f.err
}

func (f fakePlaces) Reverse(context.Context, geo.Point) (places.Place, error) {
	if f.err != nil {
		return places.Place{}, f.err
	}
	return f.found[0], nil
}

func placesHandler(p httpapi.Places) http.Handler {
	d := deps(true)
	d.Places = p
	return httpapi.New(d)
}

const grandIndonesiaJSON = `{"id":"osm:W123","name":"Grand Indonesia","label":"Jalan M.H. Thamrin, Jakarta","kind":"mall","position":{"lat":-6.195,"lng":106.82}}`

func TestPlaces(t *testing.T) {
	var calls []searchCall
	h := placesHandler(fakePlaces{found: []places.Place{grandIndonesia, {Name: "Jalan Sudirman", Position: geo.Point{Lat: -6.21, Lon: 106.82}}}, calls: &calls})

	rec := get(t, h, "/v1/places?q=grand+indo&near=-6.2,106.83&limit=5", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	want := `{"attribution":"© OpenStreetMap contributors","places":[` + grandIndonesiaJSON + `,` +
		`{"name":"Jalan Sudirman","position":{"lat":-6.21,"lng":106.82}}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
	if len(calls) != 1 || calls[0].text != "grand indo" || calls[0].near == nil || *calls[0].near != (geo.Point{Lat: -6.2, Lon: 106.83}) || calls[0].limit != 5 {
		t.Errorf("search calls = %+v, want one for %q near -6.2,106.83 limit 5", calls, "grand indo")
	}
}

func TestPlacesDefaults(t *testing.T) {
	var calls []searchCall
	rec := get(t, placesHandler(fakePlaces{calls: &calls}), "/v1/places?q=monas", nil)

	if got, want := strings.TrimSpace(rec.Body.String()), `{"attribution":"© OpenStreetMap contributors","places":[]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if len(calls) != 1 || calls[0].near != nil || calls[0].limit != 0 {
		t.Errorf("search calls = %+v, want one without near or limit", calls)
	}
}

func TestPlacesSearchUsesTheCatalogArea(t *testing.T) {
	var calls []searchCall
	c := fixtureCatalog(t)
	d := deps(true)
	d.Catalogs = fakeCatalogs{current: c}
	d.Places = fakePlaces{calls: &calls}
	get(t, httpapi.New(d), "/v1/places?q=monas", nil)
	if want := c.Bounds().Pad(0.1); len(calls) != 1 || calls[0].area != want {
		t.Errorf("search calls = %+v, want one in %+v", calls, want)
	}
}

func TestPlacesSearchBeforeTheCatalogLoads(t *testing.T) {
	var calls []searchCall
	get(t, placesHandler(fakePlaces{calls: &calls}), "/v1/places?q=monas", nil)
	if len(calls) != 1 || calls[0].area != (geo.BBox{}) {
		t.Errorf("search calls = %+v, want one without an area", calls)
	}
}

func TestReversePlace(t *testing.T) {
	rec := get(t, placesHandler(fakePlaces{found: []places.Place{grandIndonesia}}), "/v1/places/reverse?at=-6.195,106.82", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	want := `{"attribution":"© OpenStreetMap contributors","place":` + grandIndonesiaJSON + `}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
}

func TestPlacesProblems(t *testing.T) {
	upstream := errors.New("unexpected status 500")
	tests := []struct {
		name       string
		places     httpapi.Places
		target     string
		wantStatus int
		wantDetail string
	}{
		{"disabled search", nil, "/v1/places?q=monas", http.StatusServiceUnavailable, "disabled"},
		{"disabled reverse", nil, "/v1/places/reverse?at=-6.2,106.8", http.StatusServiceUnavailable, "disabled"},
		{"no query", fakePlaces{}, "/v1/places", http.StatusBadRequest, "q"},
		{"short query", fakePlaces{}, "/v1/places?q=ab", http.StatusBadRequest, "q"},
		{"bad near", fakePlaces{}, "/v1/places?q=monas&near=-6.2", http.StatusBadRequest, "near"},
		{"near out of range", fakePlaces{}, "/v1/places?q=monas&near=-96.2,106.8", http.StatusBadRequest, "near"},
		{"bad limit", fakePlaces{}, "/v1/places?q=monas&limit=many", http.StatusBadRequest, "limit"},
		{"limit too large", fakePlaces{}, "/v1/places?q=monas&limit=21", http.StatusBadRequest, "limit"},
		{"zero limit", fakePlaces{}, "/v1/places?q=monas&limit=0", http.StatusBadRequest, "limit"},
		{"no point", fakePlaces{}, "/v1/places/reverse", http.StatusBadRequest, "at"},
		{"bad point", fakePlaces{}, "/v1/places/reverse?at=north,east", http.StatusBadRequest, "at"},
		{"search rate limited", fakePlaces{err: places.ErrRateLimited}, "/v1/places?q=monas", http.StatusTooManyRequests, ""},
		{"reverse rate limited", fakePlaces{err: places.ErrRateLimited}, "/v1/places/reverse?at=-6.2,106.8", http.StatusTooManyRequests, ""},
		{"search upstream failure", fakePlaces{err: upstream}, "/v1/places?q=monas", http.StatusBadGateway, ""},
		{"reverse upstream failure", fakePlaces{err: upstream}, "/v1/places/reverse?at=-6.2,106.8", http.StatusBadGateway, ""},
		{"nothing there", fakePlaces{err: places.ErrNotFound}, "/v1/places/reverse?at=-6.2,106.8", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, placesHandler(tt.places), tt.target, nil)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			p := decodeProblem(t, rec)
			if p.Status != tt.wantStatus || !strings.Contains(p.Detail, tt.wantDetail) {
				t.Errorf("problem = %+v, want %d mentioning %q", p, tt.wantStatus, tt.wantDetail)
			}
			if strings.Contains(p.Detail, upstream.Error()) {
				t.Errorf("problem detail %q leaks the upstream error", p.Detail)
			}
		})
	}
}
