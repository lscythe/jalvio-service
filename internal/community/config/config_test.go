package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lscythe/jalvio-service/internal/community/config"
)

const databaseURL = "postgres://community@localhost:5432/community"

// env returns a getenv for vars plus the one required setting.
func env(vars map[string]string) func(string) string {
	return func(key string) string {
		if v, ok := vars[key]; ok {
			return v
		}
		if key == "COMMUNITY_DATABASE_URL" {
			return databaseURL
		}
		return ""
	}
}

func TestListenAddress(t *testing.T) {
	if got := config.ListenAddress(env(nil)); got != ":8081" {
		t.Errorf("default = %q, want :8081", got)
	}
	if got := config.ListenAddress(env(map[string]string{"COMMUNITY_LISTEN": "127.0.0.1:9001"})); got != "127.0.0.1:9001" {
		t.Errorf("override = %q, want 127.0.0.1:9001", got)
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		Listen:          ":8081",
		DatabaseURL:     databaseURL,
		MaxConns:        5,
		AccountsPerHour: 10,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load = %+v, want %+v", cfg, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"COMMUNITY_LISTEN":            "127.0.0.1:9001",
		"COMMUNITY_DATABASE_URL":      "postgres://u@db.example.com/db?sslmode=verify-full",
		"COMMUNITY_DB_MAX_CONNS":      "12",
		"GOOGLE_CLIENT_IDS":           "a.apps  b.apps",
		"GOOGLE_KEYS_URL":             "https://keys.example.com/certs",
		"COMMUNITY_ACCOUNTS_PER_HOUR": "3",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		Listen:          "127.0.0.1:9001",
		DatabaseURL:     "postgres://u@db.example.com/db?sslmode=verify-full",
		MaxConns:        12,
		GoogleClientIDs: []string{"a.apps", "b.apps"},
		GoogleKeysURL:   "https://keys.example.com/certs",
		AccountsPerHour: 3,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load = %+v, want %+v", cfg, want)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		vars    map[string]string
		mention string
	}{
		{"missing database URL", map[string]string{"COMMUNITY_DATABASE_URL": ""}, "COMMUNITY_DATABASE_URL"},
		{"database URL not postgres", map[string]string{"COMMUNITY_DATABASE_URL": "mysql://localhost/db"}, "COMMUNITY_DATABASE_URL"},
		{"listen without port", map[string]string{"COMMUNITY_LISTEN": "8081"}, "COMMUNITY_LISTEN"},
		{"zero connections", map[string]string{"COMMUNITY_DB_MAX_CONNS": "0"}, "COMMUNITY_DB_MAX_CONNS"},
		{"connections not a number", map[string]string{"COMMUNITY_DB_MAX_CONNS": "x"}, "COMMUNITY_DB_MAX_CONNS"},
		{"zero accounts per hour", map[string]string{"COMMUNITY_ACCOUNTS_PER_HOUR": "0"}, "COMMUNITY_ACCOUNTS_PER_HOUR"},
		{"client IDs without keys URL", map[string]string{"GOOGLE_CLIENT_IDS": "a.apps"}, "GOOGLE_KEYS_URL"},
		{"keys URL not http", map[string]string{"GOOGLE_CLIENT_IDS": "a.apps", "GOOGLE_KEYS_URL": "ftp://x"}, "GOOGLE_KEYS_URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Load(env(tc.vars))
			if err == nil {
				t.Fatal("Load accepted an invalid value")
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("error %q does not mention %q", err, tc.mention)
			}
		})
	}
}

func TestDatabaseTLS(t *testing.T) {
	for _, accepted := range []string{
		"postgres://u@localhost/db",
		"postgres://u@127.0.0.1/db",
		"postgres://u@[::1]/db",
		"postgres://u@community-db/db",
		"postgresql://u@db.example.com:6543/db?sslmode=verify-full",
	} {
		if _, err := config.Load(env(map[string]string{"COMMUNITY_DATABASE_URL": accepted})); err != nil {
			t.Errorf("Load(%s): %v", accepted, err)
		}
	}
	for _, rejected := range []string{
		"postgres://u@db.example.com/db",
		"postgres://u@db.example.com/db?sslmode=require",
		"postgres://u@db.example.com/db?sslmode=disable",
	} {
		_, err := config.Load(env(map[string]string{"COMMUNITY_DATABASE_URL": rejected}))
		if err == nil || !strings.Contains(err.Error(), "sslmode=verify-full") {
			t.Errorf("Load(%s) = %v, want an error asking for sslmode=verify-full", rejected, err)
		}
	}
}
