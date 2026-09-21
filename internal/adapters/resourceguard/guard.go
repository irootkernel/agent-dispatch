// Package resourceguard serializes cooperative snapshot and apply operations
// without placing bookkeeping inside a synchronized resource.
package resourceguard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

type Guard struct{ file *os.File }

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func Acquire(stateDir, resourceID string) (*Guard, error) {
	if !filepath.IsAbs(stateDir) || !safeName.MatchString(resourceID) {
		return nil, fmt.Errorf("invalid resource guard binding")
	}
	dir := filepath.Join(stateDir, "resource-guards")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, resourceID+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("resource %s is guarded: %w", resourceID, err)
	}
	return &Guard{file: f}, nil
}
func (g *Guard) Close() error {
	if g == nil || g.file == nil {
		return nil
	}
	_ = syscall.Flock(int(g.file.Fd()), syscall.LOCK_UN)
	err := g.file.Close()
	g.file = nil
	return err
}
