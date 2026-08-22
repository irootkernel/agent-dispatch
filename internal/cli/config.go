package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/hermeskanban"
	"github.com/rootkernel/jjukkumi/internal/adapters/hermeswebhook"
	"github.com/rootkernel/jjukkumi/internal/adapters/watchman"
	"github.com/rootkernel/jjukkumi/internal/config"
)

// runConfig implements the config command tree (cli-spec §3); this build
// implements `config validate`. `config show` arrives with the E6-T2
// operational observability surface.
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
	probeSummary := []map[string]any{}
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
		targets, code := probeHermesTargets(command, cfg, stdout, stderr)
		if code != 0 {
			return code
		}
		warnings = append(warnings, targets.warnings...)
		probeSummary = targets.summaries
	}
	return writeEnvelopeWithWarnings(stdout, command, map[string]any{
		"valid":            true,
		"config_path":      configPath,
		"routes":           len(cfg.Routes),
		"resources":        len(cfg.Resources),
		"targets":          len(cfg.Targets),
		"watchman":         watchmanState,
		"watchman_version": version,
		"probe_targets":    probeSummary,
	}, warnings)
}

// probeResult carries the per-target probe outcome of one
// `config validate --probe-targets` run.
type probeResult struct {
	summaries []map[string]any
	warnings  []string
}

// probeHermesTargets runs the read-only public capability probe for every
// configured hermes-kanban target (cli-spec §3: --probe-targets invokes
// read-only public capability probes). Every target is probed so the
// summary stays complete; a required capability the verified target does
// not provide (HER-005, config_capability_missing) and a persistent
// configuration defect (config_error, config_invalid) then fail
// validation with exit 3, while an unavailable or version-unsupported
// target is a warning because the configuration document itself is
// still valid and the adapter gates submissions again at run time.
func probeHermesTargets(command string, cfg *config.Config, stdout, stderr io.Writer) (probeResult, int) {
	var out probeResult
	var firstFailure error
	for _, id := range sortedTargetIDs(cfg) {
		target := cfg.Targets[id]
		switch target.Type {
		case "hermes-kanban":
		case "hermes-webhook":
			// The webhook adapter's capability declaration is static and
			// offline (E0-T4 §9: the receiving platform cannot be assumed
			// running), so the probe validates the target configuration
			// and the required-capability gate without network I/O.
			opts, err := webhookSinkOptions(id, target)
			if err != nil {
				entry := map[string]any{
					"target_id": id,
					"type":      "hermes-webhook",
					"state":     "config_error",
					"detail":    err.Error(),
				}
				out.summaries = append(out.summaries, entry)
				if firstFailure == nil {
					firstFailure = fmt.Errorf("target %s: %w", id, err)
				}
				continue
			}
			sink, err := hermeswebhook.NewSink(opts)
			if err != nil {
				var missing *hermeswebhook.CapabilityError
				detail := err.Error()
				state := "config_error"
				if errors.As(err, &missing) {
					state = "capability_mismatch"
					detail = missing.Error() + "; " + missing.Remediation()
				}
				entry := map[string]any{
					"target_id": id,
					"type":      "hermes-webhook",
					"state":     state,
					"detail":    detail,
				}
				out.summaries = append(out.summaries, entry)
				if firstFailure == nil {
					firstFailure = err
				}
				continue
			}
			caps, err := sink.Probe(context.Background())
			if err != nil {
				// The declaration is static today, but a future failure
				// mode must not report an empty capability set as
				// available.
				out.summaries = append(out.summaries, map[string]any{
					"target_id": id,
					"type":      "hermes-webhook",
					"state":     "config_error",
					"detail":    err.Error(),
				})
				if firstFailure == nil {
					firstFailure = err
				}
				continue
			}
			out.summaries = append(out.summaries, map[string]any{
				"target_id":    id,
				"type":         "hermes-webhook",
				"state":        "available",
				"detail":       "static capability declaration; endpoint reachability is proven only by submission (E0-T4 §9)",
				"capabilities": caps.BoolMap(),
			})
			continue
		default:
			continue
		}
		limits, err := hermesProcessLimits(cfg, target)
		if err != nil {
			// A target whose configured limits are themselves invalid is
			// a configuration defect: it is recorded and fails at the
			// end, and probing continues so the summary stays complete.
			entry := map[string]any{
				"target_id": id,
				"type":      "hermes-kanban",
				"state":     "config_error",
				"detail":    err.Error(),
			}
			out.summaries = append(out.summaries, entry)
			if firstFailure == nil {
				firstFailure = fmt.Errorf("target %s: %w", id, err)
			}
			continue
		}
		adapter := hermeskanban.New(id, target.Executable, target.CapabilityReport, target.RequiredCapabilities, limits)
		summary, caps, err := adapter.ProbeVerbose(context.Background())
		entry := map[string]any{
			"target_id": id,
			"type":      "hermes-kanban",
			"state":     summary.State,
			"detail":    summary.Detail,
		}
		if summary.Version != "" {
			entry["hermes_version"] = summary.Version
		}
		if summary.State == "available" {
			entry["capabilities"] = caps.BoolMap()
		}
		out.summaries = append(out.summaries, entry)
		var missing *hermeskanban.CapabilityError
		switch {
		case errors.As(err, &missing):
			if firstFailure == nil {
				firstFailure = missing
			}
		case summary.State == "config_error":
			if firstFailure == nil {
				firstFailure = err
			}
		case summary.State != "available":
			out.warnings = append(out.warnings, fmt.Sprintf("target %s: hermes probe state %s: %s", id, summary.State, summary.Detail))
		}
	}
	if firstFailure != nil {
		var missing *hermeskanban.CapabilityError
		var webhookMissing *hermeswebhook.CapabilityError
		switch {
		case errors.As(firstFailure, &missing):
			writeError(stderr, command, "config_capability_missing", "configuration", missing.Error()+"; "+missing.Remediation())
		case errors.As(firstFailure, &webhookMissing):
			writeError(stderr, command, "config_capability_missing", "configuration", webhookMissing.Error()+"; "+webhookMissing.Remediation())
		default:
			writeError(stderr, command, "config_invalid", "configuration", firstFailure.Error())
		}
		return out, 3
	}
	return out, 0
}

// hermesProcessLimits maps the configured limits and target timeouts onto
// the adapter's controlled-execution bounds (SEC-004); an invalid
// duration fails closed and an unset output bound keeps the adapter
// default. Durations parse through config.ParseDuration, the one
// schema-exact parser (whole-day units included).
func hermesProcessLimits(cfg *config.Config, target config.Target) (hermeskanban.ProcessLimits, error) {
	limits := hermeskanban.ProcessLimits{
		EnvironmentAllowlist: target.EnvironmentAllowlist,
	}
	if cfg.Limits.MaxSubprocessOutBytes != nil {
		limits.MaxOutputBytes = *cfg.Limits.MaxSubprocessOutBytes
	}
	if target.SubmitTimeout != "" {
		d, err := config.ParseDuration(target.SubmitTimeout)
		if err != nil {
			return limits, fmt.Errorf("submit_timeout: %v", err)
		}
		limits.SubmitTimeout = time.Duration(d.Nanos)
	}
	if target.LookupTimeout != "" {
		d, err := config.ParseDuration(target.LookupTimeout)
		if err != nil {
			return limits, fmt.Errorf("lookup_timeout: %v", err)
		}
		limits.LookupTimeout = time.Duration(d.Nanos)
	}
	return limits, nil
}

// sortedTargetIDs renders the deterministic target probe order.
func sortedTargetIDs(cfg *config.Config) []string {
	ids := make([]string, 0, len(cfg.Targets))
	for id := range cfg.Targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
