package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rootkernel/jjukkumi/internal/adapters/localfs"
	"github.com/rootkernel/jjukkumi/internal/adapters/watchman"
	"github.com/rootkernel/jjukkumi/internal/app/dispatch"
	"github.com/rootkernel/jjukkumi/internal/app/ingest"
	"github.com/rootkernel/jjukkumi/internal/config"
	"github.com/rootkernel/jjukkumi/internal/domain/policy"
	"github.com/rootkernel/jjukkumi/internal/platformpaths"
)

// runRoute implements the route command tree (cli-spec §3); this build
// implements the side-effect-free `route plan` subcommand. Route
// runtime-management subcommands (list/show/enable/disable) arrive with
// the E3 dispatch core.
// knownRouteSubcommands is the published route command tree (cli-spec
// §2). Only `plan` is implemented in this build.
var knownRouteSubcommands = map[string]bool{
	"plan": true, "list": true, "show": true, "enable": true, "disable": true,
}

func runRoute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "route", "route requires a subcommand; this build implements 'route plan'")
	}
	if args[0] != "plan" {
		if knownRouteSubcommands[args[0]] {
			writeError(stderr, "route "+args[0], "command_not_implemented", "usage",
				fmt.Sprintf("route %q is not implemented in this build; only 'route plan' is", args[0]))
			return 2
		}
		return usageError(stderr, "route", fmt.Sprintf("unknown route subcommand %q; this build implements 'route plan'", args[0]))
	}
	return runPlan("route plan", args[1:], stdout, stderr)
}

// runDispatch implements `dispatch` (cli-spec §5). Only `--dry-run` is
// implemented in this build: the default persist-and-submit path and
// `--no-submit` arrive with the E3 durable dispatch core.
func runDispatch(args []string, stdout, stderr io.Writer) int {
	dryRun := false
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		default:
			rest = append(rest, args[i])
		}
	}
	if !dryRun {
		return planErr(stderr, "dispatch", "command_not_implemented", "usage",
			"only --dry-run is implemented in this build; durable dispatch arrives with the E3 core", 2)
	}
	return runPlan("dispatch", rest, stdout, stderr)
}

// planOptions carries the shared planning flags.
type planOptions struct {
	routeID    string
	configPath string
	jsonOutput bool
}

func parsePlanFlags(command string, args []string, stdout, stderr io.Writer) (*planOptions, int) {
	opts := &planOptions{jsonOutput: true}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--route":
			if i+1 >= len(args) {
				return nil, usageError(stderr, command, "--route requires an id")
			}
			i++
			opts.routeID = args[i]
		case "--config":
			if i+1 >= len(args) {
				return nil, usageError(stderr, command, "--config requires a path")
			}
			i++
			opts.configPath = args[i]
		case "--input":
			if i+1 >= len(args) || args[i+1] != "watchman" {
				return nil, usageError(stderr, command, "--input requires 'watchman' in this build")
			}
			i++
		case "--output=json":
			opts.jsonOutput = true
		case "--output=human":
			return nil, usageError(stderr, command, fmt.Sprintf("%s prints the structured dispatch plan; only --output json is supported (cli-spec §3)", command))
		case "--output", "-o":
			if i+1 >= len(args) || args[i+1] != "json" {
				return nil, usageError(stderr, command, "--output requires 'json' for this command")
			}
			i++
			opts.jsonOutput = true
		default:
			return nil, usageError(stderr, command, fmt.Sprintf("unknown argument %q", args[i]))
		}
	}
	if opts.routeID == "" {
		return nil, usageError(stderr, command, "--route is required")
	}
	return opts, 0
}

// resolveConfigPath applies the shared configuration-path precedence
// (cli-spec §1): explicit path, then JJUKKUMI_CONFIG, then the platform
// default.
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("JJUKKUMI_CONFIG"); env != "" {
		return env
	}
	return platformpaths.DefaultConfigPath()
}

// runPlan plans one Watchman invocation end to end with no SQLite
// mutation and no target call: parse stdin, validate the trusted
// environment binding, normalize the batch against trusted route
// configuration, and evaluate the pure policy planner. The route
// revision is recomputed from the loaded configuration and carried in
// the plan (POL-007, POL-008, SEC-010: a consumer must revalidate it
// before any side effect).
func runPlan(command string, args []string, stdout, stderr io.Writer) int {
	opts, code := parsePlanFlags(command, args, stdout, stderr)
	if code != 0 {
		return code
	}
	opts.configPath = resolveConfigPath(opts.configPath)
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	route, ok := cfg.Routes[opts.routeID]
	if !ok {
		return planErr(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", opts.routeID), 3)
	}
	resource, ok := cfg.Resources[route.Source.Resource]
	if !ok {
		return planErr(stderr, command, "config_invalid", "configuration", fmt.Sprintf("resource %q is not defined", route.Source.Resource), 3)
	}
	target, ok := cfg.Targets[route.Dispatch.Target]
	if !ok {
		return planErr(stderr, command, "config_invalid", "configuration", fmt.Sprintf("target %q is not defined", route.Dispatch.Target), 3)
	}
	revision, ok := config.RouteRevision(cfg, opts.routeID)
	if !ok {
		return planErr(stderr, command, "internal_unclassified", "internal", "route revision could not be computed", 40)
	}

	env, err := watchman.ParseEnv(os.LookupEnv)
	if err != nil {
		return planErr(stderr, command, "source_missing_required_metadata", "input_rejected", err.Error(), 4)
	}
	if err := watchman.ValidateBinding(env, route.Source.TriggerName, resource.Root); err != nil {
		return planErr(stderr, command, "source_binding_mismatch", "input_rejected", err.Error(), 4)
	}

	maxStdin := int64(0)
	if cfg.Limits.MaxStdinBytes != nil {
		maxStdin = *cfg.Limits.MaxStdinBytes
	} else {
		maxStdin = watchman.DefaultMaxStdinBytes
	}
	input, err := watchman.ReadInput(os.Stdin, env, maxStdin)
	if err != nil {
		if errors.Is(err, watchman.ErrStdinTooLarge) {
			return planErr(stderr, command, "source_input_too_large", "input_rejected", err.Error(), 4)
		}
		return planErr(stderr, command, "source_malformed_json", "input_rejected", err.Error(), 4)
	}

	engine, err := newPatternEngine(route)
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	resolver, err := localfs.NewResolver(resource.Root)
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	maxHash := int64(0)
	if cfg.Limits.MaxHashFileBytes != nil {
		maxHash = *cfg.Limits.MaxHashFileBytes
	} else {
		maxHash = 64 << 20
	}

	batch, err := ingest.BuildBatch(input.Entries, engine, resolver, ingest.NoFacts{}, env.Flags(), route.Source.Resource, ingest.Options{MaxHashBytes: maxHash})
	if err != nil {
		if errors.Is(err, ingest.ErrUnsafePath) {
			return planErr(stderr, command, "source_unsafe_path", "security", err.Error(), 30)
		}
		return planErr(stderr, command, "internal_unclassified", "internal", err.Error(), 40)
	}

	plan, err := dispatch.Evaluate(dispatch.RoutePolicy{
		RouteID:              opts.routeID,
		RouteRevision:        revision,
		ResourceID:           route.Source.Resource,
		AutomaticThreshold:   route.Batching.AutomaticThreshold,
		HardLimit:            route.Batching.HardLimit,
		MaxManifestBytes:     route.Batching.MaxManifestBytes,
		BulkAction:           route.Policy.BulkAction,
		OverflowAction:       route.Policy.OverflowAction,
		FreshInstanceAction:  route.Policy.FreshInstanceAction,
		RequiredCapabilities: target.RequiredCapabilities,
	}, dispatch.Input{Batch: batch, Flags: env.Flags()})
	if err != nil {
		return planErr(stderr, command, "internal_unclassified", "internal", err.Error(), 40)
	}

	// POL-008/SEC-010: revalidate the plan's revision against the active
	// snapshot before emitting it; single-shot today, load-bearing when
	// persistence arrives.
	if plan.Route.Revision != revision {
		return planErr(stderr, command, "internal_unclassified", "internal", "plan revision does not match the active route revision", 40)
	}
	if !opts.jsonOutput {
		opts.jsonOutput = true // machine output only (CLI-001)
	}
	return writeEnvelope(stdout, command, plan)
}

// newPatternEngine compiles the route's pattern sets with the case mode
// resolved identically to the route revision (config.CaseMode), so the
// engine's behavior and the revision's recorded mode cannot diverge.
func newPatternEngine(route config.Route) (*policy.Engine, error) {
	mode := policy.CaseSensitive
	if config.CaseMode() == "insensitive" {
		mode = policy.CaseInsensitive
	}
	return policy.NewEngine(route.Source.Include, route.Source.Exclude, route.Policy.Protected, route.Policy.Immutable, mode)
}

// planErr writes one error envelope and returns the exit code.
func planErr(w io.Writer, command, code, category, message string, exit int) int {
	writeError(w, command, code, category, message)
	return exit
}
