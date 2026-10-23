package httpapi

import (
	"cmp"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
)

const (
	maxDepartureStops = 10
	busesPerGroup     = 3
	maxBBoxDegrees    = 1.0
)

var world = geo.BBox{West: -180, South: -90, East: 180, North: 90}

type live struct {
	catalogs Catalogs
	feed     Feed
	buses    Buses
}

type departuresResponse struct {
	Feed  string           `json:"feed"`
	Stops []stopDepartures `json:"stops"`
}

type stopDepartures struct {
	StopID     string           `json:"stopId"`
	Departures []departureGroup `json:"departures"`
}

type departureGroup struct {
	RouteID   string        `json:"routeId,omitempty"`
	RouteCode string        `json:"routeCode"`
	Headsign  string        `json:"headsign,omitempty"`
	Buses     []arrivalItem `json:"buses"`
}

type arrivalItem struct {
	FleetNumber string    `json:"fleetNumber"`
	PatternID   string    `json:"patternId"`
	Minutes     int       `json:"minutes"`
	ObservedAt  time.Time `json:"observedAt"`
	Stale       bool      `json:"stale"`
}

type busesResponse struct {
	Feed  string    `json:"feed"`
	Buses []busItem `json:"buses"`
}

type busDetailResponse struct {
	Feed     string         `json:"feed"`
	Bus      busItem        `json:"bus"`
	Upcoming []upcomingStop `json:"upcoming"`
}

type busItem struct {
	FleetNumber            string    `json:"fleetNumber"`
	RouteID                string    `json:"routeId,omitempty"`
	RouteCode              string    `json:"routeCode"`
	PatternID              string    `json:"patternId"`
	Headsign               string    `json:"headsign,omitempty"`
	Class                  string    `json:"class,omitempty"`
	VehicleType            string    `json:"vehicleType,omitempty"`
	Position               position  `json:"position"`
	Bearing                int       `json:"bearing"`
	PrevStopID             string    `json:"prevStopId,omitempty"`
	NextStopID             string    `json:"nextStopId,omitempty"`
	AtStopID               string    `json:"atStopId,omitempty"`
	NextStopDistanceMeters int       `json:"nextStopDistanceMeters,omitempty"`
	ObservedAt             time.Time `json:"observedAt"`
	Stale                  bool      `json:"stale"`
}

type upcomingStop struct {
	StopID  string `json:"stopId"`
	Minutes int    `json:"minutes"`
}

func (l *live) serveDepartures(w http.ResponseWriter, r *http.Request) {
	c := l.catalogs.Current()
	if c == nil {
		writeProblem(w, http.StatusServiceUnavailable)
		return
	}
	ids, err := idList(r.URL.Query().Get("stops"))
	if err != nil || len(ids) > maxDepartureStops {
		writeBadRequest(w, "stops must list 1 to 10 comma-separated stop IDs")
		return
	}
	resp := departuresResponse{Feed: feedState(l.feed), Stops: make([]stopDepartures, 0, len(ids))}
	for _, id := range ids {
		resp.Stops = append(resp.Stops, stopDepartures{StopID: id, Departures: l.departuresAt(c, id)})
	}
	writeJSON(w, http.StatusOK, resp)
}

// departuresAt groups the soonest buses by route and headsign. The feed
// predicts arrivals at stations, so a platform uses its station's.
func (l *live) departuresAt(c *catalog.Catalog, stopID string) []departureGroup {
	groups := []departureGroup{}
	stop, ok := c.Stop(stopID)
	if !ok {
		return groups
	}
	if stop.Kind == catalog.Platform {
		stopID = stop.ParentID
	}
	index := map[[2]string]int{}
	for _, a := range l.buses.ArrivalsAt(stopID) {
		key := [2]string{a.RouteCode, a.Headsign}
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, departureGroup{RouteID: routeID(c, a.RouteCode), RouteCode: a.RouteCode, Headsign: a.Headsign})
		}
		if len(groups[i].Buses) < busesPerGroup {
			groups[i].Buses = append(groups[i].Buses, arrivalItem{
				FleetNumber: a.Plate, PatternID: a.TripID, Minutes: a.Minutes, ObservedAt: a.ObservedAt, Stale: a.Stale,
			})
		}
	}
	return groups
}

func (l *live) serveBuses(w http.ResponseWriter, r *http.Request) {
	c := l.catalogs.Current()
	if c == nil {
		writeProblem(w, http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	if !q.Has("bbox") && !q.Has("routes") {
		writeBadRequest(w, "give bbox or routes")
		return
	}
	box := world
	if q.Has("bbox") {
		var err error
		if box, err = parseBBox(q.Get("bbox")); err != nil {
			writeBadRequest(w, "bbox must be west,south,east,north spanning at most 1 degree on each axis")
			return
		}
	}
	resp := busesResponse{Feed: feedState(l.feed), Buses: []busItem{}}
	var codes []string
	if q.Has("routes") {
		ids, err := idList(q.Get("routes"))
		if err != nil {
			writeBadRequest(w, "routes must list comma-separated route IDs")
			return
		}
		for _, id := range ids {
			if route, ok := c.Route(id); ok {
				codes = append(codes, route.Code)
			}
		}
		if len(codes) == 0 {
			writeJSON(w, http.StatusOK, resp)
			return
		}
	}
	for _, b := range l.buses.InBBox(box, codes) {
		resp.Buses = append(resp.Buses, newBusItem(c, b))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (l *live) serveBus(w http.ResponseWriter, r *http.Request) {
	c := l.catalogs.Current()
	if c == nil {
		writeProblem(w, http.StatusServiceUnavailable)
		return
	}
	b, ok := l.buses.Bus(r.PathValue("fleetNumber"))
	if !ok {
		writeProblem(w, http.StatusNotFound)
		return
	}
	upcoming := make([]upcomingStop, 0, len(b.ETAs))
	for stopID, minutes := range b.ETAs {
		upcoming = append(upcoming, upcomingStop{StopID: stopID, Minutes: minutes})
	}
	slices.SortFunc(upcoming, func(a, b upcomingStop) int {
		return cmp.Or(cmp.Compare(a.Minutes, b.Minutes), cmp.Compare(a.StopID, b.StopID))
	})
	writeJSON(w, http.StatusOK, busDetailResponse{Feed: feedState(l.feed), Bus: newBusItem(c, b), Upcoming: upcoming})
}

func newBusItem(c *catalog.Catalog, b fleet.Bus) busItem {
	return busItem{
		FleetNumber:            b.Plate,
		RouteID:                routeID(c, b.RouteCode),
		RouteCode:              b.RouteCode,
		PatternID:              b.TripID,
		Headsign:               b.Headsign,
		Class:                  b.Class,
		VehicleType:            b.VehicleType,
		Position:               position{Lat: b.Position.Lat, Lng: b.Position.Lon},
		Bearing:                b.Bearing,
		PrevStopID:             b.PrevStopID,
		NextStopID:             b.NextStopID,
		AtStopID:               b.AtStopID,
		NextStopDistanceMeters: b.NextStopDistanceMeters,
		ObservedAt:             b.ObservedAt,
		Stale:                  b.Stale,
	}
}

func routeID(c *catalog.Catalog, code string) string {
	route, _ := c.RouteByCode(code)
	return route.ID
}

var errList = errors.New("empty list item")

func idList(s string) ([]string, error) {
	ids := strings.Split(s, ",")
	if slices.Contains(ids, "") {
		return nil, errList
	}
	return ids, nil
}

func parseBBox(s string) (geo.BBox, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return geo.BBox{}, errors.New("bbox needs four numbers")
	}
	var v [4]float64
	for i, part := range parts {
		n, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return geo.BBox{}, err
		}
		v[i] = n
	}
	return newBBox(v)
}

func newBBox(v [4]float64) (geo.BBox, error) {
	box := geo.BBox{West: v[0], South: v[1], East: v[2], North: v[3]}
	if !(box.West < box.East && box.South < box.North && box.East-box.West <= maxBBoxDegrees && box.North-box.South <= maxBBoxDegrees) {
		return geo.BBox{}, errors.New("bbox is empty or too large")
	}
	return box, nil
}
