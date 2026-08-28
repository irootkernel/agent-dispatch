package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Example returns a disabled example configuration bound to the given
// instance and resource root (E11-T1 destinations contract). It is the
// template written by `agent-dispatch init`: dispatch stays disabled
// until an explicit `route enable` (cli-spec §3), no Watchman trigger is
// installed, and notifications stay disabled because no sink is
// configured (NTF-001).
func Example(instanceID, resourceRoot string) *Config {
	return &Config{
		Version: 1,
		Instance: Instance{
			ID:       instanceID,
			LogPaths: "relative",
		},
		Limits: Limits{},
		Resources: map[string]Resource{
			"vault-main": {
				Type:      "directory",
				Root:      resourceRoot,
				FileScope: "markdown",
				Git:       &Git{Mode: "disabled"},
			},
		},
		HermesTargets: map[string]HermesTarget{
			"hermes-main": {
				Board:                "agent-dispatch",
				Executable:           "hermes",
				MinimumVersion:       MinimumEligibleHermesVersion,
				Compatibility:        "capability_probe",
				SubmitTimeout:        "30s",
				LookupTimeout:        "15s",
				EnvironmentAllowlist: []string{"HOME", "PATH"},
			},
		},
		Routes: map[string]Route{
			"wiki-maintenance": {
				Enabled: false,
				Source: Source{
					Type:        "watchman-trigger",
					SourceID:    "vault-main-watchman",
					Resource:    "vault-main",
					TriggerName: "agent-dispatch.wiki-maintenance.4f8c21",
					Include:     []string{"**/*.md"},
					Exclude:     []string{".git/**", ".obsidian/workspace*.json", ".obsidian/cache/**", ".trash/**"},
				},
				Batching:   Batching{AutomaticThreshold: 25, HardLimit: 100, MaxManifestBytes: 262144},
				FanoutMode: "all",
				Destinations: []Destination{
					{
						ID:             "indexing",
						Target:         "hermes-main",
						Profile:        "wiki-maintainer",
						Skills:         []string{"llm-wiki"},
						Workstream:     "indexing",
						MutexKey:       "wiki-publish",
						ExecutionHints: ExecutionHints{MaxRuntime: "30m", MaxAttempts: 2},
					},
				},
				Notifications: &Notifications{
					Events: []string{"work_completed", "work_failed", "delivery_unknown"},
					Sinks:  []NotificationSink{},
				},
				SubmissionRetry:  Retry{MaxAttempts: 3, InitialBackoff: "2s", MaxBackoff: "2m", Multiplier: 2.0, JitterFraction: 0.2},
				LatestState:      true,
				FailureBudget:    2,
				ActiveStaleAfter: "2h",
				Policy: Policy{
					Protected:           []string{"raw/**", "canon/**"},
					Immutable:           []string{},
					BulkAction:          "quarantine",
					OverflowAction:      "reconcile",
					FreshInstanceAction: "reconcile",
					UnsafePathAction:    "quarantine",
				},
				Reconciliation: Reconciliation{Initial: true, DailyExpected: true},
				Retention:      &Retention{},
			},
		},
		Retention: &Retention{},
	}
}

// WriteExample validates the example configuration (schema and semantic
// checks) and writes it to configPath, creating parent directories with
// owner-only permissions (SEC-008). The file appears through an atomic
// hard-link from a private temporary file, so an existing file, including
// one appearing through a symlink or a concurrent writer, is never
// overwritten and a failed write leaves no partial configuration.
func WriteExample(cfg *Config, configPath string) error {
	if err := SchemaValidate(cfg); err != nil {
		return fmt.Errorf("example config failed schema validation: %w", err)
	}
	if errs, _ := SemanticValidate(cfg); len(errs) > 0 {
		return fmt.Errorf("example config failed semantic validation: %s", joinErrors(errs))
	}
	data, err := MarshalYAML(cfg)
	if err != nil {
		return err
	}
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Write to a private temporary file, then link it into place: the link
	// fails atomically if the target exists, and a failed write leaves no
	// partial configuration behind to block a later init.
	tmp, err := os.CreateTemp(dir, ".agent-dispatch-init-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmpName, configPath); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%s already exists; init refuses to overwrite", configPath)
		}
		return err
	}
	return nil
}

// EnsureStateDir creates the state directory with owner-only permissions
// if absent and verifies it is a usable directory. A symbolic link as the
// state directory itself is rejected so state placement stays explicit
// (SCP-007); intermediate OS-level symlinks are accepted.
func EnsureStateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return placementError{fmt.Sprintf("%s is a symbolic link; state directory must be a real directory", dir)}
		}
		if !info.IsDir() {
			return placementError{fmt.Sprintf("%s exists and is not a directory", dir)}
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Only the final component is checked: intermediate OS-level symlinks
	// (for example macOS /var -> /private/var) are legitimate, while a
	// symlinked state directory itself hides the real placement. Re-check
	// after creation so a check-then-create swap is caught.
	info, err = os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return placementError{fmt.Sprintf("%s is a symbolic link; state directory must be a real directory", dir)}
	}
	if !info.IsDir() {
		return placementError{fmt.Sprintf("%s exists and is not a directory", dir)}
	}
	return nil
}

// IsStateDirPlacementError reports whether err is a state-directory
// placement rejection (symbolic link or non-directory) rather than an
// ordinary filesystem failure.
func IsStateDirPlacementError(err error) bool {
	var placement placementError
	return errors.As(err, &placement)
}

type placementError struct{ msg string }

func (e placementError) Error() string { return e.msg }
