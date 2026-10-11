package config

import (
	"fmt"
	"slices"

	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
)

func validateRawSyncManagement(sync map[string]any) error {
	raw, present := sync["management"]
	if !present {
		return nil
	}
	management, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("yaml: sync.management must be an object")
	}
	enabled, ok := management["enabled"].(bool)
	if !ok {
		return fmt.Errorf("yaml: sync.management.enabled is required and must be a boolean")
	}
	actions, ok := management["actions"].([]any)
	if !ok {
		return fmt.Errorf("yaml: sync.management.actions is required and must be an array")
	}
	if enabled && len(actions) == 0 {
		return fmt.Errorf("yaml: enabled sync.management requires actions")
	}
	seen := map[string]bool{}
	for _, rawAction := range actions {
		action, ok := rawAction.(string)
		if !ok || !syncmanagement.ValidAction(action) || seen[action] {
			return fmt.Errorf("yaml: sync.management.actions must contain unique supported actions")
		}
		seen[action] = true
	}
	return nil
}

// ManagementRevisions bind operator authority without resolving secret values.
// They do not alter the cooperative-import acknowledgement projection.
func ManagementRevisions(cfg *Config) (policy, revision string, err error) {
	management := SyncManagement{Actions: []string{}}
	if cfg.Sync != nil && cfg.Sync.Management != nil {
		management = *cfg.Sync.Management
		management.Actions = slices.Clone(management.Actions)
		slices.Sort(management.Actions)
	}
	policy, err = syncmanagement.Digest("agent-dispatch.sync-management.policy/v1", management)
	if err != nil || cfg.Sync == nil {
		return policy, "", err
	}
	nodes := slices.Clone(cfg.Sync.Nodes)
	slices.SortFunc(nodes, func(a, b SyncNode) int {
		if a.InstanceID < b.InstanceID {
			return -1
		}
		if a.InstanceID > b.InstanceID {
			return 1
		}
		return 0
	})
	syncRevision, _ := SyncRevision(cfg)
	revision, err = syncmanagement.Digest("agent-dispatch.sync-management.config/v1", struct {
		SyncRevision string     `json:"sync_revision"`
		SyncEnabled  bool       `json:"sync_enabled"`
		Bounds       SyncBounds `json:"bounds"`
		Nodes        []SyncNode `json:"nodes"`
	}{syncRevision, cfg.Sync.Enabled, cfg.Sync.Bounds, nodes})
	return policy, revision, err
}
