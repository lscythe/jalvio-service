package fleet

import (
	"cmp"
	"slices"
	"sync"
	"time"

	"github.com/lscythe/jalvio-service/internal/geo"
)

const (
	// StaleAfter marks a bus stale once no report has arrived for this long.
	StaleAfter = 60 * time.Second
	// RemoveAfter drops a bus once no report has arrived for this long.
	RemoveAfter = 180 * time.Second
)

// Bus is the latest report for a bus. LastSeen is the local receive time, so
// expiry does not depend on the feed's clock.
type Bus struct {
	Update
	LastSeen time.Time
	Stale    bool
}

type Arrival struct {
	Plate      string
	RouteCode  string
	TripID     string
	Headsign   string
	Minutes    int
	Stale      bool
	ObservedAt time.Time
}

// Fleet holds the latest state of every reporting bus. It is safe for
// concurrent use. Updates are replaced whole, never modified in place.
type Fleet struct {
	mu          sync.RWMutex
	buses       map[string]Bus
	pending     map[string]bool // plate → still present, since the last flush
	subscribers map[*Subscription]struct{}
}

func New() *Fleet {
	return &Fleet{
		buses:       map[string]Bus{},
		pending:     map[string]bool{},
		subscribers: map[*Subscription]struct{}{},
	}
}

// Apply stores u unless an equal or newer report for the bus is already held.
func (f *Fleet) Apply(u Update, now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cur, ok := f.buses[u.Plate]; ok && !u.ObservedAt.After(cur.ObservedAt) {
		return false
	}
	f.buses[u.Plate] = Bus{Update: u, LastSeen: now}
	f.pending[u.Plate] = true
	return true
}

// Expire marks quiet buses stale and removes silent ones.
func (f *Fleet) Expire(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for plate, b := range f.buses {
		switch age := now.Sub(b.LastSeen); {
		case age >= RemoveAfter:
			delete(f.buses, plate)
			f.pending[plate] = false
		case age >= StaleAfter && !b.Stale:
			b.Stale = true
			f.buses[plate] = b
			f.pending[plate] = true
		}
	}
}

func (f *Fleet) Len() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.buses)
}

func (f *Fleet) Bus(plate string) (Bus, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	b, ok := f.buses[plate]
	return b, ok
}

// ByRoute returns the route's buses ordered by plate.
func (f *Fleet) ByRoute(code string) []Bus {
	return f.filter(func(b Bus) bool { return b.RouteCode == code })
}

// InBBox returns buses inside box, limited to routes when any are given,
// ordered by plate.
func (f *Fleet) InBBox(box geo.BBox, routes []string) []Bus {
	return f.filter(func(b Bus) bool {
		return box.Contains(b.Position) && (len(routes) == 0 || slices.Contains(routes, b.RouteCode))
	})
}

// ArrivalsAt lists buses expected at a stop or station, soonest first.
func (f *Fleet) ArrivalsAt(stopID string) []Arrival {
	f.mu.RLock()
	var arrivals []Arrival
	for _, b := range f.buses {
		minutes, ok := b.ETAs[stopID]
		if !ok {
			continue
		}
		arrivals = append(arrivals, Arrival{
			Plate:      b.Plate,
			RouteCode:  b.RouteCode,
			TripID:     b.TripID,
			Headsign:   b.Headsign,
			Minutes:    minutes,
			Stale:      b.Stale,
			ObservedAt: b.ObservedAt,
		})
	}
	f.mu.RUnlock()
	slices.SortFunc(arrivals, func(a, b Arrival) int {
		return cmp.Or(cmp.Compare(a.Minutes, b.Minutes), cmp.Compare(a.Plate, b.Plate))
	})
	return arrivals
}

func (f *Fleet) filter(keep func(Bus) bool) []Bus {
	f.mu.RLock()
	var out []Bus
	for _, b := range f.buses {
		if keep(b) {
			out = append(out, b)
		}
	}
	f.mu.RUnlock()
	slices.SortFunc(out, byPlate)
	return out
}

func byPlate(a, b Bus) int { return cmp.Compare(a.Plate, b.Plate) }
