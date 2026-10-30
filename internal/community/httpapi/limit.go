package httpapi

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	limitWindow   = time.Hour
	sweepInterval = time.Minute
)

// accountLimiter counts device account creations per client address.
type accountLimiter struct {
	perHour int
	clock   func() time.Time

	mu        sync.Mutex
	addresses map[string]*addressLimit
	sweptAt   time.Time
}

type addressLimit struct {
	limiter *rate.Limiter
	seen    time.Time
}

func newAccountLimiter(perHour int, clock func() time.Time) *accountLimiter {
	return &accountLimiter{perHour: perHour, clock: clock, addresses: map[string]*addressLimit{}}
}

func (l *accountLimiter) allow(address string) bool {
	now := l.clock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.sweptAt) >= sweepInterval {
		// An address idle for the whole window has a full allowance again, so
		// forgetting it changes nothing.
		for addr, a := range l.addresses {
			if now.Sub(a.seen) >= limitWindow {
				delete(l.addresses, addr)
			}
		}
		l.sweptAt = now
	}
	a, ok := l.addresses[address]
	if !ok {
		a = &addressLimit{limiter: rate.NewLimiter(rate.Every(limitWindow/time.Duration(l.perHour)), l.perHour)}
		l.addresses[address] = a
	}
	a.seen = now
	return a.limiter.AllowN(now, 1)
}

func (l *accountLimiter) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.addresses)
}
