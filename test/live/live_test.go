//go:build live

// Package live checks the real upstreams. It only builds with the live tag:
//
//	mise run live
//
// Each test reads the same environment and regions file as the service, runs
// once per configured region, and skips what is not configured. Output is
// counts only, never payloads.
package live_test

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/config"
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
	"github.com/lscythe/jalvio-service/internal/places"
	"github.com/lscythe/jalvio-service/internal/regions"
	"github.com/lscythe/jalvio-service/internal/stopapi"
)

const userAgent = "jalvio-service/live-test"

// testWriter sends log output to the test log, one call per line.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func require(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if os.Getenv(key) == "" {
			t.Skipf("%s is not set", key)
		}
	}
}

// eachRegion runs check as a subtest for every region in the service's
// regions file.
func eachRegion(t *testing.T, check func(t *testing.T, r regions.Region)) {
	t.Helper()
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.RegionsFile); errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no regions file at %s", cfg.RegionsFile)
	}
	configured, err := regions.Load(cfg.RegionsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range configured {
		t.Run(r.ID, func(t *testing.T) { check(t, r) })
	}
}

// feeds holds one download per region for every test that needs it.
var feeds sync.Map

func feed(r regions.Region) (*catalog.Catalog, error) {
	load, _ := feeds.LoadOrStore(r.ID, sync.OnceValues(func() (*catalog.Catalog, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		fetcher := catalog.NewHTTPFetcher(&http.Client{}, r.GTFS.URL, userAgent)
		data, err := fetcher.Fetch(ctx)
		if err != nil {
			return nil, err
		}
		return catalog.Parse(data)
	}))
	return load.(func() (*catalog.Catalog, error))()
}

func TestGTFSFeed(t *testing.T) {
	eachRegion(t, func(t *testing.T, r regions.Region) {
		c, err := feed(r)
		if err != nil {
			t.Fatalf("feed: %v", err)
		}
		kinds := map[catalog.Kind]int{}
		for _, s := range c.Stops() {
			kinds[s.Kind]++
		}
		t.Logf("routes=%d stations=%d platforms=%d roadside=%d",
			len(c.Routes()), kinds[catalog.Station], kinds[catalog.Platform], kinds[catalog.Roadside])
	})
}

func TestBroker(t *testing.T) {
	eachRegion(t, func(t *testing.T, r regions.Region) {
		if r.Live == nil {
			t.Skip("no live feed")
		}
		c, err := feed(r)
		if err != nil {
			t.Fatalf("feed: %v", err)
		}
		checkBroker(t, *r.Live, c.Bounds())
	})
}

func checkBroker(t *testing.T, cfg regions.Live, area geo.BBox) {
	duration := 15 * time.Second
	if d, err := time.ParseDuration(os.Getenv("LIVE_MQTT_DURATION")); err == nil {
		duration = d
	}
	// Connection events and client errors explain reconnects. Neither logs
	// payloads; the client's debug log, which does, stays off.
	out := testWriter{t}
	mqtt.ERROR = log.New(out, "mqtt error: ", 0)
	mqtt.CRITICAL = log.New(out, "mqtt critical: ", 0)
	mqtt.WARN = log.New(out, "mqtt warn: ", 0)
	t.Cleanup(func() { mqtt.ERROR, mqtt.CRITICAL, mqtt.WARN = mqtt.NOOPLogger{}, mqtt.NOOPLogger{}, mqtt.NOOPLogger{} })

	clientID, err := mqttfeed.LoadOrCreateClientID(filepath.Join(t.TempDir(), "mqtt-client-id"), cfg.ClientIDPrefix)
	if err != nil {
		t.Fatal(err)
	}

	buses := fleet.New()
	demand := mqttfeed.NewDemand(time.Now)
	demand.Hold("live", mqttfeed.Interest{Areas: []geo.BBox{area}})
	live := mqttfeed.New(mqttfeed.Config{
		URL:           cfg.MQTTURL,
		TLSServerName: cfg.TLSServerName,
		DialAddress:   cfg.DialAddress,
		ClientID:      clientID,
	}, buses, demand, slog.New(slog.NewTextHandler(out, nil)))

	ctx, cancel := context.WithTimeout(t.Context(), duration)
	defer cancel()
	live.Run(ctx)

	st := live.Status()
	t.Logf("seconds=%.0f messages=%d malformed=%d rejected=%v buses=%d reconnects=%d",
		duration.Seconds(), st.Messages, st.Malformed, st.Rejected, buses.Len(), st.Reconnects)
	// Reports without a GPS fix are skipped by design and don't count here.
	if st.Messages == 0 {
		t.Error("no messages received")
	}
	if st.Malformed*100 > st.Messages {
		t.Errorf("%d of %d messages malformed, want under 1%%", st.Malformed, st.Messages)
	}
}

func TestStopAPI(t *testing.T) {
	eachRegion(t, func(t *testing.T, r regions.Region) {
		if r.StopFacilities == nil {
			t.Skip("no stop facilities sync")
		}
		c, err := feed(r)
		if err != nil {
			t.Fatalf("feed: %v", err)
		}
		checkStopAPI(t, *r.StopFacilities, c)
	})
}

func checkStopAPI(t *testing.T, cfg regions.StopFacilities, c *catalog.Catalog) {
	deviceID, err := stopapi.LoadOrCreateDeviceID(filepath.Join(t.TempDir(), "device-id"))
	if err != nil {
		t.Fatal(err)
	}
	api := stopapi.New(&http.Client{Timeout: 30 * time.Second}, stopapi.Config{
		BaseURL:    cfg.URL,
		AppVersion: cfg.AppVersion,
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
