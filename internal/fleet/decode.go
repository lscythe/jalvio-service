package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/lscythe/jalvio-service/internal/geo"
)

// Reasons Decode rejects a message.
var (
	ErrTopic       = errors.New("unexpected topic shape")
	ErrPayload     = errors.New("invalid payload")
	ErrPlate       = errors.New("topic plate does not match the payload")
	ErrNoRoute     = errors.New("missing route_code")
	ErrNoTrip      = errors.New("missing trip_id")
	ErrNoTimestamp = errors.New("missing timestamp")
	ErrNoPosition  = errors.New("missing position")
)

var rejectReasons = []struct {
	err    error
	reason string
}{
	{ErrTopic, "topic"},
	{ErrPayload, "payload"},
	{ErrPlate, "plate_mismatch"},
	{ErrNoRoute, "no_route"},
	{ErrNoTrip, "no_trip"},
	{ErrNoTimestamp, "no_timestamp"},
	{ErrNoPosition, "no_position"},
}

// RejectReason names the reason behind a Decode error, from a fixed set so
// it can be counted and reported without any payload content.
func RejectReason(err error) string {
	for _, r := range rejectReasons {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}
	return "other"
}

// Update is one bus position report from the live feed.
type Update struct {
	Plate                  string
	RouteCode              string
	TripID                 string
	Headsign               string
	Class                  string
	VehicleType            string
	Position               geo.Point
	Bearing                int
	PrevStopID             string
	NextStopID             string
	AtStopID               string
	NextStopDistanceMeters int
	// ETAs maps a stop or station ID to the expected minutes until arrival.
	ETAs       map[string]int
	ObservedAt time.Time
}

type payload struct {
	Latitude      float64            `json:"latitude"`
	Longitude     float64            `json:"longitude"`
	SnapLatitude  float64            `json:"snap_latitude"`
	SnapLongitude float64            `json:"snap_longitude"`
	Bearing       float64            `json:"bearing"`
	PrevStops     string             `json:"prev_stops"`
	NextStops     string             `json:"next_stops"`
	CurrStops     string             `json:"curr_stops"`
	NextDistance  number             `json:"estimated_distance_next_stops"`
	RouteCode     string             `json:"route_code"`
	TripID        string             `json:"trip_id"`
	TripHeadsign  string             `json:"trip_headsign"`
	Type          string             `json:"type"`
	BusType       string             `json:"bus_type"`
	BusBodyNo     string             `json:"bus_body_no"`
	Timestamp     number             `json:"timestamp"`
	LastUpdate    number             `json:"last_update"`
	ETAs          map[string]float64 `json:"eta_by_parent_stop_id"`
}

// number accepts a JSON number or a numeric string, since the feed sends both.
type number float64

func (n *number) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("%q is not a number", s)
	}
	*n = number(v)
	return nil
}

// Decode parses a message from /mobile_armada/<tile>/<route>/<trip>/<plate>.
func Decode(topic string, data []byte) (Update, error) {
	parts := strings.Split(topic, "/")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "mobile_armada" {
		return Update{}, ErrTopic
	}
	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		return Update{}, fmt.Errorf("%w: %w", ErrPayload, err)
	}

	switch {
	case p.BusBodyNo == "" || p.BusBodyNo != parts[5]:
		return Update{}, ErrPlate
	case p.RouteCode == "":
		return Update{}, ErrNoRoute
	case p.TripID == "":
		return Update{}, ErrNoTrip
	}
	observed := p.Timestamp
	if observed <= 0 {
		observed = p.LastUpdate
	}
	if observed <= 0 {
		return Update{}, ErrNoTimestamp
	}
	pos, ok := position(p)
	if !ok {
		return Update{}, ErrNoPosition
	}

	var etas map[string]int
	for id, minutes := range p.ETAs {
		if minutes < 0 {
			continue
		}
		if etas == nil {
			etas = map[string]int{}
		}
		etas[id] = int(math.Round(minutes))
	}

	return Update{
		Plate:                  p.BusBodyNo,
		RouteCode:              p.RouteCode,
		TripID:                 p.TripID,
		Headsign:               p.TripHeadsign,
		Class:                  p.Type,
		VehicleType:            p.BusType,
		Position:               pos,
		Bearing:                int(math.Round(p.Bearing)),
		PrevStopID:             stopID(p.PrevStops),
		NextStopID:             stopID(p.NextStops),
		AtStopID:               stopID(p.CurrStops),
		NextStopDistanceMeters: int(math.Round(float64(p.NextDistance))),
		ETAs:                   etas,
		ObservedAt:             time.UnixMilli(int64(observed)).UTC(),
	}, nil
}

// position prefers the road-snapped coordinates when the feed provides them.
func position(p payload) (geo.Point, bool) {
	for _, pt := range []geo.Point{{Lat: p.SnapLatitude, Lon: p.SnapLongitude}, {Lat: p.Latitude, Lon: p.Longitude}} {
		if pt.Lat != 0 && pt.Lon != 0 && math.Abs(pt.Lat) <= 90 && math.Abs(pt.Lon) <= 180 {
			return pt, true
		}
	}
	return geo.Point{}, false
}

// stopID extracts "H00204S" from "H00204S-Kota Bambu Arah Utara".
func stopID(s string) string {
	id, _, _ := strings.Cut(s, "-")
	return id
}
