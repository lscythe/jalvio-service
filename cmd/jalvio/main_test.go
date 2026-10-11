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
}

// startService runs the service against a local copy of the fixture feed and
// waits until it is ready.
func startService(t *testing.T, mqttURL string) (base, dataDir string) {
	t.Helper()
	feed := catalogtest.Feed(t, nil)
	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(feed)
	}))
	t.Cleanup(feedServer.Close)

	dataDir = filepath.Join(t.TempDir(), "data")
	cfg := config.Config{DataDir: dataDir, GTFSURL: feedServer.URL, GTFSRefresh: time.Hour, MQTTURL: mqttURL}
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
	base, dataDir := startService(t, "")

	var status serviceStatus
	getJSON(t, base+"/v1/status", &status)
	if status.Catalog.Version == "" {
		t.Error("status has no catalog version")
	}
	if status.Feed.State != "disabled" {
		t.Errorf("feed state = %q, want disabled", status.Feed.State)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "gtfs.zip")); err != nil {
		t.Errorf("feed not stored: %v", err)
	}
}

func TestRunServiceStartsLiveFeedWithoutBlockingReadiness(t *testing.T) {
	base, dataDir := startService(t, "wss://127.0.0.1:1/mqtt")

	var status serviceStatus
	getJSON(t, base+"/v1/status", &status)
	if status.Feed.State != "disconnected" {
		t.Errorf("feed state = %q, want disconnected", status.Feed.State)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "mqtt-client-id")); err != nil {
		t.Errorf("client ID not stored: %v", err)
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	getenv := func(key string) string {
		if key == "JALVIO_LISTEN" {
			return "not-an-address"
		}
		return ""
	}
	if err := run(t.Context(), getenv, discard); err == nil {
		t.Fatal("run succeeded with invalid config")
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
