package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lscythe/jalvio-service/internal/catalog/catalogtest"
	"github.com/lscythe/jalvio-service/internal/config"
)

var discard = slog.New(slog.DiscardHandler)

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func waitDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not return after cancel")
	}
}

func TestServeDrainsOnCancel(t *testing.T) {
	ln := listen(t)
	url := "http://" + ln.Addr().String() + "/"
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	var draining atomic.Bool
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, handler, &draining, discard) }()

	if got := get(t, url); got != http.StatusNoContent {
		t.Fatalf("GET = %d, want %d", got, http.StatusNoContent)
	}

	cancel()
	waitDone(t, done)
	if !draining.Load() {
		t.Error("draining not set during shutdown")
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("server still accepting requests after shutdown")
	}
}

// UnmarshalJSON reads the first region of /v1/status, the one these tests serve.
func (s *serviceStatus) UnmarshalJSON(b []byte) error {
	var body struct{ Regions []json.RawMessage }
	if err := json.Unmarshal(b, &body); err != nil || len(body.Regions) == 0 {
		return fmt.Errorf("status without regions: %w", err)
	}
	type region serviceStatus
	return json.Unmarshal(body.Regions[0], (*region)(s))
}

type serviceStatus struct {
	Catalog struct {
		Version string `json:"version"`
	} `json:"catalog"`
	Feed struct {
		State      string `json:"state"`
		Filters    *int   `json:"filters"`
		WholeFleet *bool  `json:"wholeFleet"`
	} `json:"feed"`
	StopFacilities struct {
		State string `json:"state"`
		Error string `json:"error"`
	} `json:"stopFacilities"`
}

// testRegion is one entry of the regions file startService writes; an empty
// gtfs.url points at the fixture feed.
type testRegion struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	TimeZone       string            `json:"timeZone"`
	GTFS           map[string]string `json:"gtfs"`
	Live           map[string]string `json:"live,omitempty"`
	StopFacilities map[string]string `json:"stopFacilities,omitempty"`
}

func jakarta() testRegion {
	return testRegion{ID: "jakarta", Name: "Jakarta", TimeZone: "Asia/Jakarta"}
}

func writeRegions(t *testing.T, path string, regions []testRegion) {
	t.Helper()
	b, err := json.Marshal(map[string][]testRegion{"regions": regions})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// launchService runs the service until ctx is cancelled or the test ends,
// serving regions (Jakarta when none are given) against a local copy of the
// fixture feed and the upstreams set in cfg.
func launchService(t *testing.T, ctx context.Context, cfg config.Config, regions ...testRegion) (base, dataDir string) {
	t.Helper()
	feed := catalogtest.Feed(t, nil)
	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(feed)
	}))
	t.Cleanup(feedServer.Close)

	if len(regions) == 0 {
		regions = []testRegion{jakarta()}
	}
	for i := range regions {
		if regions[i].GTFS["url"] == "" {
			regions[i].GTFS = map[string]string{"url": feedServer.URL, "refresh": "1h"}
		}
	}
	dir := t.TempDir()
	dataDir = filepath.Join(dir, "data")
	cfg.DataDir = dataDir
	cfg.RegionsFile = filepath.Join(dir, "regions.json")
	writeRegions(t, cfg.RegionsFile, regions)
	if cfg.LegalFile == "" {
		cfg.LegalFile = filepath.Join(dataDir, "legal.json")
	}
	cfg.MaxLiveConnections = 10
	cfg.PhotonUserAgent = "jalvio-test"
	cfg.PlacesRequestsPerMinute = 60
	ln := listen(t)
	base = "http://" + ln.Addr().String()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runService(ctx, cfg, ln, discard) }()
	t.Cleanup(func() {
		cancel()
		waitDone(t, done)
	})
	return base, dataDir
}

// startService launches the service and waits until it is ready.
func startService(t *testing.T, ctx context.Context, cfg config.Config, regions ...testRegion) (base, dataDir string) {
	t.Helper()
	base, dataDir = launchService(t, ctx, cfg, regions...)
	deadline := time.Now().Add(5 * time.Second)
	for get(t, base+"/healthz") != http.StatusNoContent {
		if time.Now().After(deadline) {
			t.Fatal("service not ready after loading the feed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return base, dataDir
}

func TestRunServiceLoadsEveryRegion(t *testing.T) {
	a := testRegion{ID: "a", Name: "A", TimeZone: "Asia/Jakarta"}
	b := testRegion{ID: "b", Name: "B", TimeZone: "Asia/Makassar"}
	base, dataDir := startService(t, t.Context(), config.Config{}, a, b)

	for _, id := range []string{"a", "b"} {
		if code := get(t, base+"/v1/regions/"+id+"/catalog"); code != http.StatusOK {
			t.Errorf("region %s catalog: status %d, want %d", id, code, http.StatusOK)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "regions", id, "gtfs.zip")); err != nil {
			t.Errorf("region %s feed not stored: %v", id, err)
		}
	}
	var list struct {
		Regions []struct {
			ID       string `json:"id"`
			TimeZone string `json:"timeZone"`
		} `json:"regions"`
	}
	getJSON(t, base+"/v1/regions", &list)
	if len(list.Regions) != 2 || list.Regions[0].ID != "a" || list.Regions[1].ID != "b" || list.Regions[1].TimeZone != "Asia/Makassar" {
		t.Errorf("regions = %+v, want a then b in Asia/Makassar", list.Regions)
	}
}

func TestRunServiceStaysUnreadyUntilEveryRegionLoads(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)
	a := testRegion{ID: "a", Name: "A", TimeZone: "Asia/Jakarta"}
	b := testRegion{ID: "b", Name: "B", TimeZone: "Asia/Jakarta", GTFS: map[string]string{"url": broken.URL}}
	base, _ := launchService(t, t.Context(), config.Config{}, a, b)

	deadline := time.Now().Add(5 * time.Second)
	for get(t, base+"/v1/regions/a/catalog") != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("region a never loaded its catalog")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code := get(t, base+"/healthz"); code != http.StatusServiceUnavailable {
		t.Errorf("healthz with region b unloaded: status %d, want %d", code, http.StatusServiceUnavailable)
	}
}

// runServiceBriefly runs a service that is expected to fail at startup,
// bounded, since one that started would serve until cancelled.
func runServiceBriefly(t *testing.T, cfg config.Config) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cfg.DataDir = filepath.Join(t.TempDir(), "data")
	if cfg.LegalFile == "" {
		cfg.LegalFile = filepath.Join(cfg.DataDir, "legal.json")
	}
	return runService(ctx, cfg, listen(t), discard)
}

func TestRunServiceRejectsAnInvalidRegionsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regions.json")
	writeRegions(t, path, []testRegion{{ID: "Jakarta", Name: "Jakarta", TimeZone: "Asia/Jakarta", GTFS: map[string]string{"url": "http://127.0.0.1:1/feed.zip"}}})
	err := runServiceBriefly(t, config.Config{RegionsFile: path})
	if err == nil || !strings.Contains(err.Error(), "regions[0].id") {
		t.Errorf("runService = %v, want an error about regions[0].id", err)
	}
}

func TestRunServiceRequiresARegionsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	err := runServiceBriefly(t, config.Config{RegionsFile: path})
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("runService = %v, want an error naming %s", err, path)
	}
}

func TestRunServiceLoadsFeedAndBecomesReady(t *testing.T) {
	base, dataDir := startService(t, t.Context(), config.Config{})

	var status serviceStatus
	getJSON(t, base+"/v1/status", &status)
	if status.Catalog.Version == "" {
		t.Error("status has no catalog version")
	}
	if status.Feed.State != "disabled" {
		t.Errorf("feed state = %q, want disabled", status.Feed.State)
	}
	if status.StopFacilities.State != "disabled" {
		t.Errorf("stop facilities state = %q, want disabled", status.StopFacilities.State)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "regions", "jakarta", "gtfs.zip")); err != nil {
		t.Errorf("feed not stored: %v", err)
	}

	var catalog struct {
		FeedVersion string `json:"feedVersion"`
	}
	getJSON(t, base+"/v1/regions/jakarta/catalog", &catalog)
	if catalog.FeedVersion != status.Catalog.Version {
		t.Errorf("catalog feedVersion = %q, want %q", catalog.FeedVersion, status.Catalog.Version)
	}
	if code := get(t, base+"/v1/regions/jakarta/buses?bbox=106.8,-6.2,106.9,-6.1"); code != http.StatusOK {
		t.Errorf("buses with the feed disabled: status %d, want %d", code, http.StatusOK)
	}

	var departures struct {
		Feed  string            `json:"feed"`
		Stops []json.RawMessage `json:"stops"`
	}
	getJSON(t, base+"/v1/regions/jakarta/departures?stops=G00001", &departures)
	if departures.Feed != "disabled" || len(departures.Stops) != 1 {
		t.Errorf("departures = %+v, want one stop with the feed disabled", departures)
	}
}

func TestRunServiceClosesLiveStreamsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	base, _ := startService(t, ctx, config.Config{})
	dialer := websocket.Dialer{Subprotocols: []string{"jalvio.live.v1"}}
	conn, resp, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(base, "http")+"/v1/regions/jakarta/live", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = resp.Body.Close()
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("first frame: %v", err)
	}

	cancel()
	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Errorf("after shutdown: %v, want close %d", err, websocket.CloseGoingAway)
	}
}

func TestRunServiceSearchesPlacesInTheNetworkArea(t *testing.T) {
	var query url.Values
	var userAgent string
	photon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, userAgent = r.URL.Query(), r.UserAgent()
		_, _ = w.Write([]byte(`{"features":[{"geometry":{"coordinates":[106.82,-6.195]},
			"properties":{"osm_type":"W","osm_id":123,"osm_value":"mall","name":"Grand Indonesia","city":"Jakarta"}}]}`))
	}))
	t.Cleanup(photon.Close)

	base, _ := startService(t, t.Context(), config.Config{PhotonURL: photon.URL})

	var found struct {
		Places []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"places"`
	}
	getJSON(t, base+"/v1/regions/jakarta/places?q=grand", &found)
	if len(found.Places) != 1 || found.Places[0].ID != "osm:W123" || found.Places[0].Kind != "mall" {
		t.Errorf("places = %+v, want osm:W123, a mall", found.Places)
	}
	// The fixture stops span 106.801–106.853 E and 6.257–6.137 S.
	want := []float64{106.701, -6.357, 106.953, -6.037}
	got := strings.Split(query.Get("bbox"), ",")
	if len(got) != len(want) {
		t.Fatalf("geocoder bbox = %q, want about %v", query.Get("bbox"), want)
	}
	for i, s := range got {
		if v, err := strconv.ParseFloat(s, 64); err != nil || math.Abs(v-want[i]) > 1e-9 {
			t.Errorf("geocoder bbox = %q, want about %v", query.Get("bbox"), want)
			break
		}
	}
	if userAgent != "jalvio-test" {
		t.Errorf("geocoder User-Agent = %q, want jalvio-test", userAgent)
	}
}

func TestRunServiceStartsLiveFeedWithoutBlockingReadiness(t *testing.T) {
	region := jakarta()
	region.Live = map[string]string{"mqttUrl": "wss://127.0.0.1:1/mqtt"}
	base, dataDir := startService(t, t.Context(), config.Config{}, region)

	var status serviceStatus
	getJSON(t, base+"/v1/status", &status)
	if status.Feed.State != "disconnected" {
		t.Errorf("feed state = %q, want disconnected", status.Feed.State)
	}
	if f := status.Feed; f.Filters == nil || *f.Filters != 0 || f.WholeFleet == nil || *f.WholeFleet {
		t.Errorf("feed filters = %v, wholeFleet = %v, want 0 and false", f.Filters, f.WholeFleet)
	}
	if code := get(t, base+"/v1/regions/jakarta/buses?bbox=106.8,-6.2,106.9,-6.1"); code != http.StatusOK {
		t.Errorf("buses with the feed configured: status %d, want %d", code, http.StatusOK)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "regions", "jakarta", "mqtt-client-id")); err != nil {
		t.Errorf("client ID not stored: %v", err)
	}
}

// stopAPI fakes the stop API with the fixture feed's three stations.
func stopAPI(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/auth/login/guest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"t","token_valid_until":4102444800}`))
	})
	mux.HandleFunc("GET /v1/bus-stop", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[
			{"stop_id":"H00001P","stop_name":"Blok M","v2_facilities":[{"name":"toilet","title":"toilet"}]},
			{"stop_id":"H00002P","stop_name":"Bundaran HI"},
			{"stop_id":"H00003P","stop_name":"Kota"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

func TestRunServiceSyncsStopFacilities(t *testing.T) {
	searchGap, detailGap := stopSearchGap, stopDetailGap
	stopSearchGap, stopDetailGap = 0, 0
	t.Cleanup(func() { stopSearchGap, stopDetailGap = searchGap, detailGap })

	region := jakarta()
	region.StopFacilities = map[string]string{"url": stopAPI(t), "appVersion": "1.0.0"}
	base, dataDir := startService(t, t.Context(), config.Config{}, region)

	var status serviceStatus
	deadline := time.Now().Add(5 * time.Second)
	for getJSON(t, base+"/v1/status", &status); status.StopFacilities.State != "synced"; getJSON(t, base+"/v1/status", &status) {
		if time.Now().After(deadline) {
			t.Fatalf("stop facilities = %+v, want synced", status.StopFacilities)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, name := range []string{"device-id", "stopinfo.json"} {
		if _, err := os.Stat(filepath.Join(dataDir, "regions", "jakarta", name)); err != nil {
			t.Errorf("%s not stored: %v", name, err)
		}
	}
}

func TestRunServicePublishesTheOwnersLegalDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legal.json")
	content := `{"terms": {"updated": "2026-10-29", "en": {"title": "Terms", "summary": [], ` +
		`"sections": [{"id": "t1", "title": "Using the app", "text": "It is free to use."}]}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	base, _ := startService(t, t.Context(), config.Config{LegalFile: path})

	var terms struct {
		Title   string `json:"title"`
		Updated string `json:"updated"`
	}
	getJSON(t, base+"/v1/legal/terms", &terms)
	if terms.Title != "Terms" || terms.Updated != "2026-10-29" {
		t.Errorf("terms = %+v, want the owner's terms", terms)
	}
	if code := get(t, base+"/v1/legal/privacy"); code != http.StatusNotFound {
		t.Errorf("unpublished privacy policy: status %d, want %d", code, http.StatusNotFound)
	}
}

func TestRunServicePublishesNoLegalDocumentsWithoutAFile(t *testing.T) {
	base, _ := startService(t, t.Context(), config.Config{})
	if code := get(t, base+"/v1/legal/terms"); code != http.StatusNotFound {
		t.Errorf("terms without a legal file: status %d, want %d", code, http.StatusNotFound)
	}
}

func TestRunServiceRejectsAnInvalidLegalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legal.json")
	if err := os.WriteFile(path, []byte(`{"terms": {"updated": "soon"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runServiceBriefly(t, config.Config{LegalFile: path})
	if err == nil || !strings.Contains(err.Error(), "terms.updated") {
		t.Errorf("runService = %v, want an error about terms.updated", err)
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	getenv := func(key string) string {
		if key == "JALVIO_LISTEN" {
			return "not-an-address"
		}
		return ""
	}
	if err := run(t.Context(), nil, getenv, discard); err == nil {
		t.Fatal("run succeeded with invalid config")
	}
}

func listenOn(addr string) func(string) string {
	return func(key string) string {
		if key == "JALVIO_LISTEN" {
			return addr
		}
		return ""
	}
}

func TestHealthcheck(t *testing.T) {
	base, _ := startService(t, t.Context(), config.Config{})
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(base, "http://"))

	if err := run(t.Context(), []string{"healthcheck"}, listenOn(":"+port), discard); err != nil {
		t.Errorf("healthcheck of a ready service: %v", err)
	}

	unready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unready.Close()
	if err := run(t.Context(), []string{"healthcheck"}, listenOn(unready.Listener.Addr().String()), discard); err == nil {
		t.Error("healthcheck of an unready service succeeded")
	}

	closed := listen(t)
	addr := closed.Addr().String()
	_ = closed.Close()
	if err := run(t.Context(), []string{"healthcheck"}, listenOn(addr), discard); err == nil {
		t.Error("healthcheck with nothing listening succeeded")
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	if err := run(t.Context(), []string{"serve-forever"}, listenOn(":0"), discard); err == nil {
		t.Error("run accepted an unknown command")
	}
}

func get(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	// Draining lets the client reuse the connection. Otherwise it may dial
	// one it never uses, which holds up the service's shutdown for 5 s.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}
