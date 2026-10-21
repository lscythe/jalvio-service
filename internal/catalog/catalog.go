package catalog

import (
	"time"

	"github.com/lscythe/jalvio-service/internal/geo"
)

// Catalog is an immutable snapshot of the static GTFS feed. Callers must not
// modify the slices it returns.
type Catalog struct {
	version      string
	routes       []Route
	routeIndex   map[string]int
	stops        []Stop
	stopIndex    map[string]int
	trips        []Trip
	tripIndex    map[string]int
	services     []Service
	serviceIndex map[string]int
	fares        []Fare
	fareIndex    map[string]int
	search       []searchEntry
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

func (c *Catalog) Route(id string) (Route, bool) { return lookup(c.routes, c.routeIndex, id) }

// Stops, Trips, Services and Fares are ordered by ID.
func (c *Catalog) Stops() []Stop { return c.stops }

func (c *Catalog) Stop(id string) (Stop, bool) { return lookup(c.stops, c.stopIndex, id) }

func (c *Catalog) Trips() []Trip { return c.trips }

func (c *Catalog) Trip(id string) (Trip, bool) { return lookup(c.trips, c.tripIndex, id) }

func (c *Catalog) Services() []Service { return c.services }

func (c *Catalog) Service(id string) (Service, bool) { return lookup(c.services, c.serviceIndex, id) }

func (c *Catalog) Fares() []Fare { return c.fares }

func (c *Catalog) Fare(id string) (Fare, bool) { return lookup(c.fares, c.fareIndex, id) }

func lookup[T any](items []T, index map[string]int, id string) (T, bool) {
	i, ok := index[id]
	if !ok {
		var zero T
		return zero, false
	}
	return items[i], true
}
