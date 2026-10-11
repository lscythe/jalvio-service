package config_test

import (
	"testing"

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
	if cfg.Listen != ":8080" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, ":8080")
	}
}

func TestLoadListen(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"JALVIO_LISTEN": "127.0.0.1:9000"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9000" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "127.0.0.1:9000")
	}
}

func TestLoadRejectsInvalidListen(t *testing.T) {
	for _, listen := range []string{"8080", "localhost", ":http:x"} {
		if _, err := config.Load(env(map[string]string{"JALVIO_LISTEN": listen})); err == nil {
			t.Errorf("Load(JALVIO_LISTEN=%q) succeeded, want error", listen)
		}
	}
}
