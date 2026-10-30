package config_test

import (
	"reflect"
	"testing"

	"github.com/lscythe/jalvio-service/internal/config"
)

func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestListenAddress(t *testing.T) {
	if got := config.ListenAddress(env(nil)); got != ":8080" {
		t.Errorf("default = %q, want :8080", got)
	}
	if got := config.ListenAddress(env(map[string]string{"JALVIO_LISTEN": "127.0.0.1:9000"})); got != "127.0.0.1:9000" {
		t.Errorf("override = %q, want 127.0.0.1:9000", got)
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		Listen:                  ":8080",
		DataDir:                 "./data",
		RegionsFile:             "data/regions.json",
		MaxLiveConnections:      2000,
		PlacesRequestsPerMinute: 60,
		LegalFile:               "data/legal.json",
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load = %+v, want %+v", cfg, want)
	}
}

func TestFilesDefaultToTheDataDir(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"JALVIO_DATA_DIR": "/srv/jalvio"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LegalFile != "/srv/jalvio/legal.json" {
		t.Errorf("LegalFile = %q, want /srv/jalvio/legal.json", cfg.LegalFile)
	}
	if cfg.RegionsFile != "/srv/jalvio/regions.json" {
		t.Errorf("RegionsFile = %q, want /srv/jalvio/regions.json", cfg.RegionsFile)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"JALVIO_LISTEN":              "127.0.0.1:9000",
		"JALVIO_DATA_DIR":            "/data",
		"REGIONS_FILE":               "/etc/jalvio/regions.json",
		"MAX_LIVE_CONNECTIONS":       "50",
		"BROWSER_ORIGINS":            "https://app.example.com  http://localhost:5173",
		"PHOTON_URL":                 "https://places.example.com",
		"PHOTON_USER_AGENT":          "jalvio-test (ops@example.com)",
		"PLACES_REQUESTS_PER_MINUTE": "30",
		"LEGAL_FILE":                 "/etc/jalvio/legal.json",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		Listen:                  "127.0.0.1:9000",
		DataDir:                 "/data",
		RegionsFile:             "/etc/jalvio/regions.json",
		MaxLiveConnections:      50,
		BrowserOrigins:          []string{"https://app.example.com", "http://localhost:5173"},
		PhotonURL:               "https://places.example.com",
		PhotonUserAgent:         "jalvio-test (ops@example.com)",
		PlacesRequestsPerMinute: 30,
		LegalFile:               "/etc/jalvio/legal.json",
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load = %+v, want %+v", cfg, want)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		key, value string
	}{
		{"JALVIO_LISTEN", "8080"},
		{"JALVIO_LISTEN", "localhost"},
		{"JALVIO_LISTEN", ":http:x"},
		{"MAX_LIVE_CONNECTIONS", "0"},
		{"MAX_LIVE_CONNECTIONS", "many"},
		{"BROWSER_ORIGINS", "app.example.com"},
		{"BROWSER_ORIGINS", "ws://app.example.com"},
		{"BROWSER_ORIGINS", "https://app.example.com/"},
		{"PHOTON_URL", "places.example.com"},
		{"PHOTON_USER_AGENT", ""},
		{"PLACES_REQUESTS_PER_MINUTE", "0"},
		{"PLACES_REQUESTS_PER_MINUTE", "fast"},
	}
	for _, tt := range tests {
		vars := map[string]string{
			"PHOTON_URL":        "https://places.example.com",
			"PHOTON_USER_AGENT": "jalvio-test (ops@example.com)",
			tt.key:              tt.value,
		}
		if _, err := config.Load(env(vars)); err == nil {
			t.Errorf("Load(%s=%q) succeeded, want error", tt.key, tt.value)
		}
	}
}
