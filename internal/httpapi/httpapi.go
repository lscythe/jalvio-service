package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
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
}

type statusResponse struct {
	Version   string        `json:"version"`
	StartedAt time.Time     `json:"started_at"`
	Catalog   catalogStatus `json:"catalog"`
	Feed      feedStatus    `json:"feed"`
}

type catalogStatus struct {
	Version   string    `json:"version,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	Error     string    `json:"error,omitempty"`
}

type feedStatus struct {
	State         string    `json:"state"`
	LastMessageAt time.Time `json:"last_message_at,omitzero"`
	Reconnects    int       `json:"reconnects"`
	Messages      int64     `json:"messages"`
	Malformed     int64     `json:"malformed"`
	Buses         int       `json:"buses"`
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
		Buses:         buses,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
