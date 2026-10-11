package config

import (
	"fmt"
	"net"
)

type Config struct {
	Listen string
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Listen: valueOr(getenv("JALVIO_LISTEN"), ":8080"),
	}
	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		return Config{}, fmt.Errorf("JALVIO_LISTEN: %w", err)
	}
	return cfg, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
