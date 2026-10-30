package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	Listen      string
	DatabaseURL string
	MaxConns    int
	// GoogleClientIDs is empty when Google sign-in is disabled.
	GoogleClientIDs []string
	GoogleKeysURL   string
	// AccountsPerHour limits device accounts created per client address.
	AccountsPerHour int
}

// ListenAddress returns COMMUNITY_LISTEN or its default, without validating it.
func ListenAddress(getenv func(string) string) string {
	return valueOr(getenv("COMMUNITY_LISTEN"), ":8081")
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Listen:      ListenAddress(getenv),
		DatabaseURL: getenv("COMMUNITY_DATABASE_URL"),
	}
	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		return Config{}, fmt.Errorf("COMMUNITY_LISTEN: %w", err)
	}
	if err := checkDatabaseURL(cfg.DatabaseURL); err != nil {
		return Config{}, fmt.Errorf("COMMUNITY_DATABASE_URL: %w", err)
	}
	var err error
	if cfg.MaxConns, err = positive(getenv, "COMMUNITY_DB_MAX_CONNS", "5"); err != nil {
		return Config{}, err
	}
	if cfg.AccountsPerHour, err = positive(getenv, "COMMUNITY_ACCOUNTS_PER_HOUR", "10"); err != nil {
		return Config{}, err
	}

	cfg.GoogleKeysURL = getenv("GOOGLE_KEYS_URL")
	if ids := strings.Fields(getenv("GOOGLE_CLIENT_IDS")); len(ids) > 0 {
		cfg.GoogleClientIDs = ids
		if cfg.GoogleKeysURL == "" {
			return Config{}, errors.New("GOOGLE_KEYS_URL: required when GOOGLE_CLIENT_IDS is set")
		}
		if u, err := url.Parse(cfg.GoogleKeysURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Config{}, fmt.Errorf("GOOGLE_KEYS_URL: %q is not an http(s) URL", cfg.GoogleKeysURL)
		}
	}
	return cfg, nil
}

// checkDatabaseURL requires a verified TLS connection to any database that is
// not on this machine or on a private compose network.
func checkDatabaseURL(raw string) error {
	if raw == "" {
		return errors.New("required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return errors.New("not a postgres:// URL")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return nil
		}
	} else if host == "localhost" || !strings.Contains(host, ".") {
		return nil
	}
	if u.Query().Get("sslmode") != "verify-full" {
		return fmt.Errorf("host %s needs sslmode=verify-full", host)
	}
	return nil
}

func positive(getenv func(string) string, key, fallback string) (int, error) {
	n, err := strconv.Atoi(valueOr(getenv(key), fallback))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s: %q is not a positive integer", key, getenv(key))
	}
	return n, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
