package catalog_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/catalog/catalogtest"
	"github.com/lscythe/jalvio-service/internal/geo"
)

func parseFixture(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Parse(catalogtest.Feed(t, nil))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func TestParseVersionIsZipHash(t *testing.T) {
	data := catalogtest.Feed(t, nil)
	c, err := catalog.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sum := sha256.Sum256(data)
	if want := hex.EncodeToString(sum[:]); c.Version() != want {
		t.Errorf("Version = %q, want %q", c.Version(), want)
	}
}

func TestParseRoutes(t *testing.T) {
	c := parseFixture(t)

	want := catalog.Route{
		ID:        "JAK.18",
		Code:      "JAK.18",
		Name:      "Kalibata - Kuningan",
		Class:     "Mikrotrans",
		Color:     "62C2D2",
		TextColor: "000000",
		FareID:    "GR",
		TripIDs:   []string{"JAK.18-R01"},
	}
	got, ok := c.Route("JAK.18")
	if !ok {
		t.Fatal("route JAK.18 not found")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Route = %+v, want %+v", got, want)
	}

	var codes []string
	for _, r := range c.Routes() {
		codes = append(codes, r.Code)
	}
	if want := []string{"1", "JAK.10A", "JAK.18"}; !reflect.DeepEqual(codes, want) {
		t.Errorf("route codes = %v, want %v", codes, want)
	}
}

func TestParseStops(t *testing.T) {
	c := parseFixture(t)

	tests := []struct {
		id   string
		want catalog.Stop
	}{
		{
			id: "H00002P",
			want: catalog.Stop{
				ID: "H00002P", Name: "Bundaran HI", Lat: -6.193, Lon: 106.823,
				Kind: catalog.Station, ChildIDs: []string{"G00002"}, RouteIDs: []string{"1"},
			},
		},
		{
			id: "G00002",
			want: catalog.Stop{
				ID: "G00002", Name: "Bundaran HI", Lat: -6.1931, Lon: 106.8231, PlatformCode: "A",
				Kind: catalog.Platform, ParentID: "H00002P", RouteIDs: []string{"1"},
			},
		},
		{
			id: "H00003P",
			want: catalog.Stop{
				ID: "H00003P", Name: "Kota", Lat: -6.137, Lon: 106.813,
				Kind: catalog.Station, ChildIDs: []string{"G00003"}, RouteIDs: []string{"1"},
				TransferIDs: []string{"B00004P"},
			},
		},
		{
			id: "B00003P",
			want: catalog.Stop{
				ID: "B00003P", Code: "KNB", Name: "Kuningan Barat", Lat: -6.236, Lon: 106.828,
				Kind: catalog.Roadside, RouteIDs: []string{"JAK.18"},
			},
		},
	}
	for _, tt := range tests {
		got, ok := c.Stop(tt.id)
		if !ok {
			t.Errorf("stop %s not found", tt.id)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Stop(%s) = %+v, want %+v", tt.id, got, tt.want)
		}
	}
	if n := len(c.Stops()); n != 10 {
		t.Errorf("len(Stops) = %d, want 10", n)
	}
}

func TestParseTrips(t *testing.T) {
	c := parseFixture(t)

	want := catalog.Trip{
		ID:        "1-R01",
		RouteID:   "1",
		ServiceID: "SH",
		Headsign:  "Kota",
		Name:      "Blok M - Kota",
		Direction: 0,
		Stops: []catalog.TripStop{
			{StopID: "G00001", FromStart: 0},
			{StopID: "G00002", FromStart: 20 * time.Minute},
			{StopID: "G00003", FromStart: 40 * time.Minute},
		},
		Shape: []geo.Point{
			{Lat: -6.243, Lon: 106.801},
			{Lat: -6.193, Lon: 106.823},
			{Lat: -6.137, Lon: 106.813},
		},
		Headways: []catalog.Headway{
			{Start: 5 * time.Hour, End: 22 * time.Hour, Every: 10 * time.Minute},
			{Start: 22 * time.Hour, End: 25 * time.Hour, Every: 30 * time.Minute},
		},
	}
	got, ok := c.Trip("1-R01")
	if !ok {
		t.Fatal("trip 1-R01 not found")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Trip = %+v,\nwant %+v", got, want)
	}

	if got, _ := c.Trip("JAK.18-R01"); got.Headways != nil {
		t.Errorf("JAK.18-R01 headways = %v, want none", got.Headways)
	}
}

func TestRouteByCode(t *testing.T) {
	c := parseFixture(t)

	r, ok := c.RouteByCode("JAK.18")
	if !ok || r.ID != "JAK.18" {
		t.Errorf("RouteByCode(JAK.18) = %+v, %v; want route JAK.18", r, ok)
	}
	if _, ok := c.RouteByCode("99"); ok {
		t.Error("RouteByCode found an unknown code")
	}
}

func TestBoundsCoverEveryStop(t *testing.T) {
	want := geo.BBox{West: 106.801, South: -6.257, East: 106.853, North: -6.137}
	if got := parseFixture(t).Bounds(); got != want {
		t.Errorf("Bounds = %+v, want %+v", got, want)
	}
}

func TestCatalogListsAreOrderedByID(t *testing.T) {
	c := parseFixture(t)

	var trips, services, fares []string
	for _, tr := range c.Trips() {
		trips = append(trips, tr.ID)
	}
	for _, s := range c.Services() {
		services = append(services, s.ID)
	}
	for _, f := range c.Fares() {
		fares = append(fares, f.ID)
	}
	if want := []string{"1-R01", "1-R02", "JAK.18-R01"}; !reflect.DeepEqual(trips, want) {
		t.Errorf("Trips = %v, want %v", trips, want)
	}
	if want := []string{"HK", "SH"}; !reflect.DeepEqual(services, want) {
		t.Errorf("Services = %v, want %v", services, want)
	}
	if want := []string{"FP", "GR"}; !reflect.DeepEqual(fares, want) {
		t.Errorf("Fares = %v, want %v", fares, want)
	}
}

func TestParseFares(t *testing.T) {
	c := parseFixture(t)

	got, ok := c.Fare("FP")
	if !ok {
		t.Fatal("fare FP not found")
	}
	if want := (catalog.Fare{ID: "FP", Price: 3500, Currency: "IDR"}); got != want {
		t.Errorf("Fare = %+v, want %+v", got, want)
	}
	if r, _ := c.Route("1"); r.FareID != "FP" {
		t.Errorf("route 1 FareID = %q, want FP", r.FareID)
	}
	if r, _ := c.Route("JAK.10A"); r.FareID != "" {
		t.Errorf("route JAK.10A FareID = %q, want none", r.FareID)
	}
}

func TestParseWithoutFares(t *testing.T) {
	c, err := catalog.Parse(catalogtest.Feed(t, nil, "fare_attributes.txt", "fare_rules.txt"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r, _ := c.Route("1"); r.FareID != "" {
		t.Errorf("route 1 FareID = %q, want none", r.FareID)
	}
}

func TestParseServices(t *testing.T) {
	c := parseFixture(t)

	want := catalog.Service{
		ID:    "HK",
		Days:  [7]bool{time.Monday: true, time.Tuesday: true, time.Wednesday: true, time.Thursday: true, time.Friday: true},
		Start: time.Date(2004, time.January, 15, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2027, time.December, 31, 0, 0, 0, 0, time.UTC),
	}
	got, ok := c.Service("HK")
	if !ok {
		t.Fatal("service HK not found")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Service = %+v, want %+v", got, want)
	}
	if _, ok := c.Service("nope"); ok {
		t.Error("unknown service found")
	}
}

func TestServiceRunsOn(t *testing.T) {
	c := parseFixture(t)
	weekdays, _ := c.Service("HK")
	jakarta := time.FixedZone("WIB", 7*60*60)

	tests := []struct {
		name string
		day  time.Time
		want bool
	}{
		{"weekday", time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC), true},
		{"weekend", time.Date(2026, time.October, 24, 12, 0, 0, 0, time.UTC), false},
		{"first day", time.Date(2004, time.January, 15, 0, 0, 0, 0, time.UTC), true},
		{"before the first day", time.Date(2004, time.January, 14, 23, 59, 0, 0, time.UTC), false},
		{"last day", time.Date(2027, time.December, 31, 23, 59, 0, 0, time.UTC), true},
		{"after the last day", time.Date(2028, time.January, 3, 0, 0, 0, 0, time.UTC), false},
		{"date in the day's own zone", time.Date(2026, time.October, 24, 1, 0, 0, 0, jakarta), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := weekdays.RunsOn(tt.day); got != tt.want {
				t.Errorf("RunsOn(%v) = %v, want %v", tt.day, got, tt.want)
			}
		})
	}
}

func TestParseAcceptsByteOrderMark(t *testing.T) {
	raw, err := os.ReadFile("testdata/feed/routes.txt")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Parse(catalogtest.Feed(t, map[string]string{"routes.txt": "\xEF\xBB\xBF" + string(raw)}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := c.Route("1"); !ok {
		t.Error("route 1 not found")
	}
}

func TestParseRejectsInvalidFeed(t *testing.T) {
	tests := []struct {
		name    string
		replace map[string]string
		omit    []string
		wantErr string
	}{
		{
			name:    "not a zip",
			wantErr: "zip",
		},
		{
			name:    "missing required file",
			omit:    []string{"stop_times.txt"},
			wantErr: "stop_times.txt",
		},
		{
			name:    "missing required column",
			replace: map[string]string{"routes.txt": "route_id,route_long_name\n1,Blok M - Kota\n"},
			wantErr: "route_short_name",
		},
		{
			name:    "no trips",
			replace: map[string]string{"trips.txt": "trip_id,route_id,service_id,direction_id,shape_id\n"},
			wantErr: "trips.txt",
		},
		{
			name: "trip with unknown route",
			replace: map[string]string{"trips.txt": "trip_id,route_id,service_id,trip_headsign,direction_id,shape_id\n" +
				"1-R01,99,SH,Kota,0,1-R01_shp\n"},
			wantErr: "unknown route",
		},
		{
			name: "trip with unknown service",
			replace: map[string]string{"trips.txt": "trip_id,route_id,service_id,trip_headsign,direction_id,shape_id\n" +
				"1-R01,1,XX,Kota,0,1-R01_shp\n"},
			wantErr: "unknown service",
		},
		{
			name: "invalid fare price",
			replace: map[string]string{"fare_attributes.txt": "fare_id,price,currency_type\n" +
				"FP,free,IDR\n"},
			wantErr: "price",
		},
		{
			name: "transfer with unknown stop",
			replace: map[string]string{"transfers.txt": "from_stop_id,to_stop_id\n" +
				"H00003P,X99999\n"},
			wantErr: "unknown stop",
		},
		{
			name: "fare rule with unknown fare",
			replace: map[string]string{"fare_rules.txt": "fare_id,route_id\n" +
				"XX,1\n"},
			wantErr: "unknown fare",
		},
		{
			name: "fare rule with unknown route",
			replace: map[string]string{"fare_rules.txt": "fare_id,route_id\n" +
				"FP,99\n"},
			wantErr: "unknown route",
		},
		{
			name:    "missing calendar",
			omit:    []string{"calendar.txt"},
			wantErr: "calendar.txt",
		},
		{
			name: "invalid service day",
			replace: map[string]string{"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
				"SH,1,1,1,1,1,1,yes,20040115,20271231\n"},
			wantErr: "sunday",
		},
		{
			name: "invalid service date",
			replace: map[string]string{"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
				"SH,1,1,1,1,1,1,1,20040115,2027-12-31\n"},
			wantErr: "end_date",
		},
		{
			name: "trip with unknown shape",
			replace: map[string]string{"trips.txt": "trip_id,route_id,service_id,trip_headsign,direction_id,shape_id\n" +
				"1-R01,1,SH,Kota,0,nope\n"},
			wantErr: "unknown shape",
		},
		{
			name: "stop time with unknown stop",
			replace: map[string]string{"stop_times.txt": "trip_id,stop_sequence,stop_id,arrival_time\n" +
				"1-R01,0,G00001,05:00:00\n1-R01,1,X99999,05:20:00\n"},
			wantErr: "unknown stop",
		},
		{
			name: "stop time with unknown trip",
			replace: map[string]string{"stop_times.txt": "trip_id,stop_sequence,stop_id,arrival_time\n" +
				"nope,0,G00001,05:00:00\n"},
			wantErr: "unknown trip",
		},
		{
			name: "trip with fewer than two stops",
			replace: map[string]string{"stop_times.txt": "trip_id,stop_sequence,stop_id,arrival_time\n" +
				"1-R01,0,G00001,05:00:00\n1-R02,0,G00003,05:00:00\n1-R02,1,G00001,05:40:00\n" +
				"JAK.18-R01,0,B00001P,05:00:00\nJAK.18-R01,1,B00002P,05:10:00\n"},
			wantErr: "1-R01",
		},
		{
			name: "invalid arrival time",
			replace: map[string]string{"stop_times.txt": "trip_id,stop_sequence,stop_id,arrival_time\n" +
				"1-R01,0,G00001,5am\n"},
			wantErr: "arrival_time",
		},
		{
			name: "arrival before the previous stop",
			replace: map[string]string{"stop_times.txt": "trip_id,stop_sequence,stop_id,arrival_time\n" +
				"1-R01,0,G00001,05:20:00\n1-R01,1,G00002,05:00:00\n1-R02,0,G00003,05:00:00\n1-R02,1,G00001,05:40:00\n" +
				"JAK.18-R01,0,B00001P,05:00:00\nJAK.18-R01,1,B00002P,05:10:00\n"},
			wantErr: "trip 1-R01 arrives at G00002 before G00001",
		},
		{
			name: "stop with unknown parent",
			replace: map[string]string{"stops.txt": "stop_id,stop_name,stop_lat,stop_lon,location_type,parent_station\n" +
				"G00001,Blok M,-6.2431,106.8011,0,H99999P\n"},
			wantErr: "unknown parent",
		},
		{
			name: "invalid coordinate",
			replace: map[string]string{"stops.txt": "stop_id,stop_name,stop_lat,stop_lon,location_type,parent_station\n" +
				"B00001P,Kalibata City,south,106.853,0,\n"},
			wantErr: "stop_lat",
		},
		{
			name: "frequency for unknown trip",
			replace: map[string]string{"frequencies.txt": "trip_id,start_time,end_time,headway_secs\n" +
				"nope,05:00:00,22:00:00,600\n"},
			wantErr: "unknown trip",
		},
		{
			name: "invalid service time",
			replace: map[string]string{"frequencies.txt": "trip_id,start_time,end_time,headway_secs\n" +
				"1-R01,05:75:00,22:00:00,600\n"},
			wantErr: "start_time",
		},
		{
			name: "invalid stop sequence",
			replace: map[string]string{"stop_times.txt": "trip_id,stop_sequence,stop_id,arrival_time\n" +
				"1-R01,first,G00001,05:00:00\n"},
			wantErr: "stop_sequence",
		},
		{
			name: "invalid direction",
			replace: map[string]string{"trips.txt": "trip_id,route_id,service_id,direction_id,shape_id\n" +
				"1-R01,1,SH,north,1-R01_shp\n"},
			wantErr: "direction_id",
		},
		{
			name: "invalid shape point",
			replace: map[string]string{"shapes.txt": "shape_id,shape_pt_sequence,shape_pt_lat,shape_pt_lon\n" +
				"1-R01_shp,0,-6.243,east\n"},
			wantErr: "shape_pt_lon",
		},
		{
			name: "invalid headway",
			replace: map[string]string{"frequencies.txt": "trip_id,start_time,end_time,headway_secs\n" +
				"1-R01,05:00:00,22:00:00,0\n"},
			wantErr: "headway_secs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte("not a zip")
			if tt.name != "not a zip" {
				data = catalogtest.Feed(t, tt.replace, tt.omit...)
			}
			_, err := catalog.Parse(data)
			if err == nil {
				t.Fatal("Parse succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestParseRealFeed parses a local copy of the official feed when
// JALVIO_GTFS_ZIP points at one.
func TestParseRealFeed(t *testing.T) {
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
	t.Logf("%d routes, %d stops", len(c.Routes()), len(c.Stops()))
}
