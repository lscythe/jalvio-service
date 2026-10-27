package mqttfeed_test

import (
	"slices"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
)

var t0 = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time            { return c.now }
func (c *fakeClock) advanceTo(d time.Duration) { c.now = t0.Add(d) }
func newFakeClock() *fakeClock                 { return &fakeClock{now: t0} }
func routeInterest(code string) mqttfeed.Interest {
	return mqttfeed.Interest{RouteCodes: []string{code}}
}

var centralArea = geo.BBox{West: 106.80, South: -6.20, East: 106.83, North: -6.17}

func TestDemandHoldAndRelease(t *testing.T) {
	clock := newFakeClock()
	d := mqttfeed.NewDemand(clock.Now)
	d.Hold("a", routeInterest("1"))
	if got, want := d.Wanted(), []string{"/mobile_armada/+/1/#"}; !slices.Equal(got, want) {
		t.Fatalf("Wanted = %v, want %v", got, want)
	}

	d.Release("a")
	clock.advanceTo(29 * time.Second)
	if got := d.Wanted(); len(got) != 1 {
		t.Errorf("Wanted 29 s after release = %v, want the route kept for the grace", got)
	}
	clock.advanceTo(31 * time.Second)
	if got := d.Wanted(); len(got) != 0 {
		t.Errorf("Wanted 31 s after release = %v, want empty", got)
	}
}

func TestDemandHoldReplaces(t *testing.T) {
	clock := newFakeClock()
	d := mqttfeed.NewDemand(clock.Now)
	d.Hold("a", routeInterest("1"))
	d.Wanted()
	d.Hold("a", routeInterest("2"))
	clock.advanceTo(31 * time.Second)
	if got, want := d.Wanted(), []string{"/mobile_armada/+/2/#"}; !slices.Equal(got, want) {
		t.Errorf("Wanted = %v, want only the replacing route", got)
	}
}

func TestDemandSharedFilterSurvivesOneRelease(t *testing.T) {
	clock := newFakeClock()
	d := mqttfeed.NewDemand(clock.Now)
	area := mqttfeed.Interest{Areas: []geo.BBox{centralArea}}
	d.Hold("a", area)
	d.Hold("b", area)
	d.Release("a")
	clock.advanceTo(31 * time.Second)
	if got, want := d.Wanted(), mqttfeed.TileFilters(centralArea); !slices.Equal(got, want) {
		t.Errorf("Wanted = %v, want the area still held by b", got)
	}
}

func TestDemandTouchLease(t *testing.T) {
	clock := newFakeClock()
	d := mqttfeed.NewDemand(clock.Now)
	bus := mqttfeed.Interest{FleetNumbers: []string{"TST-001"}}
	d.Touch(bus)
	for _, at := range []time.Duration{59 * time.Second, 89 * time.Second} {
		clock.advanceTo(at)
		if got := d.Wanted(); len(got) != 1 {
			t.Errorf("Wanted at +%v = %v, want the bus within lease and grace", at, got)
		}
	}
	clock.advanceTo(91 * time.Second)
	if got := d.Wanted(); len(got) != 0 {
		t.Errorf("Wanted at +91s = %v, want empty", got)
	}

	clock.advanceTo(100 * time.Second)
	d.Touch(bus)
	clock.advanceTo(150 * time.Second)
	d.Touch(bus)
	clock.advanceTo(239 * time.Second)
	if got := d.Wanted(); len(got) != 1 {
		t.Errorf("Wanted 89 s after a renewed touch = %v, want the bus", got)
	}
	clock.advanceTo(241 * time.Second)
	if got := d.Wanted(); len(got) != 0 {
		t.Errorf("Wanted 91 s after a renewed touch = %v, want empty", got)
	}
}

func TestDemandTouchDoesNotGrow(t *testing.T) {
	d := mqttfeed.NewDemand(newFakeClock().Now)
	for range 1000 {
		d.Touch(mqttfeed.Interest{Areas: []geo.BBox{centralArea}})
	}
	if got, want := len(d.Wanted()), len(mqttfeed.TileFilters(centralArea)); got != want {
		t.Errorf("len(Wanted) = %d, want %d", got, want)
	}
}

func TestDemandWholeFleetAboveThreshold(t *testing.T) {
	clock := newFakeClock()
	d := mqttfeed.NewDemand(clock.Now)
	city := geo.BBox{West: 106.6, South: -6.4, East: 107.1, North: -5.9}
	if n := len(mqttfeed.TileFilters(city)); n <= mqttfeed.MaxTiles {
		t.Fatalf("test area has %d tiles, want more than %d", n, mqttfeed.MaxTiles)
	}
	d.Hold("a", mqttfeed.Interest{Areas: []geo.BBox{city}, RouteCodes: []string{"1"}})
	want := []string{mqttfeed.WholeFleet}
	if got := d.Wanted(); !slices.Equal(got, want) {
		t.Fatalf("Wanted = %v, want %v", got, want)
	}
	d.Release("a")
	clock.advanceTo(29 * time.Second)
	if got := d.Wanted(); !slices.Equal(got, want) {
		t.Errorf("Wanted 29 s after release = %v, want %v", got, want)
	}
	clock.advanceTo(31 * time.Second)
	if got := d.Wanted(); len(got) != 0 {
		t.Errorf("Wanted 31 s after release = %v, want empty", got)
	}
}

func TestDemandInvalidValuesIgnored(t *testing.T) {
	d := mqttfeed.NewDemand(newFakeClock().Now)
	d.Touch(mqttfeed.Interest{RouteCodes: []string{"#"}, FleetNumbers: []string{"a/b"}})
	if got := d.Wanted(); len(got) != 0 {
		t.Errorf("Wanted = %v, want empty", got)
	}
}
