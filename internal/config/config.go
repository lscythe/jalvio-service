package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Listen  string
	DataDir string
	// RegionsFile lists the regions served and their upstreams.
	RegionsFile        string
	MaxLiveConnections int
	// BrowserOrigins may open the live stream; requests without an Origin
	// header, such as the native apps', are always allowed.
	BrowserOrigins []string
	// PhotonURL is empty when place search is disabled.
	PhotonURL               string
	PhotonUserAgent         string
	PlacesRequestsPerMinute int
	// LegalFile holds the owner's terms and privacy policy; when it does not
	// exist, none are published.
	LegalFile string
}

// ListenAddress returns JALVIO_LISTEN or its default, without validating it.
func ListenAddress(getenv func(string) string) string {
	return valueOr(getenv("JALVIO_LISTEN"), ":8080")
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Listen:  ListenAddress(getenv),
		DataDir: valueOr(getenv("JALVIO_DATA_DIR"), "./data"),
	}
	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		return Config{}, fmt.Errorf("JALVIO_LISTEN: %w", err)
	}
	cfg.RegionsFile = valueOr(getenv("REGIONS_FILE"), filepath.Join(cfg.DataDir, "regions.json"))

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

	cfg.LegalFile = valueOr(getenv("LEGAL_FILE"), filepath.Join(cfg.DataDir, "legal.json"))
	return cfg, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
