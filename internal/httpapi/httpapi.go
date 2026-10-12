package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
	"github.com/lscythe/jalvio-service/internal/stopinfo"
)

type Info struct {
	Version   string
	StartedAt time.Time
}

type Catalogs interface {
	Status() catalog.Status
}

type Feed interface {
	Status() mqttfeed.Status
}

type StopInfo interface {
	Status() stopinfo.Status
}

type Buses interface {
	Len() int
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
}

type statusResponse struct {
	Version   string        `json:"version"`
	StartedAt time.Time     `json:"started_at"`
	Catalog   catalogStatus `json:"catalog"`
	Feed      feedStatus    `json:"feed"`
	Stops     stopsStatus   `json:"stop_facilities"`
}

type catalogStatus struct {
	Version   string    `json:"version,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	Error     string    `json:"error,omitempty"`
}

type feedStatus struct {
	State         string           `json:"state"`
	LastMessageAt time.Time        `json:"last_message_at,omitzero"`
	Reconnects    int              `json:"reconnects"`
	Messages      int64            `json:"messages"`
	Malformed     int64            `json:"malformed"`
	Rejected      map[string]int64 `json:"rejected,omitempty"`
	Buses         int              `json:"buses"`
}

type stopsStatus struct {
	State     string    `json:"state"`
	SyncedAt  time.Time `json:"synced_at,omitzero"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
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
	return problemFallback(mux)
}

func newFeedStatus(feed Feed, buses int) feedStatus {
	if feed == nil {
		return feedStatus{State: "disabled", Buses: buses}
	}
	st := feed.Status()
	state := "disconnected"
	if st.Connected {
		state = "connected"
	}
	return feedStatus{
		State:         state,
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
