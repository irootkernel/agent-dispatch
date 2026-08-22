// Package maintenance implements the retention policy resolution
// (OPS-003, OPS-004, retention-and-privacy §2-§3): the default
// per-class horizons, the configured overrides, and the cutoff
// computation the prune planner and executor share. Unresolved
// lineages have no horizon — they are retained until resolution.
package maintenance

import (
	"fmt"
	"time"

	"github.com/rootkernel/jjukkumi/internal/config"
)

// Default horizons (OPS-003).
const (
	DefaultObservations       = 30 * 24 * time.Hour
	DefaultAttempts           = 30 * 24 * time.Hour
	DefaultCompletedReceipts  = 180 * 24 * time.Hour
	DefaultResolvedQuarantine = 180 * 24 * time.Hour
)

// Policy is the effective per-class retention horizon set.
type Policy struct {
	Observations       time.Duration
	Attempts           time.Duration
	CompletedReceipts  time.Duration
	ResolvedQuarantine time.Duration
}

// EffectivePolicy resolves the instance-level retention overrides over
// the defaults (configuration-spec §10). The unresolved class has no
// horizon and never appears here.
func EffectivePolicy(cfg *config.Retention) (Policy, error) {
	p := Policy{
		Observations:       DefaultObservations,
		Attempts:           DefaultAttempts,
		CompletedReceipts:  DefaultCompletedReceipts,
		ResolvedQuarantine: DefaultResolvedQuarantine,
	}
	if cfg == nil {
		return p, nil
	}
	apply := func(field, text string, into *time.Duration) error {
		if text == "" {
			return nil
		}
		d, err := config.ParseDuration(text)
		if err != nil {
			return fmt.Errorf("retention.%s: %w", field, err)
		}
		*into = time.Duration(d.Nanos)
		return nil
	}
	for _, set := range []struct {
		field string
		text  string
		into  *time.Duration
	}{
		{"observations", deref(cfg.Observations), &p.Observations},
		{"attempts", deref(cfg.Attempts), &p.Attempts},
		{"completed_receipts", deref(cfg.CompletedReceipts), &p.CompletedReceipts},
		{"resolved_quarantine", deref(cfg.ResolvedQuarantine), &p.ResolvedQuarantine},
	} {
		if err := apply(set.field, set.text, set.into); err != nil {
			return Policy{}, err
		}
	}
	if cfg.Unresolved != nil && *cfg.Unresolved != "" && *cfg.Unresolved != "forever" {
		return Policy{}, fmt.Errorf("retention.unresolved: only forever is valid (the unresolved class is retained until resolution)")
	}
	return p, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ClampBefore narrows every horizon to at most the --before age, so an
// explicit operator bound can only prune more, never less.
func (p Policy) ClampBefore(before time.Duration) Policy {
	if before <= 0 {
		return p
	}
	min := func(a, b time.Duration) time.Duration {
		if a < b {
			return a
		}
		return b
	}
	p.Observations = min(p.Observations, before)
	p.Attempts = min(p.Attempts, before)
	p.CompletedReceipts = min(p.CompletedReceipts, before)
	p.ResolvedQuarantine = min(p.ResolvedQuarantine, before)
	return p
}
