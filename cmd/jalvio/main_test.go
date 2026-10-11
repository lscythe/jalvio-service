package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

type serviceStatus struct {
	Catalog struct {
		Version string `json:"version"`
	} `json:"catalog"`
	Feed struct {
		State string `json:"state"`
	} `json:"feed"`
	StopFacilities struct {
		State string `json:"state"`
		Error string `json:"error"`
	} `json:"stop_facilities"`
}

// startService runs the service against a local copy of the fixture feed and
// waits until it is ready.
func startService(t *testing.T, mqttURL, stopAPIURL string) (base, dataDir string) {
	t.Helper()
	feed := catalogtest.Feed(t, nil)
	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(feed)
	}))
	t.Cleanup(feedServer.Close)

	dataDir = filepath.Join(t.TempDir(), "data")
	cfg := config.Config{
		DataDir:           dataDir,
		GTFSURL:           feedServer.URL,
		GTFSRefresh:       time.Hour,
		MQTTURL:           mqttURL,
		StopAPIURL:        stopAPIURL,
		StopAPIAppVersion: "1.0.0",
		StopInfoSyncAt:    3 * time.Hour,
	}
	ln := listen(t)
	base = "http://" + ln.Addr().String()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runService(ctx, cfg, ln, discard) }()
	t.Cleanup(func() {
		cancel()
		waitDone(t, done)
	})

	deadline := time.Now().Add(5 * time.Second)
	for get(t, base+"/healthz") != http.StatusNoContent {
		if time.Now().After(deadline) {
			t.Fatal("service not ready after loading the feed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return base, dataDir
}

func TestRunServiceLoadsFeedAndBecomesReady(t *testing.T) {
	base, dataDir := startService(t, "", "")

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
	if _, err := os.Stat(filepath.Join(dataDir, "gtfs.zip")); err != nil {
		t.Errorf("feed not stored: %v", err)
	}
}

func TestRunServiceStartsLiveFeedWithoutBlockingReadiness(t *testing.T) {
	base, dataDir := startService(t, "wss://127.0.0.1:1/mqtt", "")

	var status serviceStatus
	getJSON(t, base+"/v1/status", &status)
	if status.Feed.State != "disconnected" {
		t.Errorf("feed state = %q, want disconnected", status.Feed.State)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "mqtt-client-id")); err != nil {
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

	base, dataDir := startService(t, "", stopAPI(t))

	var status serviceStatus
	deadline := time.Now().Add(5 * time.Second)
	for getJSON(t, base+"/v1/status", &status); status.StopFacilities.State != "synced"; getJSON(t, base+"/v1/status", &status) {
		if time.Now().After(deadline) {
			t.Fatalf("stop facilities = %+v, want synced", status.StopFacilities)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, name := range []string{"device-id", "stopinfo.json"} {
		if _, err := os.Stat(filepath.Join(dataDir, name)); err != nil {
			t.Errorf("%s not stored: %v", name, err)
		}
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
	base, _ := startService(t, "", "")
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
