package mqttfeed_test

import (
	"bufio"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// broker is a minimal MQTT 3.1.1 broker over TLS WebSocket. It handles just
// enough of the protocol for the feed: CONNECT, SUBSCRIBE, UNSUBSCRIBE,
// PINGREQ, and QoS 0 PUBLISH to clients whose filters match.
type broker struct {
	server *httptest.Server

	mu             sync.Mutex
	conns          map[*websocket.Conn]*brokerConn
	serverNames    []string
	subprotocols   []string
	subscriptions  []string
	subscribeCount map[string]int
	refused        map[string]bool
	subscribed     chan struct{}
}

type brokerConn struct {
	write   sync.Mutex
	filters map[string]bool
}

func newBroker(t *testing.T) *broker {
	t.Helper()
	b := &broker{
		conns:          map[*websocket.Conn]*brokerConn{},
		subscribeCount: map[string]int{},
		refused:        map[string]bool{},
		subscribed:     make(chan struct{}, 16),
	}
	upgrader := websocket.Upgrader{Subprotocols: []string{"mqtt"}}
	b.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		b.mu.Lock()
		b.serverNames = append(b.serverNames, r.TLS.ServerName)
		b.subprotocols = append(b.subprotocols, ws.Subprotocol())
		b.conns[ws] = &brokerConn{filters: map[string]bool{}}
		b.mu.Unlock()
		b.serve(ws)
	}))
	t.Cleanup(func() {
		b.dropClients()
		b.server.Close()
	})
	return b
}

func (b *broker) addr() string { return strings.TrimPrefix(b.server.URL, "https://") }

func (b *broker) rootCAs() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(b.server.Certificate())
	return pool
}

func (b *broker) serve(ws *websocket.Conn) {
	defer b.forget(ws)
	r := bufio.NewReader(&wsReader{conn: ws})
	for {
		kind, body, err := readPacket(r)
		if err != nil {
			return
		}
		switch kind {
		case 1: // CONNECT
			b.send(ws, []byte{0x20, 0x02, 0x00, 0x00})
		case 8: // SUBSCRIBE
			codes := b.subscribe(ws, filtersIn(body[2:], true))
			b.send(ws, append(append([]byte{0x90}, remainingLength(2+len(codes))...), append(body[:2:2], codes...)...))
			select {
			case b.subscribed <- struct{}{}:
			default:
			}
		case 10: // UNSUBSCRIBE
			b.unsubscribe(ws, filtersIn(body[2:], false))
			b.send(ws, []byte{0xB0, 0x02, body[0], body[1]})
		case 12: // PINGREQ
			b.send(ws, []byte{0xD0, 0x00})
		case 14: // DISCONNECT
			return
		}
	}
}

// filtersIn reads the topic filters of a SUBSCRIBE (each followed by a QoS
// byte) or UNSUBSCRIBE payload.
func filtersIn(payload []byte, withQoS bool) []string {
	var filters []string
	for len(payload) >= 2 {
		n := int(binary.BigEndian.Uint16(payload))
		filters = append(filters, string(payload[2:2+n]))
		payload = payload[2+n:]
		if withQoS {
			payload = payload[1:]
		}
	}
	return filters
}

func (b *broker) subscribe(ws *websocket.Conn, filters []string) []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	codes := make([]byte, len(filters))
	for i, f := range filters {
		b.subscriptions = append(b.subscriptions, f)
		b.subscribeCount[f]++
		if b.refused[f] {
			codes[i] = 0x80
			continue
		}
		if c, ok := b.conns[ws]; ok {
			c.filters[f] = true
		}
	}
	return codes
}

func (b *broker) unsubscribe(ws *websocket.Conn, filters []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.conns[ws]; ok {
		for _, f := range filters {
			delete(c.filters, f)
		}
	}
}

// refuse makes later SUBSCRIBEs of filter fail with return code 0x80.
func (b *broker) refuse(filter string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refused[filter] = true
}

func (b *broker) timesSubscribed(filter string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.subscribeCount[filter]
}

func (b *broker) liveConnections() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.conns)
}

// current lists the filters live connections hold, sorted.
func (b *broker) current() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, c := range b.conns {
		for f := range c.filters {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}

// matches reports whether an MQTT topic filter matches topic.
func matches(filter, topic string) bool {
	fs, ts := strings.Split(filter, "/"), strings.Split(topic, "/")
	for i, f := range fs {
		switch {
		case f == "#":
			return true
		case i >= len(ts):
			return false
		case f != "+" && f != ts[i]:
			return false
		}
	}
	return len(fs) == len(ts)
}

// publish sends a QoS 0 message to every client with a matching filter.
func (b *broker) publish(topic string, payload []byte) {
	body := binary.BigEndian.AppendUint16(nil, uint16(len(topic)))
	body = append(append(body, topic...), payload...)
	packet := append([]byte{0x30}, remainingLength(len(body))...)
	packet = append(packet, body...)
	b.mu.Lock()
	var conns []*websocket.Conn
	for ws, c := range b.conns {
		for f := range c.filters {
			if matches(f, topic) {
				conns = append(conns, ws)
				break
			}
		}
	}
	b.mu.Unlock()
	for _, ws := range conns {
		b.send(ws, packet)
	}
}

// dropClients closes every client connection, as a broker restart would.
func (b *broker) dropClients() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ws := range b.conns {
		_ = ws.Close()
	}
}

func (b *broker) send(ws *websocket.Conn, packet []byte) {
	b.mu.Lock()
	c, ok := b.conns[ws]
	b.mu.Unlock()
	if !ok {
		return
	}
	c.write.Lock()
	defer c.write.Unlock()
	_ = ws.WriteMessage(websocket.BinaryMessage, packet)
}

func (b *broker) forget(ws *websocket.Conn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.conns, ws)
	_ = ws.Close()
}

func (b *broker) seen() (serverNames, subprotocols, subscriptions []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.serverNames...), append([]string(nil), b.subprotocols...), append([]string(nil), b.subscriptions...)
}

func readPacket(r *bufio.Reader) (kind byte, body []byte, err error) {
	header, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	length, multiplier := 0, 1
	for {
		digit, err := r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		length += int(digit&0x7F) * multiplier
		if digit&0x80 == 0 {
			break
		}
		multiplier *= 128
	}
	body = make([]byte, length)
	_, err = io.ReadFull(r, body)
	return header >> 4, body, err
}

func remainingLength(n int) []byte {
	var out []byte
	for {
		digit := byte(n % 128)
		n /= 128
		if n > 0 {
			digit |= 0x80
		}
		out = append(out, digit)
		if n == 0 {
			return out
		}
	}
}

// wsReader reads WebSocket messages as one byte stream.
type wsReader struct {
	conn *websocket.Conn
	cur  io.Reader
}

func (w *wsReader) Read(p []byte) (int, error) {
	for {
		if w.cur == nil {
			_, r, err := w.conn.NextReader()
			if err != nil {
				return 0, err
			}
			w.cur = r
		}
		n, err := w.cur.Read(p)
		if err == io.EOF {
			w.cur = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}
