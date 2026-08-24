// Command toolchaincheck enforces the exact Go toolchain pinned by the
// go.mod go directive (SCP-005): `make verify` and `make release` run it
// before any build step, so an ambient compiler cannot silently replace
// the pinned one (E9-T7, D-023 F4). The version in use is taken from
// runtime.Version — the toolchain that just compiled this tool — which
// is exactly the toolchain the Makefile's $(GO) would build with.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// VersionMatches reports whether the toolchain version in use is
// exactly the pinned version. Both inputs accept the optional "go"
// prefix (`go env GOVERSION` and runtime.Version carry it; the go.mod
// go directive does not).
func VersionMatches(inUse, want string) bool {
	return strings.TrimPrefix(inUse, "go") == strings.TrimPrefix(want, "go")
}

func main() {
	want := flag.String("want", "", "the exact toolchain version the go.mod go directive pins")
	flag.Parse()
	if *want == "" {
		fmt.Fprintln(os.Stderr, "toolchaincheck: -want is required (pass the go.mod go directive value)")
		os.Exit(2)
	}
	inUse := runtime.Version()
	if !VersionMatches(inUse, *want) {
		fmt.Fprintf(os.Stderr, "toolchaincheck: the toolchain in use is %s but go.mod pins %s; select the pinned toolchain before verifying or releasing\n", inUse, *want)
		os.Exit(1)
	}
	fmt.Printf("toolchaincheck: %s matches the go.mod pin\n", inUse)
}
