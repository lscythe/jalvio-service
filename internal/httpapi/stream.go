package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/time/rate"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
)

const (
	liveSubprotocol  = "jalvio.live.v1"
	maxInboundFrame  = 4 << 10
	subscribesPerSec = 2
	livePingEvery    = 25 * time.Second
	liveWriteWait    = 10 * time.Second
	liveStatusEvery  = time.Second
	livePongWait     = livePingEvery + liveWriteWait
)

type LiveConfig struct {
	MaxConnections int
	// BrowserOrigins may open the stream; requests without an Origin header,
	// such as the native apps', are always allowed.
	BrowserOrigins []string
}

type liveStream struct {
	catalogs Catalogs
	feed     Feed
	buses    Buses
	demand   Demand
	limits   LiveConfig
	done     <-chan struct{}
	// open counts connections across every region, which share the limit.
	open     *atomic.Int64
	sessions atomic.Int64
	upgrader websocket.Upgrader
}

func newLiveStream(r Region, d Deps, open *atomic.Int64) *liveStream {
	s := &liveStream{catalogs: r.Catalogs, feed: r.Feed, buses: r.Buses, demand: r.Demand, limits: d.Live, done: d.Done, open: open}
	s.upgrader = websocket.Upgrader{
		Subprotocols: []string{liveSubprotocol},
		CheckOrigin:  s.allowOrigin,
		Error: func(w http.ResponseWriter, _ *http.Request, status int, reason error) {
			writeProblemDetail(w, status, reason.Error())
		},
	}
	return s
}

func (s *liveStream) allowOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || slices.Contains(s.limits.BrowserOrigins, origin)
}

type statusFrame struct {
	Type           string `json:"type"`
	Feed           string `json:"feed"`
	CatalogVersion string `json:"catalogVersion,omitempty"`
}

type snapshotFrame struct {
	Type  string    `json:"type"`
	Buses []busItem `json:"buses"`
}

type updateFrame struct {
	Type    string    `json:"type"`
	Buses   []busItem `json:"buses"`
	Removed []string  `json:"removed"`
}

type subscribeFrame struct {
	Type   string     `json:"type"`
	BBox   *[]float64 `json:"bbox"`
	Routes *[]string  `json:"routes"`
}

// liveFilter selects the buses a connection follows. With byRoute set and no
// codes, it matches nothing, like a /v1/buses query for unknown routes.
type liveFilter struct {
	box     geo.BBox
	byRoute bool
	codes   []string
}

func (f liveFilter) matches(b fleet.Bus) bool {
	return f.box.Contains(b.Position) && (!f.byRoute || slices.Contains(f.codes, b.RouteCode))
}

// interest names routes alone when the filter has them: the broker cannot
// select routes inside an area, and routes are the smaller set.
func (f liveFilter) interest() mqttfeed.Interest {
	if f.byRoute {
		return mqttfeed.Interest{RouteCodes: f.codes}
	}
	return mqttfeed.Interest{Areas: []geo.BBox{f.box}}
}

func (s *liveStream) serve(w http.ResponseWriter, r *http.Request) {
	if s.catalogs.Current() == nil {
		writeProblem(w, http.StatusServiceUnavailable)
		return
	}
	if !slices.Contains(websocket.Subprotocols(r), liveSubprotocol) {
		writeBadRequest(w, "offer the "+liveSubprotocol+" subprotocol")
		return
	}
	defer s.open.Add(-1)
	if s.open.Add(1) > int64(s.limits.MaxConnections) {
		writeProblemDetail(w, http.StatusServiceUnavailable, "too many live connections")
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	// Subscribing before the first frame means no change is missed between
	// a snapshot and the batches that follow it.
	sub := s.buses.Subscribe()
	defer sub.Close()

	filters := make(chan liveFilter)
	quit := make(chan struct{})
	defer close(quit)
	go s.readFilters(conn, filters, quit)

	key := fmt.Sprintf("live-%d", s.sessions.Add(1))
	holding := false
	defer func() {
		if holding {
			s.demand.Release(key)
		}
	}()

	sess := &liveSession{conn: conn}
	if sess.sendStatus(s.status()) != nil {
		return
	}
	ping := time.NewTicker(livePingEvery)
	defer ping.Stop()
	status := time.NewTicker(liveStatusEvery)
	defer status.Stop()
	for {
		var err error
		select {
		case <-s.done:
			closeConn(conn, websocket.CloseGoingAway, "")
			return
		case f, ok := <-filters:
			if !ok {
				return
			}
			if s.demand != nil {
				s.demand.Hold(key, f.interest())
				holding = true
			}
			err = sess.sendSnapshot(s.catalogs.Current(), f, s.buses.InBBox(f.box, nil))
		case batch, ok := <-sub.Batches():
			if !ok {
				closeConn(conn, websocket.CloseTryAgainLater, "slow consumer")
				return
			}
			err = sess.sendUpdate(s.catalogs.Current(), batch)
		case <-status.C:
			err = sess.sendStatus(s.status())
		case <-ping.C:
			err = sess.write(websocket.PingMessage, nil)
		}
		if err != nil {
			return
		}
	}
}

func (s *liveStream) status() statusFrame {
	return statusFrame{Type: "status", Feed: feedState(s.feed), CatalogVersion: s.catalogs.Status().Version}
}

// readFilters turns subscribe frames into filters until the connection fails
// or a frame breaks the limits, which closes the connection.
func (s *liveStream) readFilters(conn *websocket.Conn, filters chan<- liveFilter, quit <-chan struct{}) {
	defer close(filters)
	conn.SetReadLimit(maxInboundFrame)
	_ = conn.SetReadDeadline(time.Now().Add(livePongWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(livePongWait)) })
	limiter := rate.NewLimiter(subscribesPerSec, subscribesPerSec)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		f, err := parseSubscribe(s.catalogs.Current(), data)
		if err == nil && !limiter.Allow() {
			err = errors.New("at most 2 subscribes per second")
		}
		if err != nil {
			closeConn(conn, websocket.ClosePolicyViolation, err.Error())
			return
		}
		select {
		case filters <- f:
		case <-quit:
			return
		}
	}
}

var errSubscribe = errors.New(`send {"type":"subscribe"} with bbox, routes, or both`)

func parseSubscribe(c *catalog.Catalog, data []byte) (liveFilter, error) {
	var msg subscribeFrame
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&msg); err != nil || msg.Type != "subscribe" {
		return liveFilter{}, errSubscribe
	}
	if msg.BBox == nil && msg.Routes == nil {
		return liveFilter{}, errors.New("give bbox or routes")
	}
	f := liveFilter{box: world}
	if msg.BBox != nil {
		var err error
		if len(*msg.BBox) != 4 {
			err = errors.New("bbox needs four numbers")
		} else {
			f.box, err = newBBox([4]float64(*msg.BBox))
		}
		if err != nil {
			return liveFilter{}, errors.New("bbox must be [west,south,east,north] spanning at most 1 degree on each axis")
		}
	}
	if msg.Routes != nil {
		if len(*msg.Routes) == 0 || slices.Contains(*msg.Routes, "") {
			return liveFilter{}, errors.New("routes must list route IDs")
		}
		f.byRoute = true
		for _, id := range *msg.Routes {
			if route, ok := c.Route(id); ok {
				f.codes = append(f.codes, route.Code)
			}
		}
	}
	return f, nil
}

// liveSession is the writing side of one connection. Only the serving
// goroutine uses it.
type liveSession struct {
	conn   *websocket.Conn
	filter *liveFilter
	sent   map[string]bool
	status statusFrame
}

func (s *liveSession) sendStatus(st statusFrame) error {
	if st == s.status {
		return nil
	}
	s.status = st
	return s.writeJSON(st)
}

func (s *liveSession) sendSnapshot(c *catalog.Catalog, f liveFilter, candidates []fleet.Bus) error {
	s.filter = &f
	s.sent = map[string]bool{}
	frame := snapshotFrame{Type: "snapshot", Buses: []busItem{}}
	for _, b := range candidates {
		if f.matches(b) {
			frame.Buses = append(frame.Buses, newBusItem(c, b))
			s.sent[b.Plate] = true
		}
	}
	return s.writeJSON(frame)
}

// sendUpdate passes on the part of batch the client follows: matching buses,
// and buses it was sent that stopped reporting or no longer match.
func (s *liveSession) sendUpdate(c *catalog.Catalog, batch fleet.Batch) error {
	if s.filter == nil {
		return nil
	}
	frame := updateFrame{Type: "update", Buses: []busItem{}, Removed: []string{}}
	for _, b := range batch.Upserted {
		switch {
		case s.filter.matches(b):
			frame.Buses = append(frame.Buses, newBusItem(c, b))
			s.sent[b.Plate] = true
		case s.sent[b.Plate]:
			frame.Removed = append(frame.Removed, b.Plate)
			delete(s.sent, b.Plate)
		}
	}
	for _, plate := range batch.Removed {
		if s.sent[plate] {
			frame.Removed = append(frame.Removed, plate)
			delete(s.sent, plate)
		}
	}
	if len(frame.Buses) == 0 && len(frame.Removed) == 0 {
		return nil
	}
	slices.Sort(frame.Removed)
	return s.writeJSON(frame)
}

func (s *liveSession) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.write(websocket.TextMessage, data)
}

func (s *liveSession) write(messageType int, data []byte) error {
	if err := s.conn.SetWriteDeadline(time.Now().Add(liveWriteWait)); err != nil {
		return err
	}
	return s.conn.WriteMessage(messageType, data)
}

func closeConn(conn *websocket.Conn, code int, reason string) {
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(liveWriteWait))
}
