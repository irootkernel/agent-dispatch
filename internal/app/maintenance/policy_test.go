package maintenance

import (
	"testing"
	"time"

	"github.com/rootkernel/jjukkumi/internal/config"
)

func strp(s string) *string { return &s }

// TestEffectivePolicyDefaultsAndOverrides proves OPS-003's defaults and
// the configured overrides, with the unresolved class having no horizon.
func TestEffectivePolicyDefaultsAndOverrides(t *testing.T) {
	p, err := EffectivePolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Observations != 30*24*time.Hour || p.Attempts != 30*24*time.Hour {
		t.Fatalf("30-day defaults wrong: %v", p)
	}
	if p.CompletedReceipts != 180*24*time.Hour || p.ResolvedQuarantine != 180*24*time.Hour {
		t.Fatalf("180-day defaults wrong: %v", p)
	}
	p, err = EffectivePolicy(&config.Retention{
		Observations:      strp("7d"),
		Attempts:          strp("10d"),
		CompletedReceipts: strp("90d"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Observations != 7*24*time.Hour || p.Attempts != 10*24*time.Hour || p.CompletedReceipts != 90*24*time.Hour {
		t.Fatalf("overrides not applied: %v", p)
	}
	if _, err := EffectivePolicy(&config.Retention{Unresolved: strp("30d")}); err == nil {
		t.Fatalf("unresolved accepts a horizon")
	}
	if _, err := EffectivePolicy(&config.Retention{Observations: strp("bogus")}); err == nil {
		t.Fatalf("invalid duration accepted")
	}
}

// TestClampBeforeOnlyNarrows proves --before can only shorten horizons.
func TestClampBeforeOnlyNarrows(t *testing.T) {
	p := Policy{Observations: 30 * 24 * time.Hour, Attempts: 10 * time.Hour, CompletedReceipts: 180 * 24 * time.Hour, ResolvedQuarantine: 180 * 24 * time.Hour}
	c := p.ClampBefore(24 * time.Hour)
	if c.Observations != 24*time.Hour {
		t.Fatalf("longer horizon not clamped: %v", c)
	}
	if c.Attempts != 10*time.Hour {
		t.Fatalf("shorter horizon widened: %v", c)
	}
	if c.CompletedReceipts != 24*time.Hour || c.ResolvedQuarantine != 24*time.Hour {
		t.Fatalf("clamp not applied: %v", c)
	}
	if (Policy{}).ClampBefore(time.Hour) != (Policy{}) {
		t.Fatalf("zero clamp must be a no-op")
	}
}
