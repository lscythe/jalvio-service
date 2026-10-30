package httpapi_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/catalog/catalogtest"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/httpapi"
	"github.com/lscythe/jalvio-service/internal/stopinfo"
)

var facilities = &stopinfo.Snapshot{
	SyncedAt: time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC),
	Stops: map[string]stopinfo.Info{
		"H00002P": {Facilities: []stopinfo.Facility{{Key: "toilet", Title: "Toilet"}, {Key: "lift", Title: "Lift"}}},
		"B00003P": {PlatformCode: "B", Description: "In front of the mall"},
	},
}

func fixtureCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Parse(catalogtest.Feed(t, nil))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func catalogHandler(c *catalog.Catalog, snap *stopinfo.Snapshot) http.Handler {
	d := deps(true)
	d.Regions[0].Catalogs = fakeCatalogs{current: c}
	d.Regions[0].StopInfo = fakeStopInfo{current: snap}
	return httpapi.New(d)
}

func get(t *testing.T, h http.Handler, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	return serveRequest(t, h, req)
}

func TestCatalog(t *testing.T) {
	c := fixtureCatalog(t)
	rec := get(t, catalogHandler(c, facilities), regionPath+"/catalog", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	golden, err := os.ReadFile("testdata/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := json.Compact(&want, bytes.ReplaceAll(golden, []byte("FEED_VERSION"), []byte(c.Version()))); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != want.String() {
		t.Errorf("body = %s\nwant   %s", got, want.String())
	}
	for header, want := range map[string]string{
		"Content-Type":  "application/json",
		"Cache-Control": "no-cache",
		"Vary":          "Accept-Encoding",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestCatalogWithoutFacilities(t *testing.T) {
	for name, info := range map[string]httpapi.StopInfo{
		"sync disabled":    nil,
		"not synced yet":   fakeStopInfo{},
		"synced, no stops": fakeStopInfo{current: &stopinfo.Snapshot{SyncedAt: facilities.SyncedAt}},
	} {
		t.Run(name, func(t *testing.T) {
			d := deps(true)
			d.Regions[0].Catalogs = fakeCatalogs{current: fixtureCatalog(t)}
			d.Regions[0].StopInfo = info
			rec := get(t, httpapi.New(d), regionPath+"/catalog", nil)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if strings.Contains(rec.Body.String(), `"facilities"`) {
				t.Errorf("body has facilities: %s", rec.Body.String())
			}
		})
	}
}

func TestShapes(t *testing.T) {
	c := fixtureCatalog(t)
	rec := get(t, catalogHandler(c, facilities), regionPath+"/catalog/shapes", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got struct {
		FeedVersion string `json:"feedVersion"`
		Shapes      []struct {
			PatternID string `json:"patternId"`
			Polyline  string `json:"polyline"`
		} `json:"shapes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.FeedVersion != c.Version() {
		t.Errorf("feedVersion = %q, want %q", got.FeedVersion, c.Version())
	}
	if len(got.Shapes) != len(c.Trips()) {
		t.Fatalf("%d shapes, want %d", len(got.Shapes), len(c.Trips()))
	}
	for i, trip := range c.Trips() {
		if s := got.Shapes[i]; s.PatternID != trip.ID || s.Polyline != geo.EncodePolyline(trip.Shape) {
			t.Errorf("shape %d = %+v, want %s %q", i, s, trip.ID, geo.EncodePolyline(trip.Shape))
		}
	}
}

func TestCatalogDocumentsAreConditional(t *testing.T) {
	for _, path := range []string{regionPath + "/catalog", regionPath + "/catalog/shapes"} {
		t.Run(path, func(t *testing.T) {
			h := catalogHandler(fixtureCatalog(t), facilities)
			etag := get(t, h, path, nil).Header().Get("ETag")
			if etag == "" {
				t.Fatal("no ETag")
			}

			rec := get(t, h, path, http.Header{"If-None-Match": {`"other", ` + etag}})
			if rec.Code != http.StatusNotModified {
				t.Fatalf("matching If-None-Match: status = %d, want %d", rec.Code, http.StatusNotModified)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("304 body = %q, want empty", rec.Body.String())
			}
			if got := rec.Header().Get("ETag"); got != etag {
				t.Errorf("304 ETag = %q, want %q", got, etag)
			}

			if rec := get(t, h, path, http.Header{"If-None-Match": {`"other"`}}); rec.Code != http.StatusOK {
				t.Errorf("stale If-None-Match: status = %d, want %d", rec.Code, http.StatusOK)
			}
		})
	}
}

func TestCatalogETagFollowsTheData(t *testing.T) {
	c := fixtureCatalog(t)
	resynced := &stopinfo.Snapshot{SyncedAt: facilities.SyncedAt.Add(24 * time.Hour), Stops: facilities.Stops}
	other, err := catalog.Parse(catalogtest.Feed(t, map[string]string{"fare_attributes.txt": "fare_id,price,currency_type\nFP,4000,IDR\nGR,0,IDR\n"}))
	if err != nil {
		t.Fatal(err)
	}
	etag := func(c *catalog.Catalog, snap *stopinfo.Snapshot, path string) string {
		return get(t, catalogHandler(c, snap), path, nil).Header().Get("ETag")
	}

	first := etag(c, facilities, regionPath+"/catalog")
	if again := etag(c, facilities, regionPath+"/catalog"); again != first {
		t.Error("catalog ETag differs for the same data")
	}
	if etag(c, resynced, regionPath+"/catalog") == first {
		t.Error("catalog ETag unchanged after a facilities sync")
	}
	if etag(other, facilities, regionPath+"/catalog") == first {
		t.Error("catalog ETag unchanged for another feed")
	}
	if etag(c, facilities, regionPath+"/catalog/shapes") != etag(c, resynced, regionPath+"/catalog/shapes") {
		t.Error("shapes ETag changed with facilities")
	}
}

func TestCatalogIsGzipped(t *testing.T) {
	h := catalogHandler(fixtureCatalog(t), facilities)
	plain := get(t, h, regionPath+"/catalog", nil)
	rec := get(t, h, regionPath+"/catalog", http.Header{"Accept-Encoding": {"br, gzip"}})

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, plain.Body.Bytes()) {
		t.Error("gzipped body differs from the plain body")
	}
	if got, want := rec.Header().Get("ETag"), plain.Header().Get("ETag"); got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}
}

// TestCatalogRealFeed checks the documents built from a local copy of the
// official feed against the contract when JALVIO_GTFS_ZIP points at one.
func TestCatalogRealFeed(t *testing.T) {
	path := os.Getenv("JALVIO_GTFS_ZIP")
	if path == "" {
		t.Skip("JALVIO_GTFS_ZIP not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	h := catalogHandler(c, nil)
	for _, target := range []string{regionPath + "/catalog", regionPath + "/catalog/shapes"} {
		plain := get(t, h, target, nil)
		gzipped := get(t, h, target, http.Header{"Accept-Encoding": {"gzip"}})
		t.Logf("%s: %d bytes, %d gzipped", target, plain.Body.Len(), gzipped.Body.Len())
	}
}

func TestCatalogBeforeTheFirstFeed(t *testing.T) {
	for _, path := range []string{regionPath + "/catalog", regionPath + "/catalog/shapes"} {
		rec := get(t, catalogHandler(nil, nil), path, nil)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want %d", path, rec.Code, http.StatusServiceUnavailable)
		}
		want := problem{Type: "about:blank", Title: "Service Unavailable", Status: http.StatusServiceUnavailable}
		if got := decodeProblem(t, rec); got != want {
			t.Errorf("%s: problem = %+v, want %+v", path, got, want)
		}
	}
}
