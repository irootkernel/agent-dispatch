// Package records holds the canonical record value objects and their
// closed enums (DAT-001). Every enum parses fail-closed: an unknown value
// is an error, never a silent default (DAT-009 posture).
package records

import "fmt"

// Operation is a change operation (source-observation $defs.change).
type Operation string

const (
	OpCreate Operation = "create"
	OpModify Operation = "modify"
	OpDelete Operation = "delete"
)

func ParseOperation(s string) (Operation, error) {
	switch Operation(s) {
	case OpCreate, OpModify, OpDelete:
		return Operation(s), nil
	}
	return "", fmt.Errorf("unknown operation %q", s)
}

// FileType classifies the changed path.
type FileType string

const (
	FileRegular   FileType = "regular"
	FileDirectory FileType = "directory"
	FileSymlink   FileType = "symlink"
	FileOther     FileType = "other"
	FileUnknown   FileType = "unknown"
)

func ParseFileType(s string) (FileType, error) {
	switch FileType(s) {
	case FileRegular, FileDirectory, FileSymlink, FileOther, FileUnknown:
		return FileType(s), nil
	}
	return "", fmt.Errorf("unknown file type %q", s)
}

// DigestStatus says whether a content digest is available for a change.
type DigestStatus string

const (
	DigestKnown         DigestStatus = "known"
	DigestUnavailable   DigestStatus = "unavailable"
	DigestNotApplicable DigestStatus = "not_applicable"
)

func ParseDigestStatus(s string) (DigestStatus, error) {
	switch DigestStatus(s) {
	case DigestKnown, DigestUnavailable, DigestNotApplicable:
		return DigestStatus(s), nil
	}
	return "", fmt.Errorf("unknown digest status %q", s)
}

// Classification labels a policy decision's batch (dispatch-plan schema).
type Classification string

const (
	ClassNormal    Classification = "normal"
	ClassProtected Classification = "protected"
	ClassBulk      Classification = "bulk"
	ClassOverflow  Classification = "overflow"
	ClassMalformed Classification = "malformed"
	ClassStale     Classification = "stale"
	ClassUnknown   Classification = "unknown"
)

func ParseClassification(s string) (Classification, error) {
	switch Classification(s) {
	case ClassNormal, ClassProtected, ClassBulk, ClassOverflow, ClassMalformed, ClassStale, ClassUnknown:
		return Classification(s), nil
	}
	return "", fmt.Errorf("unknown classification %q", s)
}

// Disposition is a policy decision outcome.
type Disposition string

const (
	DispositionDrop         Disposition = "drop"
	DispositionDispatch     Disposition = "dispatch"
	DispositionMergePending Disposition = "merge_pending"
	DispositionQuarantine   Disposition = "quarantine"
	DispositionReconcile    Disposition = "reconcile"
)

func ParseDisposition(s string) (Disposition, error) {
	switch Disposition(s) {
	case DispositionDrop, DispositionDispatch, DispositionMergePending, DispositionQuarantine, DispositionReconcile:
		return Disposition(s), nil
	}
	return "", fmt.Errorf("unknown disposition %q", s)
}

// GenerationAction is the dispatch plan's effect on route generations.
type GenerationAction string

const (
	GenNone           GenerationAction = "none"
	GenCreateIfIdle   GenerationAction = "create_if_idle"
	GenIncrementDirty GenerationAction = "increment_dirty"
	GenMergeReconcile GenerationAction = "merge_reconcile"
)

func ParseGenerationAction(s string) (GenerationAction, error) {
	switch GenerationAction(s) {
	case GenNone, GenCreateIfIdle, GenIncrementDirty, GenMergeReconcile:
		return GenerationAction(s), nil
	}
	return "", fmt.Errorf("unknown generation action %q", s)
}

// IntentState is the dispatch intent lifecycle (dispatch-intent schema).
type IntentState string

const (
	IntentReady        IntentState = "ready"
	IntentSubmitting   IntentState = "submitting"
	IntentAccepted     IntentState = "accepted"
	IntentRejected     IntentState = "rejected"
	IntentUnknown      IntentState = "unknown"
	IntentRetryWait    IntentState = "retry_wait"
	IntentReconciling  IntentState = "reconciling"
	IntentDeadLettered IntentState = "dead_lettered"
	IntentSuperseded   IntentState = "superseded"
	IntentCompleted    IntentState = "completed"
	IntentFailed       IntentState = "failed"
	IntentCanceled     IntentState = "canceled"
)

func ParseIntentState(s string) (IntentState, error) {
	switch IntentState(s) {
	case IntentReady, IntentSubmitting, IntentAccepted, IntentRejected, IntentUnknown,
		IntentRetryWait, IntentReconciling, IntentDeadLettered, IntentSuperseded,
		IntentCompleted, IntentFailed, IntentCanceled:
		return IntentState(s), nil
	}
	return "", fmt.Errorf("unknown intent state %q", s)
}

// ReceiptKind distinguishes acceptance from execution projections.
type ReceiptKind string

const (
	ReceiptAcceptance          ReceiptKind = "acceptance"
	ReceiptExecutionProjection ReceiptKind = "execution_projection"
)

func ParseReceiptKind(s string) (ReceiptKind, error) {
	switch ReceiptKind(s) {
	case ReceiptAcceptance, ReceiptExecutionProjection:
		return ReceiptKind(s), nil
	}
	return "", fmt.Errorf("unknown receipt kind %q", s)
}

// AcceptanceState is the target acceptance axis.
type AcceptanceState string

const (
	AcceptanceAccepted AcceptanceState = "accepted"
	AcceptanceRejected AcceptanceState = "rejected"
	AcceptanceUnknown  AcceptanceState = "unknown"
)

func ParseAcceptanceState(s string) (AcceptanceState, error) {
	switch AcceptanceState(s) {
	case AcceptanceAccepted, AcceptanceRejected, AcceptanceUnknown:
		return AcceptanceState(s), nil
	}
	return "", fmt.Errorf("unknown acceptance state %q", s)
}

// ExecutionState is the target execution axis.
type ExecutionState string

const (
	ExecUnavailable ExecutionState = "unavailable"
	ExecQueued      ExecutionState = "queued"
	ExecRunning     ExecutionState = "running"
	ExecSucceeded   ExecutionState = "succeeded"
	ExecFailed      ExecutionState = "failed"
	ExecCanceled    ExecutionState = "canceled"
)

func ParseExecutionState(s string) (ExecutionState, error) {
	switch ExecutionState(s) {
	case ExecUnavailable, ExecQueued, ExecRunning, ExecSucceeded, ExecFailed, ExecCanceled:
		return ExecutionState(s), nil
	}
	return "", fmt.Errorf("unknown execution state %q", s)
}

// WorkStatus is the work receipt status.
type WorkStatus string

const (
	WorkBegan     WorkStatus = "begun"
	WorkCompleted WorkStatus = "completed"
	WorkFailed    WorkStatus = "failed"
)

func ParseWorkStatus(s string) (WorkStatus, error) {
	switch WorkStatus(s) {
	case WorkBegan, WorkCompleted, WorkFailed:
		return WorkStatus(s), nil
	}
	return "", fmt.Errorf("unknown work status %q", s)
}

// FailureCode is the closed work-receipt failure set.
type FailureCode string

const (
	FailAgentError       FailureCode = "agent_error"
	FailCanceled         FailureCode = "canceled"
	FailTimeout          FailureCode = "timeout"
	FailEnvironmentError FailureCode = "environment_error"
)

func ParseFailureCode(s string) (FailureCode, error) {
	switch FailureCode(s) {
	case FailAgentError, FailCanceled, FailTimeout, FailEnvironmentError:
		return FailureCode(s), nil
	}
	return "", fmt.Errorf("unknown failure code %q", s)
}
