package reconcile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/adapters/localfs"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// scopeWalker performs the bounded, containment-defended resource
// enumeration shared by full reconciliation (E5-T4) and baseline-only
// reconciliation (E14-T2): walk the resource root under the resolver's
// containment defense, the route's include/exclude patterns, and the
// file scope, hashing every in-scope regular file with the configured
// bound. Files beyond the bound or unstable across their read are
// returned as explicit evidence lists, never silently dropped (E10-T1,
// OPS-012).
type scopeWalker struct {
	Resolver  *localfs.Resolver
	Engine    *policy.Engine
	FileScope string
	MaxHash   int64
}

// enumerate walks the resource root; see the scopeWalker contract.
func (w scopeWalker) enumerate() (facts []ports.PathFact, skippedPrefixes, overBound, unstable []string, err error) {
	var unverifiable []ports.PathFact
	root := w.Resolver.Root()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is reported by absence, never a
			// whole-command abort: one blocked entry must not stop the
			// scope's reconciliation (the pending generation still
			// collapses conservatively).
			if path == root {
				return err
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil {
				relPath := filepath.ToSlash(rel)
				if d != nil && !d.IsDir() {
					// A file-level error skips exactly that file (E9-T2/
					// L-8): the old parent-prefix form mislabeled the
					// file's siblings as Removed.
					skippedPrefixes = append(skippedPrefixes, relPath)
					return nil
				}
				skippedPrefixes = append(skippedPrefixes, relPath+"/")
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
		// A symlink never projects as a regular-file fact (E9-T2/M-24):
		// the fact set describes regular files, and WalkDir never follows
		// the link to its target — so every symlink, in-vault or
		// escaping, lands in the skipped list with a warning (round-1 F001
		// collapsed the dead Resolve branch: no symlink is ever hashed).
		if d.Type()&fs.ModeSymlink != 0 {
			skippedPrefixes = append(skippedPrefixes, relPath)
			return nil
		}
		status, clsErr := w.Engine.Classify(relPath)
		if clsErr != nil {
			// One unclassifiable path (over-long, non-UTF-8) is
			// isolated as an exists-but-unverifiable fact: the
			// reconciliation reports it and continues instead of
			// aborting the whole recovery pass (E7-T9/M-24; the
			// adjacent unresolvable branch already had this posture).
			unverifiable = append(unverifiable, ports.PathFact{Path: relPath, Exists: true})
			return nil
		}
		if status == policy.StatusExcluded || status == policy.StatusProtected || status == policy.StatusImmutable {
			return nil
		}
		if w.FileScope == "markdown" {
			lower := strings.ToLower(relPath)
			if !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".markdown") {
				return nil
			}
		}
		fact := ports.PathFact{Path: relPath, Exists: true, ObservedAt: ""}
		if _, resErr := w.Resolver.Resolve(relPath); resErr != nil {
			// An unresolvable path stays in the enumeration as an
			// exists-but-unverifiable fact: reconciliation reports it
			// rather than silently truncating the scope.
			facts = append(facts, fact)
			return nil
		}
		digest, outcome := w.hash(relPath)
		switch outcome {
		case hashKnown:
			fact.Digest = digest
		case hashOverBound:
			overBound = append(overBound, relPath)
		case hashUnstable:
			unstable = append(unstable, relPath)
		}
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].Path < facts[j].Path })
	return append(facts, unverifiable...), skippedPrefixes, overBound, unstable, nil
}

// hashOutcome classifies one enumerated file's hashing outcome so the
// bounded-read, stability, and retry contract (E10-T1, OPS-012,
// ADR-0018) surfaces as explicit evidence instead of a silent unknown
// digest.
type hashOutcome int

const (
	// hashKnown means a digest was accepted from a bounded read of a
	// file that held still across it.
	hashKnown hashOutcome = iota
	// hashOverBound means the file provably sits beyond the configured
	// hashing bound and held still across two stats — quarantine
	// evidence, not a read.
	hashOverBound
	// hashUnstable means the file changed across its read twice (the
	// initial attempt and the one retry) — explicit reconciliation
	// evidence.
	hashUnstable
	// hashUnreadable means the open or read failed (vanished, EACCES,
	// non-regular): the digest stays unknown without a quarantine or
	// instability claim.
	hashUnreadable
)

// hash reads at most MaxHash+1 bytes per attempt and accepts a digest
// only from a file whose size and modification time held still across
// the read (ADR-0018). An unstable or newly over-bound file is retried
// exactly once; after the retry the file is reported as explicit
// evidence with its digest left unknown. A file already beyond the
// bound at the stat precheck is confirmed stable by a second stat
// before it is called a stable over-bound quarantine (a file caught
// mid-growth is unstable evidence instead).
func (w scopeWalker) hash(rel string) (string, hashOutcome) {
	var lastOverBoundSize int64 = -1
	for attempt := 0; attempt < 2; attempt++ {
		f, info, err := w.Resolver.OpenRegular(rel, w.MaxHash)
		if err != nil {
			if !errors.Is(err, localfs.ErrTooLarge) {
				return "", hashUnreadable // unreadable or non-regular: digest unknown
			}
			confirm, statErr := w.Resolver.StatContained(rel)
			if statErr != nil {
				return "", hashUnreadable
			}
			if attempt == 1 {
				if confirm.Size() == lastOverBoundSize {
					return "", hashOverBound
				}
				return "", hashUnstable
			}
			lastOverBoundSize = confirm.Size()
			continue
		}
		digest, n, stable, readErr := hashStable(f, info, w.MaxHash)
		f.Close()
		if readErr != nil {
			return "", hashUnreadable
		}
		if !stable {
			continue // one retry; a second unstable read reports below
		}
		if n > w.MaxHash {
			// The stream carried more than the bound while the stats held
			// still: the honest classification is over-bound — the read
			// was cut at MaxHash+1 and the digest stays unknown.
			return "", hashOverBound
		}
		return digest, hashKnown
	}
	return "", hashUnstable
}

// hashStable hashes at most max+1 bytes from an open file and reports
// whether the file held still across the read (size and modification
// time unchanged). n reports the bytes consumed so the caller classifies
// an over-bound stream without reading it again.
func hashStable(f *os.File, before fs.FileInfo, max int64) (digest string, n int64, stable bool, err error) {
	sum, n, over, err := records.SumBounded(f, max)
	if err != nil {
		return "", n, false, err
	}
	after, err := f.Stat()
	if err != nil {
		return "", n, false, err
	}
	if !statsStable(before, after) {
		return "", n, false, nil
	}
	if over {
		return "", n, true, nil
	}
	return sum.String(), n, true, nil
}

// statsStable reports whether two stats of one file describe the same
// bytes: identical size and modification time (ADR-0018 stability
// check).
func statsStable(before, after fs.FileInfo) bool {
	return before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}
