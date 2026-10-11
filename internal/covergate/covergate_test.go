package covergate_test

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lscythe/jalvio-service/internal/covergate"
)

const module = "example.com/app"

func TestModulePath(t *testing.T) {
	fsys := fstest.MapFS{"go.mod": {Data: []byte("module example.com/app\n\ngo 1.27.2\n")}}
	got, err := covergate.ModulePath(fsys)
	if err != nil {
		t.Fatalf("ModulePath: %v", err)
	}
	if got != module {
		t.Errorf("ModulePath = %q, want %q", got, module)
	}

	if _, err := covergate.ModulePath(fstest.MapFS{"go.mod": {Data: []byte("go 1.27.2\n")}}); err == nil {
		t.Error("ModulePath without a module line succeeded, want error")
	}
}

func TestParseProfile(t *testing.T) {
	profile := `mode: atomic
example.com/app/internal/store/store.go:10.2,12.3 2 5
example.com/app/internal/store/store.go:14.2,16.3 3 0
example.com/app/internal/store/file.go:5.2,6.3 1 1
example.com/app/cmd/app/main.go:8.2,9.3 4 0
example.com/app/root.go:3.2,4.3 1 1
`
	got, err := covergate.ParseProfile(strings.NewReader(profile), module)
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	want := map[string]covergate.Counts{
		"internal/store": {Statements: 6, Covered: 3},
		"cmd/app":        {Statements: 4, Covered: 0},
		".":              {Statements: 1, Covered: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseProfile = %v, want %v", got, want)
	}
	if p := got["internal/store"].Percent(); p != 50 {
		t.Errorf("Percent = %v, want 50", p)
	}
}

func TestParseProfileRejectsMalformedLines(t *testing.T) {
	for _, line := range []string{
		"example.com/app/a.go 1 1",
		"example.com/app/a.go:1.1,2.2 x 1",
		"example.com/app/a.go:1.1,2.2 1",
		"other.org/lib/a.go:1.1,2.2 1 1",
	} {
		if _, err := covergate.ParseProfile(strings.NewReader("mode: set\n"+line+"\n"), module); err == nil {
			t.Errorf("ParseProfile(%q) succeeded, want error", line)
		}
	}
}

func TestPackages(t *testing.T) {
	fsys := fstest.MapFS{
		"go.mod":                              {},
		"cmd/app/main.go":                     {},
		"internal/store/store.go":             {},
		"internal/store/store_test.go":        {},
		"internal/store/storetest/fake.go":    {},
		"internal/store/testdata/fixture.go":  {},
		"internal/onlytests/thing_test.go":    {},
		".github/scripts/tool.go":             {},
		"internal/store/testdata/feed/a.txt":  {},
		"internal/docs/README.md":             {},
		"internal/store/nested/inner/deep.go": {},
	}
	got, err := covergate.Packages(fsys)
	if err != nil {
		t.Fatalf("Packages: %v", err)
	}
	want := []string{"cmd/app", "internal/store", "internal/store/nested/inner"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Packages = %v, want %v", got, want)
	}
}

func TestDirs(t *testing.T) {
	got := covergate.Dirs([]string{
		"internal/store/store.go",
		"internal/store/store_test.go",
		"./cmd/app",
		"cmd/app/main.go",
		"main.go",
	})
	want := []string{".", "cmd/app", "internal/store"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Dirs = %v, want %v", got, want)
	}
}

func TestCheck(t *testing.T) {
	fsys := fstest.MapFS{
		"internal/good/good.go":          {},
		"internal/good/good_test.go":     {},
		"internal/low/low.go":            {},
		"internal/low/low_test.go":       {},
		"internal/untested/untested.go":  {},
		"internal/types/types.go":        {},
		"internal/types/types_test.go":   {},
		"internal/norun/norun.go":        {},
		"internal/norun/norun_test.go":   {},
		"internal/store/storetest/s.go":  {},
		"cmd/app/main.go":                {},
		"cmd/app/main_test.go":           {},
		"cmd/bare/main.go":               {},
		"internal/onlytests/x_test.go":   {},
		"internal/store/testdata/fix.go": {},
	}
	coverage := map[string]covergate.Counts{
		"internal/good":     {Statements: 10, Covered: 8},
		"internal/low":      {Statements: 10, Covered: 7},
		"internal/types":    {Statements: 0, Covered: 0},
		"cmd/app":           {Statements: 10, Covered: 1},
		"internal/untested": {Statements: 4, Covered: 0},
	}
	dirs := []string{
		"internal/good", "internal/low", "internal/untested", "internal/types", "internal/norun",
		"internal/store/storetest", "cmd/app", "cmd/bare", "internal/onlytests", "internal/store/testdata",
	}

	got := covergate.Check(fsys, dirs, coverage, 80)
	want := []covergate.Problem{
		{Dir: "internal/low", Reason: "coverage 70.0% is below 80%"},
		{Dir: "internal/untested", Reason: "no tests"},
		{Dir: "internal/norun", Reason: "no coverage data; run the tests with -coverprofile"},
		{Dir: "cmd/bare", Reason: "no tests"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Check =\n%v\nwant\n%v", got, want)
	}
}
