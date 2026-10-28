// Package mqttfeed streams live bus positions from the MQTT
// broker into the fleet.
package mqttfeed

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"maps"
	"net"
	"net/url"
	"slices"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/lscythe/jalvio-service/internal/fleet"
)

const (
	connectTimeout       = 15 * time.Second
	connectRetryInterval = 10 * time.Second
	maxReconnectInterval = 60 * time.Second
	subscribeTimeout     = 10 * time.Second
	disconnectQuiesce    = 250 // milliseconds
	applyInterval        = 500 * time.Millisecond
	filtersPerPacket     = 500
	subackFailure        = 0x80
)

// The broker answers a ping only after the messages queued ahead of it,
// which took up to about 30 s on a slow link; Paho's 10 s default timeout
// then dropped connections that were still delivering.
var (
	keepAlive   = 30 * time.Second
	pingTimeout = 30 * time.Second
)

// DelayWindow is how many recent reports the median delay covers.
const DelayWindow = 1000

type Config struct {
	URL           string
	TLSServerName string
	// DialAddress, when set, is dialed instead of the URL's host.
	DialAddress string
	ClientID    string
	// RootCAs verifies the broker certificate; nil uses the system roots.
	RootCAs *x509.CertPool
}

type Sink interface {
	Apply(u fleet.Update, now time.Time) bool
}

type Status struct {
	Connected     bool
	LastMessageAt time.Time
	// Delay is the median time from a report's own timestamp to its receipt
	// over the last DelayWindow decoded reports, so a backlog on the broker
	// shows up as a growing delay. The median ignores buses with wrong clocks.
	// It is zero until a report is decoded.
	Delay time.Duration
	// Reconnects counts reconnect attempts since start.
	Reconnects int
	Messages   int64
	// Malformed counts messages that could not be decoded.
	Malformed int64
	// Rejected counts every skipped message by fleet.RejectReason,
	// including reports without a GPS fix, which are not malformed, and
	// filters the broker refused as subscribe_refused.
	Rejected map[string]int64
	// Filters counts the broker filters currently subscribed.
	Filters    int
	WholeFleet bool
}

type Feed struct {
	cfg    Config
	sink   Sink
	demand *Demand
	logger *slog.Logger
	resync chan struct{}

	mu sync.Mutex
	// connections counts connects, so sync knows the broker forgot every
	// filter (clean session) since it last ran.
	connections int
	status      Status
	delays      recentDelays

	// Only Run's goroutine uses these.
	syncedConnection int
	subscribed       map[string]bool
	refused          map[string]bool
}

type recentDelays struct {
	samples []time.Duration
	next    int
}

func (r *recentDelays) add(d time.Duration) {
	if len(r.samples) < DelayWindow {
		r.samples = append(r.samples, d)
		return
	}
	r.samples[r.next] = d
	r.next = (r.next + 1) % DelayWindow
}

func (r *recentDelays) median() time.Duration {
	if len(r.samples) == 0 {
		return 0
	}
	sorted := slices.Clone(r.samples)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}

func New(cfg Config, sink Sink, demand *Demand, logger *slog.Logger) *Feed {
	return &Feed{cfg: cfg, sink: sink, demand: demand, logger: logger, resync: make(chan struct{}, 1)}
}

func (f *Feed) Status() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.status
	st.Rejected = maps.Clone(f.status.Rejected)
	st.Delay = f.delays.median()
	return st
}

// Run connects, retrying until it succeeds, and keeps the broker
// subscriptions in step with what demand wants, after every connect and as
// demand changes. It returns once ctx is cancelled and the client has
// disconnected.
func (f *Feed) Run(ctx context.Context) {
	tlsConfig := &tls.Config{
		ServerName: f.cfg.TLSServerName,
		RootCAs:    f.cfg.RootCAs,
		MinVersion: tls.VersionTLS12,
	}
	opts := mqtt.NewClientOptions().
		AddBroker(f.cfg.URL).
		SetClientID(f.cfg.ClientID).
		SetCleanSession(true).
		SetConnectTimeout(connectTimeout).
		SetKeepAlive(keepAlive).
		SetPingTimeout(pingTimeout).
		SetConnectRetry(true).
		SetConnectRetryInterval(connectRetryInterval).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(maxReconnectInterval).
		SetCustomOpenConnectionFn(func(uri *url.URL, _ mqtt.ClientOptions) (net.Conn, error) {
			dialCtx, cancel := context.WithTimeout(ctx, connectTimeout)
			defer cancel()
			return dialWebSocket(dialCtx, uri.String(), f.cfg.DialAddress, tlsConfig)
		}).
		// Filters overlap (a bus can match a tile and its route), and Paho
		// calls every matching subscription handler; one default handler
		// handles each delivered message once.
		SetDefaultPublishHandler(f.handle).
		SetOnConnectHandler(f.onConnect).
		SetConnectionNotificationHandler(f.onConnectionNotification).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			f.setConnected(false)
			f.logger.Warn("mqtt connection lost", "error", err)
		}).
		SetReconnectingHandler(func(mqtt.Client, *mqtt.ClientOptions) {
			f.mu.Lock()
			f.status.Reconnects++
			f.mu.Unlock()
		})

	client := mqtt.NewClient(opts)
	client.Connect()
	tick := time.NewTicker(applyInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			client.Disconnect(disconnectQuiesce)
			f.setConnected(false)
			return
		case <-tick.C:
			f.sync(ctx, client)
		case <-f.resync:
			f.sync(ctx, client)
		}
	}
}

func (f *Feed) onConnect(mqtt.Client) {
	f.mu.Lock()
	f.status.Connected = true
	f.connections++
	f.mu.Unlock()
	f.logger.Info("mqtt connected")
	select {
	case f.resync <- struct{}{}:
	default:
	}
}

// onConnectionNotification logs why each connect attempt failed: the dial,
// TLS or WebSocket error, or the broker's CONNACK refusal. Paho retries
// without reporting it otherwise.
func (f *Feed) onConnectionNotification(_ mqtt.Client, n mqtt.ConnectionNotification) {
	if failed, ok := n.(mqtt.ConnectionNotificationFailed); ok {
		f.logger.Warn("mqtt connect failed", "error", failed.Reason)
	}
}

// sync subscribes what demand wants and the broker lacks, then unsubscribes
// what is no longer wanted, so a switch to the whole fleet has no gap.
func (f *Feed) sync(ctx context.Context, c mqtt.Client) {
	// Asked every tick, so expired demand is forgotten while offline too.
	demanded := f.demand.Wanted()
	// The client's own state, because the lost and connect handlers run in
	// separate goroutines and can leave status.Connected behind.
	if !c.IsConnectionOpen() {
		// A clean session keeps nothing once the connection is gone.
		f.setFilters(0, false)
		return
	}
	f.mu.Lock()
	connection := f.connections
	f.mu.Unlock()
	if connection != f.syncedConnection {
		f.syncedConnection = connection
		f.subscribed, f.refused = map[string]bool{}, map[string]bool{}
	}

	wanted := map[string]bool{}
	var add []string
	for _, filter := range demanded {
		wanted[filter] = true
		if !f.subscribed[filter] && !f.refused[filter] {
			add = append(add, filter)
		}
	}
	var remove []string
	for filter := range f.subscribed {
		if !wanted[filter] {
			remove = append(remove, filter)
		}
	}
	for filter := range f.refused {
		if !wanted[filter] {
			delete(f.refused, filter)
		}
	}
	slices.Sort(remove)
	// Status follows each confirmed chunk, since a slow broker can take
	// several timeouts to get through them all.
	defer f.reportFilters()
	for chunk := range slices.Chunk(add, filtersPerPacket) {
		if ctx.Err() != nil {
			return
		}
		f.subscribe(ctx, c, chunk)
		f.reportFilters()
	}
	for chunk := range slices.Chunk(remove, filtersPerPacket) {
		if ctx.Err() != nil {
			return
		}
		f.unsubscribe(ctx, c, chunk)
		f.reportFilters()
	}
}

func (f *Feed) reportFilters() {
	f.setFilters(len(f.subscribed), f.subscribed[WholeFleet])
}

func (f *Feed) setFilters(n int, wholeFleet bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.Filters = n
	f.status.WholeFleet = wholeFleet
}

// confirmed waits for the broker to answer t, giving up after
// subscribeTimeout or when ctx ends, so shutdown never waits on the broker.
func confirmed(ctx context.Context, t mqtt.Token) bool {
	timer := time.NewTimer(subscribeTimeout)
	defer timer.Stop()
	select {
	case <-t.Done():
		return t.Error() == nil
	case <-timer.C:
	case <-ctx.Done():
	}
	return false
}

// subscribe leaves filters out of the record when the broker does not
// confirm them, so the next sync tries again.
func (f *Feed) subscribe(ctx context.Context, c mqtt.Client, filters []string) {
	qos := make(map[string]byte, len(filters))
	for _, filter := range filters {
		qos[filter] = 0
	}
	token := c.SubscribeMultiple(qos, nil)
	if !confirmed(ctx, token) {
		if ctx.Err() == nil {
			f.logger.Warn("mqtt subscribe failed", "filters", len(filters), "error", token.Error())
		}
		return
	}
	refused := 0
	for filter, code := range token.(*mqtt.SubscribeToken).Result() {
		if code == subackFailure {
			f.refused[filter] = true
			refused++
			continue
		}
		f.subscribed[filter] = true
	}
	if refused > 0 {
		f.mu.Lock()
		if f.status.Rejected == nil {
			f.status.Rejected = map[string]int64{}
		}
		f.status.Rejected["subscribe_refused"] += int64(refused)
		f.mu.Unlock()
		f.logger.Warn("mqtt broker refused filters", "filters", refused)
	}
}

// unsubscribe forgets filters whatever the broker answers: a filter it kept
// only costs traffic until the next reconnect.
func (f *Feed) unsubscribe(ctx context.Context, c mqtt.Client, filters []string) {
	token := c.Unsubscribe(filters...)
	if !confirmed(ctx, token) && ctx.Err() == nil {
		f.logger.Warn("mqtt unsubscribe failed", "filters", len(filters), "error", token.Error())
	}
	for _, filter := range filters {
		delete(f.subscribed, filter)
	}
}

// handle decodes a message into the sink. Payloads are never logged.
func (f *Feed) handle(_ mqtt.Client, m mqtt.Message) {
	now := time.Now()
	u, err := fleet.Decode(m.Topic(), m.Payload())
	f.mu.Lock()
	f.status.Messages++
	f.status.LastMessageAt = now.UTC()
	if err != nil {
		// Buses without a GPS fix send zero coordinates; that is not malformed.
		if !errors.Is(err, fleet.ErrNoPosition) {
			f.status.Malformed++
		}
		if f.status.Rejected == nil {
			f.status.Rejected = map[string]int64{}
		}
		f.status.Rejected[fleet.RejectReason(err)]++
	} else {
		f.delays.add(now.Sub(u.ObservedAt))
	}
	f.mu.Unlock()
	if err == nil {
		f.sink.Apply(u, now)
	}
}

func (f *Feed) setConnected(connected bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.Connected = connected
}
