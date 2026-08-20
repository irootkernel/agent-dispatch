// Package cli implements the jjukkumi command line surface and its JSON
// envelope (CLI-001, CLI-002): successful machine-consumed output is
// structured JSON on standard output, human output never mixes with it,
// and the structured error envelope and all diagnostics go to standard
// error so a failed command's stdout stays empty.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/rootkernel/jjukkumi/internal/version"
)

// APIVersion identifies the outer envelope contract (cli-spec §12).
const APIVersion = "jjukkumi.cli/v1"

// Envelope is the outer JSON structure of every successful command.
type Envelope struct {
	APIVersion string      `json:"api_version"`
	Command    string      `json:"command"`
	OK         bool        `json:"ok"`
	Result     interface{} `json:"result"`
	Warnings   []string    `json:"warnings"`
	TraceID    string      `json:"trace_id"`
}

// ErrorBody is the error field of a failed command (error-model §1).
type ErrorBody struct {
	Code        string `json:"code"`
	Category    string `json:"category"`
	Message     string `json:"message"`
	Retryable   bool   `json:"retryable"`
	Remediation string `json:"remediation,omitempty"`
}

// ErrorEnvelope is the outer JSON structure of every failed command.
type ErrorEnvelope struct {
	APIVersion string    `json:"api_version"`
	Command    string    `json:"command"`
	OK         bool      `json:"ok"`
	Error      ErrorBody `json:"error"`
	TraceID    string    `json:"trace_id"`
}

// VersionResult is the result payload of the version command (cli-spec §3).
type VersionResult struct {
	Version         string            `json:"version"`
	Commit          string            `json:"commit"`
	BuildTime       string            `json:"build_time"`
	ConfigVersion   string            `json:"config_version"`
	SchemaRange     string            `json:"schema_range"`
	AdapterVersions map[string]string `json:"adapter_versions"`
}

// knownCommands lists the top-level commands of the v0.1 CLI tree
// (cli-spec §2). A command in this set that this build does not implement
// fails with command_not_implemented; anything else is command_unknown.
var knownCommands = map[string]bool{
	"version": true, "init": true, "config": true, "route": true,
	"watchman": true, "dispatch": true, "dispatches": true,
	"receipts": true, "work": true, "quarantine": true,
	"reconcile": true, "status": true, "doctor": true,
	"maintenance": true, "completion": true,
}

// Run executes the CLI with the given arguments and writes output to the
// given streams. It returns the process exit code. This build implements
// the version and init commands; every other registered command is
// reported as an explicit not-implemented error rather than silently
// succeeding, and an unrecognized name is command_unknown.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeError(stderr, "", "command_unknown", "usage", "usage: jjukkumi <command> [flags]; run 'jjukkumi version --output json'")
		return 2
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	default:
		if knownCommands[args[0]] {
			writeError(stderr, args[0], "command_not_implemented", "usage",
				fmt.Sprintf("command %q is not implemented in this build", args[0]))
		} else {
			writeError(stderr, args[0], "command_unknown", "usage",
				fmt.Sprintf("unknown command %q; run 'jjukkumi version --output json'", args[0]))
		}
		return 2
	}
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	jsonOutput := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output", "-o":
			v, err := parseOutputValue(stderr, "version", args, &i)
			if err != nil {
				return 2
			}
			jsonOutput = v
		case "--output=json":
			jsonOutput = true
		case "--output=human":
			jsonOutput = false
		default:
			return usageError(stderr, "version", fmt.Sprintf("unknown argument %q for version", args[i]))
		}
	}
	if jsonOutput {
		return writeEnvelope(stdout, "version", VersionResult{
			Version:         version.Version,
			Commit:          version.Commit,
			BuildTime:       version.BuildTime,
			ConfigVersion:   version.ConfigVersion,
			SchemaRange:     version.SchemaRange,
			AdapterVersions: version.AdapterVersions(),
		})
	}
	fmt.Fprintf(stdout, "jjukkumi %s (commit %s, built %s)\n", version.Version, version.Commit, version.BuildTime)
	fmt.Fprintf(stdout, "config version: %s, schema range: %s\n", version.ConfigVersion, version.SchemaRange)
	names := make([]string, 0, len(version.AdapterVersions()))
	for name := range version.AdapterVersions() {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s=%s", name, version.AdapterVersions()[name])
	}
	fmt.Fprintf(stdout, "adapters: %s\n", b.String())
	return 0
}

func usageError(stderr io.Writer, command, message string) int {
	writeError(stderr, command, "flag_invalid", "usage", message)
	return 2
}

func writeEnvelope(w io.Writer, command string, result interface{}) int {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(Envelope{
		APIVersion: APIVersion,
		Command:    command,
		OK:         true,
		Result:     result,
		Warnings:   []string{},
		TraceID:    "",
	}); err != nil {
		// Exit 1 is intentionally unassigned and must never be emitted
		// (error-model §2); an envelope write failure is internal.
		return 40
	}
	return 0
}

// WriteInternalError emits the internal error envelope used by the entry
// point's panic recovery (exit 40, error-model §2).
func WriteInternalError(w io.Writer, cause any) {
	writeError(w, "", "internal_unclassified", "internal",
		"recovered an unexpected internal failure")
	_ = cause // the raw panic value stays out of diagnostics (SEC-007)
}

// writeEnvelopeWithWarnings emits a success envelope carrying warnings.
func writeEnvelopeWithWarnings(w io.Writer, command string, result interface{}, warnings []string) int {
	if warnings == nil {
		warnings = []string{}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(Envelope{
		APIVersion: APIVersion,
		Command:    command,
		OK:         true,
		Result:     result,
		Warnings:   warnings,
		TraceID:    "",
	}); err != nil {
		return 40
	}
	return 0
}

func writeError(w io.Writer, command, code, category, message string) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(ErrorEnvelope{
		APIVersion: APIVersion,
		Command:    command,
		OK:         false,
		Error: ErrorBody{
			Code:      code,
			Category:  category,
			Message:   message,
			Retryable: false,
		},
		TraceID: "",
	})
}

// parseOutputValue consumes the value after --output/-o and reports
// whether JSON output was requested; both implemented commands share it
// (cli-spec section 1: --output human|json).
func parseOutputValue(stderr io.Writer, command string, args []string, i *int) (bool, error) {
	if *i+1 >= len(args) {
		return false, usageErrorAlreadyWritten(stderr, command, "--output requires a value: --output human|json")
	}
	*i++
	switch args[*i] {
	case "json":
		return true, nil
	case "human":
		return false, nil
	}
	return false, usageErrorAlreadyWritten(stderr, command, fmt.Sprintf("unsupported --output value %q (human or json)", args[*i]))
}

func usageErrorAlreadyWritten(stderr io.Writer, command, message string) error {
	writeError(stderr, command, "flag_invalid", "usage", message)
	return errFlag
}

var errFlag = errors.New("flag error")
