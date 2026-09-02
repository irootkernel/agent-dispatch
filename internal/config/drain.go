package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Drain policy of the v0.1.6 automatic notification draining contract
// (E16-T1, ADR-0022, NTF-010 through NTF-016): the declared block is
// optional with explicit effective defaults, the effective projection
// carries its own inspectable revision, and — per the operational
// follow-up §5 — the behavior joins the route revision but never the
// notification-policy revision, so notification identities and
// idempotency keys stay stable across drain changes.

// Drain modes (NTF-010): manual is the v0.1.5 behavior of an omitted
// block; after-command drains due work after registered successful
// transitions; scheduled drains only through the managed schedule.
const (
	DrainModeManual       = "manual"
	DrainModeAfterCommand = "after-command"
	DrainModeScheduled    = "scheduled"
)

// Effective drain defaults (v0.1.6 §4): an omitted block resolves to
// exactly these, and a declared block inherits each for an absent
// field.
const (
	DefaultDrainMode             = DrainModeManual
	DefaultDrainLimit            = 100
	DefaultDrainFailurePolicy    = "preserve-pending"
	DefaultDrainPendingWarnAfter = time.Hour
	DefaultDrainInitialBackoff   = 30 * time.Second
	DefaultDrainMaxBackoff       = 15 * time.Minute
	DefaultDrainMultiplier       = 2.0
	DefaultDrainJitterFraction   = 0.2
)

// drainFailurePolicyVocabulary is the closed failure-policy set: only
// preserve-pending exists in v0.1.6 — an automatic delivery failure
// leaves the record pending under its original identity.
var drainFailurePolicyVocabulary = map[string]bool{
	"preserve-pending": true,
}

// drainModeVocabulary is the closed mode set.
var drainModeVocabulary = map[string]bool{
	DrainModeManual:       true,
	DrainModeAfterCommand: true,
	DrainModeScheduled:    true,
}

// DrainPolicy is one route's effective drain policy: the declared block
// with every absent field resolved to its explicit default. The retry
// members are the parsed backoff envelope (NTF-014): initial delay,
// doubling cap, multiplier, and one symmetric jitter fraction applied
// per retry.
type DrainPolicy struct {
	Mode             string
	Limit            int
	FailurePolicy    string
	PendingWarnAfter time.Duration
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	Multiplier       float64
	JitterFraction   float64
}

// EffectiveNotificationDrain resolves the route's effective drain
// policy (E16-T1): nil or an absent block resolves to the manual
// v0.1.5-compat default, a declared block inherits the default for
// every absent field, and every present field must itself be valid
// (the loader's semantic validation rejects invalid declarations before
// any resolution, so an error here is a programming boundary, not an
// operator surface).
func EffectiveNotificationDrain(n *Notifications) (DrainPolicy, error) {
	policy := DrainPolicy{
		Mode:             DefaultDrainMode,
		Limit:            DefaultDrainLimit,
		FailurePolicy:    DefaultDrainFailurePolicy,
		PendingWarnAfter: DefaultDrainPendingWarnAfter,
		InitialBackoff:   DefaultDrainInitialBackoff,
		MaxBackoff:       DefaultDrainMaxBackoff,
		Multiplier:       DefaultDrainMultiplier,
		JitterFraction:   DefaultDrainJitterFraction,
	}
	if n == nil || n.Drain == nil {
		return policy, nil
	}
	d := n.Drain
	if d.Mode != "" {
		policy.Mode = d.Mode
	}
	if d.Limit != 0 {
		policy.Limit = d.Limit
	}
	if d.FailurePolicy != "" {
		policy.FailurePolicy = d.FailurePolicy
	}
	if d.PendingWarnAfter != "" {
		dur, err := ParseDuration(d.PendingWarnAfter)
		if err != nil {
			return DrainPolicy{}, fmt.Errorf("pending_warn_after: %w", err)
		}
		policy.PendingWarnAfter = time.Duration(dur.Nanos)
	}
	if d.Retry != nil {
		initial, err := ParseDuration(d.Retry.InitialBackoff)
		if err != nil {
			return DrainPolicy{}, fmt.Errorf("retry.initial_backoff: %w", err)
		}
		max, err := ParseDuration(d.Retry.MaxBackoff)
		if err != nil {
			return DrainPolicy{}, fmt.Errorf("retry.max_backoff: %w", err)
		}
		policy.InitialBackoff = time.Duration(initial.Nanos)
		policy.MaxBackoff = time.Duration(max.Nanos)
		policy.Multiplier = d.Retry.Multiplier
		policy.JitterFraction = d.Retry.JitterFraction
	}
	return policy, nil
}

// drainRevisionProjection is the canonical effective-drain projection
// the drain-policy revision digests (E16-T1): every behavior-affecting
// field in its resolved form. Durations render at nanosecond precision
// so distinct declarations never collide.
type drainRevisionProjection struct {
	Mode             string  `json:"mode"`
	Limit            int     `json:"limit"`
	FailurePolicy    string  `json:"failure_policy"`
	PendingWarnAfter int64   `json:"pending_warn_after_ns"`
	InitialBackoff   int64   `json:"initial_backoff_ns"`
	MaxBackoff       int64   `json:"max_backoff_ns"`
	Multiplier       float64 `json:"multiplier"`
	JitterFraction   float64 `json:"jitter_fraction"`
}

// NotificationDrainRevision digests the route's effective drain policy:
// the inspectable revision of v0.1.6 §5. It deliberately never feeds
// NotificationPolicyRevision — drain changes must not re-identify
// notifications the previous policy already created.
func NotificationDrainRevision(n *Notifications) string {
	policy, err := EffectiveNotificationDrain(n)
	if err != nil {
		// Semantic validation rejects invalid drain declarations before
		// any revision is computed; a failure here falls back to the
		// declared-shape defaults rather than inventing a digest.
		policy, _ = EffectiveNotificationDrain(nil)
	}
	enc, err := json.Marshal(drainRevisionProjection{
		Mode:             policy.Mode,
		Limit:            policy.Limit,
		FailurePolicy:    policy.FailurePolicy,
		PendingWarnAfter: int64(policy.PendingWarnAfter),
		InitialBackoff:   int64(policy.InitialBackoff),
		MaxBackoff:       int64(policy.MaxBackoff),
		Multiplier:       policy.Multiplier,
		JitterFraction:   policy.JitterFraction,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(enc)
	return hex.EncodeToString(sum[:])
}

// drainRouteProjection renders the drain member of the route revision's
// notifications projection (v0.1.6 §5: drain behavior joins the route
// revision). An absent block projects nothing, so a v0.1.5
// configuration without drain keeps its exact route revision — the
// manual default changes no behavior and must not pause production
// acknowledgement.
func drainRouteProjection(n *Notifications) map[string]any {
	if n == nil || n.Drain == nil {
		return nil
	}
	policy, err := EffectiveNotificationDrain(n)
	if err != nil {
		policy, _ = EffectiveNotificationDrain(nil)
	}
	if def, _ := EffectiveNotificationDrain(nil); policy == def {
		// A declared block that resolves to exactly the v0.1.5 default
		// changes no behavior and must not change the route revision.
		return nil
	}
	return map[string]any{
		"mode":               policy.Mode,
		"limit":              policy.Limit,
		"failure_policy":     policy.FailurePolicy,
		"pending_warn_after": policy.PendingWarnAfter.String(),
		"retry": map[string]any{
			"initial_backoff": policy.InitialBackoff.String(),
			"max_backoff":     policy.MaxBackoff.String(),
			"multiplier":      policy.Multiplier,
			"jitter_fraction": policy.JitterFraction,
		},
	}
}
