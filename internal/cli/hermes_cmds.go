package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
)

// runHermes implements the `hermes` command group (E11-T2, CLI-010):
// `hermes probe` runs the bounded public-interface probe and writes the
// cached evidence, and `hermes capabilities [--refresh]` prints the
// inspectable cache, re-probing when it is missing, stale, or
// explicitly refreshed (HER-011 through HER-013).
func runHermes(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "hermes", "hermes requires a subcommand: probe, capabilities, profiles, or set-minimum-version")
	}
	switch args[0] {
	case "probe":
		return runHermesProbe("hermes probe", args[1:], stdout, stderr)
	case "capabilities":
		return runHermesCapabilities("hermes capabilities", args[1:], stdout, stderr)
	case "profiles":
		return runHermesProfiles("hermes profiles", args[1:], stdout, stderr)
	case "set-minimum-version":
		return runHermesSetMinimumVersion("hermes set-minimum-version", args[1:], stdout, stderr)
	default:
		return usageError(stderr, "hermes", fmt.Sprintf("unknown hermes subcommand %q", args[0]))
	}
}

// hermesTargetSelection resolves the single hermes target the command
// addresses: --target <id> or exactly one declared hermes target.
func hermesTargetSelection(command string, args []string, stderr io.Writer) (*config.Config, string, *dispatchesFlags, bool, int) {
	// --refresh is a boolean: strip it before the valued-flag parser and
	// report it separately so the caller cannot lose it.
	refresh := false
	var rest []string
	for _, a := range args {
		if a == "--refresh" {
			refresh = true
			continue
		}
		rest = append(rest, a)
	}
	flags, code := parseDispatchesFlags(command, rest, stderr, map[string]bool{"--target": true, "--profile": true})
	if code != 0 {
		return nil, "", nil, false, code
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return nil, "", nil, false, 3
	}
	ids := sortedHermesTargetIDs(cfg)
	targetID := flags.val("--target")
	if targetID == "" {
		if len(ids) != 1 {
			writeError(stderr, command, "config_invalid", "configuration",
				fmt.Sprintf("name one hermes target with --target (declared: %v)", ids))
			return nil, "", nil, false, 3
		}
		targetID = ids[0]
	} else if _, ok := cfg.HermesTargets[targetID]; !ok {
		writeError(stderr, command, "config_invalid", "configuration",
			fmt.Sprintf("hermes target %q is not declared under hermes_targets", targetID))
		return nil, "", nil, false, 3
	}
	return cfg, targetID, &flags, refresh, 0
}

// capabilityCachePath resolves the per-target evidence cache path:
// the platform capability directory keyed by target ID.
func capabilityCachePath(targetID string) string {
	return filepath.Join(filepath.Dir(platformpaths.DefaultCapabilityReportPath()), "hermes-capability-"+targetID+".json")
}

// runHermesProbe runs the probe and writes the cache (HER-012). The
// probe always re-probes, so --refresh belongs to `hermes capabilities`
// only and is refused here at the documented usage exit (CLI-009).
func runHermesProbe(command string, args []string, stdout, stderr io.Writer) int {
	cfg, targetID, flags, refresh, code := hermesTargetSelection(command, args, stderr)
	if code != 0 {
		return code
	}
	if refresh {
		return usageError(stderr, command, "--refresh belongs to 'hermes capabilities'; the probe always re-probes")
	}
	target := cfg.HermesTargets[targetID]
	limits, err := hermesTargetProcessLimits(cfg, target)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", fmt.Sprintf("hermes_targets.%s: %v", targetID, err))
		return 3
	}
	prober, err := hermeskanban.NewProber(targetID, target.Executable, target.MinimumVersion, target.Board, flags.val("--profile"), limits)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return 3
	}
	record, err := prober.Probe(requestCtx())
	if err != nil {
		writeError(stderr, command, "hermes_executable_missing", "target_unavailable", err.Error())
		return 11
	}
	cachePath := capabilityCachePath(targetID)
	if werr := hermeskanban.WriteCapabilityRecord(record, cachePath); werr != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", fmt.Sprintf("write capability evidence: %v", werr))
		return 20
	}
	result := map[string]any{
		"target_id":          targetID,
		"fingerprint":        record.Fingerprint,
		"cache_path":         cachePath,
		"all_passed":         record.AllRequiredPassed(),
		"serialization_mode": record.EffectiveSerializationMode(),
		"shapes":             record.Shapes,
		"hermes_version":     record.HermesVersion,
	}
	var warnings []string
	if !record.AllRequiredPassed() {
		warnings = append(warnings, "capability evidence incomplete: "+incompleteDetail(record))
	}
	return writeEnvelopeWithWarnings(stdout, command, result, warnings)
}

// runHermesCapabilities prints the cached evidence, re-probing when it
// is missing, stale, or refreshed (HER-012/HER-013).
func runHermesCapabilities(command string, args []string, stdout, stderr io.Writer) int {
	cfg, targetID, flags, refresh, code := hermesTargetSelection(command, args, stderr)
	if code != 0 {
		return code
	}
	target := cfg.HermesTargets[targetID]
	cachePath := capabilityCachePath(targetID)
	record, rerr := hermeskanban.LoadCapabilityRecord(cachePath)
	stale := ""
	if rerr == nil {
		digest, derr := hermeskanban.ExecutableDigest(target.Executable)
		if derr != nil {
			// An unverifiable executable identity cannot authorize a
			// cached record: re-probe rather than serving stale
			// evidence as fresh (E11-T2 round-2 review).
			stale = "the executable identity could not be verified: " + derr.Error()
		} else {
			stale = record.StaleReasonForProfile(target.Executable, digest, "", flags.val("--profile"))
		}
	}
	if rerr != nil || stale != "" || refresh {
		limits, lerr := hermesTargetProcessLimits(cfg, target)
		if lerr != nil {
			writeError(stderr, command, "config_invalid", "configuration", fmt.Sprintf("hermes_targets.%s: %v", targetID, lerr))
			return 3
		}
		profile := flags.val("--profile")
		prober, perr := hermeskanban.NewProber(targetID, target.Executable, target.MinimumVersion, target.Board, profile, limits)
		if perr != nil {
			writeError(stderr, command, "config_invalid", "configuration", perr.Error())
			return 3
		}
		fresh, err := prober.Probe(requestCtx())
		if err != nil {
			writeError(stderr, command, "hermes_executable_missing", "target_unavailable", err.Error())
			return 11
		}
		if werr := hermeskanban.WriteCapabilityRecord(fresh, cachePath); werr != nil {
			writeError(stderr, command, "sqlite_query_failed", "storage", fmt.Sprintf("write capability evidence: %v", werr))
			return 20
		}
		record = fresh
		stale = ""
	}
	caps, cerr := record.Capabilities()
	var warnings []string
	if stale != "" {
		warnings = append(warnings, "cached capability evidence is stale: "+stale)
	}
	if cerr != nil {
		return planErr(stderr, command, "config_capability_missing", "configuration", cerr.Error(), 3)
	}
	return writeEnvelopeWithWarnings(stdout, command, map[string]any{
		"target_id":          targetID,
		"fingerprint":        record.Fingerprint,
		"cache_path":         cachePath,
		"probed_at":          record.ProbedAt,
		"hermes_version":     record.HermesVersion,
		"all_passed":         record.AllRequiredPassed(),
		"serialization_mode": record.EffectiveSerializationMode(),
		"capabilities":       caps.BoolMap(),
		"shapes":             record.Shapes,
	}, warnings)
}

func incompleteDetail(record *hermeskanban.CapabilityRecord) string {
	if len(record.MissingFlags) > 0 {
		return "missing create flags: " + joinStrings(record.MissingFlags, ", ")
	}
	return "see the per-shape details in this result"
}

func joinStrings(list []string, sep string) string {
	out := ""
	for i, s := range list {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

// runHermesSetMinimumVersion implements `hermes set-minimum-version
// <target> <version>` (E15-T2, CLI-019): the atomic target-floor update.
// Only a floor at or above the 0.20.5 product floor is accepted, only
// the selected target's minimum_version changes, and the validated
// candidate replaces the configuration atomically — unrelated routes
// and targets are preserved. The floor is behavior-affecting: every
// route binding the target changes its revision, so the result names
// each affected route and the re-probe, preflight, and production
// re-acknowledgement it now owes.
func runHermesSetMinimumVersion(command string, args []string, stdout, stderr io.Writer) int {
	// The command takes exactly two positionals beside the valued
	// --config flag (the shared parser models one positional); the ONE
	// scanner keeps the convention identical to the selector commands
	// (round-1 F004).
	positional, code := scanPositionals(command, args, map[string]bool{"--config": true}, stderr)
	if code != 0 {
		return code
	}
	if len(positional) != 2 {
		return usageError(stderr, command, "hermes set-minimum-version requires <target> <version>")
	}
	targetID, version := positional[0], positional[1]
	configPath := resolveConfigPath(selectorConfigPath(args))
	// The before-state decodes without the gates so the helper can
	// remediate a document whose current floor predates the product
	// floor (round-1 F005): a below-floor or omitted floor fails the
	// ordinary load, and the candidate's own gates below stay strict.
	raw, err := os.ReadFile(configPath)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", fmt.Sprintf("read config: %v", err))
		return 3
	}
	cfg, err := config.ParseDecoded(raw)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return 3
	}
	target, ok := cfg.HermesTargets[targetID]
	if !ok {
		writeError(stderr, command, "config_invalid", "configuration",
			fmt.Sprintf("hermes target %q is not declared under hermes_targets (declared: %v)", targetID, sortedHermesTargetIDs(cfg)))
		return 3
	}
	if target.MinimumVersion == version {
		writeError(stderr, command, "config_invalid", "configuration",
			fmt.Sprintf("hermes_targets.%s.minimum_version is already %s; nothing changed", targetID, version))
		return 3
	}
	parsed, verr := hermeskanban.ParseMinimumVersion(version)
	if verr != nil {
		writeError(stderr, command, "config_invalid", "configuration", fmt.Sprintf("hermes_targets.%s.minimum_version: %v", targetID, verr))
		return 3
	}
	if !parsed.Eligible(hermeskanban.MinimumEligibleVersion) {
		writeError(stderr, command, "config_invalid", "configuration",
			fmt.Sprintf("hermes_targets.%s.minimum_version %s is below the support floor %s; only floors at or above it are accepted (CLI-019)", targetID, version, config.MinimumEligibleHermesVersion))
		return 3
	}
	// The affected routes owe fresh probe, preflight, and production
	// re-acknowledgement: their revisions change through the floor's
	// participation in the target projection (CON-014 posture of the
	// same rule).
	var affected []string
	before := map[string]string{}
	for _, routeID := range cfg.SortedRouteIDs() {
		route := cfg.Routes[routeID]
		binds := false
		for _, dest := range route.Destinations {
			if dest.Target == targetID {
				binds = true
				break
			}
		}
		if !binds {
			continue
		}
		revision, ok := config.RouteRevision(cfg, routeID)
		if !ok {
			writeError(stderr, command, "internal_unclassified", "internal", "route revision could not be computed")
			return 40
		}
		before[routeID] = revision
		affected = append(affected, routeID)
	}
	// The repair path (CLI-019's remediation purpose): a document whose
	// current floor predates the 0.20.5 product floor — a below-floor
	// value or an omitted field — fails the ordinary load, so the
	// mutation decodes without the gates and must pass every gate as a
	// CANDIDATE before the atomic replacement (round-1 F005).
	if err := config.MutateHermesTargetRepair(configPath, targetID, func(t *config.HermesTarget) error {
		t.MinimumVersion = version
		return nil
	}); err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return 3
	}
	updated, err := config.Load(configPath)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return 3
	}
	routeEntries := []map[string]any{}
	for _, routeID := range affected {
		revision, _ := config.RouteRevision(updated, routeID)
		routeEntries = append(routeEntries, map[string]any{
			"route_id":        routeID,
			"revision_before": before[routeID],
			"revision_after":  revision,
			"acknowledgement": "stale",
		})
	}
	return writeEnvelope(stdout, command, map[string]any{
		"target_id":        targetID,
		"minimum_version":  version,
		"previous_version": target.MinimumVersion,
		"config_path":      configPath,
		"affected_routes":  routeEntries,
		"note":             "the floor update changed every affected route revision; each owes a fresh probe, preflight, and production re-acknowledgement before submission",
	})
}
