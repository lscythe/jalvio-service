package mqttfeed_test

import (
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
)

const (
	busTopic   = "/mobile_armada/0026-0028/3F/3F-L01/TST-001"
	busPayload = `{"bus_body_no":"TST-001","route_code":"3F","trip_id":"3F-L01",` +
		`"latitude":-6.2,"longitude":106.8,"timestamp":1785355911000}`
	noFixPayload = `{"bus_body_no":"TST-001","route_code":"3F","trip_id":"3F-L01",` +
		`"latitude":0,"longitude":0,"timestamp":1785355912000}`
)

// startFeed runs a feed against b. The broker URL names an unresolvable host
// so the connection only works through the dial address override, and the
// TLS server name matches the test certificate rather than that host.
func startFeed(t *testing.T, b *broker, sink *fleet.Fleet, demand *mqttfeed.Demand) (*mqttfeed.Feed, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	return runFeed(t, brokerConfig(b.addr(), b.rootCAs()), sink, demand, slog.New(slog.DiscardHandler))
}

func brokerConfig(dialAddress string, rootCAs *x509.CertPool) mqttfeed.Config {
	return mqttfeed.Config{
		URL:           "wss://broker.invalid:8084/mqtt",
		TLSServerName: "example.com",
		DialAddress:   dialAddress,
		ClientID:      "jalvio-test",
		RootCAs:       rootCAs,
	}
}

func runFeed(t *testing.T, cfg mqttfeed.Config, sink *fleet.Fleet, demand *mqttfeed.Demand, logger *slog.Logger) (*mqttfeed.Feed, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	feed := mqttfeed.New(cfg, sink, demand, logger)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		feed.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return feed, cancel, done
}

// routeDemand holds one route, like a client watching it.
func routeDemand(code string) *mqttfeed.Demand {
	d := mqttfeed.NewDemand(time.Now)
	d.Hold("test", routeInterest(code))
	return d
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitSubscribed(t *testing.T, b *broker) {
	t.Helper()
	select {
	case <-b.subscribed:
	case <-time.After(10 * time.Second):
		t.Fatal("feed did not subscribe")
	}
}

func TestFeedConnectsAndAppliesMessages(t *testing.T) {
	b := newBroker(t)
	buses := fleet.New()
	feed, _, _ := startFeed(t, b, buses, routeDemand("3F"))

	waitSubscribed(t, b)
	serverNames, subprotocols, subscriptions := b.seen()
	if serverNames[0] != "example.com" {
		t.Errorf("TLS server name = %q, want example.com", serverNames[0])
	}
	if subprotocols[0] != "mqtt" {
		t.Errorf("WebSocket subprotocol = %q, want mqtt", subprotocols[0])
	}
	if subscriptions[0] != "/mobile_armada/+/3F/#" {
		t.Errorf("subscription = %q, want /mobile_armada/+/3F/#", subscriptions[0])
	}

	b.publish(busTopic, []byte(busPayload))
	b.publish(busTopic, []byte("not json"))
	b.publish(busTopic, []byte(noFixPayload))

	waitFor(t, "all messages", func() bool { return feed.Status().Messages == 3 })
	if _, ok := buses.Bus("TST-001"); !ok {
		t.Error("bus from the feed not applied")
	}
	st := feed.Status()
	if !st.Connected || st.Malformed != 1 || st.LastMessageAt.IsZero() {
		t.Errorf("status = %+v, want connected with 1 malformed message and a last message time", st)
	}
	// A report without a GPS fix is skipped but is not malformed.
	if want := map[string]int64{"payload": 1, "no_position": 1}; !reflect.DeepEqual(st.Rejected, want) {
		t.Errorf("rejected = %v, want %v", st.Rejected, want)
	}
	st.Rejected["payload"] = 99
	if got := feed.Status().Rejected["payload"]; got != 1 {
		t.Errorf("Status shares its rejected map with the feed: got %d after a caller changed it", got)
	}
}

func reportObservedAt(at time.Time) []byte {
	return fmt.Appendf(nil, `{"bus_body_no":"TST-001","route_code":"3F","trip_id":"3F-L01",`+
		`"latitude":-6.2,"longitude":106.8,"timestamp":%d}`, at.UnixMilli())
}

func TestFeedReportsMedianDelay(t *testing.T) {
	b := newBroker(t)
	feed, _, _ := startFeed(t, b, fleet.New(), routeDemand("3F"))
	waitSubscribed(t, b)
	if d := feed.Status().Delay; d != 0 {
		t.Errorf("Delay before any report = %v, want 0", d)
	}

	now := time.Now()
	b.publish(busTopic, reportObservedAt(now.Add(-2*time.Second)))
	b.publish(busTopic, reportObservedAt(now.Add(-4*time.Second)))
	b.publish(busTopic, reportObservedAt(now.Add(-time.Hour)))
	b.publish(busTopic, []byte("not json"))
	b.publish(busTopic, []byte(noFixPayload))

	waitFor(t, "all messages", func() bool { return feed.Status().Messages == 5 })
	if d := feed.Status().Delay; d < 4*time.Second || d > 5*time.Second {
		t.Errorf("Delay = %v, want the median of the decoded reports, about 4s", d)
	}
}

func TestFeedDelayFollowsRecentReports(t *testing.T) {
	b := newBroker(t)
	feed, _, _ := startFeed(t, b, fleet.New(), routeDemand("3F"))
	waitSubscribed(t, b)

	now := time.Now()
	for range mqttfeed.DelayWindow {
		b.publish(busTopic, reportObservedAt(now.Add(-time.Minute)))
	}
	for range mqttfeed.DelayWindow/2 + 1 {
		b.publish(busTopic, reportObservedAt(now.Add(-2*time.Second)))
	}

	waitFor(t, "all messages", func() bool { return feed.Status().Messages == int64(mqttfeed.DelayWindow*3/2+1) })
	if d := feed.Status().Delay; d < 2*time.Second || d > 10*time.Second {
		t.Errorf("Delay = %v, want about 2s once recent reports outnumber older ones in the window", d)
	}
}

func TestFeedResubscribesAfterReconnect(t *testing.T) {
	b := newBroker(t)
	buses := fleet.New()
	feed, _, _ := startFeed(t, b, buses, routeDemand("3F"))
	waitSubscribed(t, b)

	b.dropClients()
	waitSubscribed(t, b)

	b.publish(busTopic, []byte(busPayload))
	waitFor(t, "message after reconnect", func() bool { _, ok := buses.Bus("TST-001"); return ok })
	if st := feed.Status(); st.Reconnects < 1 || !st.Connected {
		t.Errorf("status = %+v, want connected after at least one reconnect", st)
	}
}

func TestRunDisconnectsOnCancel(t *testing.T) {
	b := newBroker(t)
	feed, cancel, done := startFeed(t, b, fleet.New(), routeDemand("3F"))
	waitSubscribed(t, b)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if feed.Status().Connected {
		t.Error("status still connected after Run returned")
	}
}

func TestFeedLogsWhyConnectsFail(t *testing.T) {
	refusing := newBroker(t)
	refusing.refuseConnects()
	for _, tc := range []struct {
		name   string
		cfg    mqttfeed.Config
		reason string
	}{
		{"refused by the broker", brokerConfig(refusing.addr(), refusing.rootCAs()), "not Authorized"},
		{"unreachable", brokerConfig(closedAddress(t), nil), "connection refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &logBuffer{}
			runFeed(t, tc.cfg, fleet.New(), routeDemand("3F"), slog.New(slog.NewTextHandler(logs, nil)))
			waitFor(t, "the connect failure in the log", func() bool {
				s := logs.String()
				return strings.Contains(s, `msg="mqtt connect failed"`) && strings.Contains(s, tc.reason)
			})
		})
	}
}

func TestFeedReconnectsWhenPingsGoUnanswered(t *testing.T) {
	mqttfeed.SetPingTimingForTest(t, time.Second, 500*time.Millisecond)
	b := newBroker(t)
	b.ignorePings()
	feed, _, _ := startFeed(t, b, fleet.New(), routeDemand("3F"))
	waitFor(t, "a reconnect after an unanswered ping", func() bool { return feed.Status().Reconnects >= 1 })
}

// closedAddress is a local address nothing listens on.
func closedAddress(t *testing.T) string {
	t.Helper()
	l, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// logBuffer collects what the feed logs from its own goroutines.
type logBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func waitCurrent(t *testing.T, b *broker, want []string) {
	t.Helper()
	waitFor(t, fmt.Sprintf("broker filters %v", want), func() bool { return slices.Equal(b.current(), want) })
}

func TestFeedFollowsDemand(t *testing.T) {
	b := newBroker(t)
	buses := fleet.New()
	feed, _, _ := startFeed(t, b, buses, routeDemand("3F"))
	waitCurrent(t, b, []string{"/mobile_armada/+/3F/#"})

	b.publish("/mobile_armada/0026-0028/9/9-L01/TST-002", []byte(strings.ReplaceAll(busPayload, "TST-001", "TST-002")))
	b.publish(busTopic, []byte(busPayload))
	waitFor(t, "the watched route's bus", func() bool { _, ok := buses.Bus("TST-001"); return ok })
	if _, ok := buses.Bus("TST-002"); ok {
		t.Error("bus on an unwatched route applied")
	}
	if st := feed.Status(); st.Messages != 1 || st.Filters != 1 || st.WholeFleet {
		t.Errorf("status = %+v, want 1 message and 1 filter", st)
	}
}

func TestFeedUnsubscribesAfterGrace(t *testing.T) {
	b := newBroker(t)
	clock := newFakeClock()
	demand := mqttfeed.NewDemand(clock.Now)
	demand.Hold("test", routeInterest("3F"))
	feed, _, _ := startFeed(t, b, fleet.New(), demand)
	waitCurrent(t, b, []string{"/mobile_armada/+/3F/#"})

	demand.Release("test")
	clock.advanceTo(29 * time.Second)
	time.Sleep(1500 * time.Millisecond)
	if got := b.current(); len(got) != 1 {
		t.Fatalf("broker filters within the grace = %v, want the route kept", got)
	}
	clock.advanceTo(31 * time.Second)
	waitCurrent(t, b, nil)
	waitFor(t, "no filters in status", func() bool { return feed.Status().Filters == 0 })
}

func TestFeedResubscribesCurrentSetAfterReconnect(t *testing.T) {
	b := newBroker(t)
	demand := mqttfeed.NewDemand(time.Now)
	area := geo.BBox{West: 106.8272, East: 106.8272, South: -6.1754, North: -6.1754}
	demand.Hold("test", mqttfeed.Interest{Areas: []geo.BBox{area}, RouteCodes: []string{"3F"}})
	startFeed(t, b, fleet.New(), demand)
	want := demand.Wanted()
	waitCurrent(t, b, want)

	b.dropClients()
	waitFor(t, "a second connection replacing the first", func() bool {
		serverNames, _, _ := b.seen()
		return len(serverNames) == 2 && b.liveConnections() == 1
	})
	waitCurrent(t, b, want)
}

func TestFeedCountsRefusedFilters(t *testing.T) {
	b := newBroker(t)
	const refused = "/mobile_armada/+/9/#"
	b.refuse(refused)
	feed, _, _ := startFeed(t, b, fleet.New(), routeDemand("9"))
	waitFor(t, "the refusal in status", func() bool { return feed.Status().Rejected["subscribe_refused"] == 1 })

	time.Sleep(2 * time.Second)
	if got := feed.Status().Rejected["subscribe_refused"]; got != 1 {
		t.Errorf("subscribe_refused = %d after more ticks, want 1", got)
	}
	if n := b.timesSubscribed(refused); n != 1 {
		t.Errorf("refused filter subscribed %d times, want once", n)
	}
	if st := feed.Status(); st.Filters != 0 {
		t.Errorf("Filters = %d, want 0 for a refused filter", st.Filters)
	}
}

func TestFeedSubscribesNothingWithoutDemand(t *testing.T) {
	b := newBroker(t)
	feed, _, _ := startFeed(t, b, fleet.New(), mqttfeed.NewDemand(time.Now))
	waitFor(t, "connected", func() bool { return feed.Status().Connected })
	time.Sleep(1500 * time.Millisecond)
	if _, _, subscriptions := b.seen(); len(subscriptions) != 0 {
		t.Errorf("subscriptions = %v, want none", subscriptions)
	}
	if st := feed.Status(); st.Filters != 0 {
		t.Errorf("Filters = %d, want 0", st.Filters)
	}
}

func TestFeedSwitchesToWholeFleet(t *testing.T) {
	b := newBroker(t)
	demand := mqttfeed.NewDemand(time.Now)
	demand.Hold("test", mqttfeed.Interest{Areas: []geo.BBox{{West: 106.6, South: -6.4, East: 107.1, North: -5.9}}})
	feed, _, _ := startFeed(t, b, fleet.New(), demand)
	waitCurrent(t, b, []string{mqttfeed.WholeFleet})
	waitFor(t, "wholeFleet in status", func() bool { return feed.Status().WholeFleet })
}

func TestClientIDIsCreatedOnceAndReused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mqtt-client-id")

	first, err := mqttfeed.LoadOrCreateClientID(path, "client_")
	if err != nil {
		t.Fatalf("LoadOrCreateClientID: %v", err)
	}
	if !regexp.MustCompile(`^client_[0-9a-f]{16}$`).MatchString(first) {
		t.Errorf("client ID = %q, want client_ followed by 16 hex digits", first)
	}
	second, err := mqttfeed.LoadOrCreateClientID(path, "client_")
	if err != nil {
		t.Fatalf("LoadOrCreateClientID: %v", err)
	}
	if second != first {
		t.Errorf("second client ID = %q, want the stored %q", second, first)
	}

	if err := os.WriteFile(path, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mqttfeed.LoadOrCreateClientID(path, "client_"); err == nil {
		t.Error("blank stored client ID accepted, want error")
	}
}

func TestClientIDIsReplacedWhenThePrefixChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mqtt-client-id")
	if err := os.WriteFile(path, []byte("jalvio-0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	id, err := mqttfeed.LoadOrCreateClientID(path, "client_")
	if err != nil {
		t.Fatalf("LoadOrCreateClientID: %v", err)
	}
	if !regexp.MustCompile(`^client_[0-9a-f]{16}$`).MatchString(id) {
		t.Errorf("client ID = %q, want client_ followed by 16 hex digits", id)
	}
	again, err := mqttfeed.LoadOrCreateClientID(path, "client_")
	if err != nil {
		t.Fatalf("LoadOrCreateClientID: %v", err)
	}
	if again != id {
		t.Errorf("client ID after restart = %q, want the replaced %q", again, id)
	}
}
