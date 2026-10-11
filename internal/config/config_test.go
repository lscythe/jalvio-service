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
		Listen:         ":8080",
		DataDir:        "./data",
		GTFSURL:        "https://gtfs.transjakarta.co.id/files/file_gtfs.zip",
		GTFSRefresh:    24 * time.Hour,
		MQTTURL:        "wss://mqtt.tj.co.id:8084/mqtt",
		MQTTServerName: "mqtt.tj.co.id",
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load = %+v, want %+v", cfg, want)
	}
}

func TestLoadDisablesMQTT(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"MQTT_URL": "disabled"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MQTTURL != "" {
		t.Errorf("MQTTURL = %q, want empty when disabled", cfg.MQTTURL)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"JALVIO_LISTEN":        "127.0.0.1:9000",
		"JALVIO_DATA_DIR":      "/data",
		"GTFS_URL":             "http://localhost:8081/feed.zip",
		"GTFS_REFRESH":         "6h",
		"MQTT_URL":             "ws://localhost:1883/mqtt",
		"MQTT_TLS_SERVER_NAME": "broker.example",
		"MQTT_DIAL_ADDRESS":    "10.0.0.5:8084",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		Listen:          "127.0.0.1:9000",
		DataDir:         "/data",
		GTFSURL:         "http://localhost:8081/feed.zip",
		GTFSRefresh:     6 * time.Hour,
		MQTTURL:         "ws://localhost:1883/mqtt",
		MQTTServerName:  "broker.example",
		MQTTDialAddress: "10.0.0.5:8084",
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
		{"GTFS_URL", "gtfs.transjakarta.co.id/files/file_gtfs.zip"},
		{"GTFS_URL", "ftp://example.com/feed.zip"},
		{"GTFS_REFRESH", "daily"},
		{"GTFS_REFRESH", "0s"},
		{"GTFS_REFRESH", "-1h"},
		{"MQTT_URL", "https://mqtt.tj.co.id:8084/mqtt"},
		{"MQTT_URL", "mqtt.tj.co.id:8084"},
		{"MQTT_DIAL_ADDRESS", "10.0.0.5"},
	}
	for _, tt := range tests {
		if _, err := config.Load(env(map[string]string{tt.key: tt.value})); err == nil {
			t.Errorf("Load(%s=%q) succeeded, want error", tt.key, tt.value)
		}
	}
}
