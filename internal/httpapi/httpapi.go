package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
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

type Deps struct {
	Info     Info
	Ready    func() bool
	Catalogs Catalogs
	// Feed is nil when the live feed is disabled.
	Feed  Feed
	Buses Buses
	// StopInfo is nil when the stop facilities sync is disabled.
	StopInfo StopInfo
	Live     LiveConfig
	// Done closes the live streams, which server shutdown leaves open.
	Done <-chan struct{}
}

type statusResponse struct {
	Version   string        `json:"version"`
	StartedAt time.Time     `json:"startedAt"`
	Catalog   catalogStatus `json:"catalog"`
	Feed      feedStatus    `json:"feed"`
	Stops     stopsStatus   `json:"stopFacilities"`
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
	Reconnects    int              `json:"reconnects"`
	Messages      int64            `json:"messages"`
	Malformed     int64            `json:"malformed"`
	Rejected      map[string]int64 `json:"rejected,omitempty"`
	Buses         int              `json:"buses"`
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
		writeJSON(w, http.StatusOK, statusResponse{
			Version:   d.Info.Version,
			StartedAt: d.Info.StartedAt,
			Catalog:   catalogStatus(d.Catalogs.Status()),
			Feed:      newFeedStatus(d.Feed, d.Buses.Len()),
			Stops:     newStopsStatus(d.StopInfo),
		})
	})
	docs := &catalogDocuments{catalogs: d.Catalogs, stopInfo: d.StopInfo}
	mux.HandleFunc("GET /v1/catalog", docs.serveCatalog)
	mux.HandleFunc("GET /v1/catalog/shapes", docs.serveShapes)
	l := &live{catalogs: d.Catalogs, feed: d.Feed, buses: d.Buses}
	mux.HandleFunc("GET /v1/departures", l.serveDepartures)
	mux.HandleFunc("GET /v1/buses", l.serveBuses)
	mux.HandleFunc("GET /v1/buses/{fleetNumber}", l.serveBus)
	mux.HandleFunc("GET /v1/live", newLiveStream(d).serve)
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
		Reconnects:    st.Reconnects,
		Messages:      st.Messages,
		Malformed:     st.Malformed,
		Rejected:      st.Rejected,
		Buses:         buses,
	}
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
