// Package stopinfo keeps halte facilities from the stop API, refreshed by a
// daily sync and stored so restarts don't depend on the upstream. GTFS stays
// authoritative for which stops exist; this only adds details to them.
package stopinfo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lscythe/jalvio-service/internal/atomicfile"
	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/stopapi"
)

// MaxLookups bounds the detail requests for halte the name searches missed.
// A healthy run needs a handful, for names without vowels.
const MaxLookups = 20

// A run is rejected when it matches fewer halte, or finds fewer halte with
// facilities than the previous snapshot, than these percentages.
const (
	minMatchedPercent    = 95
	minFacilitiesPercent = 80
)

// The upstream name search is a substring match that is not limited by
// distance, so searching each vowel reaches nearly every halte.
var sweep = []string{"a", "e", "i", "o", "u"}

type API interface {
	SearchStops(ctx context.Context, query string, near geo.Point) ([]stopapi.Stop, error)
	Stop(ctx context.Context, id string) (stopapi.Stop, error)
}

type Catalogs interface {
	Current() *catalog.Catalog
}

type Snapshot struct {
	SyncedAt time.Time       `json:"synced_at"`
	Stops    map[string]Info `json:"stops"`
}

type Info struct {
	PlatformCode string     `json:"platform_code,omitempty"`
	Description  string     `json:"description,omitempty"`
	Facilities   []Facility `json:"facilities,omitempty"`
}

// Facility pairs a stable key, which clients map to an icon, with the
// upstream title to show when they don't know the key.
type Facility struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type Status struct {
	SyncedAt  time.Time
	CheckedAt time.Time
	Error     string
	// Requests, Matched and Unmatched describe the last run.
	Requests  int
	Matched   int
	Unmatched int
}

type Config struct {
	Path string
	// SearchGap and DetailGap space out upstream requests.
	SearchGap time.Duration
	DetailGap time.Duration
}

type Syncer struct {
	api      API
	catalogs Catalogs
	cfg      Config
	logger   *slog.Logger
	current  atomic.Pointer[Snapshot]

	mu     sync.Mutex
	status Status
}

func New(api API, catalogs Catalogs, cfg Config, logger *slog.Logger) *Syncer {
	return &Syncer{api: api, catalogs: catalogs, cfg: cfg, logger: logger}
}

// Current returns the published snapshot, or nil before the first sync.
func (s *Syncer) Current() *Snapshot { return s.current.Load() }

func (s *Syncer) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Load publishes the stored snapshot. A missing file is not an error.
func (s *Syncer) Load() error {
	data, err := os.ReadFile(filepath.Clean(s.cfg.Path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("stored snapshot %s: %w", s.cfg.Path, err)
	}
	s.publish(&snap)
	return nil
}

// Sync fetches every halte record and publishes the result if it passes the
// sanity checks. Otherwise the last good snapshot stays.
func (s *Syncer) Sync(ctx context.Context) error {
	r, err := s.collect(ctx)
	if err == nil {
		err = s.check(r)
	}
	if err == nil {
		err = s.store(r.snapshot)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.CheckedAt = time.Now().UTC()
	s.status.Requests, s.status.Matched, s.status.Unmatched = r.requests, r.matched, r.unmatched
	if err != nil {
		s.status.Error = err.Error()
		return err
	}
	s.status.Error = ""
	s.logger.Info("stop facilities synced", "requests", r.requests, "matched", r.matched, "unmatched", r.unmatched)
	return nil
}

type run struct {
	snapshot  *Snapshot
	stations  int
	requests  int
	matched   int
	unmatched int
}

func (s *Syncer) collect(ctx context.Context) (run, error) {
	c := s.catalogs.Current()
	if c == nil {
		return run{}, errors.New("catalog not loaded")
	}
	stations, center := stationsOf(c)
	r := run{stations: len(stations)}

	found := map[string]stopapi.Stop{}
	for i, query := range sweep {
		if i > 0 {
			if err := pause(ctx, s.cfg.SearchGap); err != nil {
				return r, err
			}
		}
		stops, err := s.api.SearchStops(ctx, query, center)
		r.requests++
		if err != nil {
			return r, fmt.Errorf("search %q: %w", query, err)
		}
		for _, st := range stops {
			found[st.ID] = st
		}
	}

	lookups := 0
	for _, id := range stations {
		if _, ok := found[id]; ok || lookups == MaxLookups {
			continue
		}
		if lookups > 0 {
			if err := pause(ctx, s.cfg.DetailGap); err != nil {
				return r, err
			}
		}
		lookups++
		r.requests++
		// The detail of a halte is its parent station's record, under another ID.
		st, err := s.api.Stop(ctx, id)
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		if err != nil {
			s.logger.Debug("halte lookup failed", "stop_id", id, "error", err)
			continue
		}
		found[id] = st
	}

	for _, id := range stations {
		if _, ok := found[id]; ok {
			r.matched++
		}
	}
	r.unmatched = len(stations) - r.matched

	r.snapshot = &Snapshot{SyncedAt: time.Now().UTC(), Stops: map[string]Info{}}
	for id, st := range found {
		if _, known := c.Stop(id); !known {
			continue
		}
		if info := infoOf(st); !info.empty() {
			r.snapshot.Stops[id] = info
		}
	}
	return r, nil
}

func (s *Syncer) check(r run) error {
	if r.matched*100 < r.stations*minMatchedPercent {
		return fmt.Errorf("matched %d of %d halte, below %d%%", r.matched, r.stations, minMatchedPercent)
	}
	if prev := s.current.Load(); prev != nil {
		got, had := withFacilities(r.snapshot), withFacilities(prev)
		if got*100 < had*minFacilitiesPercent {
			return fmt.Errorf("%d halte with facilities, previously %d", got, had)
		}
	}
	return nil
}

func (s *Syncer) store(snap *Snapshot) error {
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.cfg.Path, data); err != nil {
		return fmt.Errorf("store snapshot: %w", err)
	}
	s.publish(snap)
	return nil
}

func (s *Syncer) publish(snap *Snapshot) {
	s.current.Store(snap)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.SyncedAt = snap.SyncedAt
}

// stationsOf returns the station IDs in order and their mean position.
func stationsOf(c *catalog.Catalog) ([]string, geo.Point) {
	var ids []string
	var center geo.Point
	for _, st := range c.Stops() {
		if st.Kind != catalog.Station {
			continue
		}
		ids = append(ids, st.ID)
		center.Lat += st.Lat
		center.Lon += st.Lon
	}
	if n := float64(len(ids)); n > 0 {
		center.Lat /= n
		center.Lon /= n
	}
	return ids, center
}

func infoOf(st stopapi.Stop) Info {
	info := Info{PlatformCode: st.PlatformCode, Description: st.Description}
	seen := map[string]bool{}
	for _, f := range st.Facilities {
		key := FacilityKey(f.Name)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		title := f.Title
		if title == "" {
			title = f.Name
		}
		info.Facilities = append(info.Facilities, Facility{Key: key, Title: strings.TrimSpace(title)})
	}
	return info
}

func (i Info) empty() bool {
	return i.PlatformCode == "" && i.Description == "" && len(i.Facilities) == 0
}

func withFacilities(snap *Snapshot) int {
	n := 0
	for _, info := range snap.Stops {
		if len(info.Facilities) > 0 {
			n++
		}
	}
	return n
}

func pause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
