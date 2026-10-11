package mqttfeed_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
)

const (
	busTopic   = "/mobile_armada/0026-0028/3F/3F-L01/TST-001"
	busPayload = `{"bus_body_no":"TST-001","route_code":"3F","trip_id":"3F-L01",` +
		`"latitude":-6.2,"longitude":106.8,"timestamp":1785355911000}`
)

// startFeed runs a feed against b. The broker URL names an unresolvable host
// so the connection only works through the dial address override, and the
// TLS server name matches the test certificate rather than that host.
func startFeed(t *testing.T, b *broker, sink *fleet.Fleet) (*mqttfeed.Feed, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	feed := mqttfeed.New(mqttfeed.Config{
		URL:           "wss://broker.invalid:8084/mqtt",
		TLSServerName: "example.com",
		DialAddress:   b.addr(),
		ClientID:      "jalvio-test",
		RootCAs:       b.rootCAs(),
	}, sink, slog.New(slog.DiscardHandler))
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
	feed, _, _ := startFeed(t, b, buses)

	waitSubscribed(t, b)
	serverNames, subprotocols, subscriptions := b.seen()
	if serverNames[0] != "example.com" {
		t.Errorf("TLS server name = %q, want example.com", serverNames[0])
	}
	if subprotocols[0] != "mqtt" {
		t.Errorf("WebSocket subprotocol = %q, want mqtt", subprotocols[0])
	}
	if subscriptions[0] != "/mobile_armada/#" {
		t.Errorf("subscription = %q, want /mobile_armada/#", subscriptions[0])
	}

	b.publish(busTopic, []byte(busPayload))
	b.publish(busTopic, []byte("not json"))

	waitFor(t, "both messages", func() bool { return feed.Status().Messages == 2 })
	if _, ok := buses.Bus("TST-001"); !ok {
		t.Error("bus from the feed not applied")
	}
	st := feed.Status()
	if !st.Connected || st.Malformed != 1 || st.LastMessageAt.IsZero() {
		t.Errorf("status = %+v, want connected with 1 malformed message and a last message time", st)
	}
}

func TestFeedResubscribesAfterReconnect(t *testing.T) {
	b := newBroker(t)
	buses := fleet.New()
	feed, _, _ := startFeed(t, b, buses)
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
	feed, cancel, done := startFeed(t, b, fleet.New())
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

func TestClientIDIsCreatedOnceAndReused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mqtt-client-id")

	first, err := mqttfeed.LoadOrCreateClientID(path)
	if err != nil {
		t.Fatalf("LoadOrCreateClientID: %v", err)
	}
	if !regexp.MustCompile(`^jalvio-[0-9a-f]{16}$`).MatchString(first) {
		t.Errorf("client ID = %q, want jalvio- followed by 16 hex digits", first)
	}
	second, err := mqttfeed.LoadOrCreateClientID(path)
	if err != nil {
		t.Fatalf("LoadOrCreateClientID: %v", err)
	}
	if second != first {
		t.Errorf("second client ID = %q, want the stored %q", second, first)
	}

	if err := os.WriteFile(path, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mqttfeed.LoadOrCreateClientID(path); err == nil {
		t.Error("blank stored client ID accepted, want error")
	}
}
