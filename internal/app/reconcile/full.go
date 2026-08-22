// Full-scope reconciliation (E5-T4, OPS-006, SRC-005): enumerate the
// resource's complete in-scope file set under the containment defense,
// compare path facts against the stored snapshot, and collapse the
// result into one pending reconciliation generation — creating exactly
// one latest-state intent when the route is idle and work is due.
package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/localfs"
	"github.com/irootkernel/agent-dispatch/internal/domain/ids"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Reasons is the closed reconcile --reason set (cli-spec §9).
var Reasons = map[string]bool{
	"initial": true, "scheduled": true, "overflow": true, "fresh-instance": true,
	"lost-cursor": true, "manual": true, "delivery": true, "stale-active": true,
}

// FullStore is the durable surface full reconciliation needs.
type FullStore interface {
	ports.RouteCoordinationStore
	ports.QuarantineStore
	// ResolveUncertainReconciliation applies the operator resolution of
	// one UNCERTAIN route (feedback-loop §10): slot release, generation
	// collapse, the pending generation kept or cleared atomically with
	// the optional one latest-state intent, and the pre-enumeration
	// fence on the dirty generation and pending flag.
	ResolveUncertainReconciliation(ctx context.Context, routeID string, intent *ports.IntentInput, expectedDirty int, expectedPending bool, actor, now string) error
}

// FullResult reports one reconciliation outcome.
type FullResult struct {
	RouteID           string   `json:"route_id"`
	Reason            string   `json:"reason"`
	Enumerated        int      `json:"enumerated"`
	Compared          int      `json:"compared"`
	Added             []string `json:"added"`
	Removed           []string `json:"removed"`
	Changed           []string `json:"changed"`
	PendingReconcile  bool     `json:"pending_reconcile"`
	DecisionID        string   `json:"decision_id"`
	ReconcileDispatch string   `json:"reconcile_dispatch_id,omitempty"`
	SnapshotStored    bool     `json:"snapshot_stored"`
}

// FullService performs full-scope reconciliation for one route.
type FullService struct {
	Store      FullStore
	Resolver   *localfs.Resolver
	Engine     *policy.Engine
	ResourceID string
	// RouteRevision and PolicyRevision are the real revisions the
	// decision records (no placeholders; E5 audit).
	RouteRevision  string
	PolicyRevision string
	// FileScope "markdown" restricts enumeration to Markdown files.
	FileScope string
	MaxHash   int64
	Now       func() time.Time
	// IntentBuilder builds the latest-state intent when the route is
	// idle and the comparison found work (wired by the CLI, which owns
	// the configuration-derived request shape). The reconciliation diff
	// is the evidence manifest: paths and digests, never content.
	IntentBuilder func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error)
}

// Run enumerates, compares, persists the decision and the snapshot, and
// collapses into the single pending generation (or one intent when the
// route is idle and work is due). Paths under an unreadable subtree
// keep their stored facts: an access failure is never reported as a
// removal.
func (s *FullService) Run(ctx context.Context, routeID, reason string) (FullResult, error) {
	if !Reasons[reason] {
		return FullResult{}, fmt.Errorf("unknown reconcile reason %q", reason)
	}
	now := ids.CanonicalTimestamp(s.Now())
	// The route snapshot is read before the enumeration walk: the dirty
	// generation and pending flag observed here fence the resolution
	// transaction, so a merge or release landing inside the (potentially
	// long) enumeration window refuses as a conflict instead of being
	// silently absorbed.
	snap, err := s.Store.LoadRouteState(ctx, routeID)
	if err != nil {
		return FullResult{}, ports.WrapStore(err)
	}
	current, skipped, err := s.enumerate()
	if err != nil {
		return FullResult{}, err
	}
	stored, err := s.Store.LoadPathFacts(ctx, s.ResourceID)
	if err != nil {
		return FullResult{}, ports.WrapStore(err)
	}
	out := FullResult{RouteID: routeID, Reason: reason, Enumerated: len(current), Compared: len(stored)}
	currentMap := map[string]ports.PathFact{}
	for _, f := range current {
		currentMap[f.Path] = f
		prev, ok := stored[f.Path]
		switch {
		case !ok:
			out.Added = append(out.Added, f.Path)
		case prev.Digest != f.Digest:
			out.Changed = append(out.Changed, f.Path)
		}
	}
	for path := range stored {
		if _, ok := currentMap[path]; ok {
			continue
		}
		// A path under an unreadable subtree was not enumerated — its
		// stored fact stands (an access failure is not a removal).
		if !underSkippedPrefix(skipped, path) {
			out.Removed = append(out.Removed, path)
		}
	}
	sort.Strings(out.Added)
	sort.Strings(out.Changed)
	sort.Strings(out.Removed)
	if out.Added == nil {
		out.Added = []string{}
	}
	if out.Changed == nil {
		out.Changed = []string{}
	}
	if out.Removed == nil {
		out.Removed = []string{}
	}

	workDue := len(out.Added)+len(out.Changed)+len(out.Removed) > 0

	// The decision is batch-less: its lineage is the generation it
	// reconciles (POL-006 disposition "reconcile", machine reasons). A
	// random suffix keeps repeated same-second reconciliations distinct.
	out.DecisionID = "dec-reconcile-" + routeID + "-" + ids.CompactTimestamp(now) + "-" + ids.RandomSuffix()
	if err := s.Store.CommitReconcileDecision(ctx, ports.DecisionInput{
		DecisionID: out.DecisionID, RouteID: routeID, RouteRevision: s.routeRevision(),
		PolicyRevision: s.policyRevision(),
		Disposition:    "reconcile", Classification: "normal",
		ReasonCodesJSON: reconcileReasonCodes(reason, len(out.Added)+len(out.Changed)+len(out.Removed)),
		CreatedAt:       now, Actor: "reconcile",
		GenerationLineageJSON: generationLineage(routeID, reason),
	}); err != nil {
		return FullResult{}, ports.WrapStore(err)
	}
	// The durable work is ordered so the snapshot only advances after
	// the decision and any intent exist (E5 audit F006): an idle route
	// with due work schedules exactly one latest-state reconciliation
	// intent; an active or pending route merges into the single pending
	// generation instead (SRC-005, CON-003). An uncertain route is
	// resolved by this same operator reconciliation (feedback-loop §10).
	// Due work demands an intent builder on every eligible state: the
	// same misconfiguration must fail loudly everywhere, never silently
	// drop due work.
	resolved := false
	if snap.State == state.RouteUncertain {
		var intentPtr *ports.IntentInput
		if workDue {
			if err := intentBuilderRequired(routeID, snap.State, s.IntentBuilder); err != nil {
				return FullResult{}, err
			}
			intent, err := s.IntentBuilder(routeID, reason, out.DecisionID, s.diffChanges(currentMap, stored, skipped))
			if err != nil {
				return FullResult{}, err
			}
			intent.DecisionID = out.DecisionID
			intent.CreatedAt = now
			intentPtr = &intent
			out.ReconcileDispatch = intent.DispatchID
		}
		if err := s.Store.ResolveUncertainReconciliation(ctx, routeID, intentPtr, snap.DirtyGeneration, snap.PendingReconcile, "reconcile", now); err != nil {
			return FullResult{}, classifyResolutionError(err)
		}
		resolved = true
		// The resolution marked (work due) or cleared (no work) its own
		// pending generation atomically.
		out.PendingReconcile = intentPtr != nil
	} else if workDue && snap.State == state.RouteIdle {
		if err := intentBuilderRequired(routeID, snap.State, s.IntentBuilder); err != nil {
			return FullResult{}, err
		}
		intent, err := s.IntentBuilder(routeID, reason, out.DecisionID, s.diffChanges(currentMap, stored, skipped))
		if err != nil {
			return FullResult{}, err
		}
		intent.DecisionID = out.DecisionID
		intent.CreatedAt = now
		if err := s.Store.CommitReconcileIntent(ctx, intent, "reconcile", now); err != nil {
			return FullResult{}, ports.WrapStore(err)
		}
		out.ReconcileDispatch = intent.DispatchID
	}
	// A resolution marks or clears its own pending generation atomically
	// in the resolution transaction. A non-resolving reconciliation
	// marks the pending generation for the work it scheduled or left
	// open — except the idle no-work case, which resolves the pending
	// generation directly in one transaction (no dispatch completion is
	// needed to clear it, E5 audit remediation): no path re-marks a flag
	// only to clear it again, so no second actor's fresh signal can be
	// stomped by this run's own churn.
	if !resolved {
		if !workDue && snap.State == state.RouteIdle {
			cleared, err := s.Store.ClearPendingReconcile(ctx, routeID, snap.PendingReconcile, now)
			if err != nil {
				return FullResult{}, ports.WrapStore(err)
			}
			// A miss means the durable flag moved inside this run's
			// window: a fresh mark by another actor stands (reported
			// pending), an already-cleared flag stays cleared.
			out.PendingReconcile = !cleared && !snap.PendingReconcile
		} else {
			if err := s.Store.MarkPendingReconcile(ctx, routeID, "", now); err != nil {
				return FullResult{}, ports.WrapStore(err)
			}
			out.PendingReconcile = true
		}
	}
	// The snapshot keeps the stored facts of unreadable subtrees: their
	// last observed truth stands until a readable reconciliation can
	// verify it (forgetting them would silently drop the subtree from
	// the durable scope).
	snapshot := current
	if len(skipped) > 0 {
		for path, fact := range stored {
			if _, ok := currentMap[path]; !ok && underSkippedPrefix(skipped, path) {
				snapshot = append(snapshot, fact)
			}
		}
	}
	if err := s.Store.ReplacePathFacts(ctx, s.ResourceID, snapshot, now); err != nil {
		return FullResult{}, ports.WrapStore(err)
	}
	out.SnapshotStored = true
	return out, nil
}

// underSkippedPrefix reports whether path falls inside one of the
// enumeration's unreadable subtrees.
func underSkippedPrefix(skipped []string, path string) bool {
	for _, prefix := range skipped {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// classifyResolutionError keeps a lost resolution race (another operator
// or scheduled recipe resolved the uncertain route first) a typed state
// conflict, never a storage failure: the eligibility refusal passes
// through unwrapped while durable-store failures wrap.
func classifyResolutionError(err error) error {
	if errors.Is(err, ports.ErrStateNotEligible) || errors.Is(err, ports.ErrGenerationConflict) {
		return err
	}
	return ports.WrapStore(err)
}

// diffChanges projects the comparison diff onto canonical change items
// (the reconciliation intent's bounded evidence manifest). Paths under
// an unreadable subtree are never projected as deletes: their stored
// facts stand, and an access failure is not a removal an intent may
// assert.
func (s *FullService) diffChanges(current map[string]ports.PathFact, stored map[string]ports.PathFact, skipped []string) []records.ChangeItem {
	items := []records.ChangeItem{}
	for _, path := range sortedKeys(current) {
		fact := current[path]
		prev, existed := stored[path]
		op := records.OpModify
		if !existed {
			op = records.OpCreate
		} else if prev.Digest == fact.Digest {
			continue
		}
		items = append(items, records.ChangeItem{
			Path: path, Operation: op, ExistsAfter: true, FileType: records.FileRegular,
			BeforeDigest: records.Digest(prevDigest(stored, path)), AfterDigest: records.Digest(fact.Digest),
			DigestStatus: digestStatusOf(fact.Digest),
		})
	}
	for _, path := range sortedKeys(stored) {
		if _, still := current[path]; still {
			continue
		}
		if underSkippedPrefix(skipped, path) {
			continue
		}
		prev := stored[path]
		items = append(items, records.ChangeItem{
			Path: path, Operation: records.OpDelete, ExistsAfter: false, FileType: records.FileRegular,
			BeforeDigest: records.Digest(prev.Digest), DigestStatus: digestStatusOf(prev.Digest),
		})
	}
	return items
}

// intentBuilderRequired enforces the uniform intent-builder contract:
// due work on any eligible route state demands a builder — the same
// misconfiguration fails loudly everywhere, never silently drops due
// work.
func intentBuilderRequired(routeID string, snapState state.RouteState, builder func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error)) error {
	if builder == nil {
		return fmt.Errorf("reconciliation with due work requires an intent builder (route %s is %s)", routeID, snapState)
	}
	return nil
}

func sortedKeys(m map[string]ports.PathFact) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func prevDigest(stored map[string]ports.PathFact, path string) string {
	if prev, ok := stored[path]; ok {
		return prev.Digest
	}
	return ""
}

func digestStatusOf(digest string) records.DigestStatus {
	if digest == "" {
		return records.DigestUnavailable
	}
	return records.DigestKnown
}

// enumerate walks the resource root under the resolver's containment
// defense, the route's include/exclude patterns, and the file scope,
// hashing every in-scope regular file with the configured bound.
func (s *FullService) enumerate() ([]ports.PathFact, []string, error) {
	var facts []ports.PathFact
	var skippedPrefixes []string
	root := s.Resolver.Root()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is reported by absence, never a
			// whole-command abort: one blocked directory must not stop
			// the scope's reconciliation (the pending generation still
			// collapses conservatively).
			if path == root {
				return err
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil {
				skippedPrefixes = append(skippedPrefixes, filepath.ToSlash(rel)+"/")
			}
			return fs.SkipDir
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relPath := filepath.ToSlash(rel)
		if d.IsDir() {
			return nil
		}
		status, clsErr := s.Engine.Classify(relPath)
		if clsErr != nil {
			return fmt.Errorf("classifying %q: %w", relPath, clsErr)
		}
		if status == policy.StatusExcluded || status == policy.StatusProtected || status == policy.StatusImmutable {
			return nil
		}
		if s.FileScope == "markdown" && !strings.HasSuffix(relPath, ".md") && !strings.HasSuffix(relPath, ".markdown") {
			return nil
		}
		fact := ports.PathFact{Path: relPath, Exists: true, ObservedAt: ""}
		if _, resErr := s.Resolver.Resolve(relPath); resErr != nil {
			// An unresolvable path stays in the enumeration as an
			// exists-but-unverifiable fact: reconciliation reports it
			// rather than silently truncating the scope.
			facts = append(facts, fact)
			return nil
		}
		digest, ok := s.hash(relPath)
		if ok {
			fact.Digest = digest
		}
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].Path < facts[j].Path })
	return facts, skippedPrefixes, nil
}

func (s *FullService) hash(rel string) (string, bool) {
	f, _, err := s.Resolver.OpenRegular(rel, s.MaxHash)
	if err != nil {
		return "", false // unreadable or over-bound: digest unknown
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), true
}

func (s *FullService) routeRevision() string {
	if s.RouteRevision != "" {
		return s.RouteRevision
	}
	return "unknown"
}

func (s *FullService) policyRevision() string {
	if s.PolicyRevision != "" {
		return s.PolicyRevision
	}
	return "unknown"
}

// reconcileReasonCodes builds the sorted, encoder-produced reason array
// (the record contract orders decision reason codes).
func reconcileReasonCodes(reason string, files int) string {
	codes := []string{fmt.Sprintf("reconcile:%s", reason), fmt.Sprintf("files:%d", files)}
	sort.Strings(codes)
	raw, _ := json.Marshal(codes)
	return string(raw)
}

// generationLineage encodes the batch-less decision's lineage document.
func generationLineage(routeID, reason string) string {
	raw, _ := json.Marshal(map[string]any{"route_id": routeID, "reason": reason, "origin": "full-reconciliation"})
	return string(raw)
}
