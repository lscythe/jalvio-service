package stopinfo

import (
	"context"
	"time"
)

// maxAge is how old a stored snapshot may be before startup syncs again.
const maxAge = 24 * time.Hour

// DailyAt returns the next hour:minute after now, in loc.
func DailyAt(hour, minute int, loc *time.Location) func(time.Time) time.Time {
	return func(now time.Time) time.Time {
		now = now.In(loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		return next
	}
}

// Run syncs at startup when the snapshot is missing or old, then at each time
// next returns. A failed sync is retried once after retryDelay. Run returns
// when ctx is cancelled.
func (s *Syncer) Run(ctx context.Context, next func(time.Time) time.Time, retryDelay time.Duration) {
	if snap := s.Current(); snap == nil || time.Since(snap.SyncedAt) >= maxAge {
		s.syncWithRetry(ctx, retryDelay)
	}
	for {
		if pause(ctx, time.Until(next(time.Now()))) != nil {
			return
		}
		s.syncWithRetry(ctx, retryDelay)
	}
}

func (s *Syncer) syncWithRetry(ctx context.Context, retryDelay time.Duration) {
	err := s.Sync(ctx)
	if err == nil || ctx.Err() != nil {
		return
	}
	s.logger.Warn("stop facilities sync failed", "error", err, "retry_in", retryDelay.String())
	if pause(ctx, retryDelay) != nil {
		return
	}
	if err := s.Sync(ctx); err != nil && ctx.Err() == nil {
		s.logger.Warn("stop facilities sync failed", "error", err)
	}
}
