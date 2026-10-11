package catalog

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lscythe/jalvio-service/internal/atomicfile"
)

type Fetcher interface {
	Fetch(ctx context.Context) ([]byte, error)
}

type Status struct {
	Version   string
	UpdatedAt time.Time
	CheckedAt time.Time
	Error     string
}

// Source keeps the current catalog, refreshing it from the feed and storing
// the last good feed at path so restarts don't depend on the feed host.
type Source struct {
	fetcher Fetcher
	path    string
	logger  *slog.Logger
	current atomic.Pointer[Catalog]

	mu     sync.Mutex
	status Status
}

func NewSource(fetcher Fetcher, path string, logger *slog.Logger) *Source {
	return &Source{fetcher: fetcher, path: path, logger: logger}
}

// Current returns the published catalog, or nil before the first load.
func (s *Source) Current() *Catalog { return s.current.Load() }

func (s *Source) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Load publishes the stored feed. A missing file is not an error.
func (s *Source) Load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	c, err := Parse(data)
	if err != nil {
		return fmt.Errorf("stored feed %s: %w", s.path, err)
	}
	s.publish(c)
	return nil
}

// Refresh fetches the feed and publishes it if it changed. On failure the
// current catalog and stored feed stay in place.
func (s *Source) Refresh(ctx context.Context) error {
	err := s.refresh(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.status.Error = err.Error()
		return err
	}
	s.status.Error = ""
	s.status.CheckedAt = time.Now().UTC()
	return nil
}

func (s *Source) refresh(ctx context.Context) error {
	data, err := s.fetcher.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch feed: %w", err)
	}
	if cur := s.current.Load(); cur != nil && cur.Version() == version(data) {
		return nil
	}
	c, err := Parse(data)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.path, data); err != nil {
		return fmt.Errorf("store feed: %w", err)
	}
	s.publish(c)
	return nil
}

func (s *Source) publish(c *Catalog) {
	s.current.Store(c)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Version = c.Version()
	s.status.UpdatedAt = time.Now().UTC()
}

// Run refreshes immediately, then every interval, retrying sooner after a
// failure. It returns when ctx is cancelled.
func (s *Source) Run(ctx context.Context, interval, retryDelay time.Duration) {
	for {
		wait := interval
		if err := s.Refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger.Warn("feed refresh failed", "error", err, "retry_in", retryDelay.String())
			wait = retryDelay
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
