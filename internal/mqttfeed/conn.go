package mqttfeed

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

// dialWebSocket opens a WebSocket with the "mqtt" subprotocol, connecting to
// dialAddress instead of the URL's host when one is set. TLS still verifies
// the certificate against tlsConfig.ServerName.
func dialWebSocket(ctx context.Context, url, dialAddress string, tlsConfig *tls.Config) (net.Conn, error) {
	dialer := websocket.Dialer{
		TLSClientConfig: tlsConfig,
		Subprotocols:    []string{"mqtt"},
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if dialAddress != "" {
				addr = dialAddress
			}
			return new(net.Dialer).DialContext(ctx, network, addr)
		},
	}
	ws, resp, err := dialer.DialContext(ctx, url, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	return &wsConn{Conn: ws}, nil
}

// wsConn presents WebSocket binary messages as a byte stream, which is what
// the MQTT client expects.
type wsConn struct {
	*websocket.Conn
	reader io.Reader
}

func (c *wsConn) Read(p []byte) (int, error) {
	for {
		if c.reader == nil {
			_, r, err := c.NextReader()
			if err != nil {
				return 0, err
			}
			c.reader = r
		}
		n, err := c.reader.Read(p)
		if err == io.EOF {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	if err := c.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}
