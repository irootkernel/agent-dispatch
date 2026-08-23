package workreceipt

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Exact self-change attribution (E5-T3, FBK-002, FBK-004, FBK-008): a
// validated completion receipt is matched against the dirty generation's
// observed changes. Only an exact path and after-digest match inside the
// run's temporal window may mark a change verified self-generated; every
// other outcome retains the change as unresolved, so a mixed or uncertain
// batch never fully suppresses.

// SuppressionOutcome labels one observed path's attribution result.
const (
	OutcomeSuppressed = "verified_self_generated"
	OutcomeMissing    = "receipt_missing_path"
	OutcomeExtra      = "receipt_extra_path"
	OutcomeMismatch   = "digest_mismatch"
	OutcomeNoDigest   = "digest_unverified"
	OutcomeBeforeRun  = "observed_before_run"
	// OutcomeImmaterial labels a receipt path the route never needs
	// provenance for: it is outside the route's effective scope, or its
	// reported after-digest equals the durable path fact (a byte-identical
	// rewrite the ingestion dropped as unchanged). Such a path is recorded
	// for audit but never blocks full suppression (E8-T1, H-1.1).
	OutcomeImmaterial = "receipt_immaterial_path"
)

// PathDecision is one observed path's attribution outcome.
type PathDecision struct {
	Path           string `json:"path"`
	Outcome        string `json:"outcome"`
	ObservedDigest string `json:"observed_digest,omitempty"`
	ReceiptDigest  string `json:"receipt_digest,omitempty"`
	ObservedAt     string `json:"observed_at"`
	LastObservedAt string `json:"last_observed_at,omitempty"`
}

// AttributionDecision is the auditable result of one receipt match.
type AttributionDecision struct {
	ReceiptID       string         `json:"receipt_id"`
	DispatchID      string         `json:"dispatch_id"`
	RunID           string         `json:"run_id"`
	BegunAt         string         `json:"begun_at"`
	CompletedAt     string         `json:"completed_at"`
	SuppressedPaths []string       `json:"suppressed"`
	Suppressed      []PathDecision `json:"suppressed_decisions"`
	Unresolved      []PathDecision `json:"unresolved"`
	// FullySuppressed reports every observed change of the generation
	// was verified self-generated (the batch is clearable).
	FullySuppressed bool `json:"fully_suppressed"`
	// FactsUnavailable reports the durable path-fact surface could not be
	// loaded for this match, so identical-rewrite claims classify
	// conservatively as extra provenance (E8-T1 round-1 F008).
	FactsUnavailable bool `json:"facts_unavailable,omitempty"`
}

// ReceiptEvidence is the validated receipt side of the match.
type ReceiptEvidence struct {
	ReceiptID   string
	DispatchID  string
	RunID       string
	ResourceID  string
	BegunAt     string
	CompletedAt string
	Changes     []changeEntry
	// OutsideScope reports whether a path is outside the route's
	// effective scope (excluded or not admitted by the include set); nil
	// means every path is in scope.
	OutsideScope func(path string) bool
	// FactDigest returns the durable path fact's current digest and
	// presence; nil means no fact surface is available.
	FactDigest func(path string) (digest string, ok bool)
}

// immaterial reports whether one receipt-only path never needs
// provenance: outside the route's effective scope, or a byte-identical
// rewrite (the reported after-digest equals the durable path fact).
func (r ReceiptEvidence) immaterial(path string, afterDigest string) bool {
	if r.OutsideScope != nil && r.OutsideScope(path) {
		return true
	}
	if r.FactDigest != nil && afterDigest != "" {
		if fact, ok := r.FactDigest(path); ok && fact != "" && fact == afterDigest {
			return true
		}
	}
	return false
}

// Match applies the exact-suppression algorithm (feedback-loop §5) over
// the dirty generation's observed changes. Multiple observations of one
// path collapse to the latest (the last write wins; an earlier matching
// digest never suppresses a later divergent one). A receipt path never
// observed is unproven extra provenance: it cannot verify anything and
// blocks full suppression (AC-404 — missing, extra, or mismatched
// provenance never suppresses).
func Match(receipt ReceiptEvidence, observed []ports.DirtyChange) AttributionDecision {
	decision := AttributionDecision{
		ReceiptID:   receipt.ReceiptID,
		DispatchID:  receipt.DispatchID,
		RunID:       receipt.RunID,
		BegunAt:     receipt.BegunAt,
		CompletedAt: receipt.CompletedAt,
	}
	// Latest observation per path.
	latest := map[string]ports.DirtyChange{}
	for _, c := range observed {
		prev, ok := latest[c.Path]
		if !ok || !before(c.ObservedAt, prev.ObservedAt) {
			latest[c.Path] = c
		}
	}
	receiptByPath := map[string]changeEntry{}
	for _, e := range receipt.Changes {
		receiptByPath[e.Path] = e
	}
	fully := true
	for _, path := range sortedPaths(latest) {
		obs := latest[path]
		d := PathDecision{Path: path, ObservedDigest: obs.AfterDigest, ObservedAt: obs.ObservedAt}
		entry, ok := receiptByPath[path]
		if !ok {
			d.Outcome = OutcomeMissing
		} else if entry.AfterDigest == nil || *entry.AfterDigest == "" {
			d.Outcome = OutcomeNoDigest
			d.ReceiptDigest = ""
		} else if obs.AfterDigest == "" || obs.DigestStatus != "known" {
			d.Outcome = OutcomeNoDigest
			d.ReceiptDigest = *entry.AfterDigest
		} else if *entry.AfterDigest != obs.AfterDigest {
			d.Outcome = OutcomeMismatch
			d.ReceiptDigest = *entry.AfterDigest
		} else {
			d.Outcome = OutcomeSuppressed
			d.ReceiptDigest = *entry.AfterDigest
		}
		// The temporal window: a change last observed before the run
		// began cannot be the run's own output (it predates the agent).
		if d.Outcome == OutcomeSuppressed && receipt.BegunAt != "" && before(d.ObservedAt, receipt.BegunAt) {
			d.Outcome = OutcomeBeforeRun
		}
		if d.Outcome == OutcomeSuppressed {
			decision.SuppressedPaths = append(decision.SuppressedPaths, path)
			decision.Suppressed = append(decision.Suppressed, d)
		} else {
			decision.Unresolved = append(decision.Unresolved, d)
			fully = false
		}
	}
	// Receipt paths the generation never observed stay unresolved unless
	// they are immaterial to the route (outside the effective scope, or a
	// byte-identical rewrite matching the durable path fact): material
	// claims with no durable observation behind them block the exact
	// match and must not clear the route (E8-T1, H-1.1).
	for _, path := range sortedChangePaths(receipt.Changes) {
		if _, ok := latest[path]; ok {
			continue
		}
		entry := receiptByPath[path]
		afterDigest := ""
		if entry.AfterDigest != nil {
			afterDigest = *entry.AfterDigest
		}
		if receipt.immaterial(path, afterDigest) {
			decision.Suppressed = append(decision.Suppressed, PathDecision{
				Path: path, Outcome: OutcomeImmaterial, ReceiptDigest: afterDigest,
			})
			continue
		}
		d := PathDecision{Path: path, Outcome: OutcomeExtra, ReceiptDigest: afterDigest}
		decision.Unresolved = append(decision.Unresolved, d)
		fully = false
	}
	// An empty observed window is unproven, never cleared: the matcher
	// itself refuses vacuous suppression (E5 audit).
	if len(latest) == 0 {
		fully = false
	}
	decision.FullySuppressed = fully
	return decision
}

// sortedPaths returns the observed map's paths in canonical order so the
// audited decision document is deterministic.
func sortedPaths(m map[string]ports.DirtyChange) []string {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func sortedChangePaths(entries []changeEntry) []string {
	paths := make([]string, 0, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		paths = append(paths, e.Path)
	}
	sort.Strings(paths)
	return paths
}

// ContextJSON renders the bounded audit document for the decision.
func (d AttributionDecision) ContextJSON() string {
	raw, _ := json.Marshal(d)
	return string(raw)
}

// before reports whether timestamp a sorts strictly before b under the
// canonical UTC RFC 3339 second-precision form (lexicographic order).
func before(a, b string) bool {
	if !validTimestamp(a) || !validTimestamp(b) {
		return false
	}
	return a < b
}

func validTimestamp(s string) bool {
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}
