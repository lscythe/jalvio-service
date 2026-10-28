package mqttfeed

import (
	"slices"
	"sync"
	"time"
)

const (
	// MaxFilters is the most filters subscribed one by one; beyond it the
	// whole fleet is cheaper for the broker and about the same traffic for
	// us, and anonymous requests cannot pile up broker subscriptions.
	MaxFilters = 1500
	leaseFor   = 60 * time.Second
	// releaseGrace keeps a filter after it is no longer needed, so panning
	// and zooming do not churn subscriptions.
	releaseGrace = 30 * time.Second
)

// Demand records what clients watch. WebSocket sessions hold interest until
// they release it; REST polls touch it for a lease.
type Demand struct {
	clock func() time.Time

	mu         sync.Mutex
	holds      map[string][]string
	leaseEnds  map[string]time.Time
	releasedAt map[string]time.Time
}

func NewDemand(clock func() time.Time) *Demand {
	return &Demand{
		clock:      clock,
		holds:      map[string][]string{},
		leaseEnds:  map[string]time.Time{},
		releasedAt: map[string]time.Time{},
	}
}

// Hold replaces whatever key held before.
func (d *Demand) Hold(key string, i Interest) {
	filters := i.filters()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.release(key, filters)
	d.holds[key] = filters
}

func (d *Demand) Release(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.release(key, nil)
	delete(d.holds, key)
}

func (d *Demand) release(key string, kept []string) {
	now := d.clock()
	keep := make(map[string]bool, len(kept))
	for _, f := range kept {
		keep[f] = true
	}
	for _, f := range d.holds[key] {
		if !keep[f] {
			d.releasedAt[f] = now
		}
	}
}

func (d *Demand) Touch(i Interest) {
	filters := i.filters()
	end := d.clock().Add(leaseFor)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, f := range filters {
		d.leaseEnds[f] = end
	}
}

// Wanted lists the filters to subscribe, sorted: everything held, leased, or
// released within the grace, or the whole fleet above MaxFilters filters.
func (d *Demand) Wanted() []string {
	now := d.clock()
	d.mu.Lock()
	defer d.mu.Unlock()
	wanted := map[string]bool{}
	for _, filters := range d.holds {
		for _, f := range filters {
			wanted[f] = true
		}
	}
	keepRecent(wanted, d.leaseEnds, now)
	keepRecent(wanted, d.releasedAt, now)

	if wanted[WholeFleet] || len(wanted) > MaxFilters {
		return []string{WholeFleet}
	}
	out := make([]string, 0, len(wanted))
	for f := range wanted {
		out = append(out, f)
	}
	slices.Sort(out)
	return out
}

// keepRecent adds the filters whose time ended less than the grace ago and
// forgets the rest.
func keepRecent(wanted map[string]bool, ended map[string]time.Time, now time.Time) {
	for f, at := range ended {
		if now.Before(at.Add(releaseGrace)) {
			wanted[f] = true
		} else {
			delete(ended, f)
		}
	}
}
