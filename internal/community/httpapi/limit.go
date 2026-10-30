package httpapi

import (
	"net"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	limitWindow   = time.Hour
	sweepInterval = time.Minute
	// maxTrackedAddresses keeps the limiter to a few tens of megabytes.
	maxTrackedAddresses = 100_000
)

// accountLimiter counts device account creations per client address, and
// tracks at most maxAddresses of them.
type accountLimiter struct {
	perHour      int
	maxAddresses int
	clock        func() time.Time

	mu        sync.Mutex
	addresses map[string]*addressLimit
	sweptAt   time.Time
}

type addressLimit struct {
	limiter *rate.Limiter
	seen    time.Time
}

func newAccountLimiter(perHour, maxAddresses int, clock func() time.Time) *accountLimiter {
	return &accountLimiter{perHour: perHour, maxAddresses: maxAddresses, clock: clock, addresses: map[string]*addressLimit{}}
}

func (l *accountLimiter) allow(address string) bool {
	now := l.clock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.sweptAt) >= sweepInterval {
		l.sweepLocked(now)
	}
	a, ok := l.addresses[address]
	if !ok {
		// Past the cap, a flood of new addresses is refused rather than
		// tracked, so memory and the sweep stay bounded.
		if len(l.addresses) >= l.maxAddresses {
			return false
		}
		a = &addressLimit{limiter: rate.NewLimiter(rate.Every(limitWindow/time.Duration(l.perHour)), l.perHour)}
		l.addresses[address] = a
	}
	a.seen = now
	return a.limiter.AllowN(now, 1)
}

// sweepLocked forgets addresses idle for the whole window: they have a full
// allowance again, so forgetting them changes nothing.
func (l *accountLimiter) sweepLocked(now time.Time) {
	for addr, a := range l.addresses {
		if now.Sub(a.seen) >= limitWindow {
			delete(l.addresses, addr)
		}
	}
	l.sweptAt = now
}

// clientKey is the address a creation counts against: the IPv4 address, or
// the /64 an IPv6 client is normally given whole.
func clientKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return prefix.String()
}

func (l *accountLimiter) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.addresses)
}
