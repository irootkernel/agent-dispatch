// Command importlint enforces the package dependency direction rules from
// docs/docs/04-implementation/repository-layout.md so the skeleton stays
// enforceable from the first commit:
//
//   - internal/domain imports nothing from internal/{app,config,cli,
//     adapters,observability,platformpaths};
//   - internal/ports imports nothing from internal/{app,adapters,config,cli,
//     observability};
//   - internal/config imports nothing from internal/{app,cli,adapters,
//     observability}.
//
// It reads package import sets from `go list -json` so the rules are checked
// against the real build graph, not a filename heuristic.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

const modulePrefix = "github.com/irootkernel/agent-dispatch/"

type goPackage struct {
	ImportPath string   `json:"ImportPath"`
	Imports    []string `json:"Imports"`
}

// forbiddenFor maps an internal package family to the internal families it
// must not import, mirroring repository-layout.md's package rules. Keys and
// values carry a trailing slash for readability only; matching uses
// familyPrefixMatch, which also covers the family root package.
var forbiddenFor = map[string][]string{
	"internal/domain/": {"internal/app/", "internal/config/", "internal/cli/", "internal/adapters/", "internal/observability/", "internal/platformpaths/"},
	"internal/ports/":  {"internal/app/", "internal/adapters/", "internal/config/", "internal/cli/", "internal/observability/"},
	"internal/config/": {"internal/app/", "internal/cli/", "internal/adapters/", "internal/observability/"},
}

// familyPrefixMatch reports whether path is the family package itself or
// a subpackage of it, so family roots like internal/ports are covered.
func familyPrefixMatch(path, family string) bool {
	return path == family || strings.HasPrefix(path, family+"/")
}

// checkPackages applies the rules to a package set and returns the number
// of internal packages inspected and every violation found.
func checkPackages(pkgs []goPackage) (int, []string) {
	var violations []string
	count := 0
	for _, pkg := range pkgs {
		if !strings.HasPrefix(pkg.ImportPath, modulePrefix+"internal/") {
			continue
		}
		count++
		for family, forbidden := range forbiddenFor {
			if !familyPrefixMatch(pkg.ImportPath, modulePrefix+strings.TrimSuffix(family, "/")) {
				continue
			}
			for _, imp := range pkg.Imports {
				if !strings.HasPrefix(imp, modulePrefix+"internal/") {
					continue
				}
				for _, bad := range forbidden {
					if familyPrefixMatch(imp, modulePrefix+strings.TrimSuffix(bad, "/")) {
						violations = append(violations,
							fmt.Sprintf("%s must not import %s (rule: %s may not depend on %s)",
								pkg.ImportPath, imp, strings.TrimSuffix(family, "/"), strings.TrimSuffix(bad, "/")))
					}
				}
			}
		}
	}
	sort.Strings(violations)
	return count, violations
}

func main() {
	cmd := exec.Command("go", "list", "-json", "./internal/...")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			fmt.Fprintf(os.Stderr, "importlint: go list failed: %v\n%s", err, ee.Stderr)
		} else {
			fmt.Fprintf(os.Stderr, "importlint: go list failed: %v\n", err)
		}
		os.Exit(1)
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	var pkgs []goPackage
	for dec.More() {
		var pkg goPackage
		if err := dec.Decode(&pkg); err != nil {
			fmt.Fprintf(os.Stderr, "importlint: decoding go list output: %v\n", err)
			os.Exit(1)
		}
		pkgs = append(pkgs, pkg)
	}
	count, violations := checkPackages(pkgs)
	if count == 0 {
		fmt.Fprintln(os.Stderr, "importlint: no internal packages found")
		os.Exit(1)
	}
	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintf(os.Stderr, "importlint: %s\n", v)
		}
		os.Exit(1)
	}
	fmt.Printf("importlint: %d internal packages satisfy the dependency direction rules\n", count)
}
