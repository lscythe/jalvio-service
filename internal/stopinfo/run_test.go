package stopinfo_test

import (
	"context"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/stopinfo"
)

func TestDailyAt(t *testing.T) {
	wib := time.FixedZone("WIB", 7*60*60)
	next := stopinfo.DailyAt(3, 0)
	tests := []struct{ now, want time.Time }{
		{time.Date(2026, 10, 12, 1, 0, 0, 0, wib), time.Date(2026, 10, 12, 3, 0, 0, 0, wib)},
		{time.Date(2026, 10, 12, 3, 0, 0, 0, wib), time.Date(2026, 10, 13, 3, 0, 0, 0, wib)},
		{time.Date(2026, 12, 31, 23, 0, 0, 0, wib), time.Date(2027, 1, 1, 3, 0, 0, 0, wib)},
	}
	for _, tt := range tests {
		if got := next(tt.now); !got.Equal(tt.want) {
			t.Errorf("next(%v) = %v, want %v", tt.now, got, tt.want)
		}
	}
}

func startRun(t *testing.T, s *stopinfo.Syncer, next func(time.Time) time.Time, retryDelay time.Duration) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		s.Run(ctx, next, retryDelay)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})
	return cancel
}

func waitSearches(t *testing.T, api *fakeAPI, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for api.searchCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("searches = %d, want %d", api.searchCount(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func inAnHour(now time.Time) time.Time { return now.Add(time.Hour) }

func TestRunSyncsAtStartupWithoutSnapshot(t *testing.T) {
	api := newFakeAPI()
	s := newSyncer(t, api, testCatalog(t), "")
	startRun(t, s, inAnHour, time.Hour)

	waitSearches(t, api, 5)
	deadline := time.Now().Add(5 * time.Second)
	for s.Current() == nil {
		if time.Now().After(deadline) {
			t.Fatal("startup sync published nothing")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRunWaitsForScheduleWhenSnapshotIsFresh(t *testing.T) {
	api := newFakeAPI()
	s := newSyncer(t, api, testCatalog(t), "")
	if err := s.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	startRun(t, s, func(now time.Time) time.Time { return now.Add(200 * time.Millisecond) }, time.Hour)

	time.Sleep(50 * time.Millisecond)
	if n := api.searchCount(); n != 5 {
		t.Fatalf("searches = %d before the scheduled run, want 5", n)
	}
	waitSearches(t, api, 10)
}

func TestRunRetriesOnceAfterFailure(t *testing.T) {
	api := newFakeAPI()
	api.failQuery = "a"
	s := newSyncer(t, api, testCatalog(t), "")
	startRun(t, s, inAnHour, 10*time.Millisecond)

	waitSearches(t, api, 2)
	time.Sleep(50 * time.Millisecond)
	if n := api.searchCount(); n != 2 {
		t.Errorf("searches = %d, want 2: the failed run and one retry", n)
	}
	if s.Status().Error == "" {
		t.Error("status has no error after failed runs")
	}
}
