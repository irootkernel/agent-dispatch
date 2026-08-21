package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/app/workreceipt"
	"github.com/rootkernel/jjukkumi/internal/config"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// runWork implements `work begin|complete|fail` (cli-spec §7, FBK-005):
// the plugin-free cooperative receipt commands a Hermes task calls. Every
// rejection is reported as work_receipt_invalid with the bounded reasons
// and nothing is persisted beyond the audit entry.
func runWork(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "work", "work requires a subcommand: begin, complete, or fail")
	}
	sub, rest := args[0], args[1:]
	if sub != "begin" && sub != "complete" && sub != "fail" {
		return usageError(stderr, "work", fmt.Sprintf("unknown work subcommand %q", sub))
	}
	command := "work " + sub
	switch sub {
	case "begin":
		return runWorkBegin(command, rest, stdout, stderr)
	case "complete":
		return runWorkComplete(command, rest, stdout, stderr)
	default:
		return runWorkFail(command, rest, stdout, stderr)
	}
}

// runWorkFlags parses the shared work flags plus its value-only flags.
func runWorkFlags(command string, args []string, stderr io.Writer) (dispatchesFlags, int) {
	return parseDispatchesFlags(command, args, stderr, map[string]bool{
		"--dispatch-id": true, "--run-id": true, "--external-task-id": true,
		"--base-revision": true, "--result-revision": true, "--manifest": true,
		"--failure-code": true, "--detail": true,
	})
}

// workReceiptErr maps one service failure onto the stable exit codes
// (error-model): validation rejections are input_rejected (4), lineage
// conflicts are transition_invalid (14), storage failures are 20.
func workReceiptErr(stderr io.Writer, command string, err error) int {
	var invalid *workreceipt.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeError(stderr, command, "work_receipt_invalid", "input_rejected", invalid.Error())
		return 4
	case errors.Is(err, ports.ErrIntentNotFound):
		writeError(stderr, command, "dispatch_not_found", "usage", err.Error())
		return 4
	case errors.Is(err, ports.ErrRunNotBegun), errors.Is(err, ports.ErrRunAlreadyRecorded):
		writeError(stderr, command, "work_receipt_invalid", "input_rejected", err.Error())
		return 4
	case errors.Is(err, ports.ErrStateNotEligible), errors.Is(err, sqlite.ErrOptimisticConcurrency):
		// The generation fence surfaces as a conflict: the receipt
		// matched a different generation than the one completed.
		writeError(stderr, command, "transition_invalid", "conflict", err.Error())
		return 14
	default:
		// Typed classification: store surfaces are storage; anything
		// else is an internal-class defect, never a storage relabel.
		var storeErr *workreceipt.StoreError
		if errors.As(err, &storeErr) {
			writeError(stderr, command, "sqlite_query_failed", "storage", storeErr.Err.Error())
			return 20
		}
		writeError(stderr, command, "internal_unclassified", "internal", err.Error())
		return 40
	}
}

func runWorkBegin(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := runWorkFlags(command, args, stderr)
	if code != 0 {
		return code
	}
	dispatchID := flags.val("--dispatch-id")
	runID := flags.val("--run-id")
	if dispatchID == "" || runID == "" {
		return usageError(stderr, command, "work begin requires --dispatch-id and --run-id")
	}
	intent, store, exit := loadWorkIntent(command, flags.val("--config"), dispatchID, stderr)
	if exit != 0 {
		return exit
	}
	defer store.Close()
	service, exit := workService(command, flags.val("--config"), store, intent.RouteID, stderr)
	if exit != 0 {
		return exit
	}
	result, err := service.Begin(requestCtx(), workreceipt.BeginInput{
		DispatchID: dispatchID, RunID: runID,
		ExternalTaskID: flags.val("--external-task-id"), BaseRevision: flags.val("--base-revision"),
	})
	if err != nil {
		return workReceiptErr(stderr, command, err)
	}
	return writeEnvelope(stdout, command, result)
}

func runWorkComplete(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := runWorkFlags(command, args, stderr)
	if code != 0 {
		return code
	}
	dispatchID := flags.val("--dispatch-id")
	runID := flags.val("--run-id")
	manifestRef := flags.val("--manifest")
	if dispatchID == "" || runID == "" || manifestRef == "" {
		return usageError(stderr, command, "work complete requires --dispatch-id, --run-id, and --manifest")
	}
	manifest, code := readManifest(command, manifestRef, stderr)
	if code != 0 {
		return code
	}
	intent, store, exit := loadWorkIntent(command, flags.val("--config"), dispatchID, stderr)
	if exit != 0 {
		return exit
	}
	defer store.Close()
	service, exit := workService(command, flags.val("--config"), store, intent.RouteID, stderr)
	if exit != 0 {
		return exit
	}
	result, err := service.Complete(requestCtx(), workreceipt.CompleteInput{
		DispatchID: dispatchID, RunID: runID, ManifestJSON: manifest,
		ResultRevision: flags.val("--result-revision"),
	})
	if err != nil {
		return workReceiptErr(stderr, command, err)
	}
	if result.AuditWarning != nil {
		return writeEnvelopeWithWarnings(stdout, command, result, []string{
			"the completion committed but its attribution audit append failed: " + result.AuditWarning.Error(),
		})
	}
	return writeEnvelope(stdout, command, result)
}

func runWorkFail(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := runWorkFlags(command, args, stderr)
	if code != 0 {
		return code
	}
	dispatchID := flags.val("--dispatch-id")
	runID := flags.val("--run-id")
	failureCode := flags.val("--failure-code")
	if dispatchID == "" || runID == "" || failureCode == "" {
		return usageError(stderr, command, "work fail requires --dispatch-id, --run-id, and --failure-code")
	}
	intent, store, exit := loadWorkIntent(command, flags.val("--config"), dispatchID, stderr)
	if exit != 0 {
		return exit
	}
	defer store.Close()
	service, exit := workService(command, flags.val("--config"), store, intent.RouteID, stderr)
	if exit != 0 {
		return exit
	}
	result, err := service.Fail(requestCtx(), workreceipt.FailInput{
		DispatchID: dispatchID, RunID: runID, FailureCode: failureCode, Detail: flags.val("--detail"),
	})
	if err != nil {
		return workReceiptErr(stderr, command, err)
	}
	return writeEnvelope(stdout, command, result)
}

// loadWorkIntent resolves the dispatch (and its route) on the shared
// operator store; the route owns the failure budget and resource root.
func loadWorkIntent(command, configPath, dispatchID string, stderr io.Writer) (ports.IntentSnapshot, storeOp, int) {
	store, closer, code := openOperatorStore(command, configPath, stderr)
	if code != 0 {
		return ports.IntentSnapshot{}, nil, code
	}
	intent, err := store.LoadIntent(requestCtx(), dispatchID)
	if err != nil {
		closer.Close()
		if errors.Is(err, ports.ErrIntentNotFound) {
			writeError(stderr, command, "dispatch_not_found", "usage", err.Error())
			return ports.IntentSnapshot{}, nil, 4
		}
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return ports.IntentSnapshot{}, nil, 20
	}
	return intent, store, 0
}

// workService assembles the receipt service over the open store: the
// resource resolver carries the containment defense (SEC-002) and the
// route contributes its failure budget.
func workService(command, configPath string, store storeOp, routeID string, stderr io.Writer) (*workreceipt.Service, int) {
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return nil, 3
	}
	route, ok := cfg.Routes[routeID]
	if !ok {
		writeError(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", routeID))
		return nil, 3
	}
	resource, ok := cfg.Resources[route.Source.Resource]
	if !ok {
		writeError(stderr, command, "config_invalid", "configuration", fmt.Sprintf("resource %q is not defined", route.Source.Resource))
		return nil, 3
	}
	runtime, err := newRouteRuntime(cfg, route, resource)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return nil, 3
	}
	return &workreceipt.Service{
		Store:         store,
		Now:           time.Now,
		Resolver:      runtime.resolver,
		FailureBudget: route.Dispatch.FailureBudget,
	}, 0
}

// readManifest loads the manifest document from a file or standard
// input with an explicit size bound (SEC-009).
func readManifest(command, ref string, stderr io.Writer) (string, int) {
	var body []byte
	var err error
	if ref == "-" {
		body, err = io.ReadAll(io.LimitReader(os.Stdin, workreceipt.MaxManifestBytes+1))
	} else {
		f, openErr := os.Open(ref)
		if openErr != nil {
			writeError(stderr, command, "work_receipt_invalid", "input_rejected", openErr.Error())
			return "", 4
		}
		defer f.Close()
		body, err = io.ReadAll(io.LimitReader(f, workreceipt.MaxManifestBytes+1))
	}
	if err != nil {
		writeError(stderr, command, "work_receipt_invalid", "input_rejected", err.Error())
		return "", 4
	}
	if len(body) > workreceipt.MaxManifestBytes {
		writeError(stderr, command, "work_receipt_invalid", "input_rejected", fmt.Sprintf("manifest exceeds %d bytes", workreceipt.MaxManifestBytes))
		return "", 4
	}
	return string(body), 0
}
