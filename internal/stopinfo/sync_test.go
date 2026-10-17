package stopinfo_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/catalog/catalogtest"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/stopapi"
	"github.com/lscythe/jalvio-service/internal/stopinfo"
)

// fakeAPI answers name searches with a case-insensitive substring match, like
// the upstream, and serves details by ID.
type fakeAPI struct {
	mu        sync.Mutex
	stops     []stopapi.Stop
	details   map[string]stopapi.Stop
	failQuery string
	searches  []string
	nears     []geo.Point
	lookups   []string
}

func (f *fakeAPI) SearchStops(_ context.Context, query string, near geo.Point) ([]stopapi.Stop, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, query)
	f.nears = append(f.nears, near)
	if query == f.failQuery {
		return nil, errors.New("unexpected status 500 Internal Server Error")
	}
	var out []stopapi.Stop
	for _, s := range f.stops {
		if strings.Contains(strings.ToLower(s.Name), query) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeAPI) Stop(_ context.Context, id string) (stopapi.Stop, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, id)
	s, ok := f.details[id]
	if !ok {
		return stopapi.Stop{}, errors.New("unexpected status 404 Not Found")
	}
	return s, nil
}

func (f *fakeAPI) searchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.searches)
}

// upstreamStops mirrors the fixture feed: stations H00001P Blok M, H00002P
// Bundaran HI, H00003P Kota, and the no-vowel station H00004P PGC added by
// testCatalog, which only a detail lookup finds.
func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		stops: []stopapi.Stop{
			{ID: "H00001P", Name: "Blok M", Facilities: []stopapi.Facility{
				{Name: "toilet", Title: "toilet"},
				{Name: "eskalator", Title: "eskalator"},
			}},
			{ID: "H00002P", Name: "Bundaran HI", Facilities: []stopapi.Facility{
				{Name: "praying area", Title: "area beribadah"},
				{Name: "accessible for all"},
			}},
			{ID: "H00003P", Name: "Kota"},
			{ID: "B00002P", Name: "Pancoran"},
			{ID: "B00003P", Name: "Kuningan Barat", PlatformCode: "B", Description: "in front of the mall"},
			{ID: "H09999P", Name: "Not In The Feed", Facilities: []stopapi.Facility{{Name: "toilet", Title: "toilet"}}},
		},
		details: map[string]stopapi.Stop{
			"H00004P": {ID: "G00099", Name: "PGC", Facilities: []stopapi.Facility{{Name: "lift", Title: "lift"}}},
		},
	}
}

func fixtureStops(t *testing.T, extra ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "catalog", "testdata", "feed", "stops.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(string(raw), "\r\n") + "\n" + strings.Join(extra, "\n") + "\n"
}

type catalogs struct{ c *catalog.Catalog }

func (c catalogs) Current() *catalog.Catalog { return c.c }

func testCatalog(t *testing.T, extraStations ...string) catalogs {
	t.Helper()
	rows := []string{"H00004P,,PGC,,-6.26,106.86,1,,1,,,2,"}
	rows = append(rows, extraStations...)
	c, err := catalog.Parse(catalogtest.Feed(t, map[string]string{"stops.txt": fixtureStops(t, rows...)}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return catalogs{c}
}

func newSyncer(t *testing.T, api stopinfo.API, cats stopinfo.Catalogs, path string) *stopinfo.Syncer {
	t.Helper()
	if path == "" {
		path = filepath.Join(t.TempDir(), "stopinfo.json")
	}
	return stopinfo.New(api, cats, stopinfo.Config{Path: path}, slog.New(slog.DiscardHandler))
}

var wantStops = map[string]stopinfo.Info{
	"H00001P": {Facilities: []stopinfo.Facility{
		{Key: "toilet", Title: "toilet"},
		{Key: "escalator", Title: "eskalator"},
	}},
	"H00002P": {Facilities: []stopinfo.Facility{
		{Key: "praying_area", Title: "area beribadah"},
		{Key: "accessible_for_all", Title: "accessible for all"},
	}},
	"H00004P": {Facilities: []stopinfo.Facility{{Key: "lift", Title: "lift"}}},
	"B00003P": {PlatformCode: "B", Description: "in front of the mall"},
}

func TestSyncBuildsSnapshot(t *testing.T) {
	api := newFakeAPI()
	s := newSyncer(t, api, testCatalog(t), "")

	before := time.Now()
	if err := s.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	snap := s.Current()
	if snap == nil {
		t.Fatal("no snapshot published")
	}
	if !reflect.DeepEqual(snap.Stops, wantStops) {
		t.Errorf("stops =\n%+v\nwant\n%+v", snap.Stops, wantStops)
	}
	if snap.SyncedAt.Before(before) || snap.SyncedAt.Location() != time.UTC {
		t.Errorf("SyncedAt = %v, want a UTC time after %v", snap.SyncedAt, before)
	}
	if want := []string{"a", "e", "i", "o", "u"}; !reflect.DeepEqual(api.searches, want) {
		t.Errorf("searches = %v, want %v", api.searches, want)
	}
	if want := []string{"H00004P"}; !reflect.DeepEqual(api.lookups, want) {
		t.Errorf("lookups = %v, want only the station the searches missed", api.lookups)
	}

	// The searches are centred on the stations so every halte is in reach.
	center := api.nears[0]
	if center.Lat > -6.13 || center.Lat < -6.27 || center.Lon < 106.80 || center.Lon > 106.87 {
		t.Errorf("search centre = %+v, want the middle of the fixture stations", center)
	}

	st := s.Status()
	want := stopinfo.Status{SyncedAt: snap.SyncedAt, CheckedAt: st.CheckedAt, Requests: 6, Matched: 4}
	if st != want || st.CheckedAt.IsZero() {
		t.Errorf("status = %+v, want %+v with a check time", st, want)
	}
}

func TestSnapshotSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stopinfo.json")
	if err := newSyncer(t, newFakeAPI(), testCatalog(t), path).Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	restarted := newSyncer(t, newFakeAPI(), testCatalog(t), path)
	if restarted.Current() != nil {
		t.Fatal("snapshot published before Load")
	}
	if err := restarted.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	snap := restarted.Current()
	if snap == nil || !reflect.DeepEqual(snap.Stops, wantStops) {
		t.Fatalf("loaded snapshot = %+v, want the synced stops", snap)
	}
	if st := restarted.Status(); !st.SyncedAt.Equal(snap.SyncedAt) {
		t.Errorf("status SyncedAt = %v, want %v", st.SyncedAt, snap.SyncedAt)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	missing := newSyncer(t, newFakeAPI(), testCatalog(t), filepath.Join(dir, "missing.json"))
	if err := missing.Load(); err != nil || missing.Current() != nil {
		t.Errorf("Load of a missing file = %v with snapshot %v, want nothing", err, missing.Current())
	}

	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := newSyncer(t, newFakeAPI(), testCatalog(t), corrupt).Load(); err == nil {
		t.Error("Load accepted a corrupt snapshot")
	}

	if err := newSyncer(t, newFakeAPI(), testCatalog(t), dir).Load(); err == nil {
		t.Error("Load of a directory succeeded")
	}
}

func TestSyncKeepsLastGoodSnapshot(t *testing.T) {
	tests := []struct {
		name     string
		breakAPI func(*fakeAPI)
		want     string
	}{
		{
			name:     "search fails",
			breakAPI: func(a *fakeAPI) { a.failQuery = "i" },
			want:     `search "i"`,
		},
		{
			name:     "too few halte matched",
			breakAPI: func(a *fakeAPI) { delete(a.details, "H00004P") },
			want:     "matched 3 of 4 halte",
		},
		{
			name: "facilities disappear",
			breakAPI: func(a *fakeAPI) {
				a.stops[0].Facilities = nil
				a.stops[1].Facilities = nil
			},
			want: "1 halte with facilities, previously 3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stopinfo.json")
			api := newFakeAPI()
			s := newSyncer(t, api, testCatalog(t), path)
			if err := s.Sync(t.Context()); err != nil {
				t.Fatalf("first Sync: %v", err)
			}
			good := s.Current()
			stored, _ := os.ReadFile(path)

			tt.breakAPI(api)
			err := s.Sync(t.Context())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tt.want)
			}
			if s.Current() != good {
				t.Error("bad run replaced the snapshot")
			}
			if now, _ := os.ReadFile(path); string(now) != string(stored) {
				t.Error("bad run replaced the stored snapshot")
			}
			if st := s.Status(); st.Error != err.Error() || !st.SyncedAt.Equal(good.SyncedAt) {
				t.Errorf("status = %+v, want the error and the last good sync time", st)
			}
		})
	}
}

func TestSyncCapsDetailLookups(t *testing.T) {
	var extra []string
	for i := range 25 {
		extra = append(extra, fmt.Sprintf("H1%04dP,,%d,,-6.2,106.8,1,,1,,,2,", i, i))
	}
	api := newFakeAPI()
	s := newSyncer(t, api, testCatalog(t, extra...), "")

	if err := s.Sync(t.Context()); err == nil {
		t.Fatal("Sync succeeded with most halte unmatched")
	}
	if len(api.lookups) != stopinfo.MaxLookups {
		t.Errorf("lookups = %d, want %d", len(api.lookups), stopinfo.MaxLookups)
	}
	if st := s.Status(); st.Matched != 4 || st.Unmatched != 25 {
		t.Errorf("status = %+v, want 4 matched and 25 unmatched", st)
	}
}

func TestSyncNeedsCatalog(t *testing.T) {
	s := newSyncer(t, newFakeAPI(), catalogs{}, "")
	if err := s.Sync(t.Context()); err == nil || !strings.Contains(err.Error(), "catalog") {
		t.Errorf("err = %v, want one about the missing catalog", err)
	}
}

func TestSyncStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := stopinfo.New(newFakeAPI(), testCatalog(t), stopinfo.Config{
		Path:      filepath.Join(t.TempDir(), "stopinfo.json"),
		SearchGap: time.Hour,
	}, slog.New(slog.DiscardHandler))

	if err := s.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestFacilityKey(t *testing.T) {
	tests := map[string]string{
		"toilet":               "toilet",
		"praying area":         "praying_area",
		"accessible for all":   "accessible_for_all",
		"eskalator":            "escalator",
		"Information Center":   "information_center",
		"nursery":              "nursery",
		" Ruang  Laktasi/ASI ": "ruang_laktasi_asi",
	}
	for name, want := range tests {
		if got := stopinfo.FacilityKey(name); got != want {
			t.Errorf("FacilityKey(%q) = %q, want %q", name, got, want)
		}
	}
}
