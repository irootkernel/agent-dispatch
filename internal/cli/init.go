package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
)

// runInit implements `agent-dispatch init` (cli-spec §3): it creates the state
// directory and a disabled example configuration after checking for
// existing files. It never installs a Watchman trigger or enables
// dispatch; those require their own explicit commands. The state directory
// is prepared first so a failure writing the configuration cannot leave a
// partially initialized installation that init then refuses to repair.
func runInit(args []string, stdout, stderr io.Writer) int {
	instanceID := ""
	configPath := ""
	stateDir := ""
	resourceRoot := ""
	jsonOutput := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config":
			if i+1 >= len(args) {
				return usageError(stderr, "init", "--config requires a path")
			}
			i++
			configPath = args[i]
		// --state-dir is a global option (cli-spec s1): the global scan
		// consumes it before dispatch and resolveStateDirOverride
		// applies it, so init has no per-command branch for it.
		case "--instance-id":
			if i+1 >= len(args) {
				return usageError(stderr, "init", "--instance-id requires a value")
			}
			i++
			instanceID = args[i]
		case "--resource-root":
			if i+1 >= len(args) {
				return usageError(stderr, "init", "--resource-root requires a directory path")
			}
			i++
			resourceRoot = args[i]
		case "--output", "-o":
			v, err := parseOutputValue(stderr, "init", args, &i)
			if err != nil {
				return 2
			}
			jsonOutput = v
		case "--output=json":
			jsonOutput = true
		case "--output=human":
			jsonOutput = false
		default:
			return usageError(stderr, "init", fmt.Sprintf("unknown argument %q for init", args[i]))
		}
	}
	if configPath == "" {
		// Same precedence as configuration loading (spec section 1): an
		// explicit path, then AGENT_DISPATCH_CONFIG, then the platform default.
		if env := os.Getenv("AGENT_DISPATCH_CONFIG"); env != "" {
			configPath = env
		} else {
			configPath = platformpaths.DefaultConfigPath()
		}
	}
	if stateDir == "" {
		stateDir = resolveStateDirOverride("")
	}
	if instanceID == "" {
		instanceID = "agent-dispatch-local"
	}
	if resourceRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			writeError(stderr, "init", "config_invalid", "configuration", fmt.Sprintf("cannot resolve home directory: %v", err))
			return 3
		}
		resourceRoot = filepath.Join(home, "Documents", "Obsidian", "MainVault")
	}

	if err := config.EnsureStateDir(stateDir); err != nil {
		if config.IsStateDirPlacementError(err) {
			writeError(stderr, "init", "state_directory_not_local", "configuration", err.Error())
		} else {
			writeError(stderr, "init", "config_invalid", "configuration", fmt.Sprintf("state directory: %v", err))
		}
		return 3
	}
	// Spec section 3: a state directory inside the watched vault warns.
	var warnings []string
	if w := config.StateDirInsideRootWarning(stateDir, resourceRoot); w != "" {
		warnings = append(warnings, w)
	}
	cfg := config.Example(instanceID, resourceRoot)
	// An explicitly chosen state directory persists into the written
	// configuration so later invocations honor it (E7-T9/M-29: recording
	// an empty state_dir silently redirected subsequent commands to the
	// platform default). An environment-derived default stays dynamic:
	// freezing it would defeat AGENT_DISPATCH_STATE_DIR overrides.
	if globalStateDir != "" {
		cfg.Instance.StateDir = stateDir
	}
	if err := config.WriteExample(cfg, configPath); err != nil {
		writeError(stderr, "init", "config_invalid", "configuration", err.Error())
		return 3
	}
	if jsonOutput {
		return writeEnvelopeWithWarnings(stdout, "init", map[string]any{
			"config_path": configPath,
			"state_dir":   stateDir,
			"enabled":     false,
		}, warnings)
	}
	for _, w := range warnings {
		fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	fmt.Fprintf(stdout, "wrote disabled example configuration: %s\n", configPath)
	fmt.Fprintf(stdout, "state directory ready: %s\n", stateDir)
	fmt.Fprintf(stdout, "dispatch stays disabled until 'agent-dispatch route enable' is run explicitly\n")
	return 0
}
