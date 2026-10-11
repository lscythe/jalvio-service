// Command covergate fails when a package lacks tests or, outside cmd/, falls
// below the coverage threshold. With no paths it checks every package;
// otherwise it checks the packages holding the given files or directories.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lscythe/jalvio-service/internal/covergate"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("covergate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "coverage.out", "Go cover profile")
	min := flags.Float64("min", 80, "minimum statement coverage percent per package")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	problems, checked, err := check(*profilePath, *min, flags.Args())
	if err != nil {
		fmt.Fprintln(stderr, "covergate:", err)
		return 2
	}
	for _, p := range problems {
		fmt.Fprintf(stderr, "%s: %s\n", p.Dir, p.Reason)
	}
	if len(problems) > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "covergate: %d package(s) have tests and at least %g%% coverage\n", checked, *min)
	return 0
}

func check(profilePath string, min float64, paths []string) ([]covergate.Problem, int, error) {
	root := os.DirFS(".")
	module, err := covergate.ModulePath(root)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(filepath.Clean(profilePath))
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	coverage, err := covergate.ParseProfile(f, module)
	if err != nil {
		return nil, 0, err
	}

	dirs := covergate.Dirs(paths)
	if len(paths) == 0 {
		if dirs, err = covergate.Packages(root); err != nil {
			return nil, 0, err
		}
	}
	return covergate.Check(root, dirs, coverage, min), len(dirs), nil
}
