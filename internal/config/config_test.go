package config_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/config"
)

func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		Listen:      ":8080",
		DataDir:     "./data",
		GTFSURL:     "https://gtfs.example.com/feed.zip",
		GTFSRefresh: 24 * time.Hour,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load = %+v, want %+v", cfg, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"JALVIO_LISTEN":   "127.0.0.1:9000",
		"JALVIO_DATA_DIR": "/data",
		"GTFS_URL":        "http://localhost:8081/feed.zip",
		"GTFS_REFRESH":    "6h",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		Listen:      "127.0.0.1:9000",
		DataDir:     "/data",
		GTFSURL:     "http://localhost:8081/feed.zip",
		GTFSRefresh: 6 * time.Hour,
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
		{"GTFS_URL", "gtfs.example.com/feed.zip"},
		{"GTFS_URL", "ftp://example.com/feed.zip"},
		{"GTFS_REFRESH", "daily"},
		{"GTFS_REFRESH", "0s"},
		{"GTFS_REFRESH", "-1h"},
	}
	for _, tt := range tests {
		if _, err := config.Load(env(map[string]string{tt.key: tt.value})); err == nil {
			t.Errorf("Load(%s=%q) succeeded, want error", tt.key, tt.value)
		}
	}
}
