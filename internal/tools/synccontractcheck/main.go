package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/irootkernel/agent-dispatch/internal/synccontractcheck"
)

func main() {
	dir := flag.String("dir", "docs/contracts/sync-provider-v1", "provider bundle directory")
	update := flag.Bool("update-checksums", false, "rewrite SHA256SUMS deterministically from bundle.json")
	flag.Parse()
	if *update {
		if err := synccontractcheck.UpdateChecksums(*dir); err != nil {
			fmt.Fprintf(os.Stderr, "sync-contract-check: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("sync-contract-check: checksums updated")
		return
	}
	if err := synccontractcheck.Check(*dir); err != nil {
		fmt.Fprintf(os.Stderr, "sync-contract-check: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("sync-contract-check: bundle valid")
}
