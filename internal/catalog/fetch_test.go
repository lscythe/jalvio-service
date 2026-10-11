package catalog

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPFetcherReturnsBody(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.UserAgent()
		_, _ = w.Write([]byte("zip bytes"))
	}))
	defer srv.Close()

	data, err := NewHTTPFetcher(srv.Client(), srv.URL, "jalvio-service/test").Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != "zip bytes" {
		t.Errorf("data = %q, want %q", data, "zip bytes")
	}
	if gotUA != "jalvio-service/test" {
		t.Errorf("User-Agent = %q, want jalvio-service/test", gotUA)
	}
}

func TestHTTPFetcherRejectsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := NewHTTPFetcher(srv.Client(), srv.URL, "ua").Fetch(t.Context())
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want one mentioning 404", err)
	}
}

func TestHTTPFetcherRejectsOversizedFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	f := NewHTTPFetcher(srv.Client(), srv.URL, "ua")
	f.maxSize = 5
	if _, err := f.Fetch(t.Context()); err == nil {
		t.Fatal("Fetch succeeded, want size error")
	}
}
