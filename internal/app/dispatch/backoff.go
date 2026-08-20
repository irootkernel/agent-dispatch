package dispatch

import (
	"fmt"
	"math"
	"time"
)

// Backoff is the persisted exponential submission retry policy
// (configuration-spec §9, DUR-007). The policy is delivery-only; it never
// controls Hermes execution retries and never applies while an outcome is
// ambiguous (DUR-008: an ambiguity does not consume a normal retry until
// reconciliation proves non-acceptance).
type Backoff struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Multiplier     float64
	// JitterFraction in [0, 0.5] bounds the injectable jitter unit.
	JitterFraction float64
}

// Validate fails closed on out-of-envelope policies.
func (b Backoff) Validate() error {
	if b.MaxAttempts < 1 || b.MaxAttempts > 10 {
		return fmt.Errorf("max_attempts %d outside 1..10", b.MaxAttempts)
	}
	if b.InitialBackoff <= 0 || b.MaxBackoff < b.InitialBackoff {
		return fmt.Errorf("backoff bounds invalid (initial %s, max %s)", b.InitialBackoff, b.MaxBackoff)
	}
	if b.Multiplier <= 1 {
		return fmt.Errorf("multiplier %v must exceed 1", b.Multiplier)
	}
	if b.JitterFraction < 0 || b.JitterFraction > 0.5 {
		return fmt.Errorf("jitter fraction %v outside 0..0.5", b.JitterFraction)
	}
	return nil
}

// Delay returns the deterministic backoff delay after the given number of
// attempts, with the jitter unit injected in [0, 1): the same policy,
// attempt count, and unit always produce the same delay (TST-001). A unit
// below 0 or above 1 fails closed.
func (b Backoff) Delay(attemptCount int, jitterUnit float64) (time.Duration, error) {
	if err := b.Validate(); err != nil {
		return 0, err
	}
	if attemptCount < 1 {
		return 0, fmt.Errorf("attempt count %d must be >= 1", attemptCount)
	}
	if jitterUnit < 0 || jitterUnit > 1 {
		return 0, fmt.Errorf("jitter unit %v outside 0..1", jitterUnit)
	}
	exp := float64(b.InitialBackoff) * math.Pow(b.Multiplier, float64(attemptCount-1))
	capped := math.Min(exp, float64(b.MaxBackoff))
	jittered := capped * (1 + b.JitterFraction*jitterUnit)
	if jittered > float64(b.MaxBackoff) {
		jittered = float64(b.MaxBackoff)
	}
	return time.Duration(jittered), nil
}

// Exhausted reports whether the attempt budget is spent: automatic
// attempts stop at the limit (DUR-007, AC-205 posture).
func (b Backoff) Exhausted(attemptCount int) bool {
	return attemptCount >= b.MaxAttempts
}
