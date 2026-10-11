package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
)

type Info struct {
	Version   string
	StartedAt time.Time
}

type Catalogs interface {
	Status() catalog.Status
}

type statusResponse struct {
	Version   string        `json:"version"`
	StartedAt time.Time     `json:"started_at"`
	Catalog   catalogStatus `json:"catalog"`
}

type catalogStatus struct {
	Version   string    `json:"version,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	Error     string    `json:"error,omitempty"`
}

func New(info Info, ready func() bool, catalogs Catalogs) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, statusResponse{
			Version:   info.Version,
			StartedAt: info.StartedAt,
			Catalog:   catalogStatus(catalogs.Status()),
		})
	})
	return problemFallback(mux)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
