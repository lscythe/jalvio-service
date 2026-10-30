package httpapi

import (
	"fmt"
	"testing"
	"time"
)

func TestAccountLimitStaysBoundedUnderSustainedLoad(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	l := newAccountLimiter(10, 100, func() time.Time { return now })
	if !l.allow("192.0.2.1") {
		t.Fatal("first address refused")
	}
	refused := 0
	for i := range 1000 {
		now = now.Add(time.Second)
		if !l.allow(fmt.Sprintf("10.0.%d.%d", i/256, i%256)) {
			refused++
		}
	}
	if n := l.len(); n > 100 {
		t.Errorf("limiter holds %d addresses, want at most 100", n)
	}
	if refused == 0 {
		t.Error("new addresses beyond the cap were allowed")
	}
	if !l.allow("192.0.2.1") {
		t.Error("an address already tracked was refused at the cap")
	}
}

func TestAccountLimitForgetsIdleAddresses(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	l := newAccountLimiter(10, 100_000, func() time.Time { return now })
	for i := range 10_000 {
		l.allow(fmt.Sprintf("10.%d.%d.1", i/256, i%256))
	}
	now = now.Add(2 * time.Hour)
	l.allow("192.0.2.1")
	if n := l.len(); n >= 100 {
		t.Errorf("limiter holds %d addresses after two idle hours, want fewer than 100", n)
	}
}

func TestAccountLimitKeepsActiveAddresses(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	l := newAccountLimiter(1, 100_000, func() time.Time { return now })
	if !l.allow("192.0.2.1") {
		t.Fatal("first creation refused")
	}
	now = now.Add(30 * time.Minute)
	l.allow("198.51.100.7")
	if l.allow("192.0.2.1") {
		t.Error("an address active within the hour was forgotten and allowed again")
	}
}
