package fleet_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
)

var t0 = time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)

func update(plate, route string, pos geo.Point, observed time.Time, etas map[string]int) fleet.Update {
	return fleet.Update{
		Plate:      plate,
		RouteCode:  route,
		TripID:     route + "-L01",
		Headsign:   "Kota",
		Position:   pos,
		ETAs:       etas,
		ObservedAt: observed,
	}
}

var (
	monas = geo.Point{Lat: -6.1754, Lon: 106.8272}
	blokM = geo.Point{Lat: -6.2443, Lon: 106.7983}
)

func plates(buses []fleet.Bus) []string {
	var out []string
	for _, b := range buses {
		out = append(out, b.Plate)
	}
	return out
}

func TestApplyKeepsNewestReport(t *testing.T) {
	f := fleet.New()
	if !f.Apply(update("A-1", "1", monas, t0, nil), t0) {
		t.Fatal("first report rejected")
	}
	if f.Apply(update("A-1", "1", blokM, t0.Add(-time.Second), nil), t0.Add(time.Second)) {
		t.Error("older report accepted")
	}
	if f.Apply(update("A-1", "1", blokM, t0, nil), t0.Add(time.Second)) {
		t.Error("duplicate report accepted")
	}

	bus, ok := f.Bus("A-1")
	if !ok {
		t.Fatal("bus not found")
	}
	if bus.Position != monas || !bus.LastSeen.Equal(t0) {
		t.Errorf("bus = %+v, want the first report seen at %v", bus, t0)
	}

	if !f.Apply(update("A-1", "1", blokM, t0.Add(5*time.Second), nil), t0.Add(6*time.Second)) {
		t.Fatal("newer report rejected")
	}
	if bus, _ := f.Bus("A-1"); bus.Position != blokM {
		t.Errorf("position = %v, want %v", bus.Position, blokM)
	}
	if f.Len() != 1 {
		t.Errorf("Len = %d, want 1", f.Len())
	}
}

func TestApplyClampsFutureReportsToReceiveTime(t *testing.T) {
	f := fleet.New()
	ahead := time.Hour

	f.Apply(update("A-1", "1", monas, t0.Add(ahead), nil), t0)
	if bus, _ := f.Bus("A-1"); !bus.ObservedAt.Equal(t0) {
		t.Errorf("ObservedAt = %v, want the receive time %v", bus.ObservedAt, t0)
	}

	if !f.Apply(update("A-1", "1", blokM, t0.Add(ahead+2*time.Second), nil), t0.Add(2*time.Second)) {
		t.Error("next report from the same fast clock rejected")
	}

	corrected := t0.Add(5 * time.Second)
	if !f.Apply(update("A-1", "1", monas, corrected, nil), corrected.Add(time.Second)) {
		t.Fatal("report after the clock was corrected rejected")
	}
	if bus, _ := f.Bus("A-1"); !bus.ObservedAt.Equal(corrected) || bus.Position != monas {
		t.Errorf("bus = %+v, want the corrected report observed at %v", bus, corrected)
	}
}

func TestExpire(t *testing.T) {
	f := fleet.New()
	f.Apply(update("A-1", "1", monas, t0, nil), t0)

	f.Expire(t0.Add(fleet.StaleAfter - time.Second))
	if bus, _ := f.Bus("A-1"); bus.Stale {
		t.Error("bus stale before StaleAfter")
	}

	f.Expire(t0.Add(fleet.StaleAfter))
	if bus, _ := f.Bus("A-1"); !bus.Stale {
		t.Error("bus not stale after StaleAfter")
	}

	f.Apply(update("A-1", "1", monas, t0.Add(fleet.StaleAfter), nil), t0.Add(fleet.StaleAfter))
	if bus, _ := f.Bus("A-1"); bus.Stale {
		t.Error("fresh report did not clear stale")
	}

	f.Expire(t0.Add(fleet.StaleAfter + fleet.RemoveAfter))
	if _, ok := f.Bus("A-1"); ok {
		t.Error("bus not removed after RemoveAfter")
	}
}

func TestByRoute(t *testing.T) {
	f := fleet.New()
	f.Apply(update("B-2", "1", monas, t0, nil), t0)
	f.Apply(update("A-1", "1", blokM, t0, nil), t0)
	f.Apply(update("C-3", "9C", monas, t0, nil), t0)

	if got, want := plates(f.ByRoute("1")), []string{"A-1", "B-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ByRoute(1) = %v, want %v", got, want)
	}
	if got := f.ByRoute("JAK.18"); got != nil {
		t.Errorf("ByRoute(JAK.18) = %v, want none", got)
	}
}

func TestInBBox(t *testing.T) {
	f := fleet.New()
	f.Apply(update("A-1", "1", monas, t0, nil), t0)
	f.Apply(update("B-2", "9C", monas, t0, nil), t0)
	f.Apply(update("C-3", "1", blokM, t0, nil), t0)
	central := geo.BBox{West: 106.80, South: -6.20, East: 106.85, North: -6.15}

	tests := []struct {
		routes []string
		want   []string
	}{
		{routes: nil, want: []string{"A-1", "B-2"}},
		{routes: []string{"9C"}, want: []string{"B-2"}},
		{routes: []string{"JAK.18"}, want: nil},
	}
	for _, tt := range tests {
		if got := plates(f.InBBox(central, tt.routes)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("InBBox(routes=%v) = %v, want %v", tt.routes, got, tt.want)
		}
	}
}

func TestArrivalsAt(t *testing.T) {
	f := fleet.New()
	f.Apply(update("A-1", "1", monas, t0, map[string]int{"H00001P": 7, "H00002P": 12}), t0)
	f.Apply(update("B-2", "9C", monas, t0, map[string]int{"H00001P": 2}), t0)
	f.Apply(update("C-3", "1", monas, t0, map[string]int{"H00001P": 7}), t0)
	f.Apply(update("D-4", "1", monas, t0, map[string]int{"H00009P": 1}), t0)

	got := f.ArrivalsAt("H00001P")
	want := []fleet.Arrival{
		{Plate: "B-2", RouteCode: "9C", TripID: "9C-L01", Headsign: "Kota", Minutes: 2, ObservedAt: t0},
		{Plate: "A-1", RouteCode: "1", TripID: "1-L01", Headsign: "Kota", Minutes: 7, ObservedAt: t0},
		{Plate: "C-3", RouteCode: "1", TripID: "1-L01", Headsign: "Kota", Minutes: 7, ObservedAt: t0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ArrivalsAt =\n%+v\nwant\n%+v", got, want)
	}
}
