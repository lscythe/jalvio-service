package httpapi

import (
	"encoding/json"
	"net/http"
)

// problem is an RFC 9457 problem details body.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func writeProblem(w http.ResponseWriter, status int) {
	writeProblemDetail(w, status, "")
}

func writeBadRequest(w http.ResponseWriter, detail string) {
	writeProblemDetail(w, http.StatusBadRequest, detail)
}

func writeProblemDetail(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
}

// problemFallback replaces ServeMux's plain-text 404 and 405 responses with
// problem details, keeping the Allow header the mux sets.
func problemFallback(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		h.ServeHTTP(&statusOnly{ResponseWriter: w}, r)
	})
}

// statusOnly turns the mux's error status into a problem response and
// discards the mux's own body.
type statusOnly struct {
	http.ResponseWriter
	wrote bool
}

func (s *statusOnly) WriteHeader(status int) {
	if s.wrote {
		return
	}
	s.wrote = true
	writeProblem(s.ResponseWriter, status)
}

func (s *statusOnly) Write(b []byte) (int, error) {
	return len(b), nil
}
