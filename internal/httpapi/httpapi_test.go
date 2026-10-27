package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/httpapi"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
	"github.com/lscythe/jalvio-service/internal/stopinfo"
)

var startedAt = time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)

type fakeCatalogs struct {
	status  catalog.Status
	current *catalog.Catalog
}

func (f fakeCatalogs) Status() catalog.Status    { return f.status }
func (f fakeCatalogs) Current() *catalog.Catalog { return f.current }

type fakeFeed struct{ status mqttfeed.Status }

func (f fakeFeed) Status() mqttfeed.Status { return f.status }

type fakeStopInfo struct {
	status  stopinfo.Status
	current *stopinfo.Snapshot
}

func (f fakeStopInfo) Status() stopinfo.Status     { return f.status }
func (f fakeStopInfo) Current() *stopinfo.Snapshot { return f.current }

type countedBuses struct {
	*fleet.Fleet
	n int
}

func (c countedBuses) Len() int { return c.n }

func newHandler(ready bool) http.Handler {
	return httpapi.New(deps(ready))
}

func deps(ready bool) httpapi.Deps {
	return httpapi.Deps{
		Info:     httpapi.Info{Version: "1.2.3", StartedAt: startedAt},
		Ready:    func() bool { return ready },
		Catalogs: fakeCatalogs{},
		Buses:    fleet.New(),
	}
}

func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	return serveRequest(t, h, httptest.NewRequestWithContext(t.Context(), method, target, nil))
}

func serveRequest(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	checkContract(t, req, rec)
	return rec
}

func TestHealthz(t *testing.T) {
	tests := []struct {
		ready bool
		want  int
	}{
		{ready: true, want: http.StatusNoContent},
		{ready: false, want: http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		rec := serve(t, newHandler(tt.ready), http.MethodGet, "/healthz")
		if rec.Code != tt.want {
			t.Errorf("ready=%v: status = %d, want %d", tt.ready, rec.Code, tt.want)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("ready=%v: body = %q, want empty", tt.ready, rec.Body.String())
		}
	}
}

func TestStatus(t *testing.T) {
	checked := startedAt.Add(time.Hour)
	lastMessage := startedAt.Add(2 * time.Hour)
	const prefix = `{"version":"1.2.3","startedAt":"2026-10-12T03:00:00Z",`
	tests := []struct {
		name    string
		catalog catalog.Status
		feed    httpapi.Feed
		stops   httpapi.StopInfo
		buses   int
		want    string
	}{
		{
			name:    "starting with the feed disabled",
			catalog: catalog.Status{Error: "fetch feed: timeout"},
			want: prefix + `"catalog":{"error":"fetch feed: timeout"},` +
				`"feed":{"state":"disabled","reconnects":0,"messages":0,"malformed":0,"buses":0},` +
				`"stopFacilities":{"state":"disabled","requests":0,"matched":0,"unmatched":0}}`,
		},
		{
			name:    "catalog loaded and feed connected",
			catalog: catalog.Status{Version: "abc", UpdatedAt: startedAt, CheckedAt: checked},
			feed: fakeFeed{mqttfeed.Status{
				Connected: true, LastMessageAt: lastMessage, Delay: 12360 * time.Millisecond,
				Reconnects: 2, Messages: 1500, Malformed: 3,
				Rejected: map[string]int64{"no_position": 2, "payload": 1},
			}},
			stops: fakeStopInfo{status: stopinfo.Status{
				SyncedAt: startedAt, CheckedAt: checked, Error: "matched 1 of 272 halte, below 95%",
				Requests: 6, Matched: 1, Unmatched: 271,
			}},
			buses: 1412,
			want: prefix + `"catalog":{"version":"abc","updatedAt":"2026-10-12T03:00:00Z","checkedAt":"2026-10-12T04:00:00Z"},` +
				`"feed":{"state":"connected","lastMessageAt":"2026-10-12T05:00:00Z","delaySeconds":12.4,` +
				`"reconnects":2,"messages":1500,"malformed":3,` +
				`"rejected":{"no_position":2,"payload":1},"buses":1412},` +
				`"stopFacilities":{"state":"synced","syncedAt":"2026-10-12T03:00:00Z","checkedAt":"2026-10-12T04:00:00Z",` +
				`"error":"matched 1 of 272 halte, below 95%","requests":6,"matched":1,"unmatched":271}}`,
		},
		{
			name:  "feed disconnected",
			feed:  fakeFeed{mqttfeed.Status{Reconnects: 1}},
			stops: fakeStopInfo{status: stopinfo.Status{CheckedAt: checked, Error: "catalog not loaded"}},
			want: prefix + `"catalog":{},` +
				`"feed":{"state":"disconnected","reconnects":1,"messages":0,"malformed":0,"buses":0},` +
				`"stopFacilities":{"state":"pending","checkedAt":"2026-10-12T04:00:00Z","error":"catalog not loaded",` +
				`"requests":0,"matched":0,"unmatched":0}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := deps(true)
			d.Catalogs = fakeCatalogs{status: tt.catalog}
			d.Feed = tt.feed
			d.StopInfo = tt.stops
			d.Buses = countedBuses{fleet.New(), tt.buses}
			rec := serve(t, httpapi.New(d), http.MethodGet, "/v1/status")

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.want {
				t.Errorf("body = %s\nwant   %s", got, tt.want)
			}
		})
	}
}

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) problem {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", got)
	}
	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	return p
}

func TestUnknownPathIsProblem(t *testing.T) {
	rec := serve(t, newHandler(true), http.MethodGet, "/v1/nope")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	want := problem{Type: "about:blank", Title: "Not Found", Status: http.StatusNotFound}
	if got := decodeProblem(t, rec); got != want {
		t.Errorf("problem = %+v, want %+v", got, want)
	}
}

func TestWrongMethodIsProblem(t *testing.T) {
	rec := serve(t, newHandler(true), http.MethodPost, "/v1/status")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, http.MethodGet) {
		t.Errorf("Allow = %q, want it to include GET", allow)
	}
	want := problem{Type: "about:blank", Title: "Method Not Allowed", Status: http.StatusMethodNotAllowed}
	if got := decodeProblem(t, rec); got != want {
		t.Errorf("problem = %+v, want %+v", got, want)
	}
}
