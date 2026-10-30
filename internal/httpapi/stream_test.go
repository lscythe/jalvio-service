package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/httpapi"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
)

const liveProtocol = "jalvio.live.v1"

var liveSchemas = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	path, err := filepath.Abs("../../api/live.schema.json")
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	schemas := map[string]*jsonschema.Schema{}
	for _, def := range []string{"Subscribe", "ServerFrame"} {
		if schemas[def], err = c.Compile(path + "#/$defs/" + def); err != nil {
			return nil, err
		}
	}
	return schemas, nil
})

// checkFrame fails the test when data doesn't match the named definition in
// api/live.schema.json.
func checkFrame(t *testing.T, def string, data []byte) {
	t.Helper()
	schemas, err := liveSchemas()
	if err != nil {
		t.Fatalf("api/live.schema.json: %v", err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("frame %s: %v", data, err)
	}
	if err := schemas[def].Validate(v); err != nil {
		t.Errorf("frame %s breaks the %s contract: %v", data, def, err)
	}
}

// quietFleet is liveFleet with its pending changes flushed, so the first
// batch a stream sees holds only what the test changes.
func quietFleet() *fleet.Fleet {
	f := liveFleet()
	f.Flush()
	return f
}

func liveDeps(t *testing.T, buses httpapi.Buses) httpapi.Deps {
	t.Helper()
	d := deps(true)
	d.Regions[0].Catalogs = fakeCatalogs{status: catalog.Status{Version: "feed-1"}, current: fixtureCatalog(t)}
	d.Regions[0].Buses = buses
	d.Live = httpapi.LiveConfig{MaxConnections: 10}
	return d
}

func startLive(t *testing.T, d httpapi.Deps) string {
	t.Helper()
	srv := httptest.NewServer(httpapi.New(d))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + regionPath + "/live"
}

func dialLive(t *testing.T, url string, header http.Header) (*websocket.Conn, error) {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{liveProtocol}}
	conn, resp, err := dialer.DialContext(t.Context(), url, header)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return nil, &handshakeError{resp.StatusCode}
		}
		return nil, err
	}
	t.Cleanup(func() { _ = conn.Close() })
	if conn.Subprotocol() != liveProtocol {
		t.Errorf("subprotocol = %q, want %q", conn.Subprotocol(), liveProtocol)
	}
	return conn, nil
}

type handshakeError struct{ status int }

func (e *handshakeError) Error() string { return http.StatusText(e.status) }

func mustDialLive(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, err := dialLive(t, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func readFrame(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	checkFrame(t, "ServerFrame", data)
	return bytes.TrimSpace(data)
}

type frame struct {
	Type  string `json:"type"`
	Buses []struct {
		FleetNumber string `json:"fleetNumber"`
	} `json:"buses"`
	Removed []string `json:"removed"`
}

// readBuses reads a frame of the given type and returns the fleet numbers it
// carries and removes.
func readBuses(t *testing.T, conn *websocket.Conn, wantType string) (buses, removed []string) {
	t.Helper()
	data := readFrame(t, conn)
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("frame %s: %v", data, err)
	}
	if f.Type != wantType {
		t.Fatalf("frame %s, want type %q", data, wantType)
	}
	for _, b := range f.Buses {
		buses = append(buses, b.FleetNumber)
	}
	return buses, f.Removed
}

func subscribe(t *testing.T, conn *websocket.Conn, msg string) {
	t.Helper()
	checkFrame(t, "Subscribe", []byte(msg))
	send(t, conn, msg)
}

func send(t *testing.T, conn *websocket.Conn, msg string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// readClose skips frames until the server closes the connection.
func readClose(t *testing.T, conn *websocket.Conn) *websocket.CloseError {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		var closeErr *websocket.CloseError
		if !errors.As(err, &closeErr) {
			t.Fatalf("read: %v, want a close frame", err)
		}
		return closeErr
	}
}

func moveBus(f *fleet.Fleet, fleetNumber string, at time.Time, lat, lng float64) {
	b, _ := f.Bus(fleetNumber)
	u := b.Update
	u.Position = geo.Point{Lat: lat, Lon: lng}
	u.ObservedAt = at
	f.Apply(u, at)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func onlyHold(t *testing.T, demand *recordingDemand) (string, mqttfeed.Interest) {
	t.Helper()
	held := demand.held()
	if len(held) != 1 {
		t.Fatalf("holds = %+v, want one", held)
	}
	for key, i := range held {
		return key, i
	}
	return "", mqttfeed.Interest{}
}

func TestLiveHoldsAndReleases(t *testing.T) {
	d := liveDeps(t, quietFleet())
	demand := newRecordingDemand()
	d.Regions[0].Demand = demand
	conn := mustDialLive(t, startLive(t, d))
	readFrame(t, conn)

	subscribe(t, conn, `{"type":"subscribe","bbox":[106.82,-6.2,106.83,-6.18]}`)
	readBuses(t, conn, "snapshot")
	key, i := onlyHold(t, demand)
	if want := (mqttfeed.Interest{Areas: []geo.BBox{{West: 106.82, South: -6.2, East: 106.83, North: -6.18}}}); !reflect.DeepEqual(i, want) {
		t.Errorf("hold = %+v, want %+v", i, want)
	}

	time.Sleep(time.Second) // stay under the subscribe rate limit
	subscribe(t, conn, `{"type":"subscribe","bbox":[106.82,-6.2,106.83,-6.18],"routes":["JAK.18"]}`)
	readBuses(t, conn, "snapshot")
	replacedKey, replaced := onlyHold(t, demand)
	if want := (mqttfeed.Interest{RouteCodes: []string{"JAK.18"}}); replacedKey != key || !reflect.DeepEqual(replaced, want) {
		t.Errorf("hold %q = %+v, want %q to hold only %+v", replacedKey, replaced, key, want)
	}

	_ = conn.Close()
	eventually(t, "the session's release", func() bool { return slices.Equal(demand.releases(), []string{key}) })
}

func TestLiveSessionsUseDistinctKeys(t *testing.T) {
	d := liveDeps(t, quietFleet())
	demand := newRecordingDemand()
	d.Regions[0].Demand = demand
	url := startLive(t, d)
	for range 2 {
		conn := mustDialLive(t, url)
		readFrame(t, conn)
		subscribe(t, conn, `{"type":"subscribe","routes":["1"]}`)
		readBuses(t, conn, "snapshot")
	}
	if held := demand.held(); len(held) != 2 {
		t.Errorf("holds = %+v, want one per session", held)
	}
}

func TestLiveStreamsSubscribedBuses(t *testing.T) {
	f := quietFleet()
	conn := mustDialLive(t, startLive(t, liveDeps(t, f)))

	if got, want := string(readFrame(t, conn)), `{"type":"status","feed":"disabled","catalogVersion":"feed-1"}`; got != want {
		t.Errorf("first frame = %s, want %s", got, want)
	}

	subscribe(t, conn, `{"type":"subscribe","bbox":[106.82,-6.2,106.83,-6.18]}`)
	if buses, _ := readBuses(t, conn, "snapshot"); !slices.Equal(buses, []string{"TST-001", "XX-001"}) {
		t.Errorf("snapshot = %v, want [TST-001 XX-001]", buses)
	}

	later := now.Add(time.Second)
	moveBus(f, "TST-001", later, -6.3, 106.823)  // leaves the box
	moveBus(f, "TST-002", later, -6.19, 106.825) // enters it
	moveBus(f, "TST-003", later, -6.25, 106.80)  // moves outside it
	f.Flush()
	buses, removed := readBuses(t, conn, "update")
	if !slices.Equal(buses, []string{"TST-002"}) || !slices.Equal(removed, []string{"TST-001"}) {
		t.Errorf("update = %v removed %v, want [TST-002] removed [TST-001]", buses, removed)
	}

	subscribe(t, conn, `{"type":"subscribe","routes":["JAK.18","NOPE"]}`)
	if buses, _ := readBuses(t, conn, "snapshot"); !slices.Equal(buses, []string{"MT-001"}) {
		t.Errorf("snapshot = %v, want [MT-001]", buses)
	}

	f.Expire(now.Add(time.Hour))
	f.Flush()
	buses, removed = readBuses(t, conn, "update")
	if len(buses) != 0 || !slices.Equal(removed, []string{"MT-001"}) {
		t.Errorf("update = %v removed %v, want [] removed [MT-001]", buses, removed)
	}
}

func TestLiveSubscribesToRoutesInABox(t *testing.T) {
	conn := mustDialLive(t, startLive(t, liveDeps(t, quietFleet())))
	readFrame(t, conn)

	subscribe(t, conn, `{"type":"subscribe","bbox":[106.82,-6.2,106.83,-6.18],"routes":["1"]}`)
	if buses, _ := readBuses(t, conn, "snapshot"); !slices.Equal(buses, []string{"TST-001"}) {
		t.Errorf("snapshot = %v, want [TST-001]", buses)
	}
	subscribe(t, conn, `{"type":"subscribe","routes":["NOPE"]}`)
	if buses, _ := readBuses(t, conn, "snapshot"); len(buses) != 0 {
		t.Errorf("snapshot = %v, want none", buses)
	}
}

func TestLiveSkipsChangesOutsideTheSubscription(t *testing.T) {
	f := quietFleet()
	conn := mustDialLive(t, startLive(t, liveDeps(t, f)))
	readFrame(t, conn)
	subscribe(t, conn, `{"type":"subscribe","routes":["JAK.18"]}`)
	readBuses(t, conn, "snapshot")

	moveBus(f, "TST-001", now.Add(time.Second), -6.2, 106.82)
	f.Flush()
	moveBus(f, "MT-001", now.Add(time.Second), -6.26, 106.85)
	f.Flush()
	if buses, _ := readBuses(t, conn, "update"); !slices.Equal(buses, []string{"MT-001"}) {
		t.Errorf("first update = %v, want [MT-001]", buses)
	}
}

type switchingFeed struct{ connected atomic.Bool }

func (f *switchingFeed) Status() mqttfeed.Status {
	return mqttfeed.Status{Connected: f.connected.Load()}
}

func TestLiveReportsFeedChanges(t *testing.T) {
	feed := &switchingFeed{}
	d := liveDeps(t, quietFleet())
	d.Regions[0].Feed = feed
	conn := mustDialLive(t, startLive(t, d))

	if got, want := string(readFrame(t, conn)), `{"type":"status","feed":"disconnected","catalogVersion":"feed-1"}`; got != want {
		t.Errorf("first frame = %s, want %s", got, want)
	}
	feed.connected.Store(true)
	if got, want := string(readFrame(t, conn)), `{"type":"status","feed":"connected","catalogVersion":"feed-1"}`; got != want {
		t.Errorf("after reconnect = %s, want %s", got, want)
	}
}

func TestLiveClosesOnBadSubscribes(t *testing.T) {
	tests := []struct {
		name       string
		msgs       []string
		wantCode   int
		wantReason string
	}{
		{"not json", []string{`subscribe`}, websocket.ClosePolicyViolation, "subscribe"},
		{"other type", []string{`{"type":"unsubscribe"}`}, websocket.ClosePolicyViolation, "subscribe"},
		{"unknown field", []string{`{"type":"subscribe","fleetNumber":"TST-001"}`}, websocket.ClosePolicyViolation, "subscribe"},
		{"no filter", []string{`{"type":"subscribe"}`}, websocket.ClosePolicyViolation, "bbox or routes"},
		{"short bbox", []string{`{"type":"subscribe","bbox":[106.8,-6.2,106.9]}`}, websocket.ClosePolicyViolation, "bbox"},
		{"inverted bbox", []string{`{"type":"subscribe","bbox":[106.9,-6.2,106.8,-6.1]}`}, websocket.ClosePolicyViolation, "bbox"},
		{"large bbox", []string{`{"type":"subscribe","bbox":[106.0,-6.2,107.5,-6.1]}`}, websocket.ClosePolicyViolation, "bbox"},
		{"empty routes", []string{`{"type":"subscribe","routes":[]}`}, websocket.ClosePolicyViolation, "routes"},
		{"blank route", []string{`{"type":"subscribe","routes":["1",""]}`}, websocket.ClosePolicyViolation, "routes"},
		{"too large", []string{`{"type":"subscribe","routes":["` + strings.Repeat("1", 4096) + `"]}`}, websocket.CloseMessageTooBig, ""},
		{"too often", slices.Repeat([]string{`{"type":"subscribe","routes":["1"]}`}, 3), websocket.ClosePolicyViolation, "per second"},
	}
	url := startLive(t, liveDeps(t, quietFleet()))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := mustDialLive(t, url)
			for _, msg := range tt.msgs {
				send(t, conn, msg)
			}
			got := readClose(t, conn)
			if got.Code != tt.wantCode || !strings.Contains(got.Text, tt.wantReason) {
				t.Errorf("close = %d %q, want %d mentioning %q", got.Code, got.Text, tt.wantCode, tt.wantReason)
			}
		})
	}
}

// laggingBuses hands out subscriptions that already fell too far behind.
type laggingBuses struct{ *fleet.Fleet }

func (l laggingBuses) Subscribe() *fleet.Subscription {
	sub := l.Fleet.Subscribe()
	for i := range fleet.SubscriberBuffer + 1 {
		moveBus(l.Fleet, "TST-001", now.Add(time.Duration(i+1)*time.Second), -6.19, 106.823)
		l.Flush()
	}
	return sub
}

func TestLiveClosesSlowConsumers(t *testing.T) {
	conn := mustDialLive(t, startLive(t, liveDeps(t, laggingBuses{quietFleet()})))

	got := readClose(t, conn)
	if got.Code != websocket.CloseTryAgainLater || got.Text != "slow consumer" {
		t.Errorf("close = %d %q, want %d %q", got.Code, got.Text, websocket.CloseTryAgainLater, "slow consumer")
	}
}

func TestLiveClosesOnShutdown(t *testing.T) {
	done := make(chan struct{})
	d := liveDeps(t, quietFleet())
	d.Done = done
	conn := mustDialLive(t, startLive(t, d))
	readFrame(t, conn)

	close(done)
	if got := readClose(t, conn); got.Code != websocket.CloseGoingAway {
		t.Errorf("close = %d %q, want %d", got.Code, got.Text, websocket.CloseGoingAway)
	}
}

func TestLiveChecksTheOrigin(t *testing.T) {
	d := liveDeps(t, quietFleet())
	d.Live.BrowserOrigins = []string{"https://app.example.com"}
	url := startLive(t, d)

	if _, err := dialLive(t, url, http.Header{"Origin": {"https://app.example.com"}}); err != nil {
		t.Errorf("allowed origin: %v", err)
	}
	var refused *handshakeError
	if _, err := dialLive(t, url, http.Header{"Origin": {"https://other.example.com"}}); !errors.As(err, &refused) || refused.status != http.StatusForbidden {
		t.Errorf("other origin: %v, want %d", err, http.StatusForbidden)
	}
}

func TestLiveLimitsConnections(t *testing.T) {
	d := liveDeps(t, quietFleet())
	d.Live.MaxConnections = 1
	url := startLive(t, d)

	mustDialLive(t, url)
	var refused *handshakeError
	if _, err := dialLive(t, url, nil); !errors.As(err, &refused) || refused.status != http.StatusServiceUnavailable {
		t.Errorf("second connection: %v, want %d", err, http.StatusServiceUnavailable)
	}
}

func TestLiveRejectsPlainRequests(t *testing.T) {
	tests := []struct {
		name       string
		catalogs   httpapi.Catalogs
		header     http.Header
		wantStatus int
		wantDetail string
	}{
		{"no subprotocol", nil, nil, http.StatusBadRequest, liveProtocol},
		{"not an upgrade", nil, http.Header{"Sec-Websocket-Protocol": {liveProtocol}}, http.StatusBadRequest, "websocket"},
		{"no catalog", fakeCatalogs{}, http.Header{"Sec-Websocket-Protocol": {liveProtocol}}, http.StatusServiceUnavailable, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := liveDeps(t, quietFleet())
			if tt.catalogs != nil {
				d.Regions[0].Catalogs = tt.catalogs
			}
			rec := get(t, httpapi.New(d), regionPath+"/live", tt.header)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if p := decodeProblem(t, rec); p.Status != tt.wantStatus || !strings.Contains(p.Detail, tt.wantDetail) {
				t.Errorf("problem = %+v, want %d mentioning %q", p, tt.wantStatus, tt.wantDetail)
			}
		})
	}
}
