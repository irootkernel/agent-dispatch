package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/app/ingest"
	"github.com/irootkernel/agent-dispatch/internal/app/workreceipt"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
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
		writeError(stderr, command, "dispatch_not_found", "input_rejected", err.Error())
		return 4
	case errors.Is(err, ports.ErrRunNotBegun), errors.Is(err, ports.ErrRunAlreadyRecorded):
		writeError(stderr, command, "work_receipt_invalid", "input_rejected", err.Error())
		return 4
	case errors.Is(err, ports.ErrStateNotEligible), errors.Is(err, ports.ErrGenerationConflict):
		// The generation fence surfaces as a conflict: the receipt
		// matched a different generation than the one completed.
		writeError(stderr, command, "transition_invalid", "conflict", err.Error())
		return 14
	case isStateTransitionError(err):
		// A route-guard rejection (for example the pre-E8-T1 wedge, or a
		// completion racing an operator exit) is a state conflict, never
		// an internal defect (E8-T1, H-9).
		writeError(stderr, command, "transition_invalid", "conflict", err.Error())
		return 14
	default:
		// Typed classification: store surfaces are storage; anything
		// else is an internal-class defect, never a storage relabel.
		var storeErr *ports.StoreError
		if errors.As(err, &storeErr) {
			writeError(stderr, command, "sqlite_query_failed", "storage", storeErr.Err.Error())
			return 20
		}
		writeError(stderr, command, "internal_unclassified", "internal", err.Error())
		return 40
	}
}

// isStateTransitionError reports whether the failure carries the typed
// route/intent transition error the state package documents as exit 14.
func isStateTransitionError(err error) bool {
	var terr *state.TransitionError
	return errors.As(err, &terr)
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
		if errors.Is(err, ports.ErrIntentNotFound) {
			// The unknown-dispatch rejection is audited like every other
			// invalid receipt through the service's shared shape
			// (E9-T2/L-7, round-1 F002): the service takes the store
			// directly so the unknown dispatch needs no route lookup.
			(&workreceipt.Service{Store: store, Now: time.Now}).AuditUnknownDispatch(requestCtx(), dispatchID)
			closer.Close()
			writeError(stderr, command, "dispatch_not_found", "input_rejected", err.Error())
			return ports.IntentSnapshot{}, nil, 4
		}
		closer.Close()
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return ports.IntentSnapshot{}, nil, 20
	}
	return intent, store, 0
}

// routeOutsideScope builds the route's effective-scope predicate from
// the single ingest encoding (file scope above the pattern engine);
// receipt paths it rejects are immaterial provenance and never block
// exact suppression (E8-T1, H-1.1).
func routeOutsideScope(route config.Route, resource config.Resource) (func(string) bool, error) {
	engine, err := newPatternEngine(route)
	if err != nil {
		return nil, err
	}
	return ingest.OutsideScopePredicate(engine, resource.FileScope), nil
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
	// The route's effective scope (include/exclude plus the resource's
	// file scope) decides which receipt paths are material provenance: a
	// path the route never admits must not block exact suppression as
	// extra provenance (E8-T1, H-1.1).
	outsideScope, err := routeOutsideScope(route, resource)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return nil, 3
	}
	return &workreceipt.Service{
		Store:         store,
		Now:           time.Now,
		Resolver:      runtime.resolver,
		FailureBudget: route.FailureBudget,
		OutsideScope:  outsideScope,
		Log:           opsLogger(stderr, cfg),
		TraceID:       globalTraceID,
		// The follow-up decision the store may create records the
		// independent policy digest, not a route revision echo
		// (E9-T3, L-18).
		PolicyRevision: config.PolicyRevision(route),
		// A legacy completion owing a follow-up resolves the live
		// certified lane so the follow-up child-links (E12-T1).
		DestinationResolver: routeDestinationResolver(cfg),
		// The lane-scoped follow-up (E12-T2, CON-008): each destination's
		// structural conditions come from the live configuration, and the
		// path classes evaluate through the same pattern-engine matcher
		// the dispatch surface wires (identical case mode and semantics).
		LaneConditions:  laneConditionsResolver(cfg),
		LanePathMatcher: destinationPathMatcher(route),
	}, 0
}

// laneConditionsResolver resolves one destination's structural selection
// conditions from the live configuration (E12-T2): nil conditions mean
// the destination selects unconditionally; an unknown destination fails
// closed.
func laneConditionsResolver(cfg *config.Config) func(routeID, destinationID string) (*dispatch.DestinationConditionSet, error) {
	return func(routeID, destinationID string) (*dispatch.DestinationConditionSet, error) {
		route, ok := cfg.Routes[routeID]
		if !ok {
			return nil, fmt.Errorf("route %q is not defined", routeID)
		}
		dest, ok := route.DestinationByID(destinationID)
		if !ok {
			return nil, fmt.Errorf("route %q has no destination %q", routeID, destinationID)
		}
		if dest.Conditions == nil {
			return nil, nil
		}
		return &dispatch.DestinationConditionSet{
			PathInclude: dest.Conditions.PathInclude, PathExclude: dest.Conditions.PathExclude,
			Operations: dest.Conditions.Operations, Classifications: dest.Conditions.Classifications,
			PolicyOutcomes: dest.Conditions.PolicyOutcomes,
		}, nil
	}
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
