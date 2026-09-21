// Package syncimport plans and applies exact governed-file effects without
// touching unrelated worktree content.
package syncimport

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/irootkernel/agent-dispatch/internal/adapters/localfs"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Diff returns exact write/delete effects plus the target bytes needed for the
// worktree and index. The result order is canonical and rename-safe (a rename
// is represented by one absence and one write).
func Diff(before, after map[string][]byte) ([]syncrecords.ImportPath, map[string][]byte, []string) {
	set := map[string]bool{}
	for path := range before {
		set[path] = true
	}
	for path := range after {
		set[path] = true
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	effects := make([]syncrecords.ImportPath, 0, len(paths))
	writes := map[string][]byte{}
	var deletions []string
	for _, path := range paths {
		old, oldOK := before[path]
		current, currentOK := after[path]
		if oldOK && currentOK && Digest(old) == Digest(current) {
			continue
		}
		effect := syncrecords.ImportPath{Path: path, Before: "absent", After: "absent"}
		if oldOK {
			effect.Before = Digest(old)
		}
		if currentOK {
			effect.After = Digest(current)
			writes[path] = append([]byte(nil), current...)
		} else {
			deletions = append(deletions, path)
		}
		effects = append(effects, effect)
	}
	return effects, writes, deletions
}

type EffectState string

const (
	AllBefore  EffectState = "all_before"
	AllAfter   EffectState = "all_after"
	Mixed      EffectState = "mixed_before_after"
	Unexpected EffectState = "unexpected"
)

// Inspect proves whether every effect is still at its before value, already at
// its after value, a recoverable mix, or contains an independent write.
func Inspect(root string, effects []syncrecords.ImportPath) (EffectState, error) {
	resolver, err := localfs.NewResolver(root)
	if err != nil {
		return Unexpected, err
	}
	before, after := 0, 0
	for _, effect := range effects {
		value, err := currentDigest(resolver, effect.Path)
		if err != nil {
			return Unexpected, err
		}
		switch value {
		case effect.Before:
			before++
		case effect.After:
			after++
		default:
			return Unexpected, nil
		}
	}
	switch {
	case before == len(effects):
		return AllBefore, nil
	case after == len(effects):
		return AllAfter, nil
	default:
		return Mixed, nil
	}
}

// Apply advances only effects that still have their reviewed before value.
// Effects already at the exact after value are idempotently skipped. Any other
// value is an independent edit and stops recovery without overwriting it.
func Apply(root string, effects []syncrecords.ImportPath, writes map[string][]byte) error {
	resolver, err := localfs.NewResolver(root)
	if err != nil {
		return err
	}
	for _, effect := range effects {
		value, err := currentDigest(resolver, effect.Path)
		if err != nil {
			return err
		}
		if value == effect.After {
			continue
		}
		if value != effect.Before {
			return fmt.Errorf("path %q changed after import planning", effect.Path)
		}
		target, err := resolver.Resolve(effect.Path)
		if err != nil {
			return err
		}
		if effect.After == "absent" {
			if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		raw, ok := writes[effect.Path]
		if !ok || Digest(raw) != effect.After {
			return fmt.Errorf("target bytes for %q do not match the import record", effect.Path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		// Re-resolve after directory creation so a replaced/symlinked ancestor
		// is detected before opening the temporary file.
		target, err = resolver.Resolve(effect.Path)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(target), ".agent-dispatch-import-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		okWrite := false
		defer func() {
			if !okWrite {
				_ = os.Remove(tmpName)
			}
		}()
		err = tmp.Chmod(0o600)
		if err == nil {
			var written int
			written, err = tmp.Write(raw)
			if err == nil && written != len(raw) {
				err = fmt.Errorf("short import write for %q", effect.Path)
			}
		}
		if err == nil {
			err = tmp.Sync()
		}
		closeErr := tmp.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(tmpName, target)
		}
		if err != nil {
			_ = os.Remove(tmpName)
			return err
		}
		okWrite = true
	}
	return nil
}

func currentDigest(resolver *localfs.Resolver, path string) (string, error) {
	target, err := resolver.Resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("path %q is not a regular file", path)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return Digest(raw), nil
}
