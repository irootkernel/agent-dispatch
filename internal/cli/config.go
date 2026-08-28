package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeswebhook"
	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
)

// runConfig implements the config command tree (cli-spec §3): config
// validate and config show (the normalized, redacted configuration
// view, E7-T5).
func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "config", "config requires a subcommand; this build implements 'config validate' and 'config show'")
	}
	switch args[0] {
	case "validate":
		return runConfigValidate(args[1:], stdout, stderr)
	case "show":
		return runConfigShow(args[1:], stdout, stderr)
	default:
		return usageError(stderr, "config", fmt.Sprintf("unknown config subcommand %q; this build implements 'config validate' and 'config show'", args[0]))
	}
}

// runConfigShow prints the normalized configuration with every secret
// reference redacted (SEC-006/SEC-007: values never leave the store or
// the output).
func runConfigShow(args []string, stdout, stderr io.Writer) int {
	command := "config show"
	configPath := ""
	output := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config":
			if i+1 >= len(args) {
				return usageError(stderr, command, "--config requires a value")
			}
			configPath = args[i+1]
			i++
		case "--output":
			if i+1 >= len(args) || args[i+1] != "json" {
				return usageError(stderr, command, "--output requires 'json'")
			}
			output = args[i+1]
			i++
		default:
			return usageError(stderr, command, fmt.Sprintf("unknown flag %q", args[i]))
		}
	}
	_ = output // the command emits JSON only; the flag exists for shell uniformity (E9-T2/L-11)
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	normalized, err := cfg.Normalized()
	if err != nil {
		return planErr(stderr, command, "internal_unclassified", "internal", err.Error(), 40)
	}
	var view map[string]any
	if err := json.Unmarshal(normalized, &view); err != nil {
		return planErr(stderr, command, "internal_unclassified", "internal", err.Error(), 40)
	}
	// The computed route revisions (cli-spec §3): the same digest the
	// production gate acknowledges, visible without touching the store.
	revisions := map[string]string{}
	for routeID := range cfg.Routes {
		if rev, ok := config.RouteRevision(cfg, routeID); ok {
			revisions[routeID] = rev
		}
	}
	view["computed_route_revisions"] = revisions
	return writeEnvelope(stdout, command, view)
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

	// The offline section-12 target checks run in every validation, not
	// only under --probe-targets (E8-T3): the hermes-kanban report file
	// (existence, parse, and the required-capability gate) and the static
	// webhook declaration (transport/durable split, header collision)
	// need no live target. Only the freshness check against the
	// installed version stays probe-gated, because it needs the
	// executable.
	if code := validatePatterns(command, cfg, stderr); code != 0 {
		return code
	}
	offlineWarnings, code := validateTargetsOffline(command, cfg, stderr)
	if code != 0 {
		return code
	}
	warnings = append(warnings, offlineWarnings...)
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

// validateTargetsOffline runs the probe-free section 12 checks for every
// configured target (E8-T3 posture under the E11 cutover): the hermes
// targets' execution bounds must parse, and the static webhook
// declaration must carry every capability a webhook destination names.
// A configuration defect here is exit 3 — the old probe-only gating
// let `config validate` pass configurations the targets could never
// honor.
// validatePatterns compiles every route's pattern sets with the same
// engine the dispatch path uses (§12 pattern safety, E8 correction of
// review M-18: the patterns previously compiled only at route
// plan/dispatch time, so an invalid glob passed validation).
func validatePatterns(command string, cfg *config.Config, stderr io.Writer) int {
	ids := make([]string, 0, len(cfg.Routes))
	for id := range cfg.Routes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		route := cfg.Routes[id]
		mode := policy.CaseSensitive
		if config.CaseMode() == "insensitive" {
			mode = policy.CaseInsensitive
		}
		if _, err := policy.NewEngine(route.Source.Include, route.Source.Exclude, route.Policy.Protected, route.Policy.Immutable, mode); err != nil {
			return planErr(stderr, command, "config_invalid", "configuration",
				fmt.Sprintf("route %q pattern sets do not compile: %v", id, err), 3)
		}
	}
	return 0
}

func validateTargetsOffline(command string, cfg *config.Config, stderr io.Writer) ([]string, int) {
	var warnings []string
	for _, id := range sortedTargetIDs(cfg) {
		target := cfg.Targets[id]
		switch target.Type {
		case "hermes-webhook":
			opts, err := webhookSinkOptions(id, target)
			if err != nil {
				return warnings, planErr(stderr, command, "config_invalid", "configuration", fmt.Sprintf("target %s: %v", id, err), 3)
			}
			if _, err := hermeswebhook.NewSink(opts); err != nil {
				var missing *hermeswebhook.CapabilityError
				detail := err.Error()
				if errors.As(err, &missing) {
					detail = missing.Error() + "; " + missing.Remediation()
				}
				return warnings, planErr(stderr, command, "config_capability_missing", "configuration", fmt.Sprintf("target %s: %v", id, detail), 3)
			}
		default:
			continue
		}
	}
	// Hermes targets have no offline evidence file since the cutover
	// (ADR-0017); the offline check proves their execution bounds parse
	// through the same gate the probe path uses, and the live eligibility
	// probe runs under --probe-targets (the E11-T2 capability probe
	// restores shape proof).
	for _, id := range sortedHermesTargetIDs(cfg) {
		target := cfg.HermesTargets[id]
		if _, err := hermesTargetProcessLimits(cfg, target); err != nil {
			return warnings, planErr(stderr, command, "config_invalid", "configuration", fmt.Sprintf("hermes_targets.%s: %v", id, err), 3)
		}
	}
	return warnings, 0
}

// probeResult carries the per-target probe outcome of one
// `config validate --probe-targets` run.
type probeResult struct {
	summaries []map[string]any
	warnings  []string
}

// probeHermesTargets runs the read-only public eligibility probe for
// every configured hermes target (cli-spec §3: --probe-targets invokes
// read-only public probes; E11-T1). Every target is probed so the
// summary stays complete; a persistent configuration defect
// (config_error, config_invalid) fails validation with exit 3, while an
// unavailable or below-floor target is a warning because the
// configuration document itself is still valid and the adapter gates
// submissions again at run time.
func probeHermesTargets(command string, cfg *config.Config, stdout, stderr io.Writer) (probeResult, int) {
	var out probeResult
	var firstFailure error
	for _, id := range sortedHermesTargetIDs(cfg) {
		target := cfg.HermesTargets[id]
		limits, err := hermesTargetProcessLimits(cfg, target)
		if err != nil {
			// A target whose configured limits are themselves invalid is
			// a configuration defect: it is recorded and fails at the
			// end, and probing continues so the summary stays complete.
			out.summaries = append(out.summaries, map[string]any{
				"target_id": id,
				"type":      "hermes-kanban",
				"state":     "config_error",
				"detail":    err.Error(),
			})
			if firstFailure == nil {
				firstFailure = fmt.Errorf("hermes_targets.%s: %w", id, err)
			}
			continue
		}
		adapter, err := hermeskanban.New(id, target.Executable, target.MinimumVersion, limits)
		if err != nil {
			out.summaries = append(out.summaries, map[string]any{
				"target_id": id,
				"type":      "hermes-kanban",
				"state":     "config_error",
				"detail":    err.Error(),
			})
			if firstFailure == nil {
				firstFailure = err
			}
			continue
		}
		summary, _, err := adapter.ProbeVerbose(context.Background())
		entry := map[string]any{
			"target_id": id,
			"type":      "hermes-kanban",
			"state":     summary.State,
			"detail":    summary.Detail,
		}
		if summary.Version != "" {
			entry["hermes_version"] = summary.Version
		}
		out.summaries = append(out.summaries, entry)
		switch {
		case summary.State == "config_error":
			if firstFailure == nil {
				firstFailure = err
			}
		case summary.State != "available":
			out.warnings = append(out.warnings, fmt.Sprintf("hermes_targets.%s: probe state %s: %s", id, summary.State, summary.Detail))
		}
	}
	// The webhook adapter's capability declaration is static and offline
	// (E0-T4 §9: the receiving platform cannot be assumed running), so
	// the probe validates the target configuration and the
	// required-capability gate without network I/O.
	for _, id := range sortedTargetIDs(cfg) {
		target := cfg.Targets[id]
		opts, err := webhookSinkOptions(id, target)
		if err != nil {
			out.summaries = append(out.summaries, map[string]any{
				"target_id": id, "type": "hermes-webhook", "state": "config_error", "detail": err.Error(),
			})
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
			out.summaries = append(out.summaries, map[string]any{
				"target_id": id, "type": "hermes-webhook", "state": state, "detail": detail,
			})
			if firstFailure == nil {
				firstFailure = err
			}
			continue
		}
		caps, err := sink.Probe(context.Background())
		if err != nil {
			// The declaration is static today, but a future failure mode
			// must not report an empty capability set as available.
			out.summaries = append(out.summaries, map[string]any{
				"target_id": id, "type": "hermes-webhook", "state": "config_error", "detail": err.Error(),
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
	}
	if firstFailure != nil {
		var webhookMissing *hermeswebhook.CapabilityError
		if errors.As(firstFailure, &webhookMissing) {
			writeError(stderr, command, "config_capability_missing", "configuration", webhookMissing.Error()+"; "+webhookMissing.Remediation())
		} else {
			writeError(stderr, command, "config_invalid", "configuration", firstFailure.Error())
		}
		return out, 3
	}
	return out, 0
}

// hermesTargetProcessLimits maps the configured limits and hermes target
// timeouts onto the adapter's controlled-execution bounds (SEC-004); an
// invalid duration fails closed and an unset output bound keeps the
// adapter default. Durations parse through config.ParseDuration, the one
// schema-exact parser (whole-day units included).
func hermesTargetProcessLimits(cfg *config.Config, target config.HermesTarget) (hermeskanban.ProcessLimits, error) {
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

// sortedHermesTargetIDs renders the deterministic hermes target probe
// order.
func sortedHermesTargetIDs(cfg *config.Config) []string {
	ids := make([]string, 0, len(cfg.HermesTargets))
	for id := range cfg.HermesTargets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
