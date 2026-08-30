package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeswebhook"
	"github.com/irootkernel/agent-dispatch/internal/adapters/secretresolver"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/app/doctor"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/observability"
)

// runStatus implements `agent-dispatch status` (cli-spec §10): route
// active/dirty state, queue counts, unresolved delivery, quarantine,
// last reconciliation, and the target capability summary
// (observability-and-operations §5 counters).
func runStatus(args []string, stdout, stderr io.Writer) int {
	command := "status"
	flags := parseOpsFlags(command, args, stderr, map[string]bool{"--config": true})
	if flags == nil {
		return 2
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	ctx := requestCtx()

	routes, err := store.ListRoutes(ctx)
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	intents, err := store.CountIntentsByState(ctx)
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	quarantine, err := store.CountQuarantineByState(ctx)
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	oldestUnresolved, err := store.OldestUnresolved(ctx)
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	dbBytes, err := store.DatabaseBytes()
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	// Notification delivery posture (E13-T2, observability-and-operations
	// §10): the by-state counts project the outbox; pending delivery
	// work is the operator-visible retry surface.
	notificationCounts, err := closer.CountNotificationsByState(ctx)
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	if notificationCounts == nil {
		notificationCounts = map[string]int64{}
	}

	routeRows := make([]map[string]any, 0, len(routes))
	warnings := []string{}
	for _, r := range routes {
		row := map[string]any{
			"route_id":           r.RouteID,
			"activation_state":   r.ActivationState,
			"route_state":        r.RouteState,
			"dirty_generation":   r.DirtyGeneration,
			"pending_reconcile":  r.PendingReconcile != 0,
			"last_reconciled_at": nilIfEmpty(r.LastReconciledAt),
		}
		if r.ActiveDispatchID != "" {
			row["active_dispatch_id"] = r.ActiveDispatchID
		}
		// Per-destination lane summaries (E12-T3, OPS-011): one bounded
		// row per lane — destination id, lane state, active child, dirty
		// generation.
		lanes, laneErr := closer.ListRouteLanes(ctx, r.RouteID)
		if laneErr != nil {
			return planErr(stderr, command, "sqlite_query_failed", "storage", laneErr.Error(), 20)
		}
		row["lanes"] = lanes
		if r.RouteState == "ACTIVE_DIRTY" || r.DirtyGeneration > 0 {
			warnings = append(warnings, fmt.Sprintf("route %s is dirty (generation %d): vault changes await the next dispatch", r.RouteID, r.DirtyGeneration))
		}
		if r.PendingReconcile != 0 {
			warnings = append(warnings, fmt.Sprintf("route %s has a pending reconciliation generation", r.RouteID))
		}
		routeRows = append(routeRows, row)
	}
	for _, state := range []string{"unknown", "dead_lettered"} {
		if intents[state] > 0 {
			warnings = append(warnings, fmt.Sprintf("%d dispatch intents are %s: delivery uncertainty requires operator resolution", intents[state], state))
		}
	}
	if quarantine["held"] > 0 {
		warnings = append(warnings, fmt.Sprintf("%d quarantine items are held", quarantine["held"]))
	}
	if notificationCounts["pending"] > 0 {
		warnings = append(warnings, fmt.Sprintf("%d notifications are pending delivery: run 'agent-dispatch notifications drain'", notificationCounts["pending"]))
	}
	if notificationCounts["refused"] > 0 {
		warnings = append(warnings, fmt.Sprintf("%d notifications were refused by their sink: inspect 'agent-dispatch notifications list --state refused' and retry explicitly after fixing the sink", notificationCounts["refused"]))
	}
	return writeEnvelopeWithWarnings(stdout, command, map[string]any{
		"routes":            routeRows,
		"queues":            intents,
		"quarantine":        quarantine,
		"oldest_unresolved": nilIfEmpty(oldestUnresolved),
		"database_bytes":    dbBytes,
		"targets":           targetCapabilitySummary(cfg),
		"notifications":     notificationCounts,
		// OPS-013: the five drift classes — capability, profile, skill,
		// watchman, reconciliation — surfaced per route; four of them
		// notify through the drain's drift evaluation and reconciliation
		// through the pending-reconcile transitions of E13-T1.
		"drift": routeDriftSummary(ctx, cfg, closer),
	}, warnings)
}

// targetCapabilitySummary reports the offline capability declaration
// per target — no process execution, no endpoint I/O: the static webhook
// declaration and, for hermes targets, the probed-compatibility contract
// with its frozen-interface unconditional capability set (E11-T1; the
// E11-T2 probe cache will replace the constant with recorded evidence).
func targetCapabilitySummary(cfg *config.Config) []map[string]any {
	out := make([]map[string]any, 0, len(cfg.Targets)+len(cfg.HermesTargets))
	for _, id := range sortedTargetIDs(cfg) {
		target := cfg.Targets[id]
		entry := map[string]any{"target_id": id, "type": target.Type}
		if target.Type == "hermes-webhook" {
			if opts, err := webhookSinkOptions(id, target); err == nil {
				if sink, err := hermeswebhook.NewSink(opts); err == nil {
					if caps, err := sink.Probe(context.Background()); err == nil {
						entry["capabilities"] = caps.BoolMap()
					}
				}
			}
		}
		out = append(out, entry)
	}
	for _, id := range sortedHermesTargetIDs(cfg) {
		target := cfg.HermesTargets[id]
		minimum := target.MinimumVersion
		if minimum == "" {
			minimum = config.MinimumEligibleHermesVersion
		}
		caps := map[string]bool{}
		for _, name := range hermeskanban.UnconditionalCapabilities {
			caps[name] = true
		}
		out = append(out, map[string]any{
			"target_id": id, "type": "hermes-kanban",
			"minimum_version": minimum,
			"compatibility":   target.Compatibility,
			"capabilities":    caps,
		})
	}
	return out
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// runDoctor implements `agent-dispatch doctor [--probe-targets]
// [--integrity full]` (cli-spec §10, OPS-005): one findings examination
// over configuration, store health, route runtime state, and the
// external integrations. The actionable findings are the stdout result;
// when any finding has error severity the command also emits the
// stable doctor_findings_present code and exits 3 (AC-502), with the
// doctor.finding log events carrying the per-finding severities.
func runDoctor(args []string, stdout, stderr io.Writer) int {
	command := "doctor"
	flags := parseOpsFlags(command, args, stderr, map[string]bool{"--config": true, "--integrity": true})
	if flags == nil {
		return 2
	}
	probeTargets := flags.val("--probe-targets") == "true"
	integrityFull := flags.val("--integrity") == "full"
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		// The store, integrations, and targets were never examined: a
		// configuration that fails to load produces exactly the
		// configuration finding set, nothing fabricated — and the
		// unexamined Watchman fact is explicitly marked so its
		// unavailable posture is suppressed rather than invented
		// (E8-T4 round-1 F002).
		findings := doctor.Examine(doctor.Input{
			SemanticErrors: []string{err.Error()},
			// WatchmanExamined stays false: the probe never ran, so the
			// unavailable finding must not fire (E8-T4 round-1 F002).
		})
		emitFindings(opsLogger(stderr, nil), findings)
		return writeDoctorResult(stdout, stderr, command, findings)
	}
	log := opsLogger(stderr, cfg)
	input := doctor.Input{ConfigPath: resolveConfigPath(flags.val("--config"))}
	if errs, _ := config.SemanticValidate(cfg); len(errs) > 0 {
		input.SemanticErrors = errorTexts(errs)
	}
	input.Resources = resourceFacts(cfg)
	input.Watchman = watchmanFact(requestCtx())
	// The probe ran: its unavailable posture is real evidence now.
	input.WatchmanExamined = true
	input.Targets = targetFacts(cfg)
	if probeTargets {
		// The probe surface already reports per-target construction
		// failures with stable codes; surface the ones the offline facts
		// did not already catch (no duplicate findings per target).
		gated := map[string]bool{}
		for _, f := range input.Targets {
			if f.GateError != "" {
				gated[f.TargetID] = true
			}
		}
		for _, w := range probeTargetWarnings(cfg) {
			if !gated[w.targetID] {
				input.Targets = append(input.Targets, doctor.TargetFact{TargetID: w.targetID, Type: w.targetType, GateError: w.detail})
			}
		}
	}

	// Doctor examines the store WITHOUT auto-migrating (OPS-005: the
	// migration state must be observable): a schema behind the build's
	// ledger surfaces as the migration_pending finding instead of
	// being silently advanced.
	store, storeErr := openUnmigratedStore(resolveConfigPath(flags.val("--config")))
	if storeErr != nil {
		input.Store = doctor.StoreFact{OpenError: storeErr.Error()}
		input.StoreExamined = true
	} else {
		defer store.Close()
		input.Store = storeFacts(requestCtx(), store, integrityFull)
		input.StoreExamined = true
		input.Routes = routeFacts(requestCtx(), cfg, store)
	}

	findings := doctor.Examine(input)
	emitFindings(log, findings)
	return writeDoctorResult(stdout, stderr, command, findings)
}

// writeDoctorResult emits the findings result and, when any finding has
// error severity, the stable nonzero code (AC-502). The registry maps
// doctor_findings_present to the configuration class; findings whose
// own classes are storage or migration carry that detail in their codes
// and remediation.
func writeDoctorResult(stdout, stderr io.Writer, command string, findings []doctor.Finding) int {
	findings = doctor.StampTrace(findings, globalTraceID)
	errors := 0
	for _, f := range findings {
		if f.Severity == doctor.SeverityError {
			errors++
		}
	}
	if errors > 0 {
		writeError(stderr, command, "doctor_findings_present", "configuration",
			fmt.Sprintf("%d error-severity finding(s): see the findings result for codes, summaries, and remediation", errors))
		writeEnvelope(stdout, command, map[string]any{"findings": findings, "findings_count": len(findings)})
		return 3
	}
	return writeEnvelope(stdout, command, map[string]any{"findings": findings, "findings_count": len(findings)})
}

// emitFindings logs every warning and error finding as a doctor.finding
// event (observability-and-operations §3-§4; findings themselves are
// data, not errors).
func emitFindings(log *observability.Logger, findings []doctor.Finding) {
	for _, f := range findings {
		level := observability.LevelInfo
		switch f.Severity {
		case doctor.SeverityWarning:
			level = observability.LevelWarn
		case doctor.SeverityError:
			level = observability.LevelError
		}
		log.Log(level, observability.EventDoctorFinding, observability.Correlation{}, f.Summary, map[string]any{
			"code": f.Code, "severity": string(f.Severity), "remediation": f.Remediation,
		})
	}
}

// resourceFacts stats every configured resource root (OPS-005:
// inaccessible roots; SEC-008 posture).
func resourceFacts(cfg *config.Config) []doctor.ResourceFact {
	ids := make([]string, 0, len(cfg.Resources))
	for id := range cfg.Resources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]doctor.ResourceFact, 0, len(ids))
	for _, id := range ids {
		root := cfg.Resources[id].Root
		fact := doctor.ResourceFact{ResourceID: id, Root: root}
		fi, err := os.Stat(root)
		if err != nil {
			fact.Missing = os.IsNotExist(err)
			fact.NotReadable = !fact.Missing
			out = append(out, fact)
			continue
		}
		// A real access probe (E8-T4, H-4): mode bits pass under
		// chmod 000 while the directory is actually unreadable, and
		// doctor must say so before the next reconcile fails.
		if d, derr := os.Open(root); derr == nil {
			if _, rerr := d.Readdirnames(1); rerr != nil {
				fact.NotReadable = true
			}
			d.Close()
		} else {
			fact.NotReadable = true
		}
		if fi.IsDir() && fi.Mode().Perm()&0o077 != 0 {
			fact.NotOwnerOnly = true
		}
		out = append(out, fact)
	}
	return out
}

// watchmanFact probes Watchman presence and version read-only.
func watchmanFact(ctx context.Context) doctor.WatchmanFact {
	client := watchman.NewClient("")
	version, err := client.Version(ctx)
	if err != nil {
		return doctor.WatchmanFact{UnusableBecause: err.Error()}
	}
	fact := doctor.WatchmanFact{Available: true, Version: version}
	if err := watchman.CheckVersionSupported(version); err != nil {
		fact.UnusableBecause = err.Error()
	}
	return fact
}

// targetFacts applies each target's offline construction gates and
// secret-reference resolvability without printing values (OPS-005).
func targetFacts(cfg *config.Config) []doctor.TargetFact {
	out := make([]doctor.TargetFact, 0, len(cfg.Targets)+len(cfg.HermesTargets))
	for _, id := range sortedTargetIDs(cfg) {
		target := cfg.Targets[id]
		fact := doctor.TargetFact{TargetID: id, Type: target.Type}
		switch target.Type {
		case "hermes-webhook":
			opts, err := webhookSinkOptions(id, target)
			if err != nil {
				fact.GateError = err.Error()
			} else if _, err := hermeswebhook.NewSink(opts); err != nil {
				fact.GateError = err.Error()
			}
			if target.Auth != nil {
				resolved := false
				if ref, err := config.ParseSecretRef(target.Auth.SecretRef); err == nil {
					if _, err := secretresolver.Resolve(context.Background(), ref); err == nil {
						resolved = true
					}
				}
				fact.SecretResolved = &resolved
			}
		}
		out = append(out, fact)
	}
	// Hermes targets contribute their facts independently of the webhook
	// map: a hermes-only configuration — the common post-cutover shape —
	// still reports board and executable gate errors. The offline
	// capability gate the doctor previously applied through the report
	// (E8-T4, H-4) is subsumed by the cutover contract: hermes capability
	// truth is the frozen-interface unconditional set until the E11-T2
	// probe records per-executable evidence.
	for _, id := range sortedHermesTargetIDs(cfg) {
		target := cfg.HermesTargets[id]
		fact := doctor.TargetFact{TargetID: id, Type: "hermes-kanban"}
		if target.Board == "" {
			fact.GateError = "board is not configured"
		} else if target.Executable == "" {
			fact.GateError = "executable is not configured"
		} else if !executablePresent(target.Executable) {
			fact.GateError = fmt.Sprintf("executable %q is not present", target.Executable)
		}
		out = append(out, fact)
	}
	return out
}

// executablePresent resolves a PATH-relative executable name through
// exec.LookPath and an absolute path through stat (E8-T4: the doctor
// gate previously failed every PATH-named executable).
func executablePresent(name string) bool {
	if filepath.IsAbs(name) {
		_, err := os.Stat(name)
		return err == nil
	}
	_, err := exec.LookPath(name)
	return err == nil
}

// probeTargetWarnings describes per-target probe failures surfaced by
// --probe-targets (reuse of the config validate probe path).
type probeTargetWarning struct {
	targetID   string
	targetType string
	detail     string
}

func probeTargetWarnings(cfg *config.Config) []probeTargetWarning {
	var out []probeTargetWarning
	for _, id := range sortedTargetIDs(cfg) {
		target := cfg.Targets[id]
		if target.Type != "hermes-webhook" {
			continue
		}
		if opts, err := webhookSinkOptions(id, target); err != nil {
			out = append(out, probeTargetWarning{id, target.Type, err.Error()})
		} else if _, err := hermeswebhook.NewSink(opts); err != nil {
			out = append(out, probeTargetWarning{id, target.Type, err.Error()})
		}
	}
	for _, id := range sortedHermesTargetIDs(cfg) {
		target := cfg.HermesTargets[id]
		if limits, err := hermesTargetProcessLimits(cfg, target); err != nil {
			out = append(out, probeTargetWarning{id, "hermes-kanban", err.Error()})
		} else if _, err := hermeskanban.NewSink(id, target.Executable, target.MinimumVersion, target.Board, limits, DefaultMaxManifestBytes); err != nil {
			out = append(out, probeTargetWarning{id, "hermes-kanban", err.Error()})
		}
	}
	return out
}

// storeFacts collects the durable store's health (OPS-005, OPS-008).
func storeFacts(ctx context.Context, store *sqlite.Store, integrityFull bool) doctor.StoreFact {
	fact := doctor.StoreFact{}
	var mode string
	if err := store.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err == nil {
		fact.JournalMode = mode
	}
	if v, err := store.SchemaVersion(); err == nil {
		fact.SchemaVersion = v
	}
	fact.LatestVersion = store.LatestSchemaVersion()
	if err := store.IntegrityCheck(integrityFull); err != nil {
		fact.IntegrityError = err.Error()
	}
	now := dispatch.Timestamp(time.Now())
	if leases, err := store.StaleLeases(ctx, now); err == nil {
		fact.StaleLeases = leases
	}
	if intents, err := store.CountIntentsByState(ctx); err == nil {
		fact.UnknownCount = intents["unknown"]
		fact.DeadLettered = intents["dead_lettered"]
	}
	if size, err := store.DatabaseBytes(); err == nil {
		fact.DatabaseBytes = size
	}
	return fact
}

// routeFacts collects each route's runtime facts including the stale
// active and overdue reconciliation inputs.
func routeFacts(ctx context.Context, cfg *config.Config, store *sqlite.Store) []doctor.RouteFact {
	rows, err := store.ListRoutes(ctx)
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	out := make([]doctor.RouteFact, 0, len(rows))
	for _, r := range rows {
		fact := doctor.RouteFact{
			RouteID:          r.RouteID,
			ResourceID:       r.ResourceID,
			TargetID:         r.TargetID,
			ActivationState:  r.ActivationState,
			RouteState:       r.RouteState,
			ActiveDispatchID: r.ActiveDispatchID,
			DirtyGeneration:  int64(r.DirtyGeneration),
			PendingReconcile: r.PendingReconcile != 0,
			LastReconciledAt: r.LastReconciledAt,
		}
		if route, ok := cfg.Routes[r.RouteID]; ok {
			fact.DailyExpected = route.Reconciliation.DailyExpected
			if d, err := config.ParseDuration(route.ActiveStaleAfter); err == nil {
				fact.StaleActiveAfter = time.Duration(d.Nanos)
			}
		}
		if r.LastReconciledAt != "" {
			if last, err := time.Parse(time.RFC3339, r.LastReconciledAt); err == nil && last.Before(now) {
				fact.ReconciledAge = now.Sub(last)
			}
		}
		if r.ActiveDispatchID != "" {
			// Age from the newest provable activity: the last completed
			// attempt, falling back to the expired lease boundary.
			var anchor sql.NullString
			if err := store.QueryRowContext(ctx, `SELECT MAX(completed_at) FROM dispatch_attempts WHERE dispatch_id = ?`, r.ActiveDispatchID).Scan(&anchor); err == nil && anchor.Valid && anchor.String != "" {
				if at, err := time.Parse(time.RFC3339, anchor.String); err == nil && at.Before(now) {
					fact.ActiveDispatchAge = now.Sub(at)
				}
			} else if snap, err := store.LoadIntent(ctx, r.ActiveDispatchID); err == nil && snap.LeaseExpiresAt != "" {
				if expires, err := time.Parse(time.RFC3339, snap.LeaseExpiresAt); err == nil && expires.Before(now) {
					fact.ActiveDispatchAge = now.Sub(expires)
				}
			}
		}
		out = append(out, fact)
	}
	return out
}

func errorTexts(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

// opsLogger builds the command's operational logger: stderr JSON lines
// under the configured path policy (OPS-001, SEC-007).
func opsLogger(stderr io.Writer, cfg *config.Config) *observability.Logger {
	policy := observability.PathsRelative
	if cfg != nil && cfg.Instance.LogPaths != "" {
		if p, err := observability.ParsePathPolicy(cfg.Instance.LogPaths); err == nil {
			policy = p
		}
	}
	return observability.New(stderr, globalLogLevel, policy)
}
