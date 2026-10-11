package fleet_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
)

const topic = "/mobile_armada/0026-0028/3F/3F-L01/TST-001"

// payload returns the fixture payload with the given fields replaced, or
// removed when the value is nil.
func payload(t *testing.T, changes map[string]any) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/payload.json")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for k, v := range changes {
		if v == nil {
			delete(fields, k)
			continue
		}
		fields[k] = v
	}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDecode(t *testing.T) {
	got, err := fleet.Decode(topic, payload(t, nil))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := fleet.Update{
		Plate:       "TST-001",
		RouteCode:   "3F",
		TripID:      "3F-L01",
		Headsign:    "Kalideres",
		Class:       "BRT",
		VehicleType: "Maxi Bus",
		Position:    geo.Point{Lat: -6.18512, Lon: 106.79674},
		Bearing:     354,
		PrevStopID:  "H00220S",
		NextStopID:  "H00204S",
		ETAs:        map[string]int{"H00204S": 1, "H00210S": 2, "H00235P": 11},
		ObservedAt:  time.UnixMilli(1785355911000).UTC(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decode =\n%+v\nwant\n%+v", got, want)
	}
}

func TestDecodeLenientFields(t *testing.T) {
	tests := []struct {
		name    string
		changes map[string]any
		check   func(t *testing.T, u fleet.Update)
	}{
		{
			name:    "string timestamp",
			changes: map[string]any{"timestamp": "1785355911000"},
			check: func(t *testing.T, u fleet.Update) {
				if want := time.UnixMilli(1785355911000).UTC(); !u.ObservedAt.Equal(want) {
					t.Errorf("ObservedAt = %v, want %v", u.ObservedAt, want)
				}
			},
		},
		{
			name:    "missing timestamp falls back to last_update",
			changes: map[string]any{"timestamp": 0},
			check: func(t *testing.T, u fleet.Update) {
				if want := time.UnixMilli(1785355910000).UTC(); !u.ObservedAt.Equal(want) {
					t.Errorf("ObservedAt = %v, want %v", u.ObservedAt, want)
				}
			},
		},
		{
			name:    "unsnapped position falls back to raw coordinates",
			changes: map[string]any{"snap_latitude": 0, "snap_longitude": 0},
			check: func(t *testing.T, u fleet.Update) {
				if want := (geo.Point{Lat: -6.1851, Lon: 106.7967}); u.Position != want {
					t.Errorf("Position = %v, want %v", u.Position, want)
				}
			},
		},
		{
			name:    "numeric direction and empty stops",
			changes: map[string]any{"direction": 0, "prev_stops": "", "next_stops": "", "eta_by_parent_stop_id": nil},
			check: func(t *testing.T, u fleet.Update) {
				if u.PrevStopID != "" || u.NextStopID != "" || len(u.ETAs) != 0 {
					t.Errorf("stops = %q, %q, %v; want empty", u.PrevStopID, u.NextStopID, u.ETAs)
				}
			},
		},
		{
			name:    "negative ETAs are dropped",
			changes: map[string]any{"eta_by_parent_stop_id": map[string]any{"H00204S": -1, "H00210S": 3}},
			check: func(t *testing.T, u fleet.Update) {
				if want := map[string]int{"H00210S": 3}; !reflect.DeepEqual(u.ETAs, want) {
					t.Errorf("ETAs = %v, want %v", u.ETAs, want)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := fleet.Decode(topic, payload(t, tt.changes))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			tt.check(t, u)
		})
	}
}

func TestDecodeRejectsMalformedMessages(t *testing.T) {
	fixture := func(changes map[string]any) func(*testing.T) []byte {
		return func(t *testing.T) []byte { return payload(t, changes) }
	}
	raw := func(s string) func(*testing.T) []byte {
		return func(*testing.T) []byte { return []byte(s) }
	}
	tests := []struct {
		name    string
		topic   string
		payload func(*testing.T) []byte
		wantErr string
	}{
		{name: "wrong topic prefix", topic: "/other/0026-0028/3F/3F-L01/TST-001", payload: fixture(nil), wantErr: "topic"},
		{name: "short topic", topic: "/mobile_armada/3F/TST-001", payload: fixture(nil), wantErr: "topic"},
		{name: "plate mismatch", topic: "/mobile_armada/0026-0028/3F/3F-L01/TST-999", payload: fixture(nil), wantErr: "plate"},
		{name: "invalid JSON", topic: topic, payload: raw("{"), wantErr: "payload"},
		{name: "no timestamp", topic: topic, payload: fixture(map[string]any{"timestamp": nil, "last_update": nil}), wantErr: "timestamp"},
		{name: "bad timestamp", topic: topic, payload: fixture(map[string]any{"timestamp": "soon", "last_update": nil}), wantErr: "timestamp"},
		{
			name:    "no position",
			topic:   topic,
			payload: fixture(map[string]any{"latitude": 0, "longitude": 0, "snap_latitude": 0, "snap_longitude": 0}),
			wantErr: "position",
		},
		{name: "no trip", topic: topic, payload: fixture(map[string]any{"trip_id": ""}), wantErr: "trip"},
		{name: "no route", topic: topic, payload: fixture(map[string]any{"route_code": ""}), wantErr: "route"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fleet.Decode(tt.topic, tt.payload(t))
			if err == nil {
				t.Fatal("Decode succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}
