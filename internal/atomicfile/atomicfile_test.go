package atomicfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lscythe/jalvio-service/internal/atomicfile"
)

func TestWriteReplacesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feed.zip")

	for _, content := range []string{"first", "second"} {
		if err := atomicfile.Write(path, []byte(content)); err != nil {
			t.Fatalf("Write(%q): %v", content, err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Errorf("content = %q, want %q", got, content)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want only the target file", len(entries))
	}
}

func TestWriteFailsWithoutDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "feed.zip")
	if err := atomicfile.Write(path, []byte("x")); err == nil {
		t.Fatal("Write succeeded, want error")
	}
}
