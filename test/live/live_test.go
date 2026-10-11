//go:build live

// Package live checks the real upstreams. It only builds with the live tag:
//
//	mise run live
//
// Each test reads the same environment as the service and skips when its
// upstream is not configured. Output is counts only, never payloads.
package live_test

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
	"github.com/lscythe/jalvio-service/internal/places"
	"github.com/lscythe/jalvio-service/internal/stopapi"
)

const userAgent = "jalvio-service/live-test"

func require(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if os.Getenv(key) == "" {
			t.Skipf("%s is not set", key)
		}
	}
}

// feed downloads the GTFS feed once for every test that needs it.
var feed = sync.OnceValues(func() (*catalog.Catalog, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fetcher := catalog.NewHTTPFetcher(&http.Client{}, os.Getenv("GTFS_URL"), userAgent)
	data, err := fetcher.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	return catalog.Parse(data)
})

func TestGTFSFeed(t *testing.T) {
	require(t, "GTFS_URL")
	c, err := feed()
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	kinds := map[catalog.Kind]int{}
	for _, s := range c.Stops() {
		kinds[s.Kind]++
	}
	t.Logf("routes=%d stations=%d platforms=%d roadside=%d",
		len(c.Routes()), kinds[catalog.Station], kinds[catalog.Platform], kinds[catalog.Roadside])
}

func TestBroker(t *testing.T) {
	require(t, "MQTT_URL")
	duration := 15 * time.Second
	if d, err := time.ParseDuration(os.Getenv("LIVE_MQTT_DURATION")); err == nil {
		duration = d
	}
	buses := fleet.New()
	live := mqttfeed.New(mqttfeed.Config{
		URL:           os.Getenv("MQTT_URL"),
		TLSServerName: os.Getenv("MQTT_TLS_SERVER_NAME"),
		DialAddress:   os.Getenv("MQTT_DIAL_ADDRESS"),
		ClientID:      "jalvio-live-test",
	}, buses, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithTimeout(t.Context(), duration)
	defer cancel()
	live.Run(ctx)

	st := live.Status()
	t.Logf("seconds=%.0f messages=%d malformed=%d buses=%d reconnects=%d",
		duration.Seconds(), st.Messages, st.Malformed, buses.Len(), st.Reconnects)
	if st.Messages == 0 {
		t.Error("no messages received")
	}
	if st.Malformed*100 > st.Messages {
		t.Errorf("%d of %d messages malformed, want under 1%%", st.Malformed, st.Messages)
	}
}

func TestStopAPI(t *testing.T) {
	require(t, "STOP_API_URL", "STOP_API_APP_VERSION", "GTFS_URL")
	c, err := feed()
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	deviceID, err := stopapi.LoadOrCreateDeviceID(filepath.Join(t.TempDir(), "device-id"))
	if err != nil {
		t.Fatal(err)
	}
	api := stopapi.New(&http.Client{Timeout: 30 * time.Second}, stopapi.Config{
		BaseURL:    os.Getenv("STOP_API_URL"),
		AppVersion: os.Getenv("STOP_API_APP_VERSION"),
		DeviceID:   deviceID,
		UserAgent:  userAgent,
	})

	// Name search is not limited by distance, so any point in the network works.
	stations := map[string]bool{}
	var center geo.Point
	for _, s := range c.Stops() {
		if s.Kind == catalog.Station {
			stations[s.ID] = true
			center = geo.Point{Lat: s.Lat, Lon: s.Lon}
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	stops, err := api.SearchStops(ctx, "a", center)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	matched, withFacilities := 0, 0
	for _, s := range stops {
		if stations[s.ID] {
			matched++
			if len(s.Facilities) > 0 {
				withFacilities++
			}
		}
	}
	t.Logf("records=%d stations_matched=%d/%d with_facilities=%d", len(stops), matched, len(stations), withFacilities)
	if matched == 0 {
		t.Error("search matched no GTFS stations")
	}
}

func TestPhoton(t *testing.T) {
	require(t, "PHOTON_URL", "PHOTON_USER_AGENT")
	query := os.Getenv("LIVE_PLACE_QUERY")
	if query == "" {
		query = "mall"
	}
	photon := places.NewPhoton(&http.Client{Timeout: 10 * time.Second}, os.Getenv("PHOTON_URL"), os.Getenv("PHOTON_USER_AGENT"))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	found, err := photon.Search(ctx, places.Query{Text: query, Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	t.Logf("places=%d", len(found))
	if len(found) == 0 {
		t.Error("no places found")
	}
}
