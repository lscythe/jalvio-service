package httpapi

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/legal"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
	"github.com/lscythe/jalvio-service/internal/places"
	"github.com/lscythe/jalvio-service/internal/stopinfo"
)

type Info struct {
	Version   string
	StartedAt time.Time
}

type Catalogs interface {
	Current() *catalog.Catalog
	Status() catalog.Status
}

type Feed interface {
	Status() mqttfeed.Status
}

type StopInfo interface {
	Current() *stopinfo.Snapshot
	Status() stopinfo.Status
}

type Buses interface {
	Len() int
	Bus(fleetNumber string) (fleet.Bus, bool)
	InBBox(box geo.BBox, routeCodes []string) []fleet.Bus
	ArrivalsAt(stopID string) []fleet.Arrival
	Subscribe() *fleet.Subscription
}

// Demand tells the live feed what clients watch, since it only receives
// buses someone asked for.
type Demand interface {
	Hold(key string, i mqttfeed.Interest)
	Release(key string)
	Touch(i mqttfeed.Interest)
}

type Places interface {
	Search(ctx context.Context, area geo.BBox, text string, near *geo.Point, limit int) ([]places.Place, error)
	Reverse(ctx context.Context, at geo.Point) (places.Place, error)
}

type Deps struct {
	Info  Info
	Ready func() bool
	// Regions are served in this order, each under /v1/regions/{id}/.
	Regions []Region
	// Places is nil when place search is disabled; every region shares it.
	Places Places
	// Legal is the zero value when the owner publishes no documents.
	Legal legal.Documents
	Live  LiveConfig
	// Done closes the live streams, which server shutdown leaves open.
	Done <-chan struct{}
}

type statusResponse struct {
	Version   string         `json:"version"`
	StartedAt time.Time      `json:"startedAt"`
	Regions   []regionStatus `json:"regions"`
}

type regionStatus struct {
	ID      string        `json:"id"`
	Catalog catalogStatus `json:"catalog"`
	Feed    feedStatus    `json:"feed"`
	Stops   stopsStatus   `json:"stopFacilities"`
}

type catalogStatus struct {
	Version   string    `json:"version,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
	CheckedAt time.Time `json:"checkedAt,omitzero"`
	Error     string    `json:"error,omitempty"`
}

type feedStatus struct {
	State         string           `json:"state"`
	LastMessageAt time.Time        `json:"lastMessageAt,omitzero"`
	DelaySeconds  *float64         `json:"delaySeconds,omitempty"`
	Reconnects    int              `json:"reconnects"`
	Messages      int64            `json:"messages"`
	Malformed     int64            `json:"malformed"`
	Rejected      map[string]int64 `json:"rejected,omitempty"`
	Buses         int              `json:"buses"`
	Filters       int              `json:"filters"`
	WholeFleet    bool             `json:"wholeFleet"`
}

type stopsStatus struct {
	State     string    `json:"state"`
	SyncedAt  time.Time `json:"syncedAt,omitzero"`
	CheckedAt time.Time `json:"checkedAt,omitzero"`
	Error     string    `json:"error,omitempty"`
	Requests  int       `json:"requests"`
	Matched   int       `json:"matched"`
	Unmatched int       `json:"unmatched"`
}

func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !d.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, _ *http.Request) {
		resp := statusResponse{Version: d.Info.Version, StartedAt: d.Info.StartedAt, Regions: make([]regionStatus, 0, len(d.Regions))}
		for _, r := range d.Regions {
			resp.Regions = append(resp.Regions, regionStatus{
				ID:      r.ID,
				Catalog: catalogStatus(r.Catalogs.Status()),
				Feed:    newFeedStatus(r.Feed, r.Buses.Len()),
				Stops:   newStopsStatus(r.StopInfo),
			})
		}
		writeJSON(w, http.StatusOK, resp)
	})

	var openStreams atomic.Int64
	byID := make(map[string]*regionHandlers, len(d.Regions))
	list := &regionList{}
	for _, r := range d.Regions {
		rh := newRegionHandlers(r, d, &openStreams)
		byID[r.ID] = rh
		list.regions = append(list.regions, rh)
	}
	mux.HandleFunc("GET /v1/regions", list.serve)
	for pattern, serve := range map[string]func(*regionHandlers, http.ResponseWriter, *http.Request){
		"/catalog":             func(rh *regionHandlers, w http.ResponseWriter, r *http.Request) { rh.docs.serveCatalog(w, r) },
		"/catalog/shapes":      func(rh *regionHandlers, w http.ResponseWriter, r *http.Request) { rh.docs.serveShapes(w, r) },
		"/departures":          func(rh *regionHandlers, w http.ResponseWriter, r *http.Request) { rh.live.serveDepartures(w, r) },
		"/buses":               func(rh *regionHandlers, w http.ResponseWriter, r *http.Request) { rh.live.serveBuses(w, r) },
		"/buses/{fleetNumber}": func(rh *regionHandlers, w http.ResponseWriter, r *http.Request) { rh.live.serveBus(w, r) },
		"/live":                func(rh *regionHandlers, w http.ResponseWriter, r *http.Request) { rh.stream.serve(w, r) },
		"/places":              func(rh *regionHandlers, w http.ResponseWriter, r *http.Request) { rh.places.serveSearch(w, r) },
		"/places/reverse":      func(rh *regionHandlers, w http.ResponseWriter, r *http.Request) { rh.places.serveReverse(w, r) },
	} {
		handleRegional(mux, byID, pattern, serve)
	}
	mux.HandleFunc("GET /v1/legal/{document}", (&legalDocuments{docs: d.Legal}).serve)
	return problemFallback(mux)
}

func feedState(feed Feed) string {
	if feed == nil {
		return "disabled"
	}
	return connectionState(feed.Status())
}

func connectionState(st mqttfeed.Status) string {
	if st.Connected {
		return "connected"
	}
	return "disconnected"
}

func newFeedStatus(feed Feed, buses int) feedStatus {
	if feed == nil {
		return feedStatus{State: "disabled", Buses: buses}
	}
	st := feed.Status()
	return feedStatus{
		State:         connectionState(st),
		LastMessageAt: st.LastMessageAt,
		DelaySeconds:  delaySeconds(st.Delay),
		Reconnects:    st.Reconnects,
		Messages:      st.Messages,
		Malformed:     st.Malformed,
		Rejected:      st.Rejected,
		Buses:         buses,
		Filters:       st.Filters,
		WholeFleet:    st.WholeFleet,
	}
}

func delaySeconds(d time.Duration) *float64 {
	if d == 0 {
		return nil
	}
	s := math.Round(d.Seconds()*10) / 10
	return &s
}

func newStopsStatus(stops StopInfo) stopsStatus {
	if stops == nil {
		return stopsStatus{State: "disabled"}
	}
	st := stops.Status()
	state := "pending"
	if !st.SyncedAt.IsZero() {
		state = "synced"
	}
	return stopsStatus{
		State:     state,
		SyncedAt:  st.SyncedAt,
		CheckedAt: st.CheckedAt,
		Error:     st.Error,
		Requests:  st.Requests,
		Matched:   st.Matched,
		Unmatched: st.Unmatched,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
