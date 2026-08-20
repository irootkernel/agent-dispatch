package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rootkernel/jjukkumi/internal/adapters/watchman"
	"github.com/rootkernel/jjukkumi/internal/config"
)

// runConfig implements the config command tree (cli-spec §3); this build
// implements `config validate`. `config show` arrives with the E3
// runtime-management surface.
func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "validate" {
		if len(args) > 0 && args[0] == "show" {
			writeError(stderr, "config show", "command_not_implemented", "usage", "config show is not implemented in this build")
			return 2
		}
		return usageError(stderr, "config", "config requires a subcommand; this build implements 'config validate'")
	}
	return runConfigValidate(args[1:], stdout, stderr)
}

func runConfigValidate(args []string, stdout, stderr io.Writer) int {
	command := "config validate"
	configPath := ""
	probeTargets := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config":
			if i+1 >= len(args) {
				return usageError(stderr, command, "--config requires a path")
			}
			i++
			configPath = args[i]
		case "--probe-targets":
			probeTargets = true
		case "--output=json", "--output":
			if args[i] == "--output" {
				if i+1 >= len(args) || args[i+1] != "json" {
					return usageError(stderr, command, "--output requires 'json'")
				}
				i++
			}
		default:
			return usageError(stderr, command, fmt.Sprintf("unknown argument %q", args[i]))
		}
	}
	configPath = resolveConfigPath(configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	warnings := append([]string(nil), cfg.Warnings...)

	// Watchman availability check with actionable output (E2-T5
	// acceptance): a missing or unusable Watchman is a warning here, not
	// a validation failure, because configuration itself is valid.
	watchmanState := "available"
	client := watchman.NewClient("")
	version, err := client.Version(context.Background())
	switch {
	case err != nil:
		watchmanState = "unavailable"
		var unavailable *watchman.UnavailableError
		if errors.As(err, &unavailable) {
			warnings = append(warnings, unavailable.Error()+"; "+unavailable.Remediation())
		} else {
			warnings = append(warnings, "watchman check failed: "+err.Error())
		}
	default:
		if err := watchman.CheckVersionSupported(version); err != nil {
			watchmanState = "unsupported"
			warnings = append(warnings, err.Error())
		}
	}

	if probeTargets {
		warnings = append(warnings, "--probe-targets capability probing arrives with the E4 target integration")
	}
	return writeEnvelopeWithWarnings(stdout, command, map[string]any{
		"valid":            true,
		"config_path":      configPath,
		"routes":           len(cfg.Routes),
		"resources":        len(cfg.Resources),
		"targets":          len(cfg.Targets),
		"watchman":         watchmanState,
		"watchman_version": version,
	}, warnings)
}
