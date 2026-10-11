package mqttfeed_test

import (
	"bufio"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// broker is a minimal MQTT 3.1.1 broker over TLS WebSocket. It handles just
// enough of the protocol for the feed: CONNECT, SUBSCRIBE, PINGREQ, and
// QoS 0 PUBLISH to clients.
type broker struct {
	server *httptest.Server

	mu            sync.Mutex
	conns         map[*websocket.Conn]*sync.Mutex
	serverNames   []string
	subprotocols  []string
	subscriptions []string
	subscribed    chan struct{}
}

func newBroker(t *testing.T) *broker {
	t.Helper()
	b := &broker{conns: map[*websocket.Conn]*sync.Mutex{}, subscribed: make(chan struct{}, 16)}
	upgrader := websocket.Upgrader{Subprotocols: []string{"mqtt"}}
	b.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		b.mu.Lock()
		b.serverNames = append(b.serverNames, r.TLS.ServerName)
		b.subprotocols = append(b.subprotocols, ws.Subprotocol())
		b.conns[ws] = &sync.Mutex{}
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
			id := body[:2]
			topic := string(body[4 : 4+binary.BigEndian.Uint16(body[2:4])])
			b.mu.Lock()
			b.subscriptions = append(b.subscriptions, topic)
			b.mu.Unlock()
			b.send(ws, []byte{0x90, 0x03, id[0], id[1], 0x00})
			b.subscribed <- struct{}{}
		case 12: // PINGREQ
			b.send(ws, []byte{0xD0, 0x00})
		case 14: // DISCONNECT
			return
		}
	}
}

// publish sends a QoS 0 message to every connected client.
func (b *broker) publish(topic string, payload []byte) {
	body := binary.BigEndian.AppendUint16(nil, uint16(len(topic)))
	body = append(append(body, topic...), payload...)
	packet := append([]byte{0x30}, remainingLength(len(body))...)
	packet = append(packet, body...)
	b.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(b.conns))
	for ws := range b.conns {
		conns = append(conns, ws)
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
	lock, ok := b.conns[ws]
	b.mu.Unlock()
	if !ok {
		return
	}
	lock.Lock()
	defer lock.Unlock()
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
