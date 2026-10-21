package geo_test

import (
	"math"
	"testing"

	"github.com/lscythe/jalvio-service/internal/geo"
)

func TestDistance(t *testing.T) {
	monas := geo.Point{Lat: -6.1754, Lon: 106.8272}
	blokM := geo.Point{Lat: -6.2443, Lon: 106.7983}

	if d := geo.Distance(monas, monas); d != 0 {
		t.Errorf("Distance to itself = %v, want 0", d)
	}
	// About 8.3 km between Monas and Blok M.
	if d := geo.Distance(monas, blokM); math.Abs(d-8296) > 50 {
		t.Errorf("Distance(Monas, Blok M) = %.0f m, want about 8296 m", d)
	}
	if geo.Distance(monas, blokM) != geo.Distance(blokM, monas) {
		t.Error("Distance is not symmetric")
	}
}

func TestEncodePolyline(t *testing.T) {
	tests := []struct {
		name   string
		points []geo.Point
		want   string
	}{
		{"empty", nil, ""},
		{
			"reference example",
			[]geo.Point{{Lat: 38.5, Lon: -120.2}, {Lat: 40.7, Lon: -120.95}, {Lat: 43.252, Lon: -126.453}},
			"_p~iF~ps|U_ulLnnqC_mqNvxq`@",
		},
		{
			"rounds to five decimals",
			[]geo.Point{{Lat: -6.1754049, Lon: 106.8271951}},
			"fcud@_u_kS",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := geo.EncodePolyline(tt.points); got != tt.want {
				t.Errorf("EncodePolyline = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBBoxContains(t *testing.T) {
	jakarta := geo.BBox{West: 106.6, South: -6.4, East: 107.0, North: -6.0}
	tests := []struct {
		p    geo.Point
		want bool
	}{
		{geo.Point{Lat: -6.2, Lon: 106.8}, true},
		{geo.Point{Lat: -6.4, Lon: 106.6}, true},
		{geo.Point{Lat: -5.9, Lon: 106.8}, false},
		{geo.Point{Lat: -6.2, Lon: 107.1}, false},
	}
	for _, tt := range tests {
		if got := jakarta.Contains(tt.p); got != tt.want {
			t.Errorf("Contains(%v) = %v, want %v", tt.p, got, tt.want)
		}
	}
}
