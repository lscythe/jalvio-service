package fleet

import (
	"context"
	"slices"
	"sync/atomic"
	"time"
)

// SubscriberBuffer is how many batches a subscriber may fall behind before it
// is dropped.
const SubscriberBuffer = 4

// Batch holds the buses that changed since the previous flush: new or moved
// buses, buses that turned stale, and plates that were removed.
type Batch struct {
	Upserted []Bus
	Removed  []string
}

type Subscription struct {
	fleet   *Fleet
	ch      chan Batch
	dropped atomic.Bool
}

// Batches is closed when the subscription ends.
func (s *Subscription) Batches() <-chan Batch { return s.ch }

// Dropped reports whether the subscription ended because it fell behind.
func (s *Subscription) Dropped() bool { return s.dropped.Load() }

func (s *Subscription) Close() {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	s.fleet.unsubscribe(s)
}

func (f *Fleet) Subscribe() *Subscription {
	s := &Subscription{fleet: f, ch: make(chan Batch, SubscriberBuffer)}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribers[s] = struct{}{}
	return s
}

// unsubscribe must be called with f.mu held.
func (f *Fleet) unsubscribe(s *Subscription) {
	if _, ok := f.subscribers[s]; ok {
		delete(f.subscribers, s)
		close(s.ch)
	}
}

// Flush sends the pending changes to every subscriber, dropping any whose
// buffer is full.
func (f *Fleet) Flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pending) == 0 {
		return
	}
	var batch Batch
	for plate, present := range f.pending {
		if b, ok := f.buses[plate]; present && ok {
			batch.Upserted = append(batch.Upserted, b)
		} else if !present {
			batch.Removed = append(batch.Removed, plate)
		}
	}
	clear(f.pending)
	slices.SortFunc(batch.Upserted, byPlate)
	slices.Sort(batch.Removed)

	for s := range f.subscribers {
		select {
		case s.ch <- batch:
		default:
			s.dropped.Store(true)
			f.unsubscribe(s)
		}
	}
}

// Run flushes changes and expires buses on the given intervals until ctx is
// cancelled.
func (f *Fleet) Run(ctx context.Context, flushEvery, expireEvery time.Duration) {
	flush := time.NewTicker(flushEvery)
	defer flush.Stop()
	expire := time.NewTicker(expireEvery)
	defer expire.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-flush.C:
			f.Flush()
		case now := <-expire.C:
			f.Expire(now)
		}
	}
}
