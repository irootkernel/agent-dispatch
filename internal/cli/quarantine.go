package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/app/quarantine"
	"github.com/irootkernel/agent-dispatch/internal/app/reconcile"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// runQuarantine implements `quarantine list|show|release|discard`
// (cli-spec §8, PTH-008, OPS-006): the operator surface over durable
// structural holds.
func runQuarantine(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "quarantine", "quarantine requires a subcommand: list, show, release, or discard")
	}
	sub, rest := args[0], args[1:]
	command := "quarantine " + sub
	switch sub {
	case "list":
		return runQuarantineList(command, rest, stdout, stderr)
	case "show":
		return runQuarantineShow(command, rest, stdout, stderr)
	case "release":
		return runQuarantineResolve(command, "release", rest, stdout, stderr)
	case "discard":
		return runQuarantineResolve(command, "discard", rest, stdout, stderr)
	default:
		return usageError(stderr, "quarantine", fmt.Sprintf("unknown quarantine subcommand %q", sub))
	}
}

func runQuarantineList(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	filter := ports.QuarantineFilter{RouteID: flags.val("--route"), State: flags.val("--state")}
	if filter.State != "" && filter.State != "held" && filter.State != "released" && filter.State != "discarded" && filter.State != "superseded" {
		return usageError(stderr, command, "--state must be held, released, discarded, or superseded")
	}
	if raw := flags.val("--limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			return usageError(stderr, command, "--limit must be 1..500")
		}
		filter.Limit = n
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	rows, err := store.ListQuarantine(requestCtx(), filter)
	if err != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return 20
	}
	if rows == nil {
		rows = []ports.QuarantineRecord{}
	}
	return writeEnvelope(stdout, command, map[string]any{"quarantine": rows, "count": len(rows)})
}

func runQuarantineShow(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	if flags.positional == "" {
		return usageError(stderr, command, "quarantine show requires a quarantine ID")
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	rec, err := store.LoadQuarantine(requestCtx(), flags.positional)
	if err != nil {
		return quarantineErr(stderr, command, "show", wrapQuarantineReadError(err))
	}
	return writeEnvelope(stdout, command, rec)
}

// runQuarantineResolve implements release and discard: both require an
// explicit reason and --yes in non-interactive mode, and both record the
// full operator lineage (CLI-006).
func runQuarantineResolve(command, sub string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	if flags.val("--yes") == "" {
		return usageError(stderr, command, sub+" requires --yes in non-interactive mode")
	}
	reason := flags.val("--reason")
	if reason == "" {
		return usageError(stderr, command, sub+" requires --reason")
	}
	if flags.positional == "" {
		return usageError(stderr, command, "quarantine "+sub+" requires a quarantine ID")
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	// The replacement decision carries the CURRENT computed revision
	// (epic audit round-1 F001) and the independent policy digest
	// (E9-T3, L-18): load the configuration and compute both; a route
	// not found in the configuration keeps the quarantined decision's
	// own revisions.
	routeRevision, policyRevision := "", ""
	if cfg, cerr := config.Load(resolveConfigPath(flags.val("--config"))); cerr == nil {
		var routeID string
		if qerr := closer.QueryRowContext(requestCtx(), `SELECT p.route_id FROM policy_decisions p
			JOIN quarantine_items q ON q.decision_id = p.decision_id WHERE q.quarantine_id = ?`, flags.positional).Scan(&routeID); qerr == nil {
			if rev, ok := config.RouteRevision(cfg, routeID); ok {
				routeRevision = rev
			}
			if route, ok := cfg.Routes[routeID]; ok {
				policyRevision = config.PolicyRevision(route)
			}
		}
	}
	service := &quarantine.Service{Store: store, Now: func() string { return dispatch.Timestamp(time.Now()) }, RouteRevision: routeRevision, PolicyRevision: policyRevision}
	var (
		rec ports.QuarantineRecord
		err error
	)
	if sub == "release" {
		rec, err = service.Release(requestCtx(), flags.positional, "operator", reason)
	} else {
		rec, err = service.Discard(requestCtx(), flags.positional, "operator", reason)
	}
	if err != nil {
		return quarantineErr(stderr, command, sub, err)
	}
	return writeEnvelope(stdout, command, rec)
}

func quarantineErr(stderr io.Writer, command, sub string, err error) int {
	switch {
	case errors.Is(err, ports.ErrQuarantineNotFound):
		writeError(stderr, command, "quarantine_not_found", "input_rejected", err.Error())
		return 4
	case errors.Is(err, ports.ErrQuarantineNotHeld):
		if sub == "release" {
			writeError(stderr, command, "quarantine_release_denied", "conflict", err.Error())
			return 14
		}
		writeError(stderr, command, "transition_invalid", "conflict", err.Error())
		return 14
	case errors.Is(err, ports.ErrReasonRequired):
		writeError(stderr, command, "flag_invalid", "usage", err.Error())
		return 2
	default:
		// Typed classification: store surfaces are storage; anything
		// else is an internal-class defect, never a storage relabel
		// (E5 audit).
		var storeErr *ports.StoreError
		if errors.As(err, &storeErr) {
			writeError(stderr, command, "sqlite_query_failed", "storage", storeErr.Err.Error())
			return 20
		}
		writeError(stderr, command, "internal_unclassified", "internal", err.Error())
		return 40
	}
}

// maxScheduledDrainIntents bounds the due-work drain the scheduled
// --submit path performs after its own reconciliation intent.
const maxScheduledDrainIntents = 100

// runReconcile implements `reconcile --route <id> --reason ... [--submit]`
// (cli-spec §9, OPS-006): full-scope enumeration, path-fact comparison,
// and the single pending reconciliation generation.
func runReconcile(args []string, stdout, stderr io.Writer) int {
	command := "reconcile"
	// --submit is reconcile-only: it is stripped here so the shared
	// parser rejects it on every other command. --baseline-only is the
	// same (E14-T2, CLI-017): the disabled-route baseline operation.
	submit := false
	baselineOnly := false
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--submit" {
			submit = true
			continue
		}
		if arg == "--baseline-only" {
			baselineOnly = true
			continue
		}
		filtered = append(filtered, arg)
	}
	if submit && baselineOnly {
		return usageError(stderr, command, "--baseline-only has no submit path and cannot be combined with --submit")
	}
	flags, code := parseDispatchesFlags(command, filtered, stderr, nil)
	if code != 0 {
		return code
	}
	routeID := flags.val("--route")
	reason := flags.val("--reason")
	if routeID == "" {
		return usageError(stderr, command, "reconcile requires --route")
	}
	if !reconcile.Reasons[reason] {
		return usageError(stderr, command, "--reason must be one of initial, scheduled, overflow, fresh-instance, lost-cursor, manual, delivery, stale-active, or startup")
	}
	artifacts, exit := planConfigOnly(command, flags.val("--config"), routeID, stderr)
	if exit != 0 {
		return exit
	}
	defer artifacts.close()
	store, closer, storeExit := openOperatorStore(command, flags.val("--config"), stderr)
	if storeExit != 0 {
		return storeExit
	}
	defer closer.Close()
	// The reconciliation decision records the independent policy digest
	// of the live route (E9-T3, L-18), not the route revision echo.
	policyRev := ""
	if route, ok := artifacts.cfg.Routes[routeID]; ok {
		policyRev = config.PolicyRevision(route)
	}
	// The disabled-route baseline operation (E14-T2, ADR-0020, CLI-017):
	// one observation-fenced snapshot-and-baseline transaction with no
	// submit path, guarded on both halves of the production gate.
	if baselineOnly {
		canonical := filepath.Clean(artifacts.resource.Root)
		if resolved, serr := filepath.EvalSymlinks(canonical); serr == nil {
			canonical = resolved
		}
		gitMode := "disabled"
		if artifacts.resource.Git != nil {
			gitMode = artifacts.resource.Git.Mode
		}
		baselineService := &reconcile.BaselineService{
			Store: closer, Resolver: artifacts.resolver, Engine: artifacts.engine,
			FileScope: artifacts.fileScope, MaxHash: artifacts.maxHash,
			ResourceID: artifacts.resourceID, RouteID: routeID,
			RouteRevision: artifacts.revision, PolicyRevision: policyRev,
			ConfigEnabled: artifacts.route.Enabled,
			// The clean-host materialization mirrors registerRouteState's
			// trusted values; an existing resource row is never rewritten.
			ResourceRegistration: &ports.ResourceRegistrationInput{
				ResourceID: artifacts.resourceID, Revision: artifacts.revision,
				Root: artifacts.resource.Root, CanonicalRoot: canonical,
				FileScope: artifacts.resource.FileScope, GitMode: gitMode,
			},
			Now: time.Now,
		}
		baseline, berr := baselineService.Run(requestCtx(), routeID, reason)
		if berr != nil {
			return reconcileErr(stderr, command, berr)
		}
		var warnings []string
		if baseline.ConcurrentChange {
			warnings = append(warnings, "the baseline was not stored: a newer durable path-fact mutation landed inside the enumeration window; the newer facts stand and a rerun converges")
		}
		return writeEnvelopeWithWarnings(stdout, command, baseline, warnings)
	}
	service := &reconcile.FullService{
		Store: closer, Resolver: artifacts.resolver, Engine: artifacts.engine,
		ResourceID: artifacts.resourceID, FileScope: artifacts.fileScope,
		RouteRevision: artifacts.revision, PolicyRevision: policyRev,
		MaxHash: artifacts.maxHash, Now: time.Now,
		// The builder commits the reconcile fan-out's sibling children
		// DURABLY FIRST through the app-layer loop (E12 epic whole-review
		// round 1): CLI wiring, app-layer policy.
		IntentBuilder: artifacts.reconcileIntentBuilder(closer),
	}
	result, err := service.Run(requestCtx(), routeID, reason)
	if err != nil {
		return reconcileErrWithSiblings(stderr, command, err, artifacts)
	}
	// The reconcile fan-out's sibling outcomes (committed inside the
	// builder, before the first lane's transaction): a slot-held skip is a
	// warning on stderr and in the envelope; a non-slot-held failure keeps
	// nothing silent — the reconcile still delivers its first lane, but the
	// occurrence is not fully durable and the command exits non-zero (E12
	// epic whole-review round 1).
	laneEntries, laneWarnings, laneFailure := artifacts.reconcileLaneOutcomes()
	for _, warning := range laneWarnings {
		fmt.Fprintf(stderr, "warning: %s\n", warning)
	}
	// siblingFailureOverlay writes the stable error line for a failed
	// sibling commit and returns the mapped non-zero exit (the doctor
	// posture: the result envelope ships on stdout, ok:false rides stderr).
	siblingFailureOverlay := func() int {
		if laneFailure == nil {
			return 0
		}
		code, category, exit := classifyReconcileError(laneFailure.Err)
		writeError(stderr, command, code, category,
			fmt.Sprintf("the reconcile fan-out sibling on destination lane %s did not commit its child: %s",
				laneFailure.DestinationID, dispatch.BoundLaneError(laneFailure.Err.Error())))
		return exit
	}
	if !submit {
		// The result renders exactly as before when no siblings exist (the
		// single-lane shape); the fan-out adds the per-lane listing beside
		// the result's own members, never rewrapping them.
		merged, merr := flatResultWithLanes(result, laneEntries)
		if merr != nil {
			return planErr(stderr, command, "internal_unclassified", "internal", merr.Error(), 40)
		}
		if code := writeEnvelopeWithWarnings(stdout, command, merged, laneWarnings); code != 0 {
			return code
		}
		return siblingFailureOverlay()
	}
	// --submit drives the scheduled delivery path (OPS-007): the
	// reconciliation's own intent when one was created, then the route's
	// other due work so a pending follow-up generation reaches the target
	// without a manual drain (CON-003, E7-T2/B-3). The automatic-write
	// gate still applies: a route that is not enabled submits nothing.
	rt, sink, backoff, rtErr := artifacts.submitRuntime(store)
	if rtErr != nil {
		writeError(stderr, command, "config_invalid", "configuration", rtErr.Error())
		return 3
	}
	// Expired submitting leases are recovered before unknown
	// reconciliation at the head of every submit entry point (DUR-010,
	// E8-T2/H-6): the scheduled path heals a process that died mid-submit
	// without a manual drain, exactly like the trigger and drain paths.
	// The sweep deliberately runs before the activation and enabled gates:
	// lease recovery and DUR-006 resolution are ungated maintenance
	// actions (the drain's disabled branch documents the same posture),
	// never automatic submission.
	if _, err := rt.Recover(requestCtx(), routeID); err != nil {
		return intentErr(stderr, command, err)
	}
	if _, _, err := reconcileUnknownDispatches(artifacts.cfg, store, sink, routeID, backoff); err != nil {
		return intentErr(stderr, command, err)
	}
	if rs, rsErr := store.LoadRouteState(requestCtx(), routeID); rsErr != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", rsErr.Error())
		return 20
	} else if rs.ActivationState != "enabled" {
		warnings := append([]string{fmt.Sprintf("--submit skipped: route %s activation state is %q, not enabled", routeID, rs.ActivationState)}, laneWarnings...)
		merged, merr := flatResultWithLanes(result, laneEntries)
		if merr != nil {
			return planErr(stderr, command, "internal_unclassified", "internal", merr.Error(), 40)
		}
		if code := writeEnvelopeWithWarnings(stdout, command, merged, warnings); code != 0 {
			return code
		}
		return siblingFailureOverlay()
	}
	// The YAML-key half of the two-key gate (E7-T6/M-2, epic audit
	// round 1): the scheduled path refuses automatic submission while
	// the configuration key is off, exactly like drain and dispatch.
	if route, ok := artifacts.cfg.Routes[routeID]; ok && !route.Enabled {
		warnings := append([]string{fmt.Sprintf("--submit skipped: route %q is disabled in configuration (routes.%s.enabled: false); nothing was submitted", routeID, routeID)}, laneWarnings...)
		merged, merr := flatResultWithLanes(result, laneEntries)
		if merr != nil {
			return planErr(stderr, command, "internal_unclassified", "internal", merr.Error(), 40)
		}
		if code := writeEnvelopeWithWarnings(stdout, command, merged, warnings); code != 0 {
			return code
		}
		return siblingFailureOverlay()
	}
	envelope := map[string]any{"result": result, "submitted": false}
	if len(laneEntries) > 0 {
		envelope["reconcile_lanes"] = laneEntries
	}
	if result.ReconcileDispatch != "" {
		report, err := rt.SubmitOnce(requestCtx(), result.ReconcileDispatch, "agent-dispatch-reconcile")
		if err != nil {
			return intentErr(stderr, command, err)
		}
		envelope["submitted"] = true
		envelope["submitted_state"] = string(report.To)
	}
	drained, err := rt.Drain(requestCtx(), routeID, maxScheduledDrainIntents, store)
	if err != nil {
		// The reconciliation and its own submission (when one ran) are
		// already committed; surface them inside the single failure
		// envelope so the operator sees what changed (round-1 review).
		if result.ReconcileDispatch != "" {
			err = fmt.Errorf("%w (note: the reconciliation intent %s was already submitted before this failure)", err, result.ReconcileDispatch)
		}
		return intentErr(stderr, command, err)
	}
	envelope["drained"] = map[string]any{"processed": drained.Processed, "skipped": drained.Skipped}
	if code := writeEnvelopeWithWarnings(stdout, command, envelope, laneWarnings); code != 0 {
		return code
	}
	return siblingFailureOverlay()
}

// flatResultWithLanes renders the reconcile result envelope the no-submit
// and skipped-submit paths always used — the FullResult's own members at
// the TOP level (e9t4/e5t4 pin that shape) — with the sibling lane
// listing merged in BESIDE them when one exists (E12 epic whole-review
// round 1), never rewrapping the result.
func flatResultWithLanes(result reconcile.FullResult, laneEntries []map[string]any) (map[string]any, error) {
	if len(laneEntries) == 0 {
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		merged := map[string]any{}
		if err := json.Unmarshal(raw, &merged); err != nil {
			return nil, err
		}
		return merged, nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	merged := map[string]any{"reconcile_lanes": laneEntries}
	if err := json.Unmarshal(raw, &merged); err != nil {
		return nil, err
	}
	return merged, nil
}

// classifyReconcileError maps one reconciliation-path failure onto the
// stable envelope classes and exit codes (error-model): every lost-race
// arm — the eligibility refusal, a generation/pending fence conflict, or a
// slot held by the resolution's own reservation — is a state conflict
// (14); an invalid fanout record is a NON-RETRYABLE validation failure of
// the invalid-configuration family (3 — the data is wrong, not racy, E12
// epic whole-review round 2); typed store failures are storage (20);
// anything else from the service (enumeration, bugs) is an internal-class
// defect, never a silent storage relabel. The sibling exit-code policy
// routes through the same classes (E12 epic whole-review round 1).
func classifyReconcileError(err error) (code, category string, exit int) {
	switch {
	case errors.Is(err, ports.ErrStateNotEligible),
		errors.Is(err, ports.ErrGenerationConflict),
		errors.Is(err, ports.ErrRouteSlotHeld),
		errors.Is(err, ports.ErrIdempotencyConflict):
		return "transition_invalid", "conflict", 14
	case errors.Is(err, ports.ErrInvalidFanoutRecord):
		return "config_invalid", "configuration", 3
	default:
		var storeErr *ports.StoreError
		if errors.As(err, &storeErr) {
			return "sqlite_query_failed", "storage", 20
		}
		return "internal_unclassified", "internal", 40
	}
}

// reconcileErr writes the classified error envelope and returns its exit.
func reconcileErr(stderr io.Writer, command string, err error) int {
	code, category, exit := classifyReconcileError(err)
	writeError(stderr, command, code, category, err.Error())
	return exit
}

// reconcileErrWithSiblings renders one reconciliation failure WITHOUT
// dropping the fan-out's sibling outcomes (E12 epic whole-review round 2):
// the durable-first order commits the siblings BEFORE the first lane's
// transaction, so a first-lane failure can leave COMMITTED siblings — and
// a sibling may itself have failed — and both must reach the operator
// beside the error, never silently. Slot-held skips warn on stderr, each
// committed sibling gets its stderr note, and the lane listing rides the
// error envelope's result slot.
func reconcileErrWithSiblings(stderr io.Writer, command string, err error, artifacts *reconcileArtifacts) int {
	entries, warnings, _ := artifacts.reconcileLaneOutcomes()
	for _, warning := range warnings {
		fmt.Fprintf(stderr, "warning: %s\n", warning)
	}
	code, category, exit := classifyReconcileError(err)
	if len(entries) == 0 {
		writeError(stderr, command, code, category, err.Error())
		return exit
	}
	for _, entry := range entries {
		if committed, _ := entry["committed"].(bool); committed {
			fmt.Fprintf(stderr, "note: the reconcile sibling lane %v committed its child (dispatch %v) before this failure\n",
				entry["destination_id"], entry["dispatch_id"])
		}
	}
	writeErrorWithResult(stderr, command, code, category, err.Error(),
		map[string]any{"reconcile_lanes": entries})
	return exit
}

// wrapQuarantineReadError applies the typed wrap for the read path: the
// shared domain-outcome classification passes through, everything else
// is a durable-store failure, never an internal defect.
func wrapQuarantineReadError(err error) error {
	if ports.IsQuarantineDomainOutcome(err) {
		return err
	}
	return ports.WrapStore(err)
}
