package places_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/places"
)

const searchBody = `{"type":"FeatureCollection","features":[
	{"type":"Feature","geometry":{"type":"Point","coordinates":[106.8229,-6.1944]},
	 "properties":{"osm_type":"W","osm_id":123,"osm_key":"shop","osm_value":"mall","type":"house",
	  "name":"Grand Mall","housenumber":"1","street":"Jalan Utama","district":"Menteng",
	  "city":"Jakarta Pusat","state":"Jakarta","country":"Indonesia","countrycode":"ID"}},
	{"type":"Feature","geometry":{"type":"Point","coordinates":[106.8,-6.2]},
	 "properties":{"osm_type":"N","osm_id":7,"osm_key":"place","osm_value":"house","type":"house",
	  "housenumber":"12","street":"Jalan Kecil","city":"Jakarta Pusat","state":"Jakarta"}},
	{"type":"Feature","geometry":{"type":"Point","coordinates":[106.9,-6.3]},
	 "properties":{"osm_type":"R","osm_id":9,"osm_key":"place","osm_value":"city","type":"city",
	  "name":"Jakarta Pusat","city":"Jakarta Pusat","state":"Jakarta"}}
]}`

func photonServer(t *testing.T, body string, seen *url.URL, ua *string) *places.Photon {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = *r.URL
		*ua = r.UserAgent()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return places.NewPhoton(srv.Client(), srv.URL+"/", "jalvio-service/test (ops@example.com)")
}

func TestPhotonSearch(t *testing.T) {
	var seen url.URL
	var ua string
	p := photonServer(t, searchBody, &seen, &ua)

	got, err := p.Search(t.Context(), places.Query{
		Text:  "grand mall",
		Near:  &geo.Point{Lat: -6.2, Lon: 106.83},
		Area:  geo.BBox{West: 106.4, South: -6.6, East: 107.2, North: -5.9},
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []places.Place{
		{
			ID: "osm:W123", Name: "Grand Mall", Label: "Jalan Utama 1, Menteng, Jakarta Pusat, Jakarta",
			Kind: "mall", Position: geo.Point{Lat: -6.1944, Lon: 106.8229},
		},
		{
			ID: "osm:N7", Name: "Jalan Kecil 12", Label: "Jakarta Pusat, Jakarta",
			Kind: "house", Position: geo.Point{Lat: -6.2, Lon: 106.8},
		},
		{
			ID: "osm:R9", Name: "Jakarta Pusat", Label: "Jakarta",
			Kind: "city", Position: geo.Point{Lat: -6.3, Lon: 106.9},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("places =\n%+v\nwant\n%+v", got, want)
	}

	if seen.Path != "/api" {
		t.Errorf("path = %q, want /api", seen.Path)
	}
	q := seen.Query()
	for key, want := range map[string]string{
		"q": "grand mall", "lat": "-6.2", "lon": "106.83", "limit": "5", "bbox": "106.4,-6.6,107.2,-5.9",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
	if ua != "jalvio-service/test (ops@example.com)" {
		t.Errorf("User-Agent = %q", ua)
	}
}

func TestPhotonSearchWithoutBias(t *testing.T) {
	var seen url.URL
	var ua string
	p := photonServer(t, `{"features":[]}`, &seen, &ua)

	got, err := p.Search(t.Context(), places.Query{Text: "x", Limit: 1})
	if err != nil || len(got) != 0 {
		t.Fatalf("Search = %v, %v; want no places", got, err)
	}
	for _, key := range []string{"bbox", "lat", "lon"} {
		if seen.Query().Has(key) {
			t.Errorf("%s sent without a bias", key)
		}
	}
}

func TestPhotonReverse(t *testing.T) {
	var seen url.URL
	var ua string
	p := photonServer(t, searchBody, &seen, &ua)

	got, err := p.Reverse(t.Context(), geo.Point{Lat: -6.1944, Lon: 106.8229})
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	if got.ID != "osm:W123" {
		t.Errorf("place = %+v, want the first feature", got)
	}
	q := seen.Query()
	if seen.Path != "/reverse" || q.Get("lat") != "-6.1944" || q.Get("lon") != "106.8229" {
		t.Errorf("request = %s, want /reverse at the point", seen.String())
	}

	empty := photonServer(t, `{"features":[]}`, &seen, &ua)
	if _, err := empty.Reverse(t.Context(), geo.Point{}); !errors.Is(err, places.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestPhotonErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"status", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "busy", http.StatusTooManyRequests) }, "429"},
		{"json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) }, "decode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			_, err := places.NewPhoton(srv.Client(), srv.URL, "ua").Search(t.Context(), places.Query{Text: "abc"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}
