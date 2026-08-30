package cli

import (
	"fmt"
	"io"
	"strings"
)

// The CLI-009 help contract (E11-T4): every root and group parser
// accepts -h and --help, and the help text states the subcommands, the
// required flags, the defaults, the output modes, the exit codes, the
// side effects, the approvals, one example, and the next safe command.

// helpExit is the stable exit for a successful help render (the
// error-model's usage class succeeds at showing help).
const helpExit = 0

// rootHelp is the root command's discoverability text.
const rootHelp = `agent-dispatch — durable vault-to-Hermes dispatch (v0.1.5)

Usage: agent-dispatch <command> [flags]

Commands:
  version        Print the build and schema versions.
  init           Write a disabled example configuration for this machine.
  config         validate (with optional live probes) or show the configuration.
  route          plan, list, show, enable, disable, stale, preflight,
                 set-profile, set-skills.
  watchman       install, status, remove, test — the managed trigger lifecycle.
  dispatch       Plan and submit one Watchman arrival (the trigger entrypoint).
  dispatches     list, show, retry, reprocess, rerun, refresh, drain, discard.
  receipts       list and show acceptance and execution evidence.
  events         show — aggregate-event inspection with per-child evidence.
  notifications  test, list, retry, drain — the notification delivery surface.
  work           begin, complete, fail — the Hermes companion receipt surface.
  quarantine     list, release, discard — held-path operator exits.
  reconcile      Run a full-scope reconciliation generation.
  status         Route, queue, quarantine, target, and drift snapshot.
  doctor         Findings examination over configuration, store, and integrations.
  maintenance    backup, integrity, prune.
  hermes         probe, capabilities, profiles — the public-interface probes.
  setup          wiki — the guided disabled setup walkthrough.
  completion     Emit the shell completion script.

Global flags: --config <path> (default: platform config path),
  --state-dir <abs> , --log-level <warn|info|debug>, --trace-id <id>,
  --timeout <duration>.

Output modes: --output human (default) or json (the agent-dispatch.cli/v1
envelope; empty collections serialize as [] or {}).

Exit codes: 0 success; 2 usage; 3 configuration; 4 input rejected;
10 transient; 11 target unavailable; 12 target rejected;
13 acceptance unknown; 14 conflict; 20 storage; 21 migration;
30 security; 40 internal.

Side effects and approvals: only ` + "`dispatch`" + `, ` + "`dispatches drain`" + `,
` + "`reconcile --submit`" + `, and the watchman lifecycle touch the outside world;
` + "`route enable`" + ` additionally requires the two-key production gate
(--acknowledge-production-gate <computed-revision> --yes).

Example:
  agent-dispatch init --resource-root ~/Documents/Obsidian/MainVault
  agent-dispatch config validate --probe-targets

Next safe command: agent-dispatch setup wiki`

// commandHelp is each group's help text keyed by command name. Every
// entry states the subcommands, key flags, side effects, and the next
// safe command (CLI-009).
var commandHelp = map[string]string{
	"version": `version — print the build and schema versions

Usage: agent-dispatch version [--output json]

Flags: --output human|json (default human).

Exit codes: 0.

Side effects: none.

Next safe command: agent-dispatch doctor`,
	"init": `init — write a disabled example configuration

Usage: agent-dispatch init [--resource-root <path>] [--instance-id <id>] [--state-dir <abs>]

Flags: --resource-root (default ~/Documents/Obsidian/MainVault),
  --instance-id (default the hostname), --state-dir (default platform).

Exit codes: 0; 3 configuration/placement.

Side effects: writes the config file (refuses to overwrite) and creates
the owner-only state directory. The written routes stay disabled.

Example:
  agent-dispatch init --resource-root /srv/vault

Next safe command: agent-dispatch config validate`,
	"config": `config — validate or show the configuration

Usage: agent-dispatch config <validate|show> [flags]

Subcommands:
  validate   Schema and semantic validation; --probe-targets adds the
             read-only live probes (eligibility for hermes targets, the
             static declaration for webhook targets).
  show       The redacted normalized projection with the computed route
             revisions.

Flags: --config <path>.

Exit codes: 0; 3 configuration (a probe defect is exit 3; an
unreachable target is a warning).

Side effects: none — validation never submits.

Example:
  agent-dispatch config validate --probe-targets

Next safe command: agent-dispatch hermes probe`,
	"route": `route — the route lifecycle surface

Usage: agent-dispatch route <subcommand> [flags]

Subcommands:
  plan         The dry plan for one Watchman arrival (stdin payload).
  list         The materialized routes.
  show         One route's runtime projection.
  enable       The two-key production gate (--acknowledge-production-gate
               <computed-revision> --yes; binds the capability
               fingerprint and refuses unresolved legacy work).
  disable      Immediately prevent new submissions.
  stale        Move a stale-active route to UNCERTAIN (--reason required).
  preflight    Prove every destination executable: profile, skills,
               workspace, mutex, hints, notifications, binding.
  set-profile  <route>:<destination> <profile> (omit the qualifier only
               with exactly one destination).
  set-skills   <route>:<destination> <skill>...

Exit codes: 0; 2 usage (an ambiguous route-only edit); 3 configuration;
14 conflict.

Side effects: enable/disable/stale change activation state; the
qualified edits rewrite the config atomically and pause the route
revision. plan and preflight never submit.

Example:
  agent-dispatch route preflight --route wiki

Next safe command: agent-dispatch route enable (after preflight passes)`,
	"watchman": `watchman — the managed trigger lifecycle

Usage: agent-dispatch watchman <install|status|remove|test> [flags]

Subcommands:
  install   Install the managed trigger for one route (config required).
  status    The effective binding, patterns, and trigger state.
  remove    Remove the installed trigger (--yes).
  test      Deliver a synthetic payload through the real trigger.

Flags: --config <path>, --route <id>, --yes (remove).

Exit codes: 0; 3 configuration; 20 storage.

Side effects: install/remove change the live Watchman state; test
delivers one synthetic payload (it dispatches only with the route
enabled).

Example:
  agent-dispatch watchman install --route wiki

Next safe command: agent-dispatch watchman status`,
	"dispatch": `dispatch — plan and submit one Watchman arrival

Usage: agent-dispatch dispatch --route <id> --input watchman [flags]

Flags: --route <id> (required), --input watchman (required),
  --no-submit (plan only), --dry-run.

Exit codes: 0; 3 configuration (including the capability fingerprint
block and an invalid fan-out record); 4 input rejected; 11 target
unavailable; 14 conflict; 20 storage.

Side effects: persists the observation, batch, and intent; submits to
the target unless --no-submit. The stdin payload is untrusted input and
never reaches the task text.

Example:
  agent-dispatch dispatch --route wiki --input watchman --no-submit

Next safe command: agent-dispatch dispatches list --route wiki`,
	"dispatches": `dispatches — the durable intent surface

Usage: agent-dispatch dispatches <list|show|retry|reprocess|rerun|refresh|drain|discard> [flags]

Key subcommands:
  list       Filter by route, state, age, external ref, or causal prefix.
  show       The full redacted lineage of one dispatch.
  retry      Re-arm dead-lettered work (--reason required).
  drain      Recover expired leases, reconcile unknowns, submit due work.
  discard    Close dead-lettered work as superseded (--reason required).

Flags: --route, --state, --limit, --offset, --reason.

Exit codes: 0; 2 usage; 3 configuration; 14 conflict; 20 storage.

Side effects: retry/drain/rerun/refresh may submit or reconcile;
discard closes work.

Example:
  agent-dispatch dispatches list --route wiki --state unknown

Next safe command: agent-dispatch dispatches show <id>`,
	"receipts": `receipts — acceptance and execution evidence

Usage: agent-dispatch receipts <list|show> [flags]

Flags: --dispatch <id>, --kind acceptance|execution_projection|work,
  --limit, --offset.

Exit codes: 0; 3 configuration; 20 storage.

Side effects: none — receipts are read-only evidence.

Example:
  agent-dispatch receipts list --dispatch <id>

Next safe command: agent-dispatch work begin --help`,
	"events": `events — aggregate-event inspection

Usage: agent-dispatch events <show> [flags]

Flags: --config; show takes one aggregate event ID.

Exit codes: 0; 2 usage; 3 configuration; 4 not found; 20 storage.

Side effects: none — events are read-only inspection over one
occurrence's aggregate and its per-destination children (acceptance,
execution, work receipt, retry, and completion-evidence projections;
the aggregate status is the worst child class: evidence-gap,
manual-intervention, failed, in-progress, completed).

Example:
  agent-dispatch events show <aggregate-id>

Next safe command: agent-dispatch receipts list --kind work`,
	"notifications": `notifications — the notification delivery surface

Usage: agent-dispatch notifications <test|list|retry|drain> [flags]

Flags: --config; test takes --route and --sink; list takes --route,
--state, --sink, and --limit; retry takes one notification ID; drain
takes --limit.

Exit codes: 0; 2 usage; 3 configuration; 4 not found or already
  delivered (the retry command only: an unknown notification id, or
  retrying one already delivered); 20 storage. Delivery outcomes are
  data, never exit codes.

Side effects: test delivers one transport-level probe (nothing stored,
no source event, no Hermes task — NTF-008); list is read-only; retry
re-arms one refused notification and performs one attempt under its
stable idempotency identity; drain evaluates the configured drift
classes per route (integration and Watchman drift enqueue their
intents exactly once per appearance), then performs one bounded
delivery attempt per pending notification — ambiguous and retryable
outcomes stay pending for the next pass (NTF-007), and no delivery
outcome ever changes dispatch or work state (NTF-005).

Example:
  agent-dispatch notifications drain --config <path>

Next safe command: agent-dispatch notifications list --state pending`,
	"work": `work — the Hermes companion receipt surface

Usage: agent-dispatch work <begin|complete|fail> [flags]

Flags: --dispatch-id (required), --run-id, --external-task-id,
  --reason (fail), --manifest (bounded path evidence), and for
  complete: --status completed|partially_completed|blocked (default
  completed), --remaining-manifest (partially_completed, or the v2
  document's own remaining scope), --manual-reason (blocked, or the v2
  document's own manual reason).

Exit codes: 0; 2 usage; 3 configuration; 4 rejected receipt (invalid
evidence, unknown dispatch or run, unknown document version, status
conflict); 14 conflict (generation fence, state guards); 20 storage.

Side effects: records the work receipt beside the dispatch lineage.

Example:
  agent-dispatch work begin --dispatch-id <id> --run-id run-1

Next safe command: agent-dispatch receipts list --kind work`,
	"quarantine": `quarantine — held-path operator exits

Usage: agent-dispatch quarantine <list|release|discard> [flags]

Flags: --state held|released|discarded|superseded, --reason
  (release/discard required).

Exit codes: 0; 2 usage; 14 conflict; 20 storage.

Side effects: release re-plans the held batch; discard closes it.

Example:
  agent-dispatch quarantine list --state held

Next safe command: agent-dispatch quarantine release <id> --reason ...`,
	"reconcile": `reconcile — a full-scope reconciliation generation

Usage: agent-dispatch reconcile --route <id> --reason <text> [flags]

Flags: --route (required), --reason (required), --submit (submit the
  generation; default dry enumeration), --baseline-only (the documented
  disabled-route operation: establish or refresh the initial snapshot
  and its route baseline record while the route stays disabled).

--baseline-only runs only when the route is disabled in both halves of
the production gate (configuration key off and runtime activation
disabled) and refuses active, uncertain, quarantined, or production-
enabled state. It atomically stores the bounded snapshot and the
baseline evidence in one observation-fenced transaction, creates no
policy decision, dispatch intent, Hermes task, acknowledgement, or
notification, has no submit path (combining it with --submit is a usage
error), and is safely rerunnable — a crashed attempt converges on the
next run.

Exit codes: 0; 2 usage (including --baseline-only with --submit); 3
  configuration (including an invalid fan-out record); 11 target
  unavailable; 14 conflict (including baseline refusals); 20 storage
  (including a reconcile sibling lane whose child commit failed); 40
  internal (failures the command could not attribute).

Side effects: with --submit, enumerates the vault and submits the
latest-state request; with --baseline-only, stores only the snapshot
and the route baseline record; without either, only the dry enumeration
persists.

Example:
  agent-dispatch reconcile --route wiki --reason initial --baseline-only

Next safe command: agent-dispatch status`,
	"status": `status — the operational snapshot

Usage: agent-dispatch status [flags]

Flags: --config <path>, --output human|json.

Exit codes: 0; 3 configuration; 20 storage.

Side effects: none — observational.

The JSON result carries routes, queues, quarantine, the oldest
unresolved dispatch, the database size, the per-target summary, and the
per-route OPS-013 drift projection (capability, profile, skill,
watchman, reconciliation).

Next safe command: agent-dispatch doctor`,
	"doctor": `doctor — the findings examination

Usage: agent-dispatch doctor [--probe-targets] [--integrity quick|full]

Flags: --probe-targets (add the live target probes),
  --integrity (default quick).

Exit codes: 0; 3 when any finding has error severity.

Side effects: none — the store is examined without migrating.

Example:
  agent-dispatch doctor --probe-targets --integrity full

Next safe command: resolve each error-severity finding`,
	"maintenance": `maintenance — backup, integrity, prune

Usage: agent-dispatch maintenance <backup|integrity|prune> [flags]

Subcommands:
  backup     One VACUUM INTO snapshot (--output <path>).
  integrity  quick|full database integrity.
  prune      Apply the retention policy (deletes expired evidence).

Exit codes: 0; 20 storage.

Side effects: backup writes a file; prune deletes expired rows.

Example:
  agent-dispatch maintenance backup --output /secure/state.db

Next safe command: agent-dispatch maintenance integrity --full`,
	"hermes": `hermes — the public-interface probes

Usage: agent-dispatch hermes <probe|capabilities|profiles> [flags]

Subcommands:
  probe          Run every shape probe and write the evidence cache
                 (--target <id>, --profile <profile>).
  capabilities   Print the cached record (--refresh re-probes).
  profiles       List the public profiles with on-disk status
                 (--target <id>).

Exit codes: 0; 2 usage (--refresh belongs to capabilities); 3
configuration (incomplete evidence); 11 target unavailable.

Side effects: probe/capabilities write the owner-only machine-local
evidence cache; no Hermes state is ever touched.

Example:
  agent-dispatch hermes probe --profile wiki-maintainer

Next safe command: agent-dispatch route preflight --route wiki`,
	"setup": `setup — the guided disabled setup

Usage: agent-dispatch setup wiki [--config <path>] [--route <id>]

The guided walkthrough: with a named configuration it validates it (an
enabled base becomes a re-runnable disabled draft beside it);
without one it prompts for the vault root and generates a fresh
disabled example. The route is chosen explicitly: --route names a
declared route, a single-route configuration selects its only route,
and multiple routes require the flag or an explicit interactive
choice — a non-interactive multi-route invocation fails rather than
choosing one silently. Every route-scoped step, printed Watchman
command, and the final enable command name the selected route. It
validates, probes Hermes, preflights the destination, checks the
Watchman binding state (printing the explicit install and test
commands), and runs the initial dry reconciliation. It stops before
enablement and prints the exact production-gate command; it never
accepts production approval implicitly and never installs the Watchman
trigger itself.

Exit codes: 0; 2 usage (unknown route, or multiple routes without a
selection); 3 configuration.

Side effects: writes the (disabled) configuration and the state store;
nothing is submitted and no Hermes state is touched.

Next safe command: the printed route enable command (review it first)`,
	"completion": `completion — emit the shell completion script

Usage: agent-dispatch completion <bash|zsh>

Exit codes: 0; 2 usage.

Side effects: none.

Next safe command: agent-dispatch version`,
}

// runHelp renders the help contract for the root or one command group.
// The renderer is stream-safe (pure text on stdout) and exits 0: help
// is a successful discovery surface, distinct from the usage-error
// class (CLI-009).
func runHelp(command string, stdout io.Writer) int {
	if command == "" {
		fmt.Fprint(stdout, rootHelp+"\n")
		return helpExit
	}
	text, ok := commandHelp[command]
	if !ok {
		fmt.Fprintf(stdout, "agent-dispatch %s — no help text is registered; run 'agent-dispatch --help'\n", command)
		return helpExit
	}
	fmt.Fprint(stdout, text+"\n")
	return helpExit
}

// helpGroupRequest extracts the group whose help was requested from a
// raw argv (the group is the token before the first flag).
func helpGroupRequest(args []string) string {
	if len(args) == 0 {
		return ""
	}
	if strings.HasPrefix(args[0], "-") {
		return ""
	}
	return args[0]
}
