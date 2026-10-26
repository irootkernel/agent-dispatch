// Command agent-dispatch is the CLI entry point. Argument handling and output
// live in internal/cli so they stay testable; main only wires the streams
// and the panic recovery required by error-model §2 (an unrecovered Go
// runtime panic would exit with status 2 and masquerade as a usage error).
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/irootkernel/agent-dispatch/internal/cli"
)

func main() {
	// Let failed stdout/stderr writes reach the CLI's exit-40 handling
	// instead of terminating on SIGPIPE after a durable command commits.
	signal.Ignore(syscall.SIGPIPE)
	defer func() {
		if r := recover(); r != nil {
			cli.WriteInternalError(os.Stderr, r)
			os.Exit(40)
		}
	}()
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
