package httpapi_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/httpapi"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
)

var now = time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC)

type liveBus struct {
	fleetNumber, route, trip, headsign string
	lat, lng                           float64
	etas                               map[string]int
}

// liveFleet holds synthetic buses on the fixture routes. XX-001 runs a route
// the catalog doesn't know and has gone quiet, so it is stale.
func liveFleet() *fleet.Fleet {
	f := fleet.New()
	for _, b := range []liveBus{
		{"TJ-001", "1", "1-R01", "Kota", -6.19, 106.823, map[string]int{"H00002P": 3, "H00003P": 9}},
		{"TJ-002", "1", "1-R01", "Kota", -6.22, 106.81, map[string]int{"H00002P": 12, "H00003P": 18}},
		{"TJ-003", "1", "1-R01", "Kota", -6.24, 106.80, map[string]int{"H00002P": 20}},
		{"TJ-004", "1", "1-R01", "Kota", -6.243, 106.801, map[string]int{"H00002P": 25}},
		{"TJ-005", "1", "1-R02", "Blok M", -6.14, 106.813, map[string]int{"H00002P": 5}},
	} {
		f.Apply(fleet.Update{
			Plate: b.fleetNumber, RouteCode: b.route, TripID: b.trip, Headsign: b.headsign,
			Class: "BRT", VehicleType: "Maxi Bus", Position: geo.Point{Lat: b.lat, Lon: b.lng},
			ETAs: b.etas, ObservedAt: now,
		}, now)
	}
	f.Apply(fleet.Update{
		Plate: "MT-001", RouteCode: "JAK.18", TripID: "JAK.18-R01", Headsign: "Kuningan",
		Class: "Mikrotrans", VehicleType: "Medium Bus", Position: geo.Point{Lat: -6.25, Lon: 106.85}, Bearing: 270,
		PrevStopID: "B00001P", NextStopID: "B00003P", AtStopID: "B00002P",
		ETAs: map[string]int{"B00003P": 4}, ObservedAt: now,
	}, now)
	f.Apply(fleet.Update{
		Plate: "XX-001", RouteCode: "51ST", TripID: "51ST-L01", Headsign: "Senayan",
		Position: geo.Point{Lat: -6.19, Lon: 106.822}, Bearing: 90,
		PrevStopID: "H00001P", NextStopID: "H00002P", NextStopDistanceMeters: 450,
		ETAs: map[string]int{"H00002P": 7}, ObservedAt: now.Add(-2 * time.Minute),
	}, now.Add(-2*time.Minute))
	f.Expire(now)
	return f
}

func liveHandler(t *testing.T) http.Handler {
	t.Helper()
	d := deps(true)
	d.Catalogs = fakeCatalogs{current: fixtureCatalog(t)}
	d.Buses = liveFleet()
	return httpapi.New(d)
}

func demandHandler(t *testing.T) (http.Handler, *recordingDemand) {
	t.Helper()
	demand := newRecordingDemand()
	d := deps(true)
	d.Catalogs = fakeCatalogs{current: fixtureCatalog(t)}
	d.Buses = liveFleet()
	d.Demand = demand
	return httpapi.New(d), demand
}

func checkTouches(t *testing.T, demand *recordingDemand, want ...mqttfeed.Interest) {
	t.Helper()
	if got := demand.touches(); !reflect.DeepEqual(got, want) {
		t.Errorf("touched %+v, want %+v", got, want)
	}
}

func TestBusesTouchesTheArea(t *testing.T) {
	h, demand := demandHandler(t)
	get(t, h, "/v1/buses?bbox=106.8,-6.2,106.9,-6.1", nil)
	checkTouches(t, demand, mqttfeed.Interest{Areas: []geo.BBox{{West: 106.8, South: -6.2, East: 106.9, North: -6.1}}})
}

func TestBusesTouchesRouteCodes(t *testing.T) {
	h, demand := demandHandler(t)
	get(t, h, "/v1/buses?routes=JAK.18", nil)
	get(t, h, "/v1/buses?bbox=106.8,-6.2,106.9,-6.1&routes=1", nil)
	get(t, h, "/v1/buses?routes=NOPE", nil)
	checkTouches(t, demand,
		mqttfeed.Interest{RouteCodes: []string{"JAK.18"}},
		mqttfeed.Interest{RouteCodes: []string{"1"}},
	)
}

func TestDeparturesTouchesRoutesServingStops(t *testing.T) {
	h, demand := demandHandler(t)
	get(t, h, "/v1/departures?stops=B00003P,G00002,NOPE", nil)
	get(t, h, "/v1/departures?stops=NOPE", nil)
	checkTouches(t, demand, mqttfeed.Interest{RouteCodes: []string{"1", "JAK.18"}})
}

func TestBusDetailTouchesUnknownFleetNumber(t *testing.T) {
	h, demand := demandHandler(t)
	if rec := get(t, h, "/v1/buses/NEW-1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	checkTouches(t, demand, mqttfeed.Interest{FleetNumbers: []string{"NEW-1"}})
}

func TestDepartures(t *testing.T) {
	rec := get(t, liveHandler(t), "/v1/departures?stops=G00002,B00003P,NOPE", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	const want = `{"feed":"disabled","stops":[` +
		`{"stopId":"G00002","departures":[` +
		`{"routeId":"1","routeCode":"1","headsign":"Kota","buses":[` +
		`{"fleetNumber":"TJ-001","patternId":"1-R01","minutes":3,"observedAt":"2026-10-22T01:00:00Z","stale":false},` +
		`{"fleetNumber":"TJ-002","patternId":"1-R01","minutes":12,"observedAt":"2026-10-22T01:00:00Z","stale":false},` +
		`{"fleetNumber":"TJ-003","patternId":"1-R01","minutes":20,"observedAt":"2026-10-22T01:00:00Z","stale":false}]},` +
		`{"routeId":"1","routeCode":"1","headsign":"Blok M","buses":[` +
		`{"fleetNumber":"TJ-005","patternId":"1-R02","minutes":5,"observedAt":"2026-10-22T01:00:00Z","stale":false}]},` +
		`{"routeCode":"51ST","headsign":"Senayan","buses":[` +
		`{"fleetNumber":"XX-001","patternId":"51ST-L01","minutes":7,"observedAt":"2026-10-22T00:58:00Z","stale":true}]}]},` +
		`{"stopId":"B00003P","departures":[` +
		`{"routeId":"JAK.18","routeCode":"JAK.18","headsign":"Kuningan","buses":[` +
		`{"fleetNumber":"MT-001","patternId":"JAK.18-R01","minutes":4,"observedAt":"2026-10-22T01:00:00Z","stale":false}]}]},` +
		`{"stopId":"NOPE","departures":[]}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
}

func TestLiveResponsesReportTheFeed(t *testing.T) {
	d := deps(true)
	d.Catalogs = fakeCatalogs{current: fixtureCatalog(t)}
	d.Buses = liveFleet()
	d.Feed = fakeFeed{mqttfeed.Status{Connected: true}}
	h := httpapi.New(d)

	for _, target := range []string{"/v1/departures?stops=G00002", "/v1/buses?routes=1", "/v1/buses/TJ-001"} {
		if body := get(t, h, target, nil).Body.String(); !strings.HasPrefix(body, `{"feed":"connected",`) {
			t.Errorf("%s: body = %s, want feed connected first", target, body)
		}
	}
}

func TestBuses(t *testing.T) {
	tests := []struct {
		query string
		want  []string
	}{
		{"routes=JAK.18", []string{"MT-001"}},
		{"routes=1,JAK.18", []string{"MT-001", "TJ-001", "TJ-002", "TJ-003", "TJ-004", "TJ-005"}},
		{"routes=NOPE", nil},
		{"bbox=106.82,-6.2,106.83,-6.18", []string{"TJ-001", "XX-001"}},
		{"bbox=106.82,-6.2,106.83,-6.18&routes=1", []string{"TJ-001"}},
	}
	h := liveHandler(t)
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			rec := get(t, h, "/v1/buses?"+tt.query, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			var got []string
			for _, part := range strings.Split(rec.Body.String(), `"fleetNumber":"`)[1:] {
				number, _, _ := strings.Cut(part, `"`)
				got = append(got, number)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("fleet numbers = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBus(t *testing.T) {
	h := liveHandler(t)
	tests := []struct {
		fleetNumber string
		want        string
	}{
		{
			"MT-001",
			`{"feed":"disabled","bus":{"fleetNumber":"MT-001","routeId":"JAK.18","routeCode":"JAK.18","patternId":"JAK.18-R01",` +
				`"headsign":"Kuningan","class":"Mikrotrans","vehicleType":"Medium Bus","position":{"lat":-6.25,"lng":106.85},"bearing":270,` +
				`"prevStopId":"B00001P","nextStopId":"B00003P","atStopId":"B00002P","observedAt":"2026-10-22T01:00:00Z","stale":false},` +
				`"upcoming":[{"stopId":"B00003P","minutes":4}]}`,
		},
		{
			"XX-001",
			`{"feed":"disabled","bus":{"fleetNumber":"XX-001","routeCode":"51ST","patternId":"51ST-L01","headsign":"Senayan",` +
				`"position":{"lat":-6.19,"lng":106.822},"bearing":90,"prevStopId":"H00001P","nextStopId":"H00002P","nextStopDistanceMeters":450,` +
				`"observedAt":"2026-10-22T00:58:00Z","stale":true},"upcoming":[{"stopId":"H00002P","minutes":7}]}`,
		},
		{
			"TJ-001",
			`{"feed":"disabled","bus":{"fleetNumber":"TJ-001","routeId":"1","routeCode":"1","patternId":"1-R01","headsign":"Kota",` +
				`"class":"BRT","vehicleType":"Maxi Bus","position":{"lat":-6.19,"lng":106.823},"bearing":0,` +
				`"observedAt":"2026-10-22T01:00:00Z","stale":false},"upcoming":[{"stopId":"H00002P","minutes":3},{"stopId":"H00003P","minutes":9}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.fleetNumber, func(t *testing.T) {
			rec := get(t, h, "/v1/buses/"+tt.fleetNumber, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.want {
				t.Errorf("body = %s\nwant   %s", got, tt.want)
			}
		})
	}
}

func TestUnknownBusIsNotFound(t *testing.T) {
	rec := get(t, liveHandler(t), "/v1/buses/NOPE-1", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	want := problem{Type: "about:blank", Title: "Not Found", Status: http.StatusNotFound}
	if got := decodeProblem(t, rec); got != want {
		t.Errorf("problem = %+v, want %+v", got, want)
	}
}

func TestLiveRejectsBadQueries(t *testing.T) {
	tests := []struct {
		target     string
		wantDetail string
	}{
		{"/v1/departures", "stops"},
		{"/v1/departures?stops=", "stops"},
		{"/v1/departures?stops=G00001,,G00002", "stops"},
		{"/v1/departures?stops=" + strings.Repeat("G00001,", 10) + "G00002", "stops"},
		{"/v1/buses", "bbox or routes"},
		{"/v1/buses?bbox=106.8,-6.2,106.9", "bbox"},
		{"/v1/buses?bbox=106.8,-6.2,east,-6.1", "bbox"},
		{"/v1/buses?bbox=106.9,-6.2,106.8,-6.1", "bbox"},
		{"/v1/buses?bbox=106.0,-6.2,107.5,-6.1", "bbox"},
		{"/v1/buses?routes=1,,2", "routes"},
	}
	h := liveHandler(t)
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			rec := get(t, h, tt.target, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			p := decodeProblem(t, rec)
			if p.Type != "about:blank" || p.Status != http.StatusBadRequest || !strings.Contains(p.Detail, tt.wantDetail) {
				t.Errorf("problem = %+v, want a 400 whose detail mentions %q", p, tt.wantDetail)
			}
		})
	}
}

func TestLiveBeforeTheFirstFeed(t *testing.T) {
	d := deps(true)
	d.Buses = liveFleet()
	h := httpapi.New(d)
	for _, target := range []string{"/v1/departures?stops=G00002", "/v1/buses?routes=1", "/v1/buses/TJ-001"} {
		if rec := get(t, h, target, nil); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want %d", target, rec.Code, http.StatusServiceUnavailable)
		}
	}
}
