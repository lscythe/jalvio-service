package catalog_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/catalog/catalogtest"
)

type fetchResult struct {
	data []byte
	err  error
}

// fakeFetcher returns its results in order, repeating the last one.
type fakeFetcher struct {
	mu      sync.Mutex
	results []fetchResult
}

func (f *fakeFetcher) Fetch(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.results[0]
	if len(f.results) > 1 {
		f.results = f.results[1:]
	}
	return r.data, r.err
}

func newSource(t *testing.T, results ...fetchResult) (*catalog.Source, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gtfs.zip")
	return catalog.NewSource(&fakeFetcher{results: results}, path, slog.New(slog.DiscardHandler)), path
}

func mustParse(t *testing.T, data []byte) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSourceLoad(t *testing.T) {
	feed := catalogtest.Feed(t, nil)

	t.Run("missing file", func(t *testing.T) {
		s, _ := newSource(t, fetchResult{})
		if err := s.Load(); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if s.Current() != nil {
			t.Error("Current is set without a stored feed")
		}
	})

	t.Run("stored feed", func(t *testing.T) {
		s, path := newSource(t, fetchResult{})
		if err := os.WriteFile(path, feed, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.Load(); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got, want := s.Current().Version(), mustParse(t, feed).Version(); got != want {
			t.Errorf("version = %s, want %s", got, want)
		}
	})

	t.Run("corrupt feed", func(t *testing.T) {
		s, path := newSource(t, fetchResult{})
		if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.Load(); err == nil {
			t.Fatal("Load succeeded, want error")
		}
		if s.Current() != nil {
			t.Error("Current is set from a corrupt feed")
		}
	})
}

func TestSourceRefreshPublishesAndStoresNewFeed(t *testing.T) {
	feed := catalogtest.Feed(t, nil)
	s, path := newSource(t, fetchResult{data: feed})

	if err := s.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	want := mustParse(t, feed).Version()
	if got := s.Current().Version(); got != want {
		t.Errorf("version = %s, want %s", got, want)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, feed) {
		t.Error("stored feed differs from the fetched one")
	}
	st := s.Status()
	if st.Version != want || st.UpdatedAt.IsZero() || st.CheckedAt.IsZero() || st.Error != "" {
		t.Errorf("status = %+v, want version %s with update and check times and no error", st, want)
	}
}

func TestSourceRefreshKeepsUnchangedFeed(t *testing.T) {
	feed := catalogtest.Feed(t, nil)
	s, _ := newSource(t, fetchResult{data: feed})
	if err := s.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := s.Current()
	updated := s.Status().UpdatedAt

	if err := s.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if s.Current() != first {
		t.Error("unchanged feed replaced the published catalog")
	}
	if !s.Status().UpdatedAt.Equal(updated) {
		t.Error("unchanged feed moved UpdatedAt")
	}
}

func TestSourceRefreshFailureKeepsCurrentCatalog(t *testing.T) {
	feed := catalogtest.Feed(t, nil)
	invalid := catalogtest.Feed(t, nil, "stop_times.txt")

	tests := []struct {
		name string
		next fetchResult
	}{
		{name: "fetch error", next: fetchResult{err: errors.New("connection reset")}},
		{name: "invalid feed", next: fetchResult{data: invalid}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, path := newSource(t, fetchResult{data: feed}, tt.next)
			if err := s.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			good := s.Current()

			if err := s.Refresh(t.Context()); err == nil {
				t.Fatal("Refresh succeeded, want error")
			}
			if s.Current() != good {
				t.Error("failed refresh replaced the published catalog")
			}
			if s.Status().Error == "" {
				t.Error("status does not report the failure")
			}
			stored, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(stored, feed) {
				t.Error("failed refresh overwrote the stored feed")
			}
		})
	}
}

func TestSourceRefreshSuccessClearsError(t *testing.T) {
	feed := catalogtest.Feed(t, nil)
	s, _ := newSource(t, fetchResult{err: errors.New("timeout")}, fetchResult{data: feed})

	_ = s.Refresh(t.Context())
	if err := s.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if st := s.Status(); st.Error != "" {
		t.Errorf("Error = %q after a successful refresh, want empty", st.Error)
	}
}

func TestSourceRunRetriesUntilLoaded(t *testing.T) {
	feed := catalogtest.Feed(t, nil)
	s, _ := newSource(t, fetchResult{err: errors.New("timeout")}, fetchResult{data: feed})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		s.Run(ctx, time.Hour, time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for s.Current() == nil {
		if time.Now().After(deadline) {
			t.Fatal("catalog not loaded after retry")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestLoadedClosesOnFirstPublish(t *testing.T) {
	feed := catalogtest.Feed(t, nil)
	s, _ := newSource(t, fetchResult{data: feed})

	select {
	case <-s.Loaded():
		t.Fatal("Loaded closed before a catalog was published")
	default:
	}
	for range 2 {
		if err := s.Refresh(t.Context()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	}
	select {
	case <-s.Loaded():
	default:
		t.Fatal("Loaded still open after a catalog was published")
	}
}
