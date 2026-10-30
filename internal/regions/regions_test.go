package regions_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/regions"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "regions.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T, content string) []regions.Region {
	t.Helper()
	rs, err := regions.Load(writeFile(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return rs
}

// file wraps region objects in the file's top-level object.
func file(regionJSON ...string) string {
	return `{"regions": [` + strings.Join(regionJSON, ",") + `]}`
}

// minimal is a region with only the required fields plus extra members.
func minimal(id, extra string) string {
	r := `{"id": "` + id + `", "name": "Region ` + id + `", "timeZone": "Asia/Jakarta", "gtfs": {"url": "https://gtfs.example.com/feed.zip"}`
	if extra != "" {
		r += ", " + extra
	}
	return r + "}"
}

func TestLoadExampleFile(t *testing.T) {
	rs, err := regions.Load("../../regions.example.json")
	if err != nil {
		t.Fatalf("regions.example.json: %v", err)
	}
	if len(rs) != 1 || rs[0].ID != "jakarta" || rs[0].Name != "Jakarta" || rs[0].TimeZone.String() != "Asia/Jakarta" {
		t.Fatalf("regions = %+v, want Jakarta", rs)
	}
	if rs[0].Live == nil || rs[0].StopFacilities == nil || rs[0].Map == nil {
		t.Errorf("Jakarta = %+v, want live, stop facilities and map", rs[0])
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	rs := load(t, file(
		minimal("a", ""),
		minimal("b", `"live": {"mqttUrl": "wss://broker.example.com/mqtt"}, "stopFacilities": {"url": "https://stops.example.com/v1", "appVersion": "1.0.0"}`),
	))
	a := rs[0]
	if a.GTFS.URL != "https://gtfs.example.com/feed.zip" || a.GTFS.Refresh != 24*time.Hour {
		t.Errorf("a.GTFS = %+v, want the URL with a 24h refresh", a.GTFS)
	}
	if a.Live != nil || a.StopFacilities != nil || a.Map != nil {
		t.Errorf("a = %+v, want no live feed, stop facilities or map", a)
	}
	b := rs[1]
	want := regions.Live{MQTTURL: "wss://broker.example.com/mqtt", ClientIDPrefix: "jalvio-"}
	if b.Live == nil || *b.Live != want {
		t.Errorf("b.Live = %+v, want %+v", b.Live, want)
	}
	if b.StopFacilities == nil || b.StopFacilities.SyncAt != 3*time.Hour || b.StopFacilities.AppVersion != "1.0.0" {
		t.Errorf("b.StopFacilities = %+v, want a 03:00 sync", b.StopFacilities)
	}
}

func TestLoadReadsEveryField(t *testing.T) {
	rs := load(t, file(minimal("a", `
		"gtfs": {"url": "https://gtfs.example.com/a.zip", "refresh": "6h"},
		"live": {"mqttUrl": "ws://broker.example.com/mqtt", "tlsServerName": "cert.example.com", "dialAddress": "192.0.2.10:8084", "clientIdPrefix": "client_"},
		"stopFacilities": {"url": "https://stops.example.com/v1", "appVersion": "2.0.0", "syncAt": "04:30"},
		"map": {"url": "/maps/a.pmtiles", "bytes": 1234, "updated": "2026-10-30"}`)))
	a := rs[0]
	if a.GTFS != (regions.GTFS{URL: "https://gtfs.example.com/a.zip", Refresh: 6 * time.Hour}) {
		t.Errorf("GTFS = %+v", a.GTFS)
	}
	if *a.Live != (regions.Live{MQTTURL: "ws://broker.example.com/mqtt", TLSServerName: "cert.example.com", DialAddress: "192.0.2.10:8084", ClientIDPrefix: "client_"}) {
		t.Errorf("Live = %+v", a.Live)
	}
	if *a.StopFacilities != (regions.StopFacilities{URL: "https://stops.example.com/v1", AppVersion: "2.0.0", SyncAt: 4*time.Hour + 30*time.Minute}) {
		t.Errorf("StopFacilities = %+v", a.StopFacilities)
	}
	if *a.Map != (regions.Map{URL: "/maps/a.pmtiles", Bytes: 1234, Updated: "2026-10-30"}) {
		t.Errorf("Map = %+v", a.Map)
	}
}

func TestLoadKeepsFileOrder(t *testing.T) {
	rs := load(t, file(minimal("b", ""), minimal("a", "")))
	if len(rs) != 2 || rs[0].ID != "b" || rs[1].ID != "a" {
		t.Errorf("order = %v, want b then a", rs)
	}
}

func TestLoadRequiresTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regions.json")
	if _, err := regions.Load(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("Load of a missing file = %v, want an error naming %s", err, path)
	}
}

func TestLoadRejectsInvalidFiles(t *testing.T) {
	for _, tc := range []struct {
		name, content, mention string
	}{
		{"not JSON", `{`, "regions.json"},
		{"unknown field", `{"regions": [], "footer": 1}`, "footer"},
		{"no regions", `{"regions": []}`, "regions"},
		{"empty id", file(minimal("", "")), "regions[0].id"},
		{"uppercase id", file(minimal("Jakarta", "")), "regions[0].id"},
		{"dot id", file(minimal("..", "")), "regions[0].id"},
		{"path id", file(minimal("a/b", "")), "regions[0].id"},
		{"double hyphen id", file(minimal("a--b", "")), "regions[0].id"},
		{"long id", file(minimal(strings.Repeat("a", 33), "")), "regions[0].id"},
		{"duplicate id", file(minimal("a", ""), minimal("a", "")), "regions[1].id"},
		{"empty name", file(`{"id": "a", "name": " ", "timeZone": "Asia/Jakarta", "gtfs": {"url": "https://gtfs.example.com/feed.zip"}}`), "regions[0].name"},
		{"unknown time zone", file(`{"id": "a", "name": "A", "timeZone": "Mars/Base", "gtfs": {"url": "https://gtfs.example.com/feed.zip"}}`), "regions[0].timeZone"},
		{"the host's time zone", file(`{"id": "a", "name": "A", "timeZone": "Local", "gtfs": {"url": "https://gtfs.example.com/feed.zip"}}`), "regions[0].timeZone"},
		{"no time zone", file(`{"id": "a", "name": "A", "gtfs": {"url": "https://gtfs.example.com/feed.zip"}}`), "regions[0].timeZone"},
		{"feed not http", file(minimal("a", `"gtfs": {"url": "ftp://x"}`)), "regions[0].gtfs.url"},
		{"zero refresh", file(minimal("a", `"gtfs": {"url": "https://gtfs.example.com/feed.zip", "refresh": "0s"}`)), "regions[0].gtfs.refresh"},
		{"bad refresh", file(minimal("a", `"gtfs": {"url": "https://gtfs.example.com/feed.zip", "refresh": "x"}`)), "regions[0].gtfs.refresh"},
		{"broker not ws", file(minimal("a", `"live": {"mqttUrl": "https://x"}`)), "regions[0].live.mqttUrl"},
		{"bad dial address", file(minimal("a", `"live": {"mqttUrl": "wss://broker.example.com/mqtt", "dialAddress": "x"}`)), "regions[0].live.dialAddress"},
		{"stops not http", file(minimal("a", `"stopFacilities": {"url": "x", "appVersion": "1"}`)), "regions[0].stopFacilities.url"},
		{"stops without version", file(minimal("a", `"stopFacilities": {"url": "https://stops.example.com/v1"}`)), "regions[0].stopFacilities.appVersion"},
		{"bad sync time", file(minimal("a", `"stopFacilities": {"url": "https://stops.example.com/v1", "appVersion": "1", "syncAt": "25:00"}`)), "regions[0].stopFacilities.syncAt"},
		{"map without url", file(minimal("a", `"map": {"bytes": 1, "updated": "2026-10-30"}`)), "regions[0].map.url"},
		{"map url relative", file(minimal("a", `"map": {"url": "maps/a.pmtiles", "bytes": 1, "updated": "2026-10-30"}`)), "regions[0].map.url"},
		{"map without bytes", file(minimal("a", `"map": {"url": "/a.pmtiles", "bytes": 0, "updated": "2026-10-30"}`)), "regions[0].map.bytes"},
		{"map bad date", file(minimal("a", `"map": {"url": "/a.pmtiles", "bytes": 1, "updated": "30 Oct"}`)), "regions[0].map.updated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := regions.Load(writeFile(t, tc.content))
			if err == nil {
				t.Fatal("Load accepted an invalid file")
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("error %q does not mention %q", err, tc.mention)
			}
		})
	}
}
