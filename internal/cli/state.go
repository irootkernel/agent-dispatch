package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"path/filepath"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeswebhook"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/observability"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// StateDBName is the durable database file inside the state directory.
const StateDBName = "state.db"

// errConfigurationClass marks store-opening failures caused by the
// configuration document (the typed class behind the exit-3 mapping).
var errConfigurationClass = errors.New("configuration")

// openStateStore resolves the state directory with the shared precedence
// (config instance.state_dir, then AGENT_DISPATCH_STATE_DIR, then the platform
// default), opens the SQLite store, and applies pending migrations.
// globalStateDir is the explicit --state-dir override (cli-spec §1),
// taking precedence over the configured and environment values.
var globalStateDir string

func resolveStateDirOverride(configured string) string {
	if globalStateDir != "" {
		return globalStateDir
	}
	return platformpaths.ResolveStateDir(configured)
}

func openStateStore(configPath string) (*sqlite.Store, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errConfigurationClass, err)
	}
	stateDir := resolveStateDirOverride(cfg.Instance.StateDir)
	s, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		return nil, err
	}
	if err := s.Migrate(filepath.Join(stateDir, "backups")); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// openUnmigratedStore opens the SQLite store without applying
// migrations — the doctor examination path, where the schema version
// must be observed as it stands (OPS-005) rather than advanced.
func openUnmigratedStore(configPath string) (*sqlite.Store, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errConfigurationClass, err)
	}
	stateDir := resolveStateDirOverride(cfg.Instance.StateDir)
	return sqlite.Open(filepath.Join(stateDir, StateDBName))
}

// storeOp is one durable dispatch store with its full E3-T3 surface.
type storeOp interface {
	ports.DispatchStore
	ports.InspectionStore
	ports.OperatorStore
	ports.ReconcileStore
	ports.ReceiptStore
	ports.WorkReceiptStore
	ports.QuarantineStore
	// ReleaseQuarantineWithRevision records the caller-computed current
	// revision and policy digest into the replacement decision (epic
	// audit round-1 F001; E9-T3, L-18).
	ReleaseQuarantineWithRevision(ctx context.Context, quarantineID, actor, reason, routeRevision, policyRevision, now string) (ports.QuarantineRecord, error)
	io.Closer
	ListRoutes(ctx context.Context) ([]sqlite.RouteRow, error)
	CountIntentsByState(ctx context.Context) (map[string]int64, error)
	CountQuarantineByState(ctx context.Context) (map[string]int64, error)
	OldestUnresolved(ctx context.Context) (string, error)
	StaleLeases(ctx context.Context, now string) ([]string, error)
	PlanPrune(ctx context.Context, cutoffs sqlite.PruneCutoffs) (sqlite.PrunePlan, error)
	ExecutePrune(ctx context.Context, cutoffs sqlite.PruneCutoffs, actor, reason, now string) (sqlite.PruneCounts, error)
	HasActiveWork(ctx context.Context, now string) (bool, error)
	Vacuum(ctx context.Context) error
	DatabaseBytes() (int64, error)
	IntegrityCheck(full bool) error
	SchemaVersion() (int, error)
	LatestSchemaVersion() int
	Backup(path string) error
	SetRouteActivation(ctx context.Context, routeID, activation, acknowledgeRevision, capabilityFingerprint, now string) error
	LoadRouteState(ctx context.Context, routeID string) (state.RouteSnapshot, error)
	LoadLaneState(ctx context.Context, routeID, destinationID string) (state.RouteSnapshot, error)
	LoadIntentLane(ctx context.Context, dispatchID string) (state.RouteSnapshot, error)
	CommitMergePending(ctx context.Context, lin ports.Lineage, mergeDestinations, selectedDestinations []string, actor, now string) (int, error)
	MergeSelectedLanes(ctx context.Context, routeID, batchID string, mergeDestinations, selectedDestinations []string, actor, now string) (int, error)
	CommitFanoutChild(ctx context.Context, intent ports.IntentInput) error
	CompleteActive(ctx context.Context, req ports.ActiveCompletion) (ports.FollowupCreated, error)
	ActivateDispatch(ctx context.Context, dispatchID, actor, now string) error
	ActivateFollowup(ctx context.Context, dispatchID, actor, now string) error
}

// openOperatorStore opens the store and narrows it to the operator
// surface, mapping open failures to their documented classes:
// configuration failures exit 3, migration failures (including a
// newer unsupported schema) exit 21, and storage failures exit 20.
func openOperatorStore(command string, configPath string, stderr io.Writer) (storeOp, *sqlite.Store, int) {
	s, err := openStateStore(resolveConfigPath(configPath))
	if err != nil {
		// The typed prefix from openStateStore distinguishes the
		// configuration class; the sentinel distinguishes a newer
		// database; everything else is storage.
		var tooNew *sqlite.ErrSchemaTooNewType
		if errors.Is(err, errConfigurationClass) {
			writeError(stderr, command, "config_invalid", "configuration", err.Error())
			return nil, nil, 3
		}
		if errors.As(err, &tooNew) {
			writeError(stderr, command, "migration_newer_schema", "migration", err.Error())
			return nil, nil, 21
		}
		// Concurrent first-open congestion (a WAL switch or migration
		// lock held by a peer) is retryable (E7-T7/M-5), never a fatal
		// open failure.
		var busy *ports.StoreError
		if errors.As(err, &busy) {
			writeError(stderr, command, "sqlite_busy", "transient_local", err.Error())
			return nil, nil, 10
		}
		writeError(stderr, command, "sqlite_open_failed", "storage", err.Error())
		return nil, nil, 20
	}
	return s, s, 0
}

// resolveRouteTarget resolves the shared target of a route's destinations
// (E11-T1, E12-T2 FAN-011): every destination must bind the SAME target —
// the one Hermes Kanban submission surface — and the first sorted
// destination returns as the deterministic representative for the
// single-lane surfaces (sink resolution, per-destination envelopes). An
// unresolvable target is a configuration defect.
func resolveRouteTarget(cfg *config.Config, routeID string) (config.Destination, config.ResolvedTarget, error) {
	route, ok := cfg.Routes[routeID]
	if !ok {
		return config.Destination{}, config.ResolvedTarget{}, fmt.Errorf("route %q is not defined", routeID)
	}
	dests := route.SortedDestinations()
	if len(dests) == 0 {
		return config.Destination{}, config.ResolvedTarget{}, fmt.Errorf("route %q declares no destinations", routeID)
	}
	for _, d := range dests[1:] {
		if d.Target != dests[0].Target {
			return config.Destination{}, config.ResolvedTarget{}, fmt.Errorf("route %q destinations %q and %q reference different targets %q and %q; a multi-destination route binds exactly one shared target (FAN-011)",
				routeID, dests[0].ID, d.ID, dests[0].Target, d.Target)
		}
	}
	dest := dests[0]
	resolved, ok := cfg.ResolveTarget(dest.Target)
	if !ok {
		return config.Destination{}, config.ResolvedTarget{}, fmt.Errorf("route %q destination %q references unknown target %q (declare it under hermes_targets or targets)", routeID, dest.ID, dest.Target)
	}
	return dest, resolved, nil
}

// certifiedLaneInfo is one destination's resolved lane identity with the
// canonical projection bytes its revision digests (E12-T2).
type certifiedLaneInfo struct {
	dest       config.Destination
	lane       ports.TaskDestinationRef
	projection string
}

// certifiedLanes resolves every destination lane of a route in sorted
// order (E12-T2, FAN-012): each destination's identity with its current
// destination revision and the canonical projection bytes that revision
// digests, so the fan-out builder can evaluate selection per destination
// and persist every referenced destination-revision record (DAT-010).
func certifiedLanes(cfg *config.Config, routeID string) ([]certifiedLaneInfo, error) {
	route, ok := cfg.Routes[routeID]
	if !ok {
		return nil, fmt.Errorf("route %q is not defined", routeID)
	}
	dests := route.SortedDestinations()
	if len(dests) == 0 {
		return nil, fmt.Errorf("route %q declares no destinations", routeID)
	}
	out := make([]certifiedLaneInfo, 0, len(dests))
	for _, dest := range dests {
		projection, err := config.DestinationProjectionJSON(cfg, dest)
		if err != nil {
			return nil, fmt.Errorf("destination %q projection: %v", dest.ID, err)
		}
		revision := config.DestinationRevision(cfg, route, dest)
		if revision == "" {
			return nil, fmt.Errorf("destination %q revision could not be computed", dest.ID)
		}
		out = append(out, certifiedLaneInfo{
			dest:       dest,
			lane:       ports.TaskDestinationRef{ID: dest.ID, Revision: revision, Workstream: dest.Workstream},
			projection: projection,
		})
	}
	return out, nil
}

// firstCertifiedLane resolves the canonically-first destination lane
// (E12-T2): legacy single-lane resolution — derived work whose own lane
// cannot be recovered — deterministically picks the first sorted
// destination.
func firstCertifiedLane(cfg *config.Config, routeID string) (ports.TaskDestinationRef, string, error) {
	lanes, err := certifiedLanes(cfg, routeID)
	if err != nil {
		return ports.TaskDestinationRef{}, "", err
	}
	return lanes[0].lane, lanes[0].projection, nil
}

// laneRevisionInput maps one certified lane and its projection bytes
// onto the durable destination-revision input persisted with a fanout
// (E12-T1).
func laneRevisionInput(lane ports.TaskDestinationRef, projection string) ports.DestinationRevisionInput {
	return ports.DestinationRevisionInput{DestinationID: lane.ID, Revision: lane.Revision, ProjectionJSON: projection}
}

// resolvedTargetScope returns the durable target scope an intent
// records: the kanban board slug for hermes targets and the endpoint URL
// for webhook targets — in both cases the identity of the interface
// that accepted the task, re-verified at reconciliation (migration v3).
func resolvedTargetScope(resolved config.ResolvedTarget) string {
	if resolved.Webhook != nil {
		return resolved.Webhook.Endpoint
	}
	if resolved.Hermes != nil {
		return resolved.Hermes.Board
	}
	return ""
}

// webhookSinkOptions maps one hermes-webhook target onto the adapter
// options; the submit timeout parses through the one schema-exact
// duration parser so validation and run time agree.
func webhookSinkOptions(targetID string, target config.Target) (hermeswebhook.Options, error) {
	opts := hermeswebhook.Options{
		TargetID:             targetID,
		Endpoint:             target.Endpoint,
		IdempotencyHeader:    target.IdempotencyHeader,
		RequiredCapabilities: target.RequiredCapabilities,
	}
	if target.Auth != nil {
		opts.AuthType = target.Auth.Type
		opts.SecretRef = target.Auth.SecretRef
		opts.AuthHeaderName = target.Auth.HeaderName
	}
	if target.SubmitTimeout != "" {
		d, err := config.ParseDuration(target.SubmitTimeout)
		if err != nil {
			// A schema-pattern-valid but unparseable duration (for example
			// an int64 overflow) is a configuration defect, not target
			// unavailability: it maps to the exit-3 class like every
			// other webhook construction gate.
			return opts, &hermeswebhook.ConfigError{Detail: fmt.Sprintf("submit_timeout: %v", err)}
		}
		opts.SubmitTimeout = time.Duration(d.Nanos)
	}
	return opts, nil
}

// webhookClientFactory builds the HTTP client for webhook sinks.
// Production always uses the adapter's strict client (system roots, one
// deadline, no redirects); it is a variable only so the CLI tests can
// substitute a client that trusts their loopback certificate authority
// without weakening the production transport.
var webhookClientFactory = func(timeout time.Duration) hermeswebhook.HTTPClient {
	return hermeswebhook.NewStrictClient(timeout)
}

// newSubmitRuntime assembles the one submission-runtime shape shared by
// the three submit surfaces (dispatch, drain, reconcile --submit): the
// attempt lease TTL derives from the target's submit timeout at every
// site (E8-T2/M-1; the shared constructor is the E9-T4 pin for the
// T2-F003/F004 wiring — a site cannot drift from the derivation
// without leaving it).
func newSubmitRuntime(store storeOp, sink ports.Sink, cfg *config.Config, submitTimeout string, backoff dispatch.Backoff, actor string, stderr io.Writer) *dispatch.Runtime {
	return &dispatch.Runtime{
		Store: store, Sink: sink, Now: time.Now,
		LeaseTTL: leaseTTLFor(submitTimeout), Backoff: backoff, JitterUnit: jitterUnit, Actor: actor,
		Log: opsLogger(stderr, cfg), TraceID: globalTraceID,
		StalenessCheck: stalenessCheckOf(cfg), StaleRebuilder: staleRebuilderOf(store, cfg),
	}
}

// submitTimeoutOf resolves the effective submit timeout of one resolved
// target across the two target maps.
func submitTimeoutOf(resolved config.ResolvedTarget) string {
	if resolved.Hermes != nil {
		return resolved.Hermes.SubmitTimeout
	}
	if resolved.Webhook != nil {
		return resolved.Webhook.SubmitTimeout
	}
	return ""
}

// resolveSink looks up and gates the sink adapter for one route target
// (E4-T3, E6-T1, E11). The hermes sink is constructed from the operator
// configuration, the read-only Probe gates the installed Hermes against
// the declared eligibility floor, the activation-bound capability
// fingerprint is bound for the submit-path re-proof, and the webhook
// sink applies the same fail-closed gates against its static,
// evidence-tied capability declaration — all before any submission
// (HER-002, HER-011, HER-018). No automatic fallback to any other
// target exists (DUR-008). The log (with the command's trace id) is
// attached so submission-time render
// decisions such as mutex suppression are operator-visible (E9-T3,
// T3-F007).
func resolveSink(cfg *config.Config, routeID string, log *observability.Logger) (ports.Sink, error) {
	dest, resolved, rerr := resolveRouteTarget(cfg, routeID)
	if rerr != nil {
		return nil, rerr
	}
	if resolved.Hermes != nil {
		t := resolved.Hermes
		limits, err := hermesTargetProcessLimits(cfg, *t)
		if err != nil {
			return nil, fmt.Errorf("hermes_targets.%s: %w", dest.Target, err)
		}
		sink, err := hermeskanban.NewSink(dest.Target, t.Executable, t.MinimumVersion, t.Board, limits, int64(cfg.Routes[routeID].Batching.MaxManifestBytes))
		if err != nil {
			return nil, err
		}
		sink.Log, sink.TraceID = log, globalTraceID
		// E11-T2/E8-T3 (M-6): the fresh per-executable capability record
		// owns the resource-mutex posture — a probed create surface
		// missing --mutex-key downgrades resource_mutex and the renderer
		// must suppress the key for a target that cannot honor it. A
		// missing or stale record keeps the frozen-interface default; the
		// submit path's fingerprint re-proof still blocks a changed
		// executable before any side effect.
		if record, rerr := hermeskanban.LoadCapabilityRecord(capabilityCachePath(dest.Target)); rerr == nil {
			if digest, derr := hermeskanban.ExecutableDigest(t.Executable); derr == nil && record.StaleReasonForProfile(t.Executable, digest, "", "") == "" {
				sink.SetResourceMutexSupported(record.CapabilitiesIncludeMutex())
			}
		}
		// HER-018: bind the activation-accepted capability fingerprint
		// when the route is enabled, so the submit path re-proves the
		// live executable identity before any side effect. A store that
		// cannot be opened here leaves the eligibility-only gate; the
		// staleness machinery re-asserts the acknowledgement anyway.
		stateDir := resolveStateDirOverride(cfg.Instance.StateDir)
		if store, serr := sqlite.Open(filepath.Join(stateDir, StateDBName)); serr == nil {
			if snap, lerr := store.LoadRouteState(context.Background(), routeID); lerr == nil && snap.CapabilityFingerprint != "" {
				sink.BindCapabilityFingerprint(snap.CapabilityFingerprint)
			} else if lerr != nil && log != nil {
				log.Warn(observability.EventCapabilityBindingUnavailable, observability.Correlation{
					TraceID: globalTraceID, RouteID: routeID, TargetID: dest.Target,
				}, "the activation-bound capability fingerprint could not be loaded; the submit path degrades to the eligibility-only gate until the route state is readable", nil)
			}
			store.Close()
		} else if log != nil {
			log.Warn(observability.EventCapabilityBindingUnavailable, observability.Correlation{
				TraceID: globalTraceID, RouteID: routeID, TargetID: dest.Target,
			}, "the activation-bound capability fingerprint could not be loaded; the submit path degrades to the eligibility-only gate until the store is readable", nil)
		}
		if _, err := sink.Probe(context.Background()); err != nil {
			return nil, fmt.Errorf("target %s: %w", dest.Target, err)
		}
		return sink, nil
	}
	if resolved.Webhook != nil {
		opts, err := webhookSinkOptions(dest.Target, *resolved.Webhook)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", dest.Target, err)
		}
		timeout := opts.SubmitTimeout
		if timeout == 0 {
			timeout = hermeswebhook.DefaultSubmitTimeout
		}
		opts.Client = webhookClientFactory(timeout)
		sink, err := hermeswebhook.NewSink(opts)
		if err != nil {
			return nil, err
		}
		if _, err := sink.Probe(context.Background()); err != nil {
			return nil, fmt.Errorf("target %s: %w", dest.Target, err)
		}
		return sink, nil
	}
	return nil, fmt.Errorf("unknown target %q: no sink adapter exists and no fallback is permitted (DUR-008)", dest.Target)
}

// backoffFromConfig maps the route's configured submission retry policy
// (configuration-spec §9) onto the runtime policy; an invalid configured
// policy fails closed. Durations parse through the one schema-exact
// parser so every schema-legal unit (including whole days) behaves
// identically at validation and run time.
func backoffFromConfig(retry config.Retry) (dispatch.Backoff, error) {
	initial, err := config.ParseDuration(retry.InitialBackoff)
	if err != nil {
		return dispatch.Backoff{}, fmt.Errorf("submission_retry.initial_backoff: %v", err)
	}
	maximum, err := config.ParseDuration(retry.MaxBackoff)
	if err != nil {
		return dispatch.Backoff{}, fmt.Errorf("submission_retry.max_backoff: %v", err)
	}
	return backoffFromNanos(retry, initial.Nanos, maximum.Nanos)
}

// backoffFromNanos assembles the policy from parsed nanosecond
// durations.
func backoffFromNanos(retry config.Retry, initialNanos, maximumNanos int64) (dispatch.Backoff, error) {
	initial := time.Duration(initialNanos)
	maximum := time.Duration(maximumNanos)
	b := dispatch.Backoff{
		MaxAttempts:    retry.MaxAttempts,
		InitialBackoff: initial,
		MaxBackoff:     maximum,
		Multiplier:     retry.Multiplier,
		JitterFraction: retry.JitterFraction,
	}
	if err := b.Validate(); err != nil {
		return dispatch.Backoff{}, err
	}
	return b, nil
}

// jitterUnit is the runtime jitter source: a bounded fraction in [0, 1)
// from the process-local PRNG (DUR-007's injectable jitter; tests inject
// deterministic sources directly on the Runtime).
func jitterUnit() float64 {
	return rand.Float64()
}

// sinkUnavailableExit is the stable exit for an unwired target adapter
// (error-model: target_unavailable, 11).
const sinkUnavailableExit = 11

// writeSinkError renders one resolveSink failure with the stable
// registry code: version and executable failures are target
// unavailability (11), capability and report defects are configuration
// failures (3, HER-005) — including the webhook adapter's own
// configuration and capability gates (E6-T1).
func writeSinkError(stderr io.Writer, command string, err error) int {
	var version *hermeskanban.VersionUnsupportedError
	var executable *hermeskanban.ExecutableMissingError
	var webhookCapability *hermeswebhook.CapabilityError
	var webhookConfig *hermeswebhook.ConfigError
	var multi *config.ErrMultiDestination
	switch {
	case errors.As(err, &version):
		writeError(stderr, command, "hermes_version_unsupported", "target_unavailable", version.Error()+"; "+version.Remediation())
		return sinkUnavailableExit
	case errors.As(err, &executable):
		writeError(stderr, command, "hermes_executable_missing", "target_unavailable", executable.Error()+"; "+executable.Remediation())
		return sinkUnavailableExit
	case errors.As(err, &webhookCapability):
		writeError(stderr, command, "config_capability_missing", "configuration", webhookCapability.Error()+"; "+webhookCapability.Remediation())
		return 3
	case errors.As(err, &webhookConfig):
		writeError(stderr, command, "config_invalid", "configuration", webhookConfig.Error())
		return 3
	case errors.As(err, &multi):
		writeError(stderr, command, "config_invalid", "configuration", multi.Error())
		return 3
	default:
		writeError(stderr, command, "target_definite_unavailable", "target_unavailable", err.Error())
		return sinkUnavailableExit
	}
}

// registerRouteState materializes one route's durable registration
// (E4 audit remediation for the E2-T5/E3 seam): the resource, the route
// revision, and the runtime state row, idempotently. The operator entry
// points — `watchman install` and `route enable` — call it so the
// first-use flow never meets an unregistered route; the configuration
// is the authority for every recorded value.
func registerRouteState(ctx context.Context, store *sqlite.Store, cfg *config.Config, routeID string) error {
	route, ok := cfg.Routes[routeID]
	if !ok {
		return fmt.Errorf("route %q is not defined", routeID)
	}
	resource, ok := cfg.Resources[route.Source.Resource]
	if !ok {
		return fmt.Errorf("resource %q is not defined", route.Source.Resource)
	}
	revision, ok := config.RouteRevision(cfg, routeID)
	if !ok {
		return fmt.Errorf("route %q revision could not be computed", routeID)
	}
	canonical := filepath.Clean(resource.Root)
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	gitMode := "disabled"
	if resource.Git != nil {
		gitMode = resource.Git.Mode
	}
	now := dispatch.Timestamp(time.Now())
	if err := store.RegisterResource(nil, route.Source.Resource, revision, resource.Root, canonical, resource.FileScope, gitMode); err != nil {
		return err
	}
	// The route's target binding is the destinations' shared target
	// (E12-T2, FAN-011: resolveRouteTarget guarantees they agree); the
	// canonically-first destination is the deterministic representative.
	dests := route.SortedDestinations()
	if len(dests) == 0 {
		return fmt.Errorf("route %q declares no destinations", routeID)
	}
	if err := store.RegisterRoute(nil, routeID, revision, revision, route.Source.Resource, dests[0].Target, "{}", now); err != nil {
		return err
	}
	var exists int
	if err := store.QueryRowContext(ctx, `SELECT 1 FROM route_runtime_state WHERE route_id = ?`, routeID).Scan(&exists); err == sql.ErrNoRows {
		return store.InitializeRouteState(nil, routeID)
	} else if err != nil {
		return err
	}
	return nil
}
