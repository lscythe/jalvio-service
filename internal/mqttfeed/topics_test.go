package mqttfeed_test

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
)

func TestTileFiltersPadOneTile(t *testing.T) {
	monas := geo.BBox{West: 106.8272, East: 106.8272, South: -6.1754, North: -6.1754}
	want := []string{
		"/mobile_armada/0015-0020/#",
		"/mobile_armada/0015-0021/#",
		"/mobile_armada/0016-0020/#",
		"/mobile_armada/0016-0021/#",
	}
	if got := mqttfeed.TileFilters(monas); !slices.Equal(got, want) {
		t.Errorf("TileFilters(Monas) = %v, want %v", got, want)
	}
}

// The broker's topic tile is off by up to one tile from the bus position:
// row + 100·lat lies in [-603, -601) and col - 100·lng in [-10663, -10661).
func TestTileFiltersCoverEveryReportedTile(t *testing.T) {
	box := geo.BBox{West: 106.80, South: -6.20, East: 106.83, North: -6.17}
	filters := mqttfeed.TileFilters(box)
	for lat := box.South; lat <= box.North; lat += 0.0013 {
		for lng := box.West; lng <= box.East; lng += 0.0013 {
			for _, row := range reportedIndexes(100*lat, -603) {
				for _, col := range reportedIndexes(-100*lng, -10663) {
					f := fmt.Sprintf("/mobile_armada/%04d-%04d/#", row, col)
					if !slices.Contains(filters, f) {
						t.Fatalf("bus at %.4f,%.4f may report on %s, which TileFilters(%+v) misses", lat, lng, f, box)
					}
				}
			}
		}
	}
}

// reportedIndexes lists every index i with i + offset in [low, low+2).
func reportedIndexes(offset, low float64) []int {
	var out []int
	for i := int(math.Floor(low-offset)) - 2; i <= int(math.Floor(low-offset))+3; i++ {
		if v := float64(i) + offset; v >= low && v < low+2 {
			out = append(out, i)
		}
	}
	return out
}

func TestRouteAndBusFilters(t *testing.T) {
	if f, ok := mqttfeed.RouteFilter("JAK.18"); !ok || f != "/mobile_armada/+/JAK.18/#" {
		t.Errorf("RouteFilter(JAK.18) = %q, %v", f, ok)
	}
	if f, ok := mqttfeed.BusFilter("TST-001"); !ok || f != "/mobile_armada/+/+/+/TST-001" {
		t.Errorf("BusFilter(TST-001) = %q, %v", f, ok)
	}
	for _, bad := range []string{"", "9+", "#", "a/b", "A 1"} {
		if f, ok := mqttfeed.RouteFilter(bad); ok {
			t.Errorf("RouteFilter(%q) = %q, want rejected", bad, f)
		}
		if f, ok := mqttfeed.BusFilter(bad); ok {
			t.Errorf("BusFilter(%q) = %q, want rejected", bad, f)
		}
	}
}
