package idfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lscythe/jalvio-service/internal/idfile"
)

func TestLoadOrCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id")
	calls := 0
	generate := func() string { calls++; return "id-1" }

	for range 2 {
		id, err := idfile.LoadOrCreate(path, generate)
		if err != nil {
			t.Fatalf("LoadOrCreate: %v", err)
		}
		if id != "id-1" {
			t.Errorf("id = %q, want id-1", id)
		}
	}
	if calls != 1 {
		t.Errorf("generate called %d times, want once", calls)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600", perm)
	}
}

func TestLoadOrCreateRejectsBlankFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(path, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := idfile.LoadOrCreate(path, func() string { return "new" }); err == nil {
		t.Error("blank stored ID accepted, want error")
	}
}

func TestLoadOrCreateReportsReadErrors(t *testing.T) {
	if _, err := idfile.LoadOrCreate(t.TempDir(), func() string { return "new" }); err == nil {
		t.Error("reading a directory succeeded, want error")
	}
}
