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
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Listen:  valueOr(getenv("JALVIO_LISTEN"), ":8080"),
		DataDir: valueOr(getenv("JALVIO_DATA_DIR"), "./data"),
		GTFSURL: valueOr(getenv("GTFS_URL"), "https://gtfs.transjakarta.co.id/files/file_gtfs.zip"),
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
	return cfg, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
