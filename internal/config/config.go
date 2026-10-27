package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
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
	// MQTTClientIDPrefix starts the generated client ID.
	MQTTClientIDPrefix string
	// StopAPIURL is empty when the stop facilities sync is disabled.
	StopAPIURL        string
	StopAPIAppVersion string
	// StopInfoSyncAt is the daily sync time as an offset from local midnight.
	StopInfoSyncAt     time.Duration
	MaxLiveConnections int
	// BrowserOrigins may open the live stream; requests without an Origin
	// header, such as the native apps', are always allowed.
	BrowserOrigins []string
	// PhotonURL is empty when place search is disabled.
	PhotonURL               string
	PhotonUserAgent         string
	PlacesRequestsPerMinute int
}

// ListenAddress returns JALVIO_LISTEN or its default, without validating it.
func ListenAddress(getenv func(string) string) string {
	return valueOr(getenv("JALVIO_LISTEN"), ":8080")
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Listen:  ListenAddress(getenv),
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
	cfg.MQTTClientIDPrefix = valueOr(getenv("MQTT_CLIENT_ID_PREFIX"), "jalvio-")

	cfg.StopAPIURL = getenv("STOP_API_URL")
	cfg.StopAPIAppVersion = getenv("STOP_API_APP_VERSION")
	if cfg.StopAPIURL != "" {
		if u, err := url.Parse(cfg.StopAPIURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Config{}, fmt.Errorf("STOP_API_URL: %q is not an http(s) URL", cfg.StopAPIURL)
		}
		if cfg.StopAPIAppVersion == "" {
			return Config{}, errors.New("STOP_API_APP_VERSION: required when STOP_API_URL is set")
		}
	}
	syncAt, err := time.Parse("15:04", valueOr(getenv("STOPINFO_SYNC_AT"), "03:00"))
	if err != nil {
		return Config{}, fmt.Errorf("STOPINFO_SYNC_AT: %q is not a HH:MM time", getenv("STOPINFO_SYNC_AT"))
	}
	cfg.StopInfoSyncAt = time.Duration(syncAt.Hour())*time.Hour + time.Duration(syncAt.Minute())*time.Minute

	maxLive, err := strconv.Atoi(valueOr(getenv("MAX_LIVE_CONNECTIONS"), "2000"))
	if err != nil || maxLive <= 0 {
		return Config{}, fmt.Errorf("MAX_LIVE_CONNECTIONS: %q is not a positive integer", getenv("MAX_LIVE_CONNECTIONS"))
	}
	cfg.MaxLiveConnections = maxLive
	for _, origin := range strings.Fields(getenv("BROWSER_ORIGINS")) {
		if u, err := url.Parse(origin); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			return Config{}, fmt.Errorf("BROWSER_ORIGINS: %q is not an http(s) origin", origin)
		}
		cfg.BrowserOrigins = append(cfg.BrowserOrigins, origin)
	}

	cfg.PhotonURL = getenv("PHOTON_URL")
	cfg.PhotonUserAgent = getenv("PHOTON_USER_AGENT")
	if cfg.PhotonURL != "" {
		if u, err := url.Parse(cfg.PhotonURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Config{}, fmt.Errorf("PHOTON_URL: %q is not an http(s) URL", cfg.PhotonURL)
		}
		// Public Photon instances ask for a User-Agent that identifies the operator.
		if cfg.PhotonUserAgent == "" {
			return Config{}, errors.New("PHOTON_USER_AGENT: required when PHOTON_URL is set")
		}
	}
	perMinute, err := strconv.Atoi(valueOr(getenv("PLACES_REQUESTS_PER_MINUTE"), "60"))
	if err != nil || perMinute <= 0 {
		return Config{}, fmt.Errorf("PLACES_REQUESTS_PER_MINUTE: %q is not a positive integer", getenv("PLACES_REQUESTS_PER_MINUTE"))
	}
	cfg.PlacesRequestsPerMinute = perMinute
	return cfg, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
