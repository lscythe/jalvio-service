package mqttfeed

import (
	"testing"
	"time"
)

// SetPingTimingForTest shortens the keepalive so a test can watch a ping go
// unanswered without waiting for the real timeout.
func SetPingTimingForTest(t *testing.T, every, timeout time.Duration) {
	oldEvery, oldTimeout := keepAlive, pingTimeout
	keepAlive, pingTimeout = every, timeout
	t.Cleanup(func() { keepAlive, pingTimeout = oldEvery, oldTimeout })
}
