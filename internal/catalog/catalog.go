package catalog

import (
	"time"

	"github.com/lscythe/jalvio-service/internal/geo"
)

// Catalog is an immutable snapshot of the static GTFS feed. Callers must not
// modify the slices it returns.
type Catalog struct {
	version    string
	routes     []Route
	routeIndex map[string]int
	stops      []Stop
	stopIndex  map[string]int
	trips      map[string]Trip
	search     []searchEntry
}

type Route struct {
	ID        string
	Code      string
	Name      string
	Class     string
	Color     string
	TextColor string
	TripIDs   []string
}

type Kind int

const (
	Roadside Kind = iota
	Station
	Platform
)

type Stop struct {
	ID       string
	Code     string
	Name     string
	Lat      float64
	Lon      float64
	Kind     Kind
	ParentID string
	ChildIDs []string
	RouteIDs []string
}

// Trip is a stop pattern served at the given headways. The feed models each
// pattern as one trip, and live buses report the trip they run.
type Trip struct {
	ID        string
	RouteID   string
	ServiceID string
	Headsign  string
	Name      string
	Direction int
	StopIDs   []string
	Shape     []geo.Point
	Headways  []Headway
}

// Headway is a service window. Start and End are offsets from midnight of the
// service day and may exceed 24h.
type Headway struct {
	Start time.Duration
	End   time.Duration
	Every time.Duration
}

// Version is the hex SHA-256 of the feed zip.
func (c *Catalog) Version() string { return c.version }

// Routes returns all routes ordered by code.
func (c *Catalog) Routes() []Route { return c.routes }

func (c *Catalog) Route(id string) (Route, bool) {
	i, ok := c.routeIndex[id]
	if !ok {
		return Route{}, false
	}
	return c.routes[i], true
}

// Stops returns all stops ordered by ID.
func (c *Catalog) Stops() []Stop { return c.stops }

func (c *Catalog) Stop(id string) (Stop, bool) {
	i, ok := c.stopIndex[id]
	if !ok {
		return Stop{}, false
	}
	return c.stops[i], true
}

func (c *Catalog) Trip(id string) (Trip, bool) {
	t, ok := c.trips[id]
	return t, ok
}
