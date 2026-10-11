package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/httpapi"
)

var startedAt = time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)

func newHandler(ready bool) http.Handler {
	return httpapi.New(httpapi.Info{Version: "1.2.3", StartedAt: startedAt}, func() bool { return ready })
}

func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, target, nil))
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
	rec := serve(t, newHandler(true), http.MethodGet, "/v1/status")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var body struct {
		Version   string    `json:"version"`
		StartedAt time.Time `json:"started_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Version != "1.2.3" || !body.StartedAt.Equal(startedAt) {
		t.Errorf("body = %+v, want version 1.2.3 started at %v", body, startedAt)
	}
}

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
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
