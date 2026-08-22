package hermeswebhook

import (
	"fmt"
	"sort"
	"strings"
)

// ConfigError is a definite target-configuration defect detected at
// adapter construction (invalid endpoint, header name, timeout, or
// authentication shape). It maps to the configuration error class
// (exit 3), never to target unavailability.
type ConfigError struct {
	Detail string
}

func (e *ConfigError) Error() string { return "hermes-webhook target: " + e.Detail }

// CapabilityError is the HER-005 fail-closed gate: the target's
// required capabilities name a behavior this adapter's verified
// declaration does not provide. The route must drop the requirement or
// select a different target; the adapter never emulates the behavior.
type CapabilityError struct {
	TargetID string
	Missing  []string
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("target %s does not provide the required capabilities: %s", e.TargetID, strings.Join(e.Missing, ", "))
}

// Remediation states the two operator exits from a capability mismatch.
func (e *CapabilityError) Remediation() string {
	return "remove the capability from the target's required_capabilities, or point the route at a target that provides it (HER-005; the adapter does not emulate missing capabilities)"
}

// missingCapabilities computes the sorted missing capability names
// against the adapter's declared capability set.
func missingCapabilities(targetID string, required []string) *CapabilityError {
	declared := declaredCapabilities.BoolMap()
	var missing []string
	for _, name := range required {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if supported, ok := declared[name]; !ok || !supported {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return &CapabilityError{TargetID: targetID, Missing: missing}
}
