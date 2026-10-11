package config_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/config"
)

const feedURL = "https://gtfs.example.com/feed.zip"

// env returns a getenv for vars plus the one required setting.
func env(vars map[string]string) func(string) string {
	return func(key string) string {
		if v, ok := vars[key]; ok {
			return v
		}
		if key == "GTFS_URL" {
			return feedURL
		}
		return ""
	}
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
		Listen:         ":8080",
		DataDir:        "./data",
		GTFSURL:        feedURL,
		GTFSRefresh:    24 * time.Hour,
		StopInfoSyncAt: 3 * time.Hour,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load = %+v, want %+v", cfg, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"JALVIO_LISTEN":        "127.0.0.1:9000",
		"JALVIO_DATA_DIR":      "/data",
		"GTFS_REFRESH":         "6h",
		"MQTT_URL":             "wss://broker.example.com:8084/mqtt",
		"MQTT_TLS_SERVER_NAME": "cert.example.com",
		"MQTT_DIAL_ADDRESS":    "192.0.2.10:8084",
		"STOP_API_URL":         "https://stops.example.com/v1",
		"STOP_API_APP_VERSION": "1.0.0",
		"STOPINFO_SYNC_AT":     "04:30",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		Listen:            "127.0.0.1:9000",
		DataDir:           "/data",
		GTFSURL:           feedURL,
		GTFSRefresh:       6 * time.Hour,
		MQTTURL:           "wss://broker.example.com:8084/mqtt",
		MQTTServerName:    "cert.example.com",
		MQTTDialAddress:   "192.0.2.10:8084",
		StopAPIURL:        "https://stops.example.com/v1",
		StopAPIAppVersion: "1.0.0",
		StopInfoSyncAt:    4*time.Hour + 30*time.Minute,
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
		{"GTFS_URL", ""},
		{"GTFS_URL", "gtfs.example.com/feed.zip"},
		{"GTFS_URL", "ftp://gtfs.example.com/feed.zip"},
		{"GTFS_REFRESH", "daily"},
		{"GTFS_REFRESH", "0s"},
		{"GTFS_REFRESH", "-1h"},
		{"MQTT_URL", "https://broker.example.com:8084/mqtt"},
		{"MQTT_URL", "broker.example.com:8084"},
		{"MQTT_DIAL_ADDRESS", "192.0.2.10"},
		{"STOP_API_URL", "stops.example.com/v1"},
		{"STOP_API_APP_VERSION", ""},
		{"STOPINFO_SYNC_AT", "25:00"},
		{"STOPINFO_SYNC_AT", "3am"},
	}
	for _, tt := range tests {
		vars := map[string]string{
			"STOP_API_URL":         "https://stops.example.com/v1",
			"STOP_API_APP_VERSION": "1.0.0",
			tt.key:                 tt.value,
		}
		if _, err := config.Load(env(vars)); err == nil {
			t.Errorf("Load(%s=%q) succeeded, want error", tt.key, tt.value)
		}
	}
}
