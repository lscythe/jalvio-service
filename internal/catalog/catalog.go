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
	services   map[string]Service
	fares      map[string]Fare
	search     []searchEntry
}

type Route struct {
	ID        string
	Code      string
	Name      string
	Class     string
	Color     string
	TextColor string
	FareID    string
	TripIDs   []string
}

type Fare struct {
	ID       string
	Price    int64
	Currency string
}

type Kind int

const (
	Roadside Kind = iota
	Station
	Platform
)

type Stop struct {
	ID           string
	Code         string
	Name         string
	Lat          float64
	Lon          float64
	PlatformCode string
	Kind         Kind
	ParentID     string
	ChildIDs     []string
	RouteIDs     []string
	TransferIDs  []string
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
	Stops     []TripStop
	Shape     []geo.Point
	Headways  []Headway
}

type TripStop struct {
	StopID    string
	FromStart time.Duration
}

// Headway is a service window. Start and End are offsets from midnight of the
// service day and may exceed 24h.
type Headway struct {
	Start time.Duration
	End   time.Duration
	Every time.Duration
}

// Service is a GTFS calendar: the weekdays a trip runs on, from Start through
// End. Days is indexed by time.Weekday; Start and End are UTC midnights.
type Service struct {
	ID    string
	Days  [7]bool
	Start time.Time
	End   time.Time
}

// RunsOn reports whether the service runs on day's calendar date in day's own
// location, so callers pass a time in the feed's time zone.
func (s Service) RunsOn(day time.Time) bool {
	date := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	return s.Days[day.Weekday()] && !date.Before(s.Start) && !date.After(s.End)
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

func (c *Catalog) Service(id string) (Service, bool) {
	s, ok := c.services[id]
	return s, ok
}

func (c *Catalog) Fare(id string) (Fare, bool) {
	f, ok := c.fares[id]
	return f, ok
}
