package workreceipt

import (
	"encoding/json"
	"time"

	"github.com/rootkernel/jjukkumi/internal/ports"
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
	OutcomeMismatch   = "digest_mismatch"
	OutcomeNoDigest   = "digest_unverified"
	OutcomeBeforeRun  = "observed_before_run"
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
}

// Match applies the exact-suppression algorithm (feedback-loop §5) over
// the dirty generation's observed changes. Multiple observations of one
// path collapse to the latest (the last write wins; an earlier matching
// digest never suppresses a later divergent one). Receipt paths never
// observed are recorded as suppressed evidence only when observed set is
// covered — they cannot unlock suppression by themselves.
func Match(receipt ReceiptEvidence, observed []ports.DirtyChange, now string) AttributionDecision {
	decision := AttributionDecision{
		ReceiptID:   receipt.ReceiptID,
		DispatchID:  receipt.DispatchID,
		RunID:       receipt.RunID,
		BegunAt:     receipt.BegunAt,
		CompletedAt: receipt.CompletedAt,
	}
	_ = now
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
	for path, obs := range latest {
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
	// An empty observed window is unproven, never cleared: the matcher
	// itself refuses vacuous suppression (E5 audit).
	if len(latest) == 0 {
		fully = false
	}
	decision.FullySuppressed = fully
	return decision
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
