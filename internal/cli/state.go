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

	"github.com/rootkernel/jjukkumi/internal/adapters/hermeskanban"
	"github.com/rootkernel/jjukkumi/internal/adapters/hermeswebhook"
	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/app/dispatch"
	"github.com/rootkernel/jjukkumi/internal/config"
	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/platformpaths"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// StateDBName is the durable database file inside the state directory.
const StateDBName = "state.db"

// errConfigurationClass marks store-opening failures caused by the
// configuration document (the typed class behind the exit-3 mapping).
var errConfigurationClass = errors.New("configuration")

// openStateStore resolves the state directory with the shared precedence
// (config instance.state_dir, then JJUKKUMI_STATE_DIR, then the platform
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
	SetRouteActivation(ctx context.Context, routeID, activation, acknowledgeRevision, now string) error
	LoadRouteState(ctx context.Context, routeID string) (state.RouteSnapshot, error)
	CommitMergePending(ctx context.Context, lin ports.Lineage, actor, now string) (int, error)
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
		writeError(stderr, command, "sqlite_open_failed", "storage", err.Error())
		return nil, nil, 20
	}
	return s, s, 0
}

// targetScope returns the durable target scope an intent records: the
// kanban board slug for hermes-kanban targets and the endpoint URL for
// hermes-webhook targets — in both cases the identity of the interface
// that accepted the task, re-verified at reconciliation (migration v3).
func targetScope(target config.Target) string {
	if target.Type == "hermes-webhook" {
		return target.Endpoint
	}
	return target.Board
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

// resolveSink looks up and gates the sink adapter for one route target
// (E4-T3, E6-T1). The hermes-kanban sink is constructed from the operator
// configuration, its frozen capability report is validated against the
// route's required capabilities, and the read-only Probe gates the
// installed Hermes version — all before any submission (HER-002,
// HER-005). The hermes-webhook sink applies the same fail-closed gates
// against its static, evidence-tied capability declaration. No
// automatic fallback to any other target exists (DUR-008).
func resolveSink(cfg *config.Config, target config.Target, route config.Route) (ports.Sink, error) {
	switch target.Type {
	case "hermes-kanban":
		limits, err := hermesProcessLimits(cfg, target)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", route.Dispatch.Target, err)
		}
		sink, err := hermeskanban.NewSink(route.Dispatch.Target, target.Executable, target.CapabilityReport,
			target.RequiredCapabilities, target.Board, limits, int64(route.Batching.MaxManifestBytes))
		if err != nil {
			return nil, err
		}
		if _, err := sink.Probe(context.Background()); err != nil {
			return nil, fmt.Errorf("target %s: %w", route.Dispatch.Target, err)
		}
		return sink, nil
	case "hermes-webhook":
		opts, err := webhookSinkOptions(route.Dispatch.Target, target)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", route.Dispatch.Target, err)
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
			return nil, fmt.Errorf("target %s: %w", route.Dispatch.Target, err)
		}
		return sink, nil
	default:
		return nil, fmt.Errorf("unknown target type %q: no sink adapter exists and no fallback is permitted (DUR-008)", target.Type)
	}
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
	var capability *hermeskanban.CapabilityError
	var report *hermeskanban.ReportError
	var requirement *hermeskanban.InvalidRequirementError
	var webhookCapability *hermeswebhook.CapabilityError
	var webhookConfig *hermeswebhook.ConfigError
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
	case errors.As(err, &capability):
		writeError(stderr, command, "config_capability_missing", "configuration", capability.Error()+"; "+capability.Remediation())
		return 3
	case errors.As(err, &report), errors.As(err, &requirement):
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
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
	if err := store.RegisterRoute(nil, routeID, revision, revision, route.Source.Resource, route.Dispatch.Target, "{}", now); err != nil {
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
