// Package doctor implements the operational health examination
// (OPS-005, observability-and-operations §7): one collector over the
// configuration, the durable store, the runtime route state, and the
// external integrations, producing stable-coded findings with severity,
// summary, details, and remediation (cli-spec §10). Findings are data:
// the command always succeeds at producing them.
package doctor

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Severity orders findings for operators: info observations, warning
// recoverable uncertainty, error conditions that block correct
// operation.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Finding is one doctor observation (cli-spec §10).
type Finding struct {
	Code        string   `json:"code"`
	Severity    Severity `json:"severity"`
	Summary     string   `json:"summary"`
	Details     string   `json:"details,omitempty"`
	Remediation string   `json:"remediation,omitempty"`
}

// RouteFact carries one route's durable runtime facts.
type RouteFact struct {
	RouteID           string
	ResourceID        string
	TargetID          string
	ActivationState   string
	RouteState        string
	ActiveDispatchID  string
	ActiveDispatchAge time.Duration // age of the active dispatch's last update; zero when none
	DirtyGeneration   int64
	PendingReconcile  bool
	LastReconciledAt  string        // "" when never
	ReconciledAge     time.Duration // age of the last reconciliation; zero when never
	DailyExpected     bool
	StaleActiveAfter  time.Duration // configured bound; zero when unset
}

// StoreFact carries the durable store's health facts.
type StoreFact struct {
	OpenError      string
	JournalMode    string
	SchemaVersion  int
	LatestVersion  int
	IntegrityError string
	StaleLeases    []string
	UnknownCount   int64
	DeadLettered   int64
	DatabaseBytes  int64
}

// TargetFact carries one target's configuration-level facts.
type TargetFact struct {
	TargetID string
	Type     string
	// GateError is the construction-gate failure (capability mismatch,
	// invalid endpoint or executable shape), "" when the target passes.
	GateError string
	// SecretResolved reports whether the secret reference resolves
	// without printing the value; absent for targets without one.
	SecretResolved *bool
}

// ResourceFact carries one resource root's filesystem facts.
type ResourceFact struct {
	ResourceID   string
	Root         string
	Missing      bool
	NotReadable  bool
	NotOwnerOnly bool
}

// WatchmanFact carries the Watchman probe result.
type WatchmanFact struct {
	Available       bool
	Version         string
	UnusableBecause string
}

// Input is everything one examination observes. StoreExamined is false
// when the store could not be opened (or configuration failed before
// it was tried): the store-dependent findings are then skipped instead
// of fabricated.
type Input struct {
	ConfigPath     string
	SemanticErrors []string
	Routes         []RouteFact
	Store          StoreFact
	StoreExamined  bool
	Targets        []TargetFact
	Resources      []ResourceFact
	Watchman       WatchmanFact
	// WatchmanExamined suppresses every Watchman finding when the probe
	// never ran (a configuration that failed to load examines nothing —
	// an unavailable posture must not be invented, E8-T4/H-4).
	WatchmanExamined bool
	Now              string
}

// largeDatabaseBytes is the size above which doctor warns about
// retention pressure (observability-and-operations §7).
const largeDatabaseBytes = 512 * 1024 * 1024

// Examine collects the findings for one input. Findings are sorted by
// severity (error, warning, info) then code for stable output.
func Examine(in Input) []Finding {
	// An unexamined Watchman surface never produces findings: the
	// unavailable posture is a probe result, not a default (E8-T4, H-4);
	// the WatchmanExamined gate at the finding site enforces it.
	var out []Finding
	for _, err := range in.SemanticErrors {
		out = append(out, Finding{
			Code: "config_invalid", Severity: SeverityError,
			Summary:     "Configuration fails validation",
			Details:     err,
			Remediation: "fix the configuration document; see agent-dispatch config validate",
		})
	}
	for _, r := range in.Resources {
		if r.Missing {
			out = append(out, Finding{
				Code: "resource_root_missing", Severity: SeverityError,
				Summary:     fmt.Sprintf("resource %s root does not exist", r.ResourceID),
				Details:     r.Root,
				Remediation: "create the resource root or correct resources." + r.ResourceID + ".root",
			})
			continue
		}
		if r.NotReadable {
			out = append(out, Finding{
				Code: "resource_root_not_readable", Severity: SeverityError,
				Summary:     fmt.Sprintf("resource %s root is not readable", r.ResourceID),
				Details:     r.Root,
				Remediation: "restore read access to the resource root",
			})
		}
		if r.NotOwnerOnly {
			out = append(out, Finding{
				Code: "resource_root_permissions", Severity: SeverityWarning,
				Summary:     fmt.Sprintf("resource %s root is readable beyond the owner", r.ResourceID),
				Details:     r.Root,
				Remediation: "SEC-008: restrict the root to owner-only unless broader reading is intended",
			})
		}
	}
	if in.Store.OpenError != "" {
		out = append(out, Finding{
			Code: "sqlite_open_failed", Severity: SeverityError,
			Summary:     "The durable store cannot be opened",
			Details:     in.Store.OpenError,
			Remediation: "resolve the storage condition (path, permissions, migration) before any operation",
		})
		// Store-dependent checks cannot run against an unopened store.
		return sortFindings(out)
	}
	if in.Store.IntegrityError != "" {
		out = append(out, Finding{
			Code: "sqlite_integrity_failed", Severity: SeverityError,
			Summary:     "Database integrity check failed",
			Details:     in.Store.IntegrityError,
			Remediation: "stop submission, restore a verified backup, preserve the corrupt copy (failure-recovery §1)",
		})
	}
	if in.Store.JournalMode != "" && in.Store.JournalMode != "wal" {
		out = append(out, Finding{
			Code: "journal_mode_unexpected", Severity: SeverityWarning,
			Summary:     "Database journal mode is not WAL",
			Details:     "journal_mode=" + in.Store.JournalMode,
			Remediation: "OPS-008 expects WAL for concurrent one-shot processes; verify the database was opened by this build",
		})
	}
	if in.Store.SchemaVersion != in.Store.LatestVersion {
		out = append(out, Finding{
			Code: "migration_pending", Severity: SeverityError,
			Summary:     "Database schema version is not current",
			Details:     fmt.Sprintf("schema version %d, latest %d", in.Store.SchemaVersion, in.Store.LatestVersion),
			Remediation: "run any agent-dispatch command once to apply pending migrations (OPS-009)",
		})
	}
	if len(in.Store.StaleLeases) > 0 {
		out = append(out, Finding{
			Code: "stale_attempt_lease", Severity: SeverityWarning,
			Summary:     fmt.Sprintf("%d dispatch intents hold expired attempt leases", len(in.Store.StaleLeases)),
			Details:     strings.Join(bounded(in.Store.StaleLeases, 20), ", "),
			Remediation: "run agent-dispatch dispatches drain to recover expired submitting intents",
		})
	}
	if in.Store.UnknownCount > 0 {
		out = append(out, Finding{
			Code: "unknown_dispatches", Severity: SeverityWarning,
			Summary:     fmt.Sprintf("%d dispatch intents have unknown delivery", in.Store.UnknownCount),
			Remediation: "resolve each through agent-dispatch dispatches drain and operator retry (DUR-006)",
		})
	}
	if in.Store.DeadLettered > 0 {
		out = append(out, Finding{
			Code: "dead_lettered_dispatches", Severity: SeverityWarning,
			Summary:     fmt.Sprintf("%d dispatch intents are dead-lettered", in.Store.DeadLettered),
			Remediation: "inspect agent-dispatch dispatches list --state dead_lettered and retry or rerun each",
		})
	}
	if in.Store.DatabaseBytes > largeDatabaseBytes {
		out = append(out, Finding{
			Code: "database_size_large", Severity: SeverityInfo,
			Summary:     fmt.Sprintf("Database is %d bytes", in.Store.DatabaseBytes),
			Remediation: "review retention policy and run agent-dispatch maintenance prune",
		})
	}
	if in.WatchmanExamined && !in.Watchman.Available {
		out = append(out, Finding{
			// AC-502 names Watchman unavailability as an actionable
			// error condition: triggers cannot fire at all (E8-T4, H-4).
			Code: "watchman_unavailable", Severity: SeverityError,
			Summary:     "Watchman is not reachable",
			Details:     in.Watchman.UnusableBecause,
			Remediation: "install or start Watchman; sensing continues but triggers will not fire",
		})
	} else if in.Watchman.Version != "" && in.Watchman.UnusableBecause != "" {
		out = append(out, Finding{
			Code: "watchman_version_unsupported", Severity: SeverityWarning,
			Summary:     "Watchman version is outside the verified range",
			Details:     in.Watchman.UnusableBecause,
			Remediation: "install a verified Watchman version (see the E0-T5 report)",
		})
	}
	for _, t := range in.Targets {
		if t.GateError != "" {
			out = append(out, Finding{
				// A failing construction gate is an AC-502 error
				// condition: the route can never submit against this
				// target (E8-T4, H-4).
				Code: "target_gate_failed", Severity: SeverityError,
				Summary:     fmt.Sprintf("target %s fails its construction gate", t.TargetID),
				Details:     t.GateError,
				Remediation: "correct the target configuration or its capability requirements (HER-005)",
			})
		}
		if t.SecretResolved != nil && !*t.SecretResolved {
			out = append(out, Finding{
				Code: "secret_unresolvable", Severity: SeverityWarning,
				Summary:     fmt.Sprintf("target %s secret reference does not resolve", t.TargetID),
				Remediation: "provide the referenced secret (environment variable, file, descriptor, or keychain item); the value is never printed",
			})
		}
	}
	for _, r := range in.Routes {
		if r.StaleActiveAfter > 0 && r.ActiveDispatchID != "" && r.ActiveDispatchAge > r.StaleActiveAfter {
			out = append(out, Finding{
				Code: "stale_active_route", Severity: SeverityWarning,
				Summary:     fmt.Sprintf("route %s holds an active dispatch older than active_stale_after", r.RouteID),
				Details:     "active dispatch " + r.ActiveDispatchID + ", age " + r.ActiveDispatchAge.String(),
				Remediation: "inspect the dispatch (dispatches show): drain recovers expired leases and reconciles unknown delivery, dispatches retry re-arms dead-lettered work, and accepted work is completed through work begin/work complete",
			})
		}
		switch {
		case r.DailyExpected && r.LastReconciledAt == "":
			out = append(out, Finding{
				Code: "reconciliation_never_run", Severity: SeverityInfo,
				Summary:     fmt.Sprintf("route %s expects daily reconciliation but has never reconciled", r.RouteID),
				Remediation: "run agent-dispatch reconcile --route " + r.RouteID + " --reason manual and install the daily schedule (OPS-007)",
			})
		case r.DailyExpected && r.ReconciledAge > 25*time.Hour:
			out = append(out, Finding{
				Code: "reconciliation_overdue", Severity: SeverityWarning,
				Summary:     fmt.Sprintf("route %s daily reconciliation is overdue (last run %s ago)", r.RouteID, r.ReconciledAge.Round(time.Hour)),
				Remediation: "run agent-dispatch reconcile --route " + r.RouteID + " --reason manual and verify the platform schedule (OPS-007)",
			})
		}
	}
	return sortFindings(out)
}

// sortFindings orders by severity then code for stable output.
func sortFindings(f []Finding) []Finding {
	rank := map[Severity]int{SeverityError: 0, SeverityWarning: 1, SeverityInfo: 2}
	sort.SliceStable(f, func(i, j int) bool {
		if rank[f[i].Severity] != rank[f[j].Severity] {
			return rank[f[i].Severity] < rank[f[j].Severity]
		}
		return f[i].Code < f[j].Code
	})
	return f
}

// bounded truncates a list for bounded finding details.
func bounded(ids []string, n int) []string {
	if len(ids) <= n {
		return ids
	}
	return append(ids[:n], fmt.Sprintf("… and %d more", len(ids)-n))
}
