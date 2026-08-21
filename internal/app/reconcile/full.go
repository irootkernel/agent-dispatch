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
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/localfs"
	"github.com/rootkernel/jjukkumi/internal/domain/ids"
	"github.com/rootkernel/jjukkumi/internal/domain/policy"
	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/ports"
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
}

// StoreError wraps one durable-store failure so the CLI boundary
// classifies it as storage instead of string-matching (E5 audit).
type StoreError struct{ Err error }

func (e *StoreError) Error() string { return "durable store: " + e.Err.Error() }
func (e *StoreError) Unwrap() error { return e.Err }

func wrapStore(err error) error {
	if err == nil {
		return nil
	}
	return &StoreError{Err: err}
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
// route is idle and work is due).
func (s *FullService) Run(ctx context.Context, routeID, reason string) (FullResult, error) {
	if !Reasons[reason] {
		return FullResult{}, fmt.Errorf("unknown reconcile reason %q", reason)
	}
	now := timestamp(s.Now())
	current, err := s.enumerate()
	if err != nil {
		return FullResult{}, err
	}
	stored, err := s.Store.LoadPathFacts(ctx, s.ResourceID)
	if err != nil {
		return FullResult{}, wrapStore(err)
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
		if _, ok := currentMap[path]; !ok {
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

	snap, err := s.Store.LoadRouteState(ctx, routeID)
	if err != nil {
		return FullResult{}, wrapStore(err)
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
		ReasonCodesJSON: fmt.Sprintf(`["reconcile:%s","files:%d"]`, reason, len(out.Added)+len(out.Changed)+len(out.Removed)),
		CreatedAt:       now, Actor: "reconcile",
		GenerationLineageJSON: fmt.Sprintf(`{"route_id":%q,"reason":%q,"origin":"full-reconciliation"}`, routeID, reason),
	}); err != nil {
		return FullResult{}, err
	}
	// The durable work is ordered so the snapshot only advances after
	// the decision and any intent exist (E5 audit F006): an idle route
	// with due work schedules exactly one latest-state reconciliation
	// intent; an active or pending route merges into the single pending
	// generation instead (SRC-005, CON-003).
	if workDue && snap.State == state.RouteIdle && s.IntentBuilder != nil {
		intent, err := s.IntentBuilder(routeID, reason, out.DecisionID, s.diffChanges(currentMap, stored))
		if err != nil {
			return FullResult{}, err
		}
		intent.DecisionID = out.DecisionID
		intent.CreatedAt = now
		if err := s.Store.CommitReconcileIntent(ctx, intent, "reconcile", now); err != nil {
			return FullResult{}, wrapStore(err)
		}
		out.ReconcileDispatch = intent.DispatchID
	}
	if err := s.Store.MarkPendingReconcile(ctx, routeID, "", now); err != nil {
		return FullResult{}, err
	}
	out.PendingReconcile = true
	// An idle route whose full reconciliation proved no work remains
	// resolves its pending generation: no dispatch completion is needed
	// to clear it (E5 audit remediation).
	if !workDue && snap.State == state.RouteIdle {
		if err := s.Store.ClearPendingReconcile(ctx, routeID, now); err != nil {
			return FullResult{}, wrapStore(err)
		}
		out.PendingReconcile = false
	}
	if err := s.Store.ReplacePathFacts(ctx, s.ResourceID, current, now); err != nil {
		return FullResult{}, wrapStore(err)
	}
	out.SnapshotStored = true
	return out, nil
}

// diffChanges projects the comparison diff onto canonical change items
// (the reconciliation intent's bounded evidence manifest).
func (s *FullService) diffChanges(current map[string]ports.PathFact, stored map[string]ports.PathFact) []records.ChangeItem {
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
		if _, still := current[path]; !still {
			prev := stored[path]
			items = append(items, records.ChangeItem{
				Path: path, Operation: records.OpDelete, ExistsAfter: false, FileType: records.FileRegular,
				BeforeDigest: records.Digest(prev.Digest), DigestStatus: digestStatusOf(prev.Digest),
			})
		}
	}
	return items
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
func (s *FullService) enumerate() ([]ports.PathFact, error) {
	var facts []ports.PathFact
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
		return nil, err
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].Path < facts[j].Path })
	return facts, nil
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

// timestamp renders the canonical UTC RFC 3339 second-precision form.
// It stays local because dispatch's own tests import this package (a
// test-binary import cycle), and because the truncated-UTC ordering is
// load-bearing: dirty-window comparisons sort these strings
// lexicographically.
func timestamp(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
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
