package fleet_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/fleet"
)

func receive(t *testing.T, sub *fleet.Subscription) fleet.Batch {
	t.Helper()
	select {
	case b, ok := <-sub.Batches():
		if !ok {
			t.Fatal("subscription closed")
		}
		return b
	case <-time.After(time.Second):
		t.Fatal("no batch received")
	}
	return fleet.Batch{}
}

func assertNoBatch(t *testing.T, sub *fleet.Subscription) {
	t.Helper()
	select {
	case b := <-sub.Batches():
		t.Fatalf("unexpected batch %+v", b)
	default:
	}
}

func TestFlushSendsChangedBuses(t *testing.T) {
	f := fleet.New()
	sub := f.Subscribe()
	defer sub.Close()

	f.Flush()
	assertNoBatch(t, sub)

	f.Apply(update("B-2", "1", monas, t0, nil), t0)
	f.Apply(update("A-1", "1", blokM, t0, nil), t0)
	f.Apply(update("A-1", "1", monas, t0.Add(-time.Second), nil), t0)
	f.Flush()

	b := receive(t, sub)
	if got, want := plates(b.Upserted), []string{"A-1", "B-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("upserted = %v, want %v", got, want)
	}
	if b.Removed != nil {
		t.Errorf("removed = %v, want none", b.Removed)
	}

	f.Flush()
	assertNoBatch(t, sub)
}

func TestFlushReportsStaleAndRemovedBuses(t *testing.T) {
	f := fleet.New()
	f.Apply(update("A-1", "1", monas, t0, nil), t0)
	f.Apply(update("B-2", "1", monas, t0.Add(fleet.RemoveAfter), nil), t0.Add(fleet.RemoveAfter-fleet.StaleAfter))
	sub := f.Subscribe()
	defer sub.Close()

	f.Expire(t0.Add(fleet.RemoveAfter))
	f.Flush()

	b := receive(t, sub)
	if len(b.Upserted) != 1 || b.Upserted[0].Plate != "B-2" || !b.Upserted[0].Stale {
		t.Errorf("upserted = %+v, want B-2 marked stale", b.Upserted)
	}
	if want := []string{"A-1"}; !reflect.DeepEqual(b.Removed, want) {
		t.Errorf("removed = %v, want %v", b.Removed, want)
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	f := fleet.New()
	slow := f.Subscribe()
	fast := f.Subscribe()
	defer fast.Close()

	for i := range fleet.SubscriberBuffer + 1 {
		f.Apply(update("A-1", "1", monas, t0.Add(time.Duration(i)*time.Second), nil), t0)
		f.Flush()
		receive(t, fast)
	}

	for range fleet.SubscriberBuffer {
		receive(t, slow)
	}
	if _, ok := <-slow.Batches(); ok {
		t.Fatal("slow subscription still open")
	}
	if !slow.Dropped() {
		t.Error("Dropped = false for a subscriber that fell behind")
	}
	if fast.Dropped() {
		t.Error("Dropped = true for a subscriber that kept up")
	}
	slow.Close()
}

func TestCloseStopsDelivery(t *testing.T) {
	f := fleet.New()
	sub := f.Subscribe()
	sub.Close()
	sub.Close()

	f.Apply(update("A-1", "1", monas, t0, nil), t0)
	f.Flush()

	if _, ok := <-sub.Batches(); ok {
		t.Error("closed subscription received a batch")
	}
	if sub.Dropped() {
		t.Error("Dropped = true after the subscriber closed it")
	}
}

func TestRunFlushesAndExpires(t *testing.T) {
	f := fleet.New()
	sub := f.Subscribe()
	defer sub.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		f.Run(ctx, time.Millisecond, time.Millisecond)
		close(done)
	}()

	f.Apply(update("A-1", "1", monas, t0, nil), time.Now().Add(-fleet.RemoveAfter))

	deadline := time.After(2 * time.Second)
	for removed := false; !removed; {
		select {
		case b := <-sub.Batches():
			removed = len(b.Removed) == 1
		case <-deadline:
			t.Fatal("Run did not flush the removal")
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
