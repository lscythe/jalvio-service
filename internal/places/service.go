package places

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"

	"github.com/lscythe/jalvio-service/internal/geo"
)

const (
	DefaultLimit   = 10
	MaxLimit       = 20
	minQueryLength = 3
)

type Config struct {
	// RequestsPerMinute limits upstream calls; cache hits are free.
	RequestsPerMinute int
	CacheSize         int
	CacheTTL          time.Duration
	Timeout           time.Duration
}

// Service answers place lookups from its cache, sending at most one upstream
// call per distinct lookup at a time and denying calls over the rate limit.
// Callers must not modify the returned slices.
type Service struct {
	provider Provider
	area     func() (geo.BBox, bool)
	timeout  time.Duration
	limiter  *rate.Limiter
	flights  singleflight.Group
	searches *cache[[]Place]
	reverses *cache[Place]
}

// NewService biases searches to the area reported by area, which may be
// unknown until the catalog loads.
func NewService(provider Provider, area func() (geo.BBox, bool), cfg Config) *Service {
	return &Service{
		provider: provider,
		area:     area,
		timeout:  cfg.Timeout,
		limiter:  rate.NewLimiter(rate.Limit(float64(cfg.RequestsPerMinute)/60), max(1, cfg.RequestsPerMinute/6)),
		searches: newCache[[]Place](cfg.CacheSize, cfg.CacheTTL),
		reverses: newCache[Place](cfg.CacheSize, cfg.CacheTTL),
	}
}

func (s *Service) Search(ctx context.Context, text string, near *geo.Point, limit int) ([]Place, error) {
	text = strings.Join(strings.Fields(strings.ToLower(text)), " ")
	if utf8.RuneCountInString(text) < minQueryLength {
		return nil, ErrQueryTooShort
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	q := Query{Text: text, Near: near, Limit: min(limit, MaxLimit)}
	if area, ok := s.area(); ok {
		q.Area = area
		if q.Near == nil {
			q.Near = &geo.Point{Lat: (area.South + area.North) / 2, Lon: (area.West + area.East) / 2}
		}
	}
	key := fmt.Sprintf("search|%s|%d|%s", q.Text, q.Limit, roundedPoint(q.Near, 3))
	return lookup(ctx, s, s.searches, key, func(ctx context.Context) ([]Place, error) {
		return s.provider.Search(ctx, q)
	})
}

func (s *Service) Reverse(ctx context.Context, at geo.Point) (Place, error) {
	key := "reverse|" + roundedPoint(&at, 4)
	return lookup(ctx, s, s.reverses, key, func(ctx context.Context) (Place, error) {
		return s.provider.Reverse(ctx, at)
	})
}

func lookup[V any](ctx context.Context, s *Service, c *cache[V], key string, call func(context.Context) (V, error)) (V, error) {
	var zero V
	if v, ok := c.get(key); ok {
		return v, nil
	}
	// The call outlives any one caller, since others may be waiting on it.
	flight := s.flights.DoChan(key, func() (any, error) {
		if v, ok := c.get(key); ok {
			return v, nil
		}
		if !s.limiter.Allow() {
			return nil, ErrRateLimited
		}
		callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.timeout)
		defer cancel()
		v, err := call(callCtx)
		if err != nil {
			return nil, err
		}
		c.put(key, v)
		return v, nil
	})
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case r := <-flight:
		if r.Err != nil {
			return zero, r.Err
		}
		return r.Val.(V), nil
	}
}

// roundedPoint keys nearby points together; 3 decimals is about 100 m.
func roundedPoint(p *geo.Point, decimals int) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%.*f,%.*f", decimals, p.Lat, decimals, p.Lon)
}
