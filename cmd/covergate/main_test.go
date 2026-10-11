package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func setup(t *testing.T, profile string) {
	t.Helper()
	t.Chdir(t.TempDir())
	writeFiles(t, map[string]string{
		"go.mod":                     "module example.com/app\n",
		"internal/good/good.go":      "package good\n",
		"internal/good/good_test.go": "package good\n",
		"internal/low/low.go":        "package low\n",
		"internal/low/low_test.go":   "package low\n",
		"cover.out":                  profile,
	})
}

const profile = `mode: atomic
example.com/app/internal/good/good.go:1.1,2.2 9 1
example.com/app/internal/good/good.go:3.1,4.2 1 0
example.com/app/internal/low/low.go:1.1,2.2 1 1
example.com/app/internal/low/low.go:3.1,4.2 1 0
`

func TestRunPassesNamedPackages(t *testing.T) {
	setup(t, profile)
	var stdout, stderr bytes.Buffer

	code := run([]string{"-profile", "cover.out", "internal/good/good.go"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 package") {
		t.Errorf("stdout = %q, want a summary", stdout.String())
	}
}

func TestRunFailsAllPackagesBelowThreshold(t *testing.T) {
	setup(t, profile)
	var stdout, stderr bytes.Buffer

	code := run([]string{"-profile", "cover.out", "-min", "80"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if want := "internal/low: coverage 50.0% is below 80%"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
	if strings.Contains(stderr.String(), "internal/good") {
		t.Errorf("stderr = %q, want internal/good to pass", stderr.String())
	}
}

func TestRunReportsUsageErrors(t *testing.T) {
	setup(t, profile)
	for _, args := range [][]string{
		{"-profile", "missing.out"},
		{"-min", "lots"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("run(%v) exit = %d, want 2", args, code)
		}
	}
}
