package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/config"
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

// resolveSink looks up the sink adapter for one target type. The Hermes
// adapters arrive with E4; until then submit-touching paths report the
// documented target-unavailable error with the remediation, and no
// automatic fallback to any other target exists (DUR-008).
func resolveSink(targetType string) (ports.Sink, error) {
	return nil, errors.New("no sink adapter is wired in this build: the Hermes Kanban adapter arrives with roadmap task E4-T1; configure it and rerun, or inspect with 'jjukkumi dispatches show'")
}

// sinkUnavailableExit is the stable exit for an unwired target adapter
// (error-model: target_unavailable, 11).
const sinkUnavailableExit = 11
