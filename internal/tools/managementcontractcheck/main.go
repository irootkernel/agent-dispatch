package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/irootkernel/agent-dispatch/internal/managementcontractcheck"
)

func main() {
	dir := flag.String("dir", "docs/contracts/sync-management-v1", "management bundle directory")
	flag.Parse()
	report, err := managementcontractcheck.Check(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "management-contract-check: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("management-contract-check: %s; %d JSON fixtures, %d argv fixtures, %d digest vectors\n",
		report.ContractDigest, report.JSONFixtures, report.ArgvFixtures, report.DigestVectors)
}
