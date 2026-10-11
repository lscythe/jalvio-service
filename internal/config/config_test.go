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

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		Listen:      ":8080",
		DataDir:     "./data",
		GTFSURL:     feedURL,
		GTFSRefresh: 24 * time.Hour,
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
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		Listen:          "127.0.0.1:9000",
		DataDir:         "/data",
		GTFSURL:         feedURL,
		GTFSRefresh:     6 * time.Hour,
		MQTTURL:         "wss://broker.example.com:8084/mqtt",
		MQTTServerName:  "cert.example.com",
		MQTTDialAddress: "192.0.2.10:8084",
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
	}
	for _, tt := range tests {
		if _, err := config.Load(env(map[string]string{tt.key: tt.value})); err == nil {
			t.Errorf("Load(%s=%q) succeeded, want error", tt.key, tt.value)
		}
	}
}
