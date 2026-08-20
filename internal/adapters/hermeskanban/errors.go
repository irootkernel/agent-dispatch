package hermeskanban

import (
	"fmt"
	"strings"
)

// diagnosticBound is the maximum bytes of child output any error or
// diagnostic embeds (SEC-004 bounded output; error-model §6: no
// unrestricted subprocess output). Full output is never propagated.
const diagnosticBound = 300

// ExecutableMissingError reports that the configured Hermes executable
// cannot be located before any invocation (definite pre-submit failure).
type ExecutableMissingError struct {
	Executable string
	Detail     string
}

func (e *ExecutableMissingError) Error() string {
	return fmt.Sprintf("hermes executable %q is not usable: %s", e.Executable, e.Detail)
}

// Remediation is the actionable operator guidance.
func (e *ExecutableMissingError) Remediation() string {
	return "install Hermes, ensure the configured targets executable resolves on PATH (or set an absolute path), and rerun; see docs/integrations/hermes-public-interface-report.md"
}

// CapabilityError reports required capabilities the verified target does
// not provide (HER-005: fail validation; never silently emulate, and a
// reduced guarantee requires an explicit accepted ADR, not this error).
type CapabilityError struct {
	TargetID string
	Missing  []string
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("target %s is missing required capabilities: %s; route validation fails (HER-005)", e.TargetID, strings.Join(e.Missing, ", "))
}

// Remediation is the actionable operator guidance.
func (e *CapabilityError) Remediation() string {
	return "remove the requirement, use a verified Hermes version that provides the capability, or record an accepted reduced-guarantee decision; do not emulate the capability"
}

// InvalidRequirementError reports a required_capabilities entry that is
// not one of the eight HER-004 names: a configuration defect the
// validation surface fails closed on, never a reduced guarantee.
type InvalidRequirementError struct {
	TargetID string
	Unknown  []string
}

func (e *InvalidRequirementError) Error() string {
	return fmt.Sprintf("target %s requires unknown capability names %s; valid names are the eight HER-004 declarations", e.TargetID, strings.Join(e.Unknown, ", "))
}

// TimeoutError reports an invocation that exceeded its deadline. The
// outcome after a timeout is ambiguous by construction (DUR-005): the
// write may or may not have happened.
type TimeoutError struct {
	Command string
	After   string
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("hermes %s timed out after %s; acceptance is unknown and reconciliation is required", e.Command, e.After)
}

// ExcessiveOutputError reports a capture stream that exceeded the
// configured write-side bound. Output truncation is an ambiguous outcome
// (DUR-005 / hermes-integration §8): the invocation may have acted
// before the bound tripped.
type ExcessiveOutputError struct {
	Command string
	Bound   int64
}

func (e *ExcessiveOutputError) Error() string {
	return fmt.Sprintf("hermes %s exceeded the %d-byte output bound; acceptance is unknown and reconciliation is required", e.Command, e.Bound)
}

// MalformedOutputError reports stdout that is not the verified structured
// --json shape after an invocation exited 0 (possible acceptance; DUR-005
// unknown, never failed).
type MalformedOutputError struct {
	Command string
	Reason  string
}

func (e *MalformedOutputError) Error() string {
	return fmt.Sprintf("hermes %s produced malformed structured output: %s", e.Command, e.Reason)
}

// UnknownTaskError reports the frozen exit-1 `no such task: <id>` behavior
// (E0-T4 §8). It is a deterministic absence proof for lookups, not a
// transport failure.
type UnknownTaskError struct {
	Ref string
}

func (e *UnknownTaskError) Error() string {
	return fmt.Sprintf("no such task: %s", e.Ref)
}

// UnknownBoardError reports the frozen exit-1 unknown-board behavior with
// its recovery hint (E0-T4 §8).
type UnknownBoardError struct {
	Board string
}

func (e *UnknownBoardError) Error() string {
	return fmt.Sprintf("kanban board %q does not exist", e.Board)
}

// ArgumentRejectedError reports the frozen argparse exit-2 behavior: the
// CLI rejected the argument array itself. This is a definite pre-submit
// adapter defect — nothing was sent that Hermes could have accepted.
type ArgumentRejectedError struct {
	Command string
	Detail  string
}

func (e *ArgumentRejectedError) Error() string {
	return fmt.Sprintf("hermes rejected the argument array for %s: %s", e.Command, e.Detail)
}

// CommandFailedError reports any other non-zero exit whose stderr does
// not match a frozen behavior. The outcome stays ambiguous for submit
// contexts (DUR-005); classification is the caller's contract decision.
type CommandFailedError struct {
	Command  string
	ExitCode int
	Detail   string
}

func (e *CommandFailedError) Error() string {
	return fmt.Sprintf("hermes %s failed with exit %d: %s", e.Command, e.ExitCode, e.Detail)
}

// truncate keeps embedded child output bounded in error text.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
