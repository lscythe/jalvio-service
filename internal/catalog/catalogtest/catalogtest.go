package catalogtest

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// Feed zips the fixture feed in internal/catalog/testdata/feed, replacing the
// named files' contents and leaving out the omitted files.
func Feed(t testing.TB, replace map[string]string, omit ...string) []byte {
	t.Helper()
	_, self, _, _ := runtime.Caller(0)
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(self), "..", "testdata", "feed", "*.txt"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("fixture feed not found: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, path := range paths {
		name := filepath.Base(path)
		if slices.Contains(omit, name) {
			continue
		}
		content, ok := replace[name]
		if !ok {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			content = string(raw)
		}
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
