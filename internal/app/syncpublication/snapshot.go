// Package syncpublication freezes governed Markdown without touching the
// working tree or index.
package syncpublication

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/adapters/localfs"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

type Snapshot struct {
	Files   map[string][]byte
	Records []syncrecords.SnapshotFile
}

type routeEngines struct {
	scope      *policy.Engine
	exclusions *policy.Engine
	protected  *policy.Engine
}

// Capture performs two stable, bounded enumerations. A participating writer
// is serialized by the caller's resource guard; an uncooperative writer is
// detected by the second pass and cannot create a mixed snapshot.
func Capture(cfg *config.Config, resourceID string) (Snapshot, error) {
	resource, ok := cfg.Resources[resourceID]
	if !ok {
		return Snapshot{}, fmt.Errorf("sync resource %q is not configured", resourceID)
	}
	resolver, err := localfs.NewResolver(resource.Root)
	if err != nil {
		return Snapshot{}, err
	}
	if cfg.Limits.MaxPathBytes != nil {
		resolver.SetLimits(*cfg.Limits.MaxPathBytes)
	}
	max := int64(16 << 20)
	if cfg.Limits.MaxHashFileBytes != nil {
		max = *cfg.Limits.MaxHashFileBytes
	}
	engines, _, err := buildRouteEngines(cfg, resourceID)
	if err != nil {
		return Snapshot{}, err
	}
	first, err := captureOnce(resolver, engines, max)
	if err != nil {
		return Snapshot{}, err
	}
	second, err := captureOnce(resolver, engines, max)
	if err != nil {
		return Snapshot{}, err
	}
	if !equalFiles(first, second) {
		return Snapshot{}, fmt.Errorf("resource changed while publication snapshot was frozen")
	}
	records := make([]syncrecords.SnapshotFile, 0, len(first))
	for path, raw := range first {
		sum := sha256.Sum256(raw)
		records = append(records, syncrecords.SnapshotFile{Path: path, Digest: "sha256:" + hex.EncodeToString(sum[:])})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	return Snapshot{Files: first, Records: records}, nil
}

// MatchesObservedFacts requires the frozen governed Markdown set to equal the
// maintained path-fact set in both directions. This prevents an uncooperative
// deletion from being published while it still cites the pre-deletion receipt.
func MatchesObservedFacts(cfg *config.Config, resourceID string, records []syncrecords.SnapshotFile, facts map[string]string) (bool, error) {
	engines, _, err := buildRouteEngines(cfg, resourceID)
	if err != nil {
		return false, err
	}
	observed := make(map[string]string, len(records))
	for _, record := range records {
		observed[record.Path] = record.Digest
	}
	matched := 0
	for path, digest := range facts {
		included, classifyErr := governedMarkdown(path, engines)
		if classifyErr != nil {
			return false, classifyErr
		}
		if !included {
			continue
		}
		if observed[path] != digest {
			return false, nil
		}
		matched++
	}
	return matched == len(observed), nil
}

// ValidateFiles independently applies the local scope and protected-path
// policy to a verified remote snapshot. A publisher signature proves who
// authored a tree; it does not replace the importing node's path-safety check.
func ValidateFiles(cfg *config.Config, resourceID string, files map[string][]byte) error {
	engines, _, err := buildRouteEngines(cfg, resourceID)
	if err != nil {
		return err
	}
	aliases := map[string]string{}
	for path := range files {
		if reservedMetadataPath(path) {
			return fmt.Errorf("remote content path %q targets reserved sync or Git metadata", path)
		}
		included, err := governedMarkdown(path, engines)
		if err != nil {
			return err
		}
		if !included {
			return fmt.Errorf("remote content path %q is outside the acknowledged governed scope", path)
		}
		key := records.PortablePathIdentity(path)
		if prior, exists := aliases[key]; exists && prior != path {
			return fmt.Errorf("remote content paths %q and %q alias", prior, path)
		}
		aliases[key] = path
	}
	return nil
}

// ValidateTransition applies local path safety to both the target tree and
// removals from its predecessor. A trusted signer may propose a deletion, but
// cannot override this node's protected or immutable policy.
func ValidateTransition(cfg *config.Config, resourceID string, before, after map[string][]byte) error {
	if err := ValidateFiles(cfg, resourceID, after); err != nil {
		return err
	}
	engines, _, err := buildRouteEngines(cfg, resourceID)
	if err != nil {
		return err
	}
	for path := range before {
		if _, present := after[path]; present {
			continue
		}
		if reservedMetadataPath(path) {
			return fmt.Errorf("remote content deletion targets reserved sync or Git metadata path %q", path)
		}
		if err := rejectProtectedPath(path, engines); err != nil {
			return fmt.Errorf("remote content deletion is not allowed: %w", err)
		}
	}
	return nil
}

func buildRouteEngines(cfg *config.Config, resourceID string) ([]routeEngines, policy.CaseMode, error) {
	engines := make([]routeEngines, 0)
	mode := policy.CaseSensitive
	if config.CaseMode() == "insensitive" {
		mode = policy.CaseInsensitive
	}
	for _, routeID := range sortedRouteIDs(cfg) {
		route := cfg.Routes[routeID]
		if route.Source.Resource != resourceID {
			continue
		}
		e, err := policy.NewEngine(route.Source.Include, route.Source.Exclude, route.Policy.Protected, route.Policy.Immutable, mode)
		if err != nil {
			return nil, mode, err
		}
		// Excluded, protected, and immutable names are cross-node safety
		// identities. Keep exclusions separate so they can never take
		// precedence over a protected or immutable alias.
		exclusions, err := policy.NewEngine([]string{"**"}, route.Source.Exclude, nil, nil, policy.CaseInsensitive)
		if err != nil {
			return nil, mode, err
		}
		protected, err := policy.NewProtectionEngine(route.Policy.Protected, route.Policy.Immutable, policy.CaseInsensitive)
		if err != nil {
			return nil, mode, err
		}
		engines = append(engines, routeEngines{scope: e, exclusions: exclusions, protected: protected})
	}
	if len(engines) == 0 {
		return nil, mode, fmt.Errorf("sync resource has no governing routes")
	}
	return engines, mode, nil
}

func governedMarkdown(path string, engines []routeEngines) (bool, error) {
	lower := strings.ToLower(path)
	if !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".markdown") {
		return false, nil
	}
	if err := rejectProtectedPath(path, engines); err != nil {
		return false, err
	}
	included := false
	for _, engine := range engines {
		excluded, err := engine.exclusions.Classify(path)
		if err != nil {
			return false, err
		}
		if excluded == policy.StatusExcluded {
			continue
		}
		status, err := engine.scope.Classify(path)
		if err != nil {
			return false, err
		}
		switch status {
		case policy.StatusProtected, policy.StatusImmutable:
			return false, fmt.Errorf("governed path %q is protected or immutable", path)
		case policy.StatusNormal:
			included = true
		}
	}
	return included, nil
}

func rejectProtectedPath(path string, engines []routeEngines) error {
	for _, engine := range engines {
		status, err := engine.protected.Classify(path)
		if err != nil {
			return err
		}
		if status == policy.StatusProtected || status == policy.StatusImmutable {
			return fmt.Errorf("governed path %q is protected or immutable", path)
		}
	}
	return nil
}

func captureOnce(resolver *localfs.Resolver, engines []routeEngines, max int64) (map[string][]byte, error) {
	out := map[string][]byte{}
	aliases := map[string]string{}
	err := filepath.WalkDir(resolver.Root(), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == resolver.Root() {
			return nil
		}
		rel, err := filepath.Rel(resolver.Root(), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		policyPath := rel
		if d.IsDir() {
			if reservedMetadataPath(rel) {
				return fs.SkipDir
			}
			return nil
		}
		lower := strings.ToLower(rel)
		if !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".markdown") {
			return nil
		}
		included, classifyErr := governedMarkdown(policyPath, engines)
		if classifyErr != nil {
			return classifyErr
		}
		if !included {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return fmt.Errorf("governed path %q is not a regular file", rel)
		}
		key := records.PortablePathIdentity(rel)
		if prior, exists := aliases[key]; exists && prior != rel {
			return fmt.Errorf("governed paths %q and %q alias", prior, rel)
		}
		aliases[key] = rel
		f, info, openErr := resolver.OpenRegular(rel, max)
		if openErr != nil {
			return openErr
		}
		raw, readErr := io.ReadAll(io.LimitReader(f, max+1))
		after, statErr := f.Stat()
		closeErr := f.Close()
		if readErr != nil {
			return readErr
		}
		if statErr != nil {
			return statErr
		}
		if closeErr != nil {
			return closeErr
		}
		if int64(len(raw)) > max {
			return fmt.Errorf("governed path %q exceeds the read bound", rel)
		}
		if info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
			return fmt.Errorf("governed path %q changed while read", rel)
		}
		out[rel] = raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func reservedMetadataPath(path string) bool {
	path = filepath.ToSlash(path)
	first := path
	if slash := strings.IndexByte(path, '/'); slash >= 0 {
		first = path[:slash]
	}
	identity := records.PortablePathIdentity(first)
	return identity == ".git" || identity == ".agent-dispatch-sync"
}

func sortedRouteIDs(cfg *config.Config) []string {
	ids := make([]string, 0, len(cfg.Routes))
	for id := range cfg.Routes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func equalFiles(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for p, v := range a {
		if !bytes.Equal(v, b[p]) {
			return false
		}
	}
	return true
}
