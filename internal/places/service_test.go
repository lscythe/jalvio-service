package places_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/places"
)

type fakeProvider struct {
	mu       sync.Mutex
	queries  []places.Query
	reverses []geo.Point
	err      error
	// release, when set, holds every call until it is closed.
	release chan struct{}
}

func (f *fakeProvider) Search(ctx context.Context, q places.Query) ([]places.Place, error) {
	f.mu.Lock()
	f.queries = append(f.queries, q)
	release, err := f.release, f.err
	f.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return []places.Place{{ID: "osm:N1", Name: q.Text}}, nil
}

func (f *fakeProvider) Reverse(_ context.Context, at geo.Point) (places.Place, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reverses = append(f.reverses, at)
	if f.err != nil {
		return places.Place{}, f.err
	}
	return places.Place{ID: "osm:N2", Position: at}, nil
}

func (f *fakeProvider) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queries) + len(f.reverses)
}

var area = geo.BBox{West: 106.5, South: -6.5, East: 107, North: -6}

var testConfig = places.Config{
	RequestsPerMinute: 600,
	CacheSize:         100,
	CacheTTL:          time.Hour,
	Timeout:           time.Second,
}

func newService(p places.Provider, mutate ...func(*places.Config)) *places.Service {
	cfg := testConfig
	for _, m := range mutate {
		m(&cfg)
	}
	return places.NewService(p, cfg)
}

func TestSearchRejectsShortQueries(t *testing.T) {
	p := &fakeProvider{}
	s := newService(p)
	for _, text := range []string{"", "ab", "   ab  "} {
		if _, err := s.Search(t.Context(), area, text, nil, 5); !errors.Is(err, places.ErrQueryTooShort) {
			t.Errorf("Search(%q) err = %v, want ErrQueryTooShort", text, err)
		}
	}
	if p.calls() != 0 {
		t.Errorf("provider called %d times for short queries", p.calls())
	}
}

func TestSearchBuildsBiasedQuery(t *testing.T) {
	near := geo.Point{Lat: -6.2, Lon: 106.83}
	tests := []struct {
		name  string
		near  *geo.Point
		limit int
		area  geo.BBox
		want  places.Query
	}{
		{
			name: "near the user", near: &near, limit: 5, area: area,
			want: places.Query{Text: "grand mall", Near: &near, Area: area, Limit: 5},
		},
		{
			name: "centre of the given area", limit: 0, area: area,
			want: places.Query{Text: "grand mall", Near: &geo.Point{Lat: -6.25, Lon: 106.75}, Area: area, Limit: places.DefaultLimit},
		},
		{
			name: "no area", limit: 100,
			want: places.Query{Text: "grand mall", Limit: places.MaxLimit},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &fakeProvider{}
			s := places.NewService(p, testConfig)
			if _, err := s.Search(t.Context(), tt.area, "  Grand   MALL ", tt.near, tt.limit); err != nil {
				t.Fatalf("Search: %v", err)
			}
			got := p.queries[0]
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("query = %+v (near %v), want %+v (near %v)", got, got.Near, tt.want, tt.want.Near)
			}
		})
	}
}

func TestSearchIsCached(t *testing.T) {
	p := &fakeProvider{}
	s := newService(p)
	near := geo.Point{Lat: -6.20001, Lon: 106.83001}
	nearby := geo.Point{Lat: -6.20002, Lon: 106.83002}
	elsewhere := geo.Point{Lat: -6.3, Lon: 106.83}

	for _, call := range []struct {
		text string
		near *geo.Point
	}{
		{"Grand Mall", &near},
		{"grand  mall", &nearby},
		{"grand mall", &elsewhere},
		{"grand mall", &elsewhere},
	} {
		got, err := s.Search(t.Context(), area, call.text, call.near, 5)
		if err != nil || len(got) != 1 {
			t.Fatalf("Search = %v, %v", got, err)
		}
	}
	if n := p.calls(); n != 2 {
		t.Errorf("provider calls = %d, want 2: one per rounded location", n)
	}
}

func TestSearchCachesPerArea(t *testing.T) {
	p := &fakeProvider{}
	s := newService(p)
	other := geo.BBox{West: 112.5, South: -7.5, East: 113, North: -7}
	for _, a := range []geo.BBox{area, other, area} {
		if _, err := s.Search(t.Context(), a, "grand mall", nil, 5); err != nil {
			t.Fatalf("Search: %v", err)
		}
	}
	if n := p.calls(); n != 2 {
		t.Errorf("provider calls = %d, want 2: one per area", n)
	}
	if got := p.queries[1].Area; got != other {
		t.Errorf("second query area = %+v, want %+v", got, other)
	}
}

func TestCacheEntriesExpire(t *testing.T) {
	p := &fakeProvider{}
	s := newService(p, func(c *places.Config) { c.CacheTTL = time.Millisecond })

	for range 2 {
		if _, err := s.Search(t.Context(), area, "grand mall", nil, 5); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := p.calls(); n != 2 {
		t.Errorf("provider calls = %d, want 2 after the entry expired", n)
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	p := &fakeProvider{}
	s := newService(p, func(c *places.Config) { c.CacheSize = 2 })

	for _, text := range []string{"aaa", "bbb", "aaa", "ccc", "aaa", "bbb"} {
		if _, err := s.Search(t.Context(), area, text, nil, 5); err != nil {
			t.Fatal(err)
		}
	}
	var texts []string
	for _, q := range p.queries {
		texts = append(texts, q.Text)
	}
	if want := []string{"aaa", "bbb", "ccc", "bbb"}; !reflect.DeepEqual(texts, want) {
		t.Errorf("upstream queries = %v, want %v", texts, want)
	}
}

func TestIdenticalSearchesShareOneCall(t *testing.T) {
	p := &fakeProvider{release: make(chan struct{})}
	s := newService(p)

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := s.Search(t.Context(), area, "grand mall", nil, 5); err != nil {
				t.Errorf("Search: %v", err)
			}
		})
	}
	deadline := time.Now().Add(5 * time.Second)
	for p.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(p.release)
	wg.Wait()
	if n := p.calls(); n != 1 {
		t.Errorf("provider calls = %d, want 1", n)
	}
}

func TestRateLimitDeniesWithoutCallingUpstream(t *testing.T) {
	p := &fakeProvider{}
	s := newService(p, func(c *places.Config) { c.RequestsPerMinute = 1 })

	if _, err := s.Search(t.Context(), area, "grand mall", nil, 5); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	if _, err := s.Search(t.Context(), area, "other place", nil, 5); !errors.Is(err, places.ErrRateLimited) {
		t.Errorf("second Search err = %v, want ErrRateLimited", err)
	}
	if _, err := s.Reverse(t.Context(), geo.Point{Lat: -6.2, Lon: 106.8}); !errors.Is(err, places.ErrRateLimited) {
		t.Errorf("Reverse err = %v, want ErrRateLimited", err)
	}
	if _, err := s.Search(t.Context(), area, "grand mall", nil, 5); err != nil {
		t.Errorf("cached Search: %v, want a free cache hit", err)
	}
	if n := p.calls(); n != 1 {
		t.Errorf("provider calls = %d, want 1", n)
	}
}

func TestErrorsAreNotCached(t *testing.T) {
	p := &fakeProvider{err: errors.New("unexpected status 502 Bad Gateway")}
	s := newService(p)

	if _, err := s.Search(t.Context(), area, "grand mall", nil, 5); err == nil {
		t.Fatal("Search succeeded, want the upstream error")
	}
	p.err = nil
	if _, err := s.Search(t.Context(), area, "grand mall", nil, 5); err != nil {
		t.Fatalf("Search after recovery: %v", err)
	}
	if n := p.calls(); n != 2 {
		t.Errorf("provider calls = %d, want 2", n)
	}
}

func TestUpstreamCallsTimeOut(t *testing.T) {
	p := &fakeProvider{release: make(chan struct{})}
	defer close(p.release)
	s := newService(p, func(c *places.Config) { c.Timeout = 10 * time.Millisecond })

	if _, err := s.Search(t.Context(), area, "grand mall", nil, 5); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestReverseIsCachedByRoundedPoint(t *testing.T) {
	p := &fakeProvider{}
	s := newService(p)

	for _, at := range []geo.Point{
		{Lat: -6.194401, Lon: 106.822901},
		{Lat: -6.194402, Lon: 106.822902},
		{Lat: -6.1950, Lon: 106.8229},
	} {
		if _, err := s.Reverse(t.Context(), at); err != nil {
			t.Fatalf("Reverse: %v", err)
		}
	}
	if n := p.calls(); n != 2 {
		t.Errorf("provider calls = %d, want 2", n)
	}

	p.err = places.ErrNotFound
	if _, err := s.Reverse(t.Context(), geo.Point{Lat: -6.3, Lon: 106.9}); !errors.Is(err, places.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
