package catalog

import (
	"archive/zip"
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Parse builds a Catalog from a GTFS zip, rejecting feeds with missing or
// dangling references.
func Parse(data []byte) (*Catalog, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open feed zip: %w", err)
	}
	p := &parser{
		zr:        zr,
		routes:    map[string]*Route{},
		stops:     map[string]*Stop{},
		shapes:    map[string][]sequenced[Point]{},
		trips:     map[string]*Trip{},
		stopTimes: map[string][]sequenced[string]{},
	}
	for _, step := range []func() error{p.readRoutes, p.readStops, p.readShapes, p.readTrips, p.readStopTimes, p.readFrequencies} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	sum := sha256.Sum256(data)
	return p.build(hex.EncodeToString(sum[:])), nil
}

type sequenced[T any] struct {
	seq   int
	value T
}

func values[T any](items []sequenced[T]) []T {
	slices.SortFunc(items, func(a, b sequenced[T]) int { return cmp.Compare(a.seq, b.seq) })
	out := make([]T, len(items))
	for i, item := range items {
		out[i] = item.value
	}
	return out
}

type parser struct {
	zr        *zip.Reader
	routes    map[string]*Route
	stops     map[string]*Stop
	shapes    map[string][]sequenced[Point]
	trips     map[string]*Trip
	stopTimes map[string][]sequenced[string]
}

func (p *parser) readRoutes() error {
	return readTable(p.zr, "routes.txt", true, []string{"route_id", "route_short_name"}, func(r *row) error {
		id := r.get("route_id")
		p.routes[id] = &Route{
			ID:        id,
			Code:      r.get("route_short_name"),
			Name:      r.get("route_long_name"),
			Class:     r.get("route_desc"),
			Color:     r.get("route_color"),
			TextColor: r.get("route_text_color"),
		}
		return nil
	})
}

func (p *parser) readStops() error {
	err := readTable(p.zr, "stops.txt", true, []string{"stop_id", "stop_name", "stop_lat", "stop_lon"}, func(r *row) error {
		lat, err := strconv.ParseFloat(r.get("stop_lat"), 64)
		if err != nil {
			return r.errorf("invalid stop_lat %q", r.get("stop_lat"))
		}
		lon, err := strconv.ParseFloat(r.get("stop_lon"), 64)
		if err != nil {
			return r.errorf("invalid stop_lon %q", r.get("stop_lon"))
		}
		s := &Stop{
			ID:       r.get("stop_id"),
			Code:     r.get("stop_code"),
			Name:     r.get("stop_name"),
			Lat:      lat,
			Lon:      lon,
			ParentID: r.get("parent_station"),
		}
		switch {
		case r.get("location_type") == "1":
			s.Kind = Station
		case s.ParentID != "":
			s.Kind = Platform
		}
		p.stops[s.ID] = s
		return nil
	})
	if err != nil {
		return err
	}
	for _, s := range p.stops {
		if s.ParentID == "" {
			continue
		}
		parent, ok := p.stops[s.ParentID]
		if !ok {
			return fmt.Errorf("stops.txt: stop %s has unknown parent %s", s.ID, s.ParentID)
		}
		parent.ChildIDs = append(parent.ChildIDs, s.ID)
	}
	return nil
}

func (p *parser) readShapes() error {
	return readTable(p.zr, "shapes.txt", false, []string{"shape_id", "shape_pt_sequence", "shape_pt_lat", "shape_pt_lon"}, func(r *row) error {
		seq, err := strconv.Atoi(r.get("shape_pt_sequence"))
		if err != nil {
			return r.errorf("invalid shape_pt_sequence %q", r.get("shape_pt_sequence"))
		}
		lat, err := strconv.ParseFloat(r.get("shape_pt_lat"), 64)
		if err != nil {
			return r.errorf("invalid shape_pt_lat %q", r.get("shape_pt_lat"))
		}
		lon, err := strconv.ParseFloat(r.get("shape_pt_lon"), 64)
		if err != nil {
			return r.errorf("invalid shape_pt_lon %q", r.get("shape_pt_lon"))
		}
		id := r.get("shape_id")
		p.shapes[id] = append(p.shapes[id], sequenced[Point]{seq, Point{Lat: lat, Lon: lon}})
		return nil
	})
}

func (p *parser) readTrips() error {
	err := readTable(p.zr, "trips.txt", true, []string{"trip_id", "route_id", "service_id"}, func(r *row) error {
		t := &Trip{
			ID:        r.get("trip_id"),
			RouteID:   r.get("route_id"),
			ServiceID: r.get("service_id"),
			Headsign:  r.get("trip_headsign"),
			Name:      r.get("trip_short_name"),
		}
		route, ok := p.routes[t.RouteID]
		if !ok {
			return r.errorf("trip %s has unknown route %s", t.ID, t.RouteID)
		}
		if d := r.get("direction_id"); d != "" {
			dir, err := strconv.Atoi(d)
			if err != nil {
				return r.errorf("invalid direction_id %q", d)
			}
			t.Direction = dir
		}
		if shapeID := r.get("shape_id"); shapeID != "" {
			points, ok := p.shapes[shapeID]
			if !ok {
				return r.errorf("trip %s has unknown shape %s", t.ID, shapeID)
			}
			t.Shape = values(points)
		}
		route.TripIDs = append(route.TripIDs, t.ID)
		p.trips[t.ID] = t
		return nil
	})
	if err != nil {
		return err
	}
	if len(p.trips) == 0 {
		return fmt.Errorf("trips.txt: no trips")
	}
	return nil
}

func (p *parser) readStopTimes() error {
	err := readTable(p.zr, "stop_times.txt", true, []string{"trip_id", "stop_sequence", "stop_id"}, func(r *row) error {
		tripID, stopID := r.get("trip_id"), r.get("stop_id")
		if _, ok := p.trips[tripID]; !ok {
			return r.errorf("stop time for unknown trip %s", tripID)
		}
		if _, ok := p.stops[stopID]; !ok {
			return r.errorf("stop time for unknown stop %s", stopID)
		}
		seq, err := strconv.Atoi(r.get("stop_sequence"))
		if err != nil {
			return r.errorf("invalid stop_sequence %q", r.get("stop_sequence"))
		}
		p.stopTimes[tripID] = append(p.stopTimes[tripID], sequenced[string]{seq, stopID})
		return nil
	})
	if err != nil {
		return err
	}
	for id, t := range p.trips {
		t.StopIDs = values(p.stopTimes[id])
		if len(t.StopIDs) < 2 {
			return fmt.Errorf("stop_times.txt: trip %s has fewer than 2 stops", id)
		}
	}
	return nil
}

func (p *parser) readFrequencies() error {
	return readTable(p.zr, "frequencies.txt", false, []string{"trip_id", "start_time", "end_time", "headway_secs"}, func(r *row) error {
		t, ok := p.trips[r.get("trip_id")]
		if !ok {
			return r.errorf("frequency for unknown trip %s", r.get("trip_id"))
		}
		start, err := parseClock(r.get("start_time"))
		if err != nil {
			return r.errorf("invalid start_time: %v", err)
		}
		end, err := parseClock(r.get("end_time"))
		if err != nil {
			return r.errorf("invalid end_time: %v", err)
		}
		secs, err := strconv.Atoi(r.get("headway_secs"))
		if err != nil || secs <= 0 {
			return r.errorf("invalid headway_secs %q", r.get("headway_secs"))
		}
		t.Headways = append(t.Headways, Headway{Start: start, End: end, Every: time.Duration(secs) * time.Second})
		return nil
	})
}

// parseClock parses a GTFS H:MM:SS time, which may exceed 24:00:00.
func parseClock(s string) (time.Duration, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("%q is not H:MM:SS", s)
	}
	var n [3]int
	for i, part := range parts {
		v, err := strconv.Atoi(part)
		if err != nil || v < 0 || (i > 0 && v > 59) {
			return 0, fmt.Errorf("%q is not H:MM:SS", s)
		}
		n[i] = v
	}
	return time.Duration(n[0])*time.Hour + time.Duration(n[1])*time.Minute + time.Duration(n[2])*time.Second, nil
}

func (p *parser) build(version string) *Catalog {
	routeSets := map[string]map[string]bool{}
	addRoute := func(stopID, routeID string) {
		if routeSets[stopID] == nil {
			routeSets[stopID] = map[string]bool{}
		}
		routeSets[stopID][routeID] = true
	}
	for _, t := range p.trips {
		for _, stopID := range t.StopIDs {
			addRoute(stopID, t.RouteID)
			if parent := p.stops[stopID].ParentID; parent != "" {
				addRoute(parent, t.RouteID)
			}
		}
	}

	c := &Catalog{
		version:    version,
		routeIndex: make(map[string]int, len(p.routes)),
		stopIndex:  make(map[string]int, len(p.stops)),
		trips:      make(map[string]Trip, len(p.trips)),
	}
	for _, r := range p.routes {
		c.routes = append(c.routes, *r)
	}
	slices.SortFunc(c.routes, func(a, b Route) int { return cmp.Compare(a.Code, b.Code) })
	for i, r := range c.routes {
		c.routeIndex[r.ID] = i
	}

	for _, s := range p.stops {
		slices.Sort(s.ChildIDs)
		for routeID := range routeSets[s.ID] {
			s.RouteIDs = append(s.RouteIDs, routeID)
		}
		slices.Sort(s.RouteIDs)
		c.stops = append(c.stops, *s)
	}
	slices.SortFunc(c.stops, func(a, b Stop) int { return cmp.Compare(a.ID, b.ID) })
	for i, s := range c.stops {
		c.stopIndex[s.ID] = i
	}

	for id, t := range p.trips {
		c.trips[id] = *t
	}
	return c
}
