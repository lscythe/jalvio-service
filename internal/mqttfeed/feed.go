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
	topic                = "/mobile_armada/#"
	connectTimeout       = 15 * time.Second
	connectRetryInterval = 10 * time.Second
	maxReconnectInterval = 60 * time.Second
	subscribeTimeout     = 10 * time.Second
	disconnectQuiesce    = 250 // milliseconds
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
	// including reports without a GPS fix, which are not malformed.
	Rejected map[string]int64
}

type Feed struct {
	cfg    Config
	sink   Sink
	logger *slog.Logger

	mu     sync.Mutex
	status Status
	delays recentDelays
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

func New(cfg Config, sink Sink, logger *slog.Logger) *Feed {
	return &Feed{cfg: cfg, sink: sink, logger: logger}
}

func (f *Feed) Status() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.status
	st.Rejected = maps.Clone(f.status.Rejected)
	st.Delay = f.delays.median()
	return st
}

// Run connects, retrying until it succeeds, and subscribes to the whole fleet
// after every connect. It returns once ctx is cancelled and the client has
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
		SetConnectRetry(true).
		SetConnectRetryInterval(connectRetryInterval).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(maxReconnectInterval).
		SetCustomOpenConnectionFn(func(uri *url.URL, _ mqtt.ClientOptions) (net.Conn, error) {
			dialCtx, cancel := context.WithTimeout(ctx, connectTimeout)
			defer cancel()
			return dialWebSocket(dialCtx, uri.String(), f.cfg.DialAddress, tlsConfig)
		}).
		SetOnConnectHandler(f.onConnect).
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
	<-ctx.Done()
	client.Disconnect(disconnectQuiesce)
	f.setConnected(false)
}

func (f *Feed) onConnect(c mqtt.Client) {
	f.setConnected(true)
	f.logger.Info("mqtt connected")
	token := c.Subscribe(topic, 0, f.handle)
	if !token.WaitTimeout(subscribeTimeout) {
		f.logger.Warn("mqtt subscribe timed out")
		return
	}
	if err := token.Error(); err != nil {
		f.logger.Warn("mqtt subscribe failed", "error", err)
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
