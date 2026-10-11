// Package covergate checks that Go packages have tests and enough statement
// coverage.
package covergate

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
)

type Counts struct {
	Statements int
	Covered    int
}

func (c Counts) Percent() float64 {
	if c.Statements == 0 {
		return 100
	}
	return 100 * float64(c.Covered) / float64(c.Statements)
}

type Problem struct {
	Dir    string
	Reason string
}

func ModulePath(fsys fs.FS) (string, error) {
	data, err := fs.ReadFile(fsys, "go.mod")
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(data)) {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`), nil
		}
	}
	return "", fmt.Errorf("go.mod has no module line")
}

// ParseProfile sums statements per package directory, relative to the module
// root, from a Go cover profile.
func ParseProfile(r io.Reader, modulePath string) (map[string]Counts, error) {
	counts := map[string]Counts{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		file, statements, covered, err := parseBlock(line)
		if err != nil {
			return nil, fmt.Errorf("cover profile line %q: %w", line, err)
		}
		rel, ok := strings.CutPrefix(file, modulePath+"/")
		if !ok {
			return nil, fmt.Errorf("cover profile file %s is outside module %s", file, modulePath)
		}
		c := counts[path.Dir(rel)]
		c.Statements += statements
		if covered {
			c.Covered += statements
		}
		counts[path.Dir(rel)] = c
	}
	return counts, scanner.Err()
}

// parseBlock parses "file:start,end statements count".
func parseBlock(line string) (file string, statements int, covered bool, err error) {
	i := strings.LastIndex(line, ":")
	if i < 0 {
		return "", 0, false, fmt.Errorf("missing position")
	}
	fields := strings.Fields(line[i+1:])
	if len(fields) != 3 {
		return "", 0, false, fmt.Errorf("want position, statements, and count")
	}
	statements, err = strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, false, err
	}
	count, err := strconv.Atoi(fields[2])
	if err != nil {
		return "", 0, false, err
	}
	return line[:i], statements, count > 0, nil
}

// Packages lists every directory holding non-test Go sources, skipping
// testdata, hidden directories, and test helper packages.
func Packages(fsys fs.FS) ([]string, error) {
	var dirs []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		dir := path.Dir(p)
		if isSource(d.Name()) && !isHelper(dir) && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
		return nil
	})
	slices.Sort(dirs)
	return dirs, err
}

// Dirs turns file and directory paths into a sorted, de-duplicated list of
// package directories.
func Dirs(paths []string) []string {
	var dirs []string
	for _, p := range paths {
		dir := path.Clean(p)
		if strings.HasSuffix(dir, ".go") {
			dir = path.Dir(dir)
		}
		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	slices.Sort(dirs)
	return dirs
}

// Check requires each package in dirs to have tests and, outside cmd/, at
// least min percent statement coverage.
func Check(fsys fs.FS, dirs []string, coverage map[string]Counts, min float64) []Problem {
	var problems []Problem
	for _, dir := range dirs {
		if isHelper(dir) || slices.Contains(strings.Split(dir, "/"), "testdata") {
			continue
		}
		sources, tests := countFiles(fsys, dir)
		if sources == 0 {
			continue
		}
		if tests == 0 {
			problems = append(problems, Problem{Dir: dir, Reason: "no tests"})
			continue
		}
		if dir == "cmd" || strings.HasPrefix(dir, "cmd/") {
			continue
		}
		c, ok := coverage[dir]
		if !ok {
			problems = append(problems, Problem{Dir: dir, Reason: "no coverage data; run the tests with -coverprofile"})
			continue
		}
		if p := c.Percent(); p < min {
			problems = append(problems, Problem{Dir: dir, Reason: fmt.Sprintf("coverage %.1f%% is below %g%%", p, min)})
		}
	}
	return problems
}

func countFiles(fsys fs.FS, dir string) (sources, tests int) {
	entries, _ := fs.ReadDir(fsys, dir)
	for _, e := range entries {
		switch {
		case e.IsDir():
		case strings.HasSuffix(e.Name(), "_test.go"):
			tests++
		case isSource(e.Name()):
			sources++
		}
	}
	return sources, tests
}

func isSource(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

// isHelper reports test helper packages such as catalogtest.
func isHelper(dir string) bool {
	return strings.HasSuffix(path.Base(dir), "test")
}
