package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/catalog/catalogtest"
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/httpapi"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
)

// twoRegions serves region a with live buses and region b without a feed.
func twoRegions(t *testing.T) httpapi.Deps {
	t.Helper()
	d := deps(true)
	a := testRegion("a")
	a.Catalogs = fakeCatalogs{status: catalog.Status{Version: "feed-a"}, current: fixtureCatalog(t)}
	a.Buses = liveFleet()
	a.Feed = fakeFeed{mqttfeed.Status{Connected: true}}
	b := testRegion("b")
	b.Catalogs = fakeCatalogs{status: catalog.Status{Version: "feed-b"}, current: fixtureCatalog(t)}
	d.Regions = []httpapi.Region{a, b}
	d.Live = httpapi.LiveConfig{MaxConnections: 10}
	return d
}

// swappableCatalogs lets a test publish a new catalog, as a feed refresh does.
type swappableCatalogs struct {
	mu      sync.Mutex
	current *catalog.Catalog
	status  catalog.Status
}

func (s *swappableCatalogs) Current() *catalog.Catalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

func (s *swappableCatalogs) Status() catalog.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *swappableCatalogs) publish(c *catalog.Catalog, updated time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = c
	s.status = catalog.Status{Version: c.Version(), UpdatedAt: updated}
}

type regionList struct {
	Regions []struct {
		ID       string
		Name     string
		TimeZone string
		Bounds   struct{ SouthWest, NorthEast struct{ Lat, Lng float64 } }
		Live     bool
		Catalog  struct{ URL, Version, UpdatedAt string }
		Shapes   struct{ URL string }
		Map      *struct {
			URL     string
			Bytes   int64
			Updated string
		}
	}
}

func listRegions(t *testing.T, h http.Handler) (regionList, *httptest.ResponseRecorder) {
	t.Helper()
	rec := get(t, h, "/v1/regions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/regions = %d %s", rec.Code, rec.Body)
	}
	var list regionList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	return list, rec
}

func TestRegionList(t *testing.T) {
	d := twoRegions(t)
	d.Regions[0].Map = &httpapi.Map{URL: "https://maps.example.com/a.pmtiles", Bytes: 142000000, Updated: "2026-10-30"}
	h := httpapi.New(d)
	list, _ := listRegions(t, h)
	if len(list.Regions) != 2 {
		t.Fatalf("regions = %+v, want a and b", list.Regions)
	}
	a, b := list.Regions[0], list.Regions[1]
	bounds := fixtureCatalog(t).Bounds()
	if a.ID != "a" || a.Name != "Region a" || a.TimeZone != "Asia/Jakarta" || !a.Live || b.ID != "b" || b.Live {
		t.Errorf("regions = %+v, want a live and b without a feed", list.Regions)
	}
	if a.Bounds.SouthWest.Lat != bounds.South || a.Bounds.SouthWest.Lng != bounds.West || a.Bounds.NorthEast.Lat != bounds.North || a.Bounds.NorthEast.Lng != bounds.East {
		t.Errorf("a bounds = %+v, want the catalog's %+v", a.Bounds, bounds)
	}
	if a.Catalog.URL != "/v1/regions/a/catalog" || a.Shapes.URL != "/v1/regions/a/catalog/shapes" {
		t.Errorf("a links = %+v %+v", a.Catalog, a.Shapes)
	}
	if a.Map == nil || *a.Map != (struct {
		URL     string
		Bytes   int64
		Updated string
	}{"https://maps.example.com/a.pmtiles", 142000000, "2026-10-30"}) {
		t.Errorf("a map = %+v", a.Map)
	}
	if b.Map != nil {
		t.Errorf("b map = %+v, want none", b.Map)
	}
}

func TestRegionListVersionMatchesTheCatalogETag(t *testing.T) {
	d := twoRegions(t)
	catalogs := &swappableCatalogs{}
	catalogs.publish(fixtureCatalog(t), startedAt)
	d.Regions[0].Catalogs = catalogs
	h := httpapi.New(d)

	list, rec := listRegions(t, h)
	etag := get(t, h, "/v1/regions/a/catalog", nil).Header().Get("ETag")
	if etag == "" || list.Regions[0].Catalog.Version != etag || list.Regions[0].Catalog.UpdatedAt != "2026-10-12T03:00:00Z" {
		t.Fatalf("catalog = %+v, want version %s updated 2026-10-12T03:00:00Z", list.Regions[0].Catalog, etag)
	}

	withoutFares, err := catalog.Parse(catalogtest.Feed(t, nil, "fare_attributes.txt", "fare_rules.txt"))
	if err != nil {
		t.Fatal(err)
	}
	catalogs.publish(withoutFares, startedAt.Add(time.Hour))
	refreshed, rec2 := listRegions(t, h)
	newETag := get(t, h, "/v1/regions/a/catalog", nil).Header().Get("ETag")
	if newETag == etag || refreshed.Regions[0].Catalog.Version != newETag {
		t.Errorf("after a refresh: version %s, catalog ETag %s (was %s), want them equal and new", refreshed.Regions[0].Catalog.Version, newETag, etag)
	}
	if rec2.Header().Get("ETag") == rec.Header().Get("ETag") {
		t.Error("the list's ETag did not change with the catalog")
	}
}

func TestRegionListConditional(t *testing.T) {
	h := httpapi.New(twoRegions(t))
	_, rec := listRegions(t, h)
	etag := rec.Header().Get("ETag")
	if notModified := get(t, h, "/v1/regions", http.Header{"If-None-Match": {etag}}); notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Errorf("conditional = %d %q, want bodyless 304", notModified.Code, notModified.Body)
	}
	if gz := get(t, h, "/v1/regions", http.Header{"Accept-Encoding": {"gzip"}}); gz.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", gz.Header().Get("Content-Encoding"))
	}
}

func TestRegionListBeforeEveryCatalogLoads(t *testing.T) {
	d := twoRegions(t)
	d.Regions[1].Catalogs = fakeCatalogs{}
	rec := get(t, httpapi.New(d), "/v1/regions", nil)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Errorf("GET /v1/regions = %d %q, want a 503 problem", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestUnknownRegion(t *testing.T) {
	h := httpapi.New(twoRegions(t))
	for _, path := range []string{
		"/v1/regions/nowhere/catalog",
		"/v1/regions/A/catalog",
		"/v1/regions/a/",
		"/v1/regions/nowhere/buses/TST-001",
		"/v1/regions/nowhere/live",
	} {
		rec := get(t, h, path, nil)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("GET %s = %d %q, want a 404 problem", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var refused *handshakeError
	if _, err := dialLive(t, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/regions/nowhere/live", nil); !errors.As(err, &refused) || refused.status != http.StatusNotFound {
		t.Errorf("dial an unknown region: %v, want %d", err, http.StatusNotFound)
	}
}

func TestStatusListsEveryRegion(t *testing.T) {
	rec := get(t, httpapi.New(twoRegions(t)), "/v1/status", nil)
	var status struct {
		Regions []struct {
			ID      string
			Catalog struct{ Version string }
			Feed    struct{ State string }
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Regions) != 2 || status.Regions[0].ID != "a" || status.Regions[1].ID != "b" {
		t.Fatalf("regions = %+v, want a then b", status.Regions)
	}
	if status.Regions[0].Feed.State != "connected" || status.Regions[1].Feed.State != "disabled" {
		t.Errorf("feed states = %q, %q, want connected, disabled", status.Regions[0].Feed.State, status.Regions[1].Feed.State)
	}
	if status.Regions[0].Catalog.Version != "feed-a" || status.Regions[1].Catalog.Version != "feed-b" {
		t.Errorf("catalog versions = %+v, want each region's own", status.Regions)
	}
}

func TestRegionsAreSeparate(t *testing.T) {
	d := twoRegions(t)
	d.Regions[1].Catalogs = fakeCatalogs{}
	h := httpapi.New(d)
	if rec := get(t, h, "/v1/regions/a/catalog", nil); rec.Code != http.StatusOK {
		t.Errorf("a catalog = %d, want 200", rec.Code)
	}
	if rec := get(t, h, "/v1/regions/b/catalog", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("b catalog without one loaded = %d, want 503", rec.Code)
	}

	d.Regions[1].Catalogs = fakeCatalogs{current: fixtureCatalog(t)}
	h = httpapi.New(d)
	count := func(path string) (int, string) {
		var resp struct {
			Feed  string
			Buses []json.RawMessage
		}
		rec := get(t, h, path, nil)
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("GET %s: %v (%s)", path, err, rec.Body)
		}
		return len(resp.Buses), resp.Feed
	}
	if n, feed := count("/v1/regions/a/buses?bbox=106.6,-6.4,107.0,-6.0"); n == 0 || feed != "connected" {
		t.Errorf("a buses = %d (%s), want a's live buses", n, feed)
	}
	if n, feed := count("/v1/regions/b/buses?bbox=106.6,-6.4,107.0,-6.0"); n != 0 || feed != "disabled" {
		t.Errorf("b buses = %d (%s), want none from a disabled feed", n, feed)
	}
}

func TestLiveDemandStaysInItsRegion(t *testing.T) {
	d := twoRegions(t)
	demandA, demandB := newRecordingDemand(), newRecordingDemand()
	d.Regions[0].Demand = demandA
	d.Regions[0].Buses = quietFleet()
	d.Regions[1].Demand = demandB
	d.Regions[1].Buses = fleet.New()
	srv := httptest.NewServer(httpapi.New(d))
	t.Cleanup(srv.Close)

	conn := mustDialLive(t, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/regions/a/live")
	readFrame(t, conn)
	subscribe(t, conn, `{"type":"subscribe","routes":["1"]}`)
	readBuses(t, conn, "snapshot")
	if len(demandA.held()) != 1 || len(demandB.held()) != 0 {
		t.Errorf("holds a = %v, b = %v, want one on a only", demandA.held(), demandB.held())
	}
}

func TestLiveConnectionLimitIsShared(t *testing.T) {
	d := twoRegions(t)
	d.Regions[0].Buses = quietFleet()
	d.Live.MaxConnections = 1
	srv := httptest.NewServer(httpapi.New(d))
	t.Cleanup(srv.Close)
	base := "ws" + strings.TrimPrefix(srv.URL, "http")

	mustDialLive(t, base+"/v1/regions/a/live")
	var refused *handshakeError
	if _, err := dialLive(t, base+"/v1/regions/b/live", nil); !errors.As(err, &refused) || refused.status != http.StatusServiceUnavailable {
		t.Errorf("connection in another region: %v, want %d", err, http.StatusServiceUnavailable)
	}
}
