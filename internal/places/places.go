// Package places finds destinations (addresses and points of interest) from
// an OpenStreetMap geocoder, with the caching and rate limiting a shared
// public instance expects.
package places

import (
	"context"
	"errors"

	"github.com/lscythe/jalvio-service/internal/geo"
)

var (
	ErrQueryTooShort = errors.New("query too short")
	ErrRateLimited   = errors.New("geocoder rate limit reached")
	ErrNotFound      = errors.New("no place found")
)

type Place struct {
	// ID is "osm:" plus the OSM type letter and ID, e.g. "osm:W123".
	ID       string
	Name     string
	Label    string
	Kind     string
	Position geo.Point
}

type Query struct {
	Text string
	// Near biases results towards a point; nil means no bias.
	Near *geo.Point
	// Area restricts results; the zero BBox means no restriction.
	Area  geo.BBox
	Limit int
}

type Provider interface {
	Search(ctx context.Context, q Query) ([]Place, error)
	Reverse(ctx context.Context, at geo.Point) (Place, error)
}
