// Package localfs implements the safe root resolver and containment
// defense (E2-T2, PTH-001, PTH-002, SEC-002): untrusted event paths are
// normalized, joined against a trusted resolved root, and checked for
// symlink escape before any file is opened or hashed. Deleted paths are
// never opened. Non-regular files and files over the configured size
// limit are refused structurally (the digest stays unknown, never falsely
// unchanged).
package localfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
)

// Guard error classes. Callers map them to structural policy outcomes,
// not to silent skips.
var (
	// ErrEscape means the path leaves the root lexically (traversal,
	// absolute) or through a symlink; detected before any read.
	ErrEscape = errors.New("path escapes the resource root")
	// ErrNotRegular means the contained path exists but is not a
	// regular file (directory, symlink, device, ...).
	ErrNotRegular = errors.New("path is not a regular file")
	// ErrMissing means the path disappeared between resolution and
	// open (a deleted-in-flight race); the caller re-classifies rather
	// than treating it as a type defect.
	ErrMissing = errors.New("path disappeared before open")
	// ErrTooLarge means the file exceeds the configured hash limit; its
	// digest is structurally unknown.
	ErrTooLarge = errors.New("file exceeds the size limit")
	// ErrPathTooLong means the relative path exceeds the configured
	// path-length limit (SEC-009).
	ErrPathTooLong = errors.New("relative path exceeds the length limit")
)

// DefaultMaxPathBytes is the per-path byte cap when the operator sets no
// limits.max_path_bytes (SEC-009); it matches the schema's example
// envelope and leaves ample room for deep vault trees.
const DefaultMaxPathBytes int64 = 4096

// Resolver resolves untrusted relative event paths inside one trusted
// resource root.
type Resolver struct {
	root         string // absolute, symlinks resolved
	maxPathBytes int64
}

// NewResolver validates and resolves the trusted root. The root must be
// an absolute existing directory; symlinks in the root itself are
// resolved once here so later containment checks compare canonical
// prefixes.
func NewResolver(root string) (*Resolver, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("resource root %q must be absolute", root)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolving resource root: %w", err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat resource root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("resource root %q is not a directory", root)
	}
	return &Resolver{root: resolved, maxPathBytes: DefaultMaxPathBytes}, nil
}

// SetLimits applies explicit operator limits (limits.max_path_bytes);
// zero or negative restores the default cap.
func (r *Resolver) SetLimits(maxPathBytes int64) {
	if maxPathBytes > 0 {
		r.maxPathBytes = maxPathBytes
	} else {
		r.maxPathBytes = DefaultMaxPathBytes
	}
}

// Root returns the resolved absolute root.
func (r *Resolver) Root() string { return r.root }

// checkPath validates the untrusted relative path lexically: valid UTF-8,
// relative, forward slashes, no NUL, no traversal, and inside the length
// limit. It performs no filesystem access.
func (r *Resolver) checkPath(rel string) (string, error) {
	if int64(len(rel)) > r.maxPathBytes {
		return "", ErrPathTooLong
	}
	normalized, err := records.NormalizePath(rel)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrEscape, err)
	}
	return normalized, nil
}

// Resolve returns the canonical absolute path for rel after resolving
// symlinks, verifying the result stays inside the root. For paths that
// do not exist (a deleted file), the lexically checked join is returned
// when every existing ancestor stays contained: deletion evidence never
// requires opening the path itself.
func (r *Resolver) Resolve(rel string) (string, error) {
	normalized, err := r.checkPath(rel)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(r.root, filepath.FromSlash(normalized))
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if err := r.checkAncestors(normalized); err != nil {
				return "", err
			}
			return joined, nil
		}
		return "", fmt.Errorf("resolving %q: %w", normalized, err)
	}
	if !r.contains(real) {
		return "", fmt.Errorf("%w: %q resolves outside the root", ErrEscape, normalized)
	}
	return real, nil
}

// checkAncestors verifies every existing ancestor directory of a
// not-currently-existing path resolves inside the root, so a dangling
// symlink or symlinked directory cannot position a future path outside.
// checkAncestors walks shallowest-first from the root over every
// ancestor of a not-currently-existing path. No intermediate component
// is traversed before it is itself checked, so a live or dangling
// symlink at any depth is verified against the root before the walk
// continues below it. The first missing component ends the walk:
// nothing deeper can exist.
func (r *Resolver) checkAncestors(normalized string) error {
	cur := r.root
	for _, part := range strings.Split(normalized, "/") {
		candidate := filepath.Join(cur, part)
		info, err := os.Lstat(candidate)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("lstat ancestor of %q: %w", normalized, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if real, err := filepath.EvalSymlinks(candidate); err == nil {
				if !r.contains(real) {
					return fmt.Errorf("%w: ancestor of %q resolves outside the root", ErrEscape, normalized)
				}
				cur = real
				continue
			}
			if err := r.checkSymlinkTarget(candidate); err != nil {
				return fmt.Errorf("%w: ancestor of %q is a dangling symlink outside the root: %v", ErrEscape, normalized, err)
			}
			return nil // contained dangling link; deeper components cannot exist
		}
		cur = candidate
	}
	return nil
}

// checkSymlinkTarget verifies a (possibly dangling) symlink's declared
// target points lexically inside the root.
func (r *Resolver) checkSymlinkTarget(linkPath string) error {
	target, err := os.Readlink(linkPath)
	if err != nil {
		return err
	}
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(linkPath), resolved)
	}
	resolved = filepath.Clean(resolved)
	if !r.contains(resolved) {
		return fmt.Errorf("target %q escapes the root", target)
	}
	return nil
}

func (r *Resolver) contains(abs string) bool {
	return abs == r.root || strings.HasPrefix(abs, r.root+string(filepath.Separator))
}

// OpenRegular resolves rel under containment defense, opens it without
// following a final-component symlink, and enforces the file-type and
// size guard. It is the only sanctioned way to open event paths for
// reading or hashing (SEC-002); callers never open joined paths
// themselves. A residual TOCTOU window between EvalSymlinks and open
// remains on portable Go: O_NOFOLLOW pins the final component, but an
// intermediate directory component swapped for a symlink in the window
// is not re-verified after open. A descriptor-relative openat walk is
// tracked as later hardening; v0.1 accepts the documented window.
func (r *Resolver) OpenRegular(rel string, maxSize int64) (*os.File, fs.FileInfo, error) {
	if maxSize <= 0 {
		return nil, nil, fmt.Errorf("maxSize must be positive")
	}
	real, err := r.Resolve(rel)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(real, os.O_RDONLY|syscall_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("%w: %v", ErrMissing, err)
		}
		// A symlink at the final component fails here (ELOOP): refuse
		// rather than follow (PTH-002); permission failures classify
		// as not-openable regular candidates too.
		return nil, nil, fmt.Errorf("%w: %v", ErrNotRegular, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("stat %q: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("%w: %q is %s", ErrNotRegular, rel, info.Mode().Type())
	}
	if info.Size() > maxSize {
		f.Close()
		return nil, nil, fmt.Errorf("%w: %q is %d bytes (limit %d)", ErrTooLarge, rel, info.Size(), maxSize)
	}
	return f, info, nil
}

// StatContained returns file info for rel under the same containment
// defense without keeping the file open; used for classification-time
// facts. It never opens deleted paths.
func (r *Resolver) StatContained(rel string) (fs.FileInfo, error) {
	real, err := r.Resolve(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(real)
	if err != nil {
		return nil, fmt.Errorf("lstat %q: %w", rel, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %q is a symlink", ErrNotRegular, rel)
	}
	return info, nil
}
