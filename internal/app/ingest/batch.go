// Package ingest implements meaningful-change confirmation and batch
// normalization (E2-T3, SRC-004, PTH-005, PTH-006, SCP-009, DAT-005,
// processing-pipeline §3-§4): it converts parsed Watchman entries into one
// canonical, sorted, fingerprinted change batch. Deletes are never opened,
// unchanged modifies are suppressed against prior path facts, same-path
// sequences coalesce to their final observed state, and files that cannot
// be hashed stay structurally unknown rather than falsely unchanged.
package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"sort"

	"github.com/rootkernel/jjukkumi/internal/adapters/localfs"
	"github.com/rootkernel/jjukkumi/internal/adapters/watchman"
	"github.com/rootkernel/jjukkumi/internal/domain/fingerprint"
	"github.com/rootkernel/jjukkumi/internal/domain/policy"
	"github.com/rootkernel/jjukkumi/internal/domain/records"
)

// PathFacts is the read-only prior-digest lookup port (PTH-006): the last
// known content digest per path. The durable implementation arrives with
// the E3 ingestion transaction; tests and dry-run use maps or NoFacts.
type PathFacts interface {
	// PriorDigest returns the last known digest for path and whether one
	// exists. An error is a lookup failure, not absence.
	PriorDigest(path string) (records.Digest, bool, error)
}

// NoFacts is the no-history fallback: no path has a prior digest, so
// every modify is meaningful (conservative, never falsely unchanged).
type NoFacts struct{}

func (NoFacts) PriorDigest(string) (records.Digest, bool, error) { return "", false, nil }

// MapFacts is an in-memory PathFacts for tests and dry-run planning.
type MapFacts map[string]records.Digest

func (m MapFacts) PriorDigest(path string) (records.Digest, bool, error) {
	d, ok := m[path]
	return d, ok, nil
}

// GitPathFacts are advisory Git facts for one path (SCP-009).
type GitPathFacts struct {
	Tracked    bool
	LastCommit string
}

// GitEvidence is the optional Git enrichment port (SCP-009): it MAY
// attach advisory evidence to a batch's paths. It MUST NOT be required
// for ingestion or dispatch — a nil or failing adapter never blocks or
// alters the canonical batch.
type GitEvidence interface {
	Enrich(paths []string) (map[string]GitPathFacts, error)
}

// Result is one normalized source batch.
type Result struct {
	// Changes is the canonical sorted change list.
	Changes []records.ChangeItem
	// Fingerprint is the deterministic content fingerprint over the
	// sorted batch and source flags (DAT-005).
	Fingerprint records.Digest
	// Dropped records suppressed paths with machine-readable reasons
	// (unchanged_content, create_delete_never_existed).
	Dropped []DropRecord
	// Replacements marks paths that were deleted and re-created within
	// one batch (optional evidence; no rename claim).
	Replacements map[string]bool
	// HashUnknown lists paths whose digest is structurally unavailable
	// (oversize, non-regular, vanished); they are never treated as
	// unchanged.
	HashUnknown []string
	// Protected lists the batch's protected paths (PTH-008): classified
	// but never hashed; the planner quarantines them.
	Protected []string
	// Immutable lists the batch's immutable paths.
	Immutable []string
}

// ErrUnsafePath wraps containment violations (lexical escape, symlink
// escape, over-length path) so callers can distinguish security
// rejections from other build failures.
var ErrUnsafePath = errors.New("unsafe path")

// DropRecord is one suppressed path and why.
type DropRecord struct {
	Path   string
	Reason string
}

const (
	ReasonUnchangedModify   = "unchanged_content"
	ReasonCreateDeleteNever = "create_delete_never_existed"
	ReasonExcluded          = "excluded"
)

// Options bound the batch build.
type Options struct {
	// MaxHashBytes is limits.max_hash_file_bytes; files above it stay
	// structurally unknown (PTH-006 fail-closed direction).
	MaxHashBytes int64
}

// BuildBatch normalizes entries into one canonical batch. classify comes
// from the route's pattern engine (excluded paths are dropped without
// reads; protected and immutable paths are classified but never hashed);
// resolver provides the containment-checked reads; facts provide prior
// digests. The result's change order and fingerprint are deterministic
// regardless of payload entry order.
func BuildBatch(entries []watchman.Entry, engine *policy.Engine, resolver *localfs.Resolver, facts PathFacts, flags records.SourceFlags, resourceID string, opts Options) (*Result, error) {
	if engine == nil {
		return nil, errors.New("nil pattern engine")
	}
	if resolver == nil {
		return nil, errors.New("nil resolver")
	}
	if facts == nil {
		facts = NoFacts{}
	}
	if opts.MaxHashBytes <= 0 {
		return nil, errors.New("MaxHashBytes must be positive")
	}
	if opts.MaxHashBytes == math.MaxInt64 {
		// sumBounded reads maxBytes+1; a MaxInt64 bound would wrap.
		return nil, errors.New("MaxHashBytes must be below MaxInt64")
	}
	res := &Result{Replacements: map[string]bool{}}

	// Group entries per path in payload order; ordinals keep the
	// original sequence for the coalescing rules.
	type seq struct {
		ops      []records.Operation
		last     records.Operation
		fileType records.FileType
		ordinal  int
	}
	paths := make([]string, 0, len(entries))
	byPath := map[string]*seq{}
	for _, e := range entries {
		s, ok := byPath[e.Name]
		if !ok {
			s = &seq{fileType: e.Type, ordinal: e.Ordinal}
			byPath[e.Name] = s
			paths = append(paths, e.Name)
		}
		s.ops = append(s.ops, e.Op)
		s.last = e.Op
		s.fileType = e.Type // describe the final observed state
	}
	// Paths whose digest is forced unknown by a coalescing branch.
	forcedUnknown := map[string]bool{}

	for _, path := range paths {
		s := byPath[path]
		status, err := engine.Classify(path)
		if err != nil {
			return nil, fmt.Errorf("%w: classify %q: %v", ErrUnsafePath, path, err)
		}
		if status == policy.StatusExcluded {
			res.Dropped = append(res.Dropped, DropRecord{Path: path, Reason: ReasonExcluded})
			continue
		}
		// Protected and immutable status applies to every operation,
		// including deletes: the planner quarantines regardless of the
		// coalesced operation.
		switch status {
		case policy.StatusProtected:
			res.Protected = append(res.Protected, path)
		case policy.StatusImmutable:
			res.Immutable = append(res.Immutable, path)
		}
		prior, hadPrior, err := facts.PriorDigest(path)
		if err != nil {
			return nil, fmt.Errorf("path facts for %q: %w", path, err)
		}

		item := records.ChangeItem{Path: path, FileType: s.fileType, Ordinal: s.ordinal}

		// Coalesce to the final observed state (processing-pipeline §4).
		// A delete followed by any later create/modify is replacement
		// semantics regardless of how Watchman split the events.
		sawLifeAfterDelete := false
		for i, op := range s.ops {
			if op == records.OpDelete && i+1 < len(s.ops) {
				sawLifeAfterDelete = true
			}
		}
		var finalOp records.Operation
		switch {
		case s.last == records.OpDelete:
			if s.ops[0] == records.OpCreate && !hadPrior {
				// create,delete: drop only when the path also does not
				// exist at planning time; a surviving file keeps an
				// uncertain delete.
				_, err := resolver.StatContained(path)
				switch {
				case errors.Is(err, fs.ErrNotExist), errors.Is(err, localfs.ErrNotRegular):
					res.Dropped = append(res.Dropped, DropRecord{Path: path, Reason: ReasonCreateDeleteNever})
					continue
				case errors.Is(err, localfs.ErrUnreadable):
					res.HashUnknown = append(res.HashUnknown, path)
					// An unreadable path is not proven absent: keep the
					// uncertain delete rather than asserting non-existence.
				case err == nil:
					// The file survives: keep the uncertain delete.
				default:
					return nil, fmt.Errorf("checking final state of %q: %w", path, err)
				}
			}
			// modify,delete — and create,delete over a persisted prior
			// path or a surviving file — end deleted; the uncertainty is
			// planning-stage evidence, not a different operation.
			finalOp = records.OpDelete
		case sawLifeAfterDelete:
			// delete,...,create/modify: a replacement only when the file
			// exists at planning time. A vanished path falls back to
			// delete; a non-regular recreated path keeps the create with
			// a structurally unknown digest; containment anomalies fail
			// the build instead of masquerading as absence.
			_, err := resolver.StatContained(path)
			switch {
			case err == nil:
				finalOp = records.OpCreate
				res.Replacements[path] = true
			case errors.Is(err, fs.ErrNotExist):
				finalOp = records.OpDelete
			case errors.Is(err, localfs.ErrNotRegular), errors.Is(err, localfs.ErrUnreadable):
				finalOp = records.OpCreate
				res.Replacements[path] = true
				forcedUnknown[path] = true
			default:
				return nil, fmt.Errorf("%w: checking replacement state of %q: %v", ErrUnsafePath, path, err)
			}
		case s.ops[0] == records.OpCreate && !hadPrior:
			// create,modify (or a bare create): the path is new.
			finalOp = records.OpCreate
		default:
			// modify,modify — or a create observed over a persisted
			// prior path, which is conservatively a modify.
			finalOp = records.OpModify
		}

		item.Operation = finalOp
		if finalOp == records.OpDelete {
			item.ExistsAfter = false
			item.DigestStatus = records.DigestNotApplicable
			item.BeforeDigest = priorDigestOrEmpty(hadPrior, prior)
		} else {
			item.ExistsAfter = true
			if status == policy.StatusProtected || status == policy.StatusImmutable || forcedUnknown[path] {
				// Classified but not read (E2-T2 acceptance): no hashing
				// for protected or immutable paths, and none possible
				// for a non-regular replacement.
				item.DigestStatus = records.DigestUnavailable
				if !containsPath(res.HashUnknown, path) {
					res.HashUnknown = append(res.HashUnknown, path)
				}
			} else {
				digest, known, err := hashFile(resolver, path, opts.MaxHashBytes)
				if err != nil {
					return nil, err
				}
				if known {
					item.AfterDigest = digest
					item.DigestStatus = records.DigestKnown
					if finalOp == records.OpModify && hadPrior && prior == digest && !res.Replacements[path] {
						// PTH-006: an unchanged modify with a known
						// prior digest is suppressed — unless the batch
						// shows the path was destroyed and recreated,
						// where identical content is still a real
						// replacement, never "unchanged".
						res.Dropped = append(res.Dropped, DropRecord{Path: path, Reason: ReasonUnchangedModify})
						continue
					}
				} else {
					item.DigestStatus = records.DigestUnavailable
					res.HashUnknown = append(res.HashUnknown, path)
				}
			}
		}
		if err := item.Validate(); err != nil {
			return nil, fmt.Errorf("coalesced item for %q: %w", path, err)
		}
		res.Changes = append(res.Changes, item)
	}

	records.SortChanges(res.Changes)
	sort.Slice(res.Dropped, func(i, j int) bool {
		if res.Dropped[i].Path != res.Dropped[j].Path {
			return res.Dropped[i].Path < res.Dropped[j].Path
		}
		return res.Dropped[i].Reason < res.Dropped[j].Reason
	})
	sort.Strings(res.HashUnknown)
	sort.Strings(res.Protected)
	sort.Strings(res.Immutable)
	fpInput := records.ContentFingerprintInput{
		Changes:      projection(res.Changes),
		ResourceID:   resourceID,
		Fresh:        flags.FreshInstance,
		HasRelative:  flags.HasRelative,
		Overflow:     flags.Overflow,
		RelativeRoot: flags.RelativeRoot,
	}
	fp, err := fingerprint.Content(fpInput)
	if err != nil {
		return nil, fmt.Errorf("fingerprint: %w", err)
	}
	res.Fingerprint = fp
	return res, nil
}

func priorDigestOrEmpty(had bool, d records.Digest) records.Digest {
	if !had {
		return ""
	}
	return d
}

// hashFile reads one contained regular file and returns its digest. An
// untrue structural guard (oversize, non-regular, vanished) returns
// known=false — the digest stays unknown, never falsely unchanged.
func hashFile(resolver *localfs.Resolver, path string, maxBytes int64) (records.Digest, bool, error) {
	f, _, err := resolver.OpenRegular(path, maxBytes)
	if err != nil {
		if errors.Is(err, localfs.ErrTooLarge) ||
			errors.Is(err, localfs.ErrNotRegular) ||
			errors.Is(err, localfs.ErrMissing) ||
			errors.Is(err, localfs.ErrUnreadable) {
			return "", false, nil
		}
		if errors.Is(err, localfs.ErrEscape) || errors.Is(err, localfs.ErrPathTooLong) {
			return "", false, fmt.Errorf("%w: opening %q: %v", ErrUnsafePath, path, err)
		}
		return "", false, fmt.Errorf("opening %q: %w", path, err)
	}
	defer f.Close()
	// Bound the read, not just the stat: a file that grows past the
	// limit mid-read stays structurally unknown.
	digest, bounded, err := sumBounded(f, maxBytes)
	if err != nil {
		return "", false, fmt.Errorf("hashing %q: %w", path, err)
	}
	if !bounded {
		return "", false, nil
	}
	return digest, true, nil
}

// sumBounded hashes at most maxBytes; the second result is false when
// the stream carried more, leaving the digest structurally unknown.
func sumBounded(r io.Reader, maxBytes int64) (records.Digest, bool, error) {
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(r, maxBytes+1))
	if err != nil {
		return "", false, err
	}
	if n > maxBytes {
		return "", false, nil
	}
	d, err := records.ParseDigest("sha256:" + hex.EncodeToString(h.Sum(nil)))
	if err != nil {
		return "", false, err
	}
	return d, true, nil
}

func containsPath(list []string, path string) bool {
	for _, p := range list {
		if p == path {
			return true
		}
	}
	return false
}

// projection maps change items into the fingerprint projection.
func projection(changes []records.ChangeItem) []records.FingerprintChange {
	out := make([]records.FingerprintChange, len(changes))
	for i, c := range changes {
		out[i] = records.FingerprintChange{
			AfterDigest:  string(c.AfterDigest),
			BeforeDigest: string(c.BeforeDigest),
			ExistsAfter:  c.ExistsAfter,
			Operation:    string(c.Operation),
			Path:         c.Path,
		}
	}
	return out
}

// NoGit is the default GitEvidence: no enrichment, no error. Git facts
// are optional (SCP-009) and must never gate ingestion or dispatch; the
// real adapter behind this port arrives with the Git enrichment work.
type NoGit struct{}

func (NoGit) Enrich([]string) (map[string]GitPathFacts, error) { return nil, nil }
