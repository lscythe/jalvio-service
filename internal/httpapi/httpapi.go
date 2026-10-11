package httpapi

import (
	"encoding/json"
	"net/http"
	"time"
)

type Info struct {
	Version   string
	StartedAt time.Time
}

type statusResponse struct {
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
}

func New(info Info, ready func() bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, statusResponse(info))
	})
	return problemFallback(mux)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
