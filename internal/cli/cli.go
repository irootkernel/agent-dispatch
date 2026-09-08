// Package cli implements the agent-dispatch command line surface and its JSON
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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/observability"
	"github.com/irootkernel/agent-dispatch/internal/version"
)

// APIVersion identifies the outer envelope contract (cli-spec §12).
const APIVersion = "agent-dispatch.cli/v1"

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
	// Result carries the failing command's structured detail (for
	// example the per-check preflight report) beside the error body.
	Result any `json:"result,omitempty"`
}

// VersionSummary is the compact public identity emitted by version --json.
type VersionSummary struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// globalOptionsMu guards the global option resets under concurrent
// test invocations of Run.
var globalOptionsMu sync.Mutex

// The raw spellings of this invocation's global options, captured as
// they were scanned: nested re-entry (setup wiki re-running Run per
// step) resets the parsed globals, so a wrapper that must forward the
// operator's options to every nested step re-passes these exact
// strings instead of re-deriving them from parsed values.
var (
	globalRawLogLevel string
	globalRawTraceID  string
	globalRawStateDir string
	globalRawTimeout  string
)

// knownCommands lists the top-level commands of the v0.1 CLI tree
// (cli-spec §2). Every command in this set is implemented; the default
// Run branch keeps its not-implemented guard as a safety net for future
// registrations, and an unrecognized name is command_unknown.
var knownCommands = map[string]bool{
	"version": true, "init": true, "config": true, "route": true,
	"watchman": true, "dispatch": true, "dispatches": true,
	"receipts": true, "work": true, "quarantine": true,
	"reconcile": true, "status": true, "doctor": true,
	"maintenance": true, "completion": true, "hermes": true,
	"setup": true, "events": true, "notifications": true, "schedule": true,
}

// Run executes the CLI with the given arguments and writes output to the
// given streams. It returns the process exit code. Global options
// (--log-level, --trace-id) are scanned out before dispatch and shape
// the stderr structured log (OPS-001). This build implements the full
// v0.1 command tree; an unrecognized name is command_unknown.
func Run(args []string, stdout, stderr io.Writer) int {
	// The invocation clock anchors the after-command drain's ten-second
	// whole-invocation budget (E16-T3).
	markInvocationStart()
	if len(args) == 0 {
		writeError(stderr, "", "command_unknown", "usage", "usage: agent-dispatch <command> [flags]; run 'agent-dispatch version --json'")
		return 2
	}
	// One process invocation carries one set of global options; the
	// reset keeps repeated in-process invocations (tests) from
	// inheriting a previous call's overrides. The guard makes the reset
	// itself race-clean; truly concurrent Run calls in one process are
	// not a supported CLI shape (one command per process).
	globalOptionsMu.Lock()
	globalLogLevel = observability.LevelWarn
	globalTraceID = ""
	globalStateDir = ""
	globalRequestTimeout = 0
	globalRawLogLevel = ""
	globalRawTraceID = ""
	globalRawStateDir = ""
	globalRawTimeout = ""
	globalOptionsMu.Unlock()
	rest, ok := scanGlobalOptions(args[0], args[1:], stderr)
	if !ok {
		return 2
	}
	args = rest
	// The CLI-009 help contract (E11-T4): the root and every group
	// parser accepts -h/--help and renders the discovery text at exit
	// 0, before any subcommand validation runs.
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		group := ""
		if len(args) > 1 {
			group = helpGroupRequest(args[1:])
		}
		return runHelp(group, stdout)
	}
	for _, a := range args[1:] {
		if a == "-h" || a == "--help" {
			return runHelp(args[0], stdout)
		}
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "route":
		return runRoute(args[1:], stdout, stderr)
	case "dispatch":
		return runDispatch(args[1:], stdout, stderr)
	case "dispatches":
		return runDispatches(args[1:], stdout, stderr)
	case "watchman":
		return runWatchman(args[1:], stdout, stderr)
	case "config":
		return runConfig(args[1:], stdout, stderr)
	case "receipts":
		return runReceipts(args[1:], stdout, stderr)
	case "work":
		return runWork(args[1:], stdout, stderr)
	case "events":
		return runEvents(args[1:], stdout, stderr)
	case "notifications":
		return runNotifications(args[1:], stdout, stderr)
	case "schedule":
		return runSchedule(args[1:], stdout, stderr)
	case "quarantine":
		return runQuarantine(args[1:], stdout, stderr)
	case "reconcile":
		return runReconcile(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "maintenance":
		return runMaintenance(args[1:], stdout, stderr)
	case "hermes":
		return runHermes(args[1:], stdout, stderr)
	case "setup":
		return runSetup(args[1:], stdout, stderr)
	case "completion":
		return runCompletion(args[1:], stdout, stderr)
	default:
		if knownCommands[args[0]] {
			writeError(stderr, args[0], "command_not_implemented", "usage",
				fmt.Sprintf("command %q is not implemented in this build", args[0]))
		} else {
			writeError(stderr, args[0], "command_unknown", "usage",
				fmt.Sprintf("unknown command %q; run 'agent-dispatch version --json'", args[0]))
		}
		return 2
	}
}

// scanGlobalOptions extracts the global --log-level and --trace-id
// options from one command's arguments, applying them to the process
// log state, and returns the remaining arguments with the command
// preserved in front. An unknown level or a missing option value is a
// fail-closed usage failure.
func scanGlobalOptions(command string, args []string, stderr io.Writer) ([]string, bool) {
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		take := func() (string, bool) {
			if eq := strings.IndexByte(arg, '='); eq >= 0 {
				return arg[eq+1:], true
			}
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case arg == "--log-level" || strings.HasPrefix(arg, "--log-level="):
			value, ok := take()
			if !ok {
				usageError(stderr, command, "--log-level requires a value")
				return nil, false
			}
			level, err := observability.ParseLevel(value)
			if err != nil {
				usageError(stderr, command, err.Error())
				return nil, false
			}
			globalLogLevel = level
			globalRawLogLevel = value
		case arg == "--trace-id" || strings.HasPrefix(arg, "--trace-id="):
			value, ok := take()
			if !ok {
				usageError(stderr, command, "--trace-id requires a value")
				return nil, false
			}
			globalTraceID = value
			globalRawTraceID = value
		case arg == "--state-dir" || strings.HasPrefix(arg, "--state-dir="):
			value, ok := take()
			if !ok {
				usageError(stderr, command, "--state-dir requires a value")
				return nil, false
			}
			if !filepath.IsAbs(value) {
				usageError(stderr, command, "--state-dir requires an absolute path")
				return nil, false
			}
			globalStateDir = value
			globalRawStateDir = value
		case arg == "--timeout" || strings.HasPrefix(arg, "--timeout="):
			value, ok := take()
			if !ok {
				usageError(stderr, command, "--timeout requires a value")
				return nil, false
			}
			d, err := config.ParseDuration(value)
			if err != nil {
				usageError(stderr, command, "--timeout: "+err.Error())
				return nil, false
			}
			globalRequestTimeout = time.Duration(d.Nanos)
			globalRawTimeout = value
		default:
			rest = append(rest, arg)
		}
	}
	return append([]string{command}, rest...), true
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		if len(args) != 1 || args[0] != "--json" {
			return usageError(stderr, "version", fmt.Sprintf("unknown argument %q for version", args[0]))
		}
		if err := json.NewEncoder(stdout).Encode(VersionSummary{
			Name:    "agent-dispatch",
			Version: "v" + strings.TrimPrefix(version.Version, "v"),
		}); err != nil {
			return 40
		}
		return 0
	}
	fmt.Fprintf(stdout, "agent-dispatch v%s\n", strings.TrimPrefix(version.Version, "v"))
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
		TraceID:    globalTraceID,
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
		TraceID:    globalTraceID,
	}); err != nil {
		return 40
	}
	return 0
}

func writeError(w io.Writer, command, code, category, message string) {
	writeErrorWithResult(w, command, code, category, message, nil)
}

// writeErrorWithResult writes an error envelope carrying the command's
// structured detail in its result slot.
func writeErrorWithResult(w io.Writer, command, code, category, message string, result any) {
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
		TraceID: globalTraceID,
		Result:  result,
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
