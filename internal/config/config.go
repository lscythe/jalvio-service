package config

import (
	"fmt"
	"net"
	"net/url"
	"time"
)

type Config struct {
	Listen      string
	DataDir     string
	GTFSURL     string
	GTFSRefresh time.Duration
	// MQTTURL is empty when the live feed is disabled.
	MQTTURL         string
	MQTTServerName  string
	MQTTDialAddress string
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Listen:  valueOr(getenv("JALVIO_LISTEN"), ":8080"),
		DataDir: valueOr(getenv("JALVIO_DATA_DIR"), "./data"),
		GTFSURL: getenv("GTFS_URL"),
	}
	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		return Config{}, fmt.Errorf("JALVIO_LISTEN: %w", err)
	}
	if u, err := url.Parse(cfg.GTFSURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, fmt.Errorf("GTFS_URL: %q is not an http(s) URL", cfg.GTFSURL)
	}
	refresh, err := time.ParseDuration(valueOr(getenv("GTFS_REFRESH"), "24h"))
	if err != nil || refresh <= 0 {
		return Config{}, fmt.Errorf("GTFS_REFRESH: %q is not a positive duration", getenv("GTFS_REFRESH"))
	}
	cfg.GTFSRefresh = refresh

	cfg.MQTTURL = getenv("MQTT_URL")
	if cfg.MQTTURL != "" {
		if u, err := url.Parse(cfg.MQTTURL); err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
			return Config{}, fmt.Errorf("MQTT_URL: %q is not a ws(s) URL", cfg.MQTTURL)
		}
	}
	cfg.MQTTServerName = getenv("MQTT_TLS_SERVER_NAME")
	cfg.MQTTDialAddress = getenv("MQTT_DIAL_ADDRESS")
	if cfg.MQTTDialAddress != "" {
		if _, _, err := net.SplitHostPort(cfg.MQTTDialAddress); err != nil {
			return Config{}, fmt.Errorf("MQTT_DIAL_ADDRESS: %w", err)
		}
	}
	return cfg, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
