package stopapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/stopapi"
)

const (
	stopsBody = `{"code":200,"message":"success","data":[
		{"stop_id":"H00001P","stop_name":"Kota","platform_code":"","description":"",
		 "facilities":["toilet","lift"],
		 "v2_facilities":[{"name":"toilet","title":"toilet","icon":"https://cdn.example.com/toilet.png"}]},
		{"stop_id":"B00001P","stop_name":"Kota 2","platform_code":"A","description":"near the gate",
		 "facilities":[],"v2_facilities":[]}
	],"meta":{"limit":10000}}`
	detailBody = `{"code":200,"message":"success","data":
		{"stop_id":"G00001","stop_name":"Kota","platform_code":"","description":"",
		 "facilities":["eskalator"],"v2_facilities":[{"name":"eskalator","title":"eskalator"}]}}`
)

// upstream fakes the stop API. Tokens are "tok-<n>" for the n-th login, and
// rejected tokens get 401 so tests can force a session refresh.
type upstream struct {
	srv      *httptest.Server
	logins   atomic.Int32
	validFor atomic.Int64 // seconds

	mu       sync.Mutex
	rejected map[string]bool
	requests []*http.Request
}

func newUpstream(t *testing.T, routes map[string]string) *upstream {
	t.Helper()
	u := &upstream{rejected: map[string]bool{}}
	u.validFor.Store(int64(time.Hour / time.Second))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/auth/login/guest", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		n := u.logins.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":             fmt.Sprintf("tok-%d", n),
			"token_valid_until": time.Now().Unix() + u.validFor.Load(),
		})
	})
	for pattern, body := range routes {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			u.record(r)
			token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			u.mu.Lock()
			rejected := u.rejected[token]
			u.mu.Unlock()
			if token == "" || rejected {
				http.Error(w, `{"code":401}`, http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(body))
		})
	}
	u.srv = httptest.NewServer(mux)
	t.Cleanup(u.srv.Close)
	return u
}

func (u *upstream) record(r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.requests = append(u.requests, r.Clone(r.Context()))
}

func (u *upstream) reject(token string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.rejected[token] = true
}

func (u *upstream) all() []*http.Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]*http.Request(nil), u.requests...)
}

func (u *upstream) last() *http.Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.requests[len(u.requests)-1]
}

func (u *upstream) client() *stopapi.Client {
	return stopapi.New(u.srv.Client(), stopapi.Config{
		BaseURL:    u.srv.URL + "/v1",
		AppVersion: "9.9.9",
		DeviceID:   "device-1",
		UserAgent:  "jalvio-service/test",
	})
}

var center = geo.Point{Lat: -6.2, Lon: 106.83}

func TestSearchStops(t *testing.T) {
	u := newUpstream(t, map[string]string{"GET /v1/bus-stop": stopsBody})

	got, err := u.client().SearchStops(t.Context(), "a", center)
	if err != nil {
		t.Fatalf("SearchStops: %v", err)
	}
	want := []stopapi.Stop{
		{ID: "H00001P", Name: "Kota", Facilities: []stopapi.Facility{
			{Name: "toilet", Title: "toilet"},
			{Name: "lift"},
		}},
		{ID: "B00001P", Name: "Kota 2", PlatformCode: "A", Description: "near the gate"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stops =\n%+v\nwant\n%+v", got, want)
	}

	q := u.last().URL.Query()
	for key, want := range map[string]string{
		"search_by": "a", "latitude": "-6.2", "longitude": "106.83", "limit": "10000",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
}

func TestRequestsCarryClientHeaders(t *testing.T) {
	u := newUpstream(t, map[string]string{"GET /v1/bus-stop/{id}": detailBody})
	if _, err := u.client().Stop(t.Context(), "H00001P"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	for _, r := range u.all() {
		for header, want := range map[string]string{
			"X-App-Version": "9.9.9",
			"X-Device-ID":   "device-1",
			"X-Lang":        "id",
			"User-Agent":    "jalvio-service/test",
		} {
			if got := r.Header.Get(header); got != want {
				t.Errorf("%s %s: %s = %q, want %q", r.Method, r.URL.Path, header, got, want)
			}
		}
	}
	if got := u.last().Header.Get("Authorization"); got != "Bearer tok-1" {
		t.Errorf("Authorization = %q, want Bearer tok-1", got)
	}
}

func TestStop(t *testing.T) {
	u := newUpstream(t, map[string]string{"GET /v1/bus-stop/{id}": detailBody})

	got, err := u.client().Stop(t.Context(), "H00001P")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := stopapi.Stop{ID: "G00001", Name: "Kota", Facilities: []stopapi.Facility{{Name: "eskalator", Title: "eskalator"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stop = %+v, want %+v", got, want)
	}
	if path := u.last().URL.Path; path != "/v1/bus-stop/H00001P" {
		t.Errorf("path = %q, want /v1/bus-stop/H00001P", path)
	}
}

func TestSearchStopsWithNoData(t *testing.T) {
	u := newUpstream(t, map[string]string{"GET /v1/bus-stop": `{"code":200,"message":"success","data":null}`})

	got, err := u.client().SearchStops(t.Context(), "xyz", center)
	if err != nil || len(got) != 0 {
		t.Errorf("SearchStops = %v, %v; want no stops and no error", got, err)
	}
}

func TestSessionIsReusedUntilItExpires(t *testing.T) {
	u := newUpstream(t, map[string]string{"GET /v1/bus-stop/{id}": detailBody})
	c := u.client()

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := c.Stop(t.Context(), "H00001P"); err != nil {
				t.Errorf("Stop: %v", err)
			}
		})
	}
	wg.Wait()
	if n := u.logins.Load(); n != 1 {
		t.Errorf("logins = %d, want 1 shared by concurrent calls", n)
	}

	u.validFor.Store(30) // seconds, inside the renewal margin
	expiring := u.client()
	for range 2 {
		if _, err := expiring.Stop(t.Context(), "H00001P"); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}
	if n := u.logins.Load(); n != 3 {
		t.Errorf("logins = %d, want 3: a new login for each call while the token is about to expire", n)
	}
}

func TestUnauthorizedRenewsSessionOnce(t *testing.T) {
	u := newUpstream(t, map[string]string{"GET /v1/bus-stop/{id}": detailBody})
	c := u.client()
	if _, err := c.Stop(t.Context(), "H00001P"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	u.reject("tok-1")
	if _, err := c.Stop(t.Context(), "H00001P"); err != nil {
		t.Fatalf("Stop after the token was revoked: %v", err)
	}
	if got := u.last().Header.Get("Authorization"); got != "Bearer tok-2" {
		t.Errorf("retry Authorization = %q, want Bearer tok-2", got)
	}

	u.reject("tok-2")
	u.reject("tok-3")
	_, err := c.Stop(t.Context(), "H00001P")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want 401 after one renewal", err)
	}
	if n := u.logins.Load(); n != 3 {
		t.Errorf("logins = %d, want 3", n)
	}
}

func TestUpstreamErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "login rejected",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "no", http.StatusForbidden)
			},
			want: "403",
		},
		{
			name: "login without token",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"token":""}`))
			},
			want: "no token",
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					_, _ = w.Write([]byte(`{"token":"t","token_valid_until":4102444800}`))
					return
				}
				http.Error(w, "boom", http.StatusInternalServerError)
			},
			want: "500",
		},
		{
			name: "invalid JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					_, _ = w.Write([]byte(`{"token":"t","token_valid_until":4102444800}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":[`))
			},
			want: "decode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			c := stopapi.New(srv.Client(), stopapi.Config{BaseURL: srv.URL})

			_, err := c.SearchStops(t.Context(), "a", center)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}

func TestOversizedResponseIsRejected(t *testing.T) {
	huge := `{"data":[` + strings.Repeat(`{"stop_id":"B00001P"},`, 1<<20) + `{}]}`
	u := newUpstream(t, map[string]string{"GET /v1/bus-stop": huge})

	if _, err := u.client().SearchStops(t.Context(), "a", center); err == nil {
		t.Fatal("SearchStops accepted an oversized response")
	}
}
