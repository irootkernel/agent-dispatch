package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"path/filepath"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/hermeskanban"
	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/app/dispatch"
	"github.com/rootkernel/jjukkumi/internal/config"
	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/platformpaths"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// StateDBName is the durable database file inside the state directory.
const StateDBName = "state.db"

// openStateStore resolves the state directory with the shared precedence
// (config instance.state_dir, then JJUKKUMI_STATE_DIR, then the platform
// default), opens the SQLite store, and applies pending migrations.
func openStateStore(configPath string) (*sqlite.Store, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("configuration: %w", err)
	}
	stateDir := platformpaths.ResolveStateDir(cfg.Instance.StateDir)
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

// storeOp is one durable dispatch store with its full E3-T3 surface.
type storeOp interface {
	ports.DispatchStore
	ports.InspectionStore
	ports.OperatorStore
	ports.ReconcileStore
	ListRoutes(ctx context.Context) ([]sqlite.RouteRow, error)
	SetRouteActivation(ctx context.Context, routeID, activation, acknowledgeRevision, now string) error
	LoadRouteState(ctx context.Context, routeID string) (state.RouteSnapshot, error)
	CommitMergePending(ctx context.Context, lin ports.Lineage, actor, now string) (int, error)
	CompleteActive(ctx context.Context, req ports.ActiveCompletion) (ports.FollowupCreated, error)
	ActivateDispatch(ctx context.Context, dispatchID, actor, now string) error
	ActivateFollowup(ctx context.Context, dispatchID, actor, now string) error
}

// openOperatorStore opens the store and narrows it to the operator
// surface, mapping open failures to the documented storage error.
func openOperatorStore(command string, configPath string, stderr io.Writer) (storeOp, *sqlite.Store, int) {
	s, err := openStateStore(resolveConfigPath(configPath))
	if err != nil {
		writeError(stderr, command, "sqlite_open_failed", "storage", err.Error())
		return nil, nil, 20
	}
	return s, s, 0
}

// resolveSink looks up and gates the sink adapter for one route target
// (E4-T3). The hermes-kanban sink is constructed from the operator
// configuration, its frozen capability report is validated against the
// route's required capabilities, and the read-only Probe gates the
// installed Hermes version — all before any submission (HER-002,
// HER-005). No automatic fallback to any other target exists (DUR-008).
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
		return nil, errors.New("no sink adapter is wired in this build: the Hermes Webhook adapter arrives with roadmap task E6-T1; configure it and rerun, or inspect with 'jjukkumi dispatches show'")
	default:
		return nil, fmt.Errorf("unknown target type %q: no sink adapter exists and no fallback is permitted (DUR-008)", target.Type)
	}
}

// backoffFromConfig maps the route's configured submission retry policy
// (configuration-spec §9) onto the runtime policy; an invalid configured
// policy fails closed.
func backoffFromConfig(retry config.Retry) (dispatch.Backoff, error) {
	initial, err := time.ParseDuration(retry.InitialBackoff)
	if err != nil {
		return dispatch.Backoff{}, fmt.Errorf("submission_retry.initial_backoff: %v", err)
	}
	maximum, err := time.ParseDuration(retry.MaxBackoff)
	if err != nil {
		return dispatch.Backoff{}, fmt.Errorf("submission_retry.max_backoff: %v", err)
	}
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
// failures (3, HER-005).
func writeSinkError(stderr io.Writer, command string, err error) int {
	var version *hermeskanban.VersionUnsupportedError
	var executable *hermeskanban.ExecutableMissingError
	var capability *hermeskanban.CapabilityError
	var report *hermeskanban.ReportError
	var requirement *hermeskanban.InvalidRequirementError
	switch {
	case errors.As(err, &version):
		writeError(stderr, command, "hermes_version_unsupported", "target_unavailable", version.Error()+"; "+version.Remediation())
		return sinkUnavailableExit
	case errors.As(err, &executable):
		writeError(stderr, command, "hermes_executable_missing", "target_unavailable", executable.Error()+"; "+executable.Remediation())
		return sinkUnavailableExit
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
