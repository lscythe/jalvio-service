package stopapi_test

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/lscythe/jalvio-service/internal/stopapi"
)

func TestDeviceIDIsAStableUUID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device-id")

	first, err := stopapi.LoadOrCreateDeviceID(path)
	if err != nil {
		t.Fatalf("LoadOrCreateDeviceID: %v", err)
	}
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuid.MatchString(first) {
		t.Errorf("device ID = %q, want a random UUID", first)
	}
	second, err := stopapi.LoadOrCreateDeviceID(path)
	if err != nil {
		t.Fatalf("LoadOrCreateDeviceID: %v", err)
	}
	if second != first {
		t.Errorf("second device ID = %q, want the stored %q", second, first)
	}
}
