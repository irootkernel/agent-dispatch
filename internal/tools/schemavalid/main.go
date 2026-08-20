// Command schemavalid validates the SOT docs package: it compiles every
// schemas/*.json document as Draft 2020-12 and validates the example and
// integration documents. It is the Makefile schema-validation target and
// replaces the retired docs/scripts/validate-json-schemas.py (D-015).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rootkernel/jjukkumi/internal/schemavalid"
)

func main() {
	root := flag.String("root", "docs", "SOT package root containing schemas/ and examples/")
	flag.Parse()
	lines, failures, err := schemavalid.Validate(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "schemavalid: %v\n", err)
		os.Exit(1)
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	for _, failure := range failures {
		fmt.Printf("FAIL %s\n", failure)
	}
	if len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "%d document(s) failed validation\n", len(failures))
		os.Exit(1)
	}
	fmt.Println("all documents valid")
}
