package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// MutateFile loads the configuration at path, applies one mutation to
// the loaded document, validates the candidate (schema and semantic
// checks), and replaces the file atomically (CLI-015): the candidate is
// written to a private temporary file in the same directory, fsynced,
// and renamed over the original with the original's mode preserved. A
// rejected candidate or a failed write leaves the original file
// untouched; the mutation never partially writes a configuration.
// Mutation commands are single-writer by design: two concurrent
// mutations on one file serialize only at the final rename (the last
// validated candidate wins) — every written document is still complete
// and validated — exactly like the operator editing the file by hand.
func MutateFile(path string, mutate func(*Config) error) error {
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	if err := mutate(cfg); err != nil {
		return err
	}
	if err := SchemaValidate(cfg); err != nil {
		return fmt.Errorf("candidate failed schema validation; nothing was written: %w", err)
	}
	if errs, _ := SemanticValidate(cfg); len(errs) > 0 {
		return fmt.Errorf("candidate failed semantic validation; nothing was written: semantic: %s", joinErrors(errs))
	}
	data, err := MarshalYAML(cfg)
	if err != nil {
		return err
	}
	return atomicReplace(path, data)
}

// MutateDestination applies one destination-qualified mutation: the
// mutation receives the exact destination identified by routeID and
// destinationID and may modify only that destination's fields. The
// route's destination-qualified edit pauses the acknowledged route
// through the ordinary revision change; nothing here edits `enabled`.
func MutateDestination(path, routeID, destinationID string, mutate func(*Destination) error) error {
	return MutateFile(path, func(cfg *Config) error {
		route, ok := cfg.Routes[routeID]
		if !ok {
			return fmt.Errorf("route %q is not defined", routeID)
		}
		index := -1
		for i, d := range route.Destinations {
			if d.ID == destinationID {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("destination %q is not defined in route %q", destinationID, routeID)
		}
		if err := mutate(&route.Destinations[index]); err != nil {
			return err
		}
		cfg.Routes[routeID] = route
		return nil
	})
}

// atomicReplace writes data to path through a same-directory private
// temporary file (fsynced, mode copied from the existing file or
// owner-only for a new file) and renames it into place, so readers
// observe either the old or the new complete document, never a partial
// write.
func atomicReplace(path string, data []byte) error {
	dir := filepath.Dir(path)
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".agent-dispatch-mutate-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if dirf, err := os.Open(dir); err == nil {
		defer dirf.Close()
		_ = dirf.Sync()
	}
	return nil
}
