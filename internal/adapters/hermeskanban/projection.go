package hermeskanban

import (
	"context"
	"errors"
	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// UnknownStatusError reports a task status outside the version-frozen
// public enum: the projection becomes unknown (unavailable), never
// success (HER-008, E4-T4 acceptance).
type UnknownStatusError struct {
	Status string
}

func (e *UnknownStatusError) Error() string {
	return fmt.Sprintf("task status %q is outside the frozen public enum; the execution projection is unknown", truncate(e.Status, 40))
}

// executionStates is the documented, version-tested mapping from the
// frozen public status enum (E0-T4 §6) onto the portable execution
// axis (HER-008):
//
//	done                 -> succeeded   (the public complete operation set completed_at)
//	running              -> running     (a worker holds the task)
//	ready, scheduled,
//	todo, triage, review -> queued      (accepted, not yet executing)
//	blocked              -> queued      (paused; not a terminal outcome and never failed)
//	archived             -> canceled    (removed from the active queue)
//
// Any other status string is malformed and maps to the unknown
// projection (unavailable), never success.
var executionStates = map[string]records.ExecutionState{
	"done":      records.ExecSucceeded,
	"running":   records.ExecRunning,
	"ready":     records.ExecQueued,
	"scheduled": records.ExecQueued,
	"todo":      records.ExecQueued,
	"triage":    records.ExecQueued,
	"review":    records.ExecQueued,
	"blocked":   records.ExecQueued,
	"archived":  records.ExecCanceled,
}

// MapExecution projects one typed task record onto the portable
// execution axis. The observation time is the terminal timestamp when
// one exists (completed_at for done), else the record's created_at.
func MapExecution(task TaskRecord) (ports.ExecutionProjection, error) {
	state, ok := executionStates[task.Status]
	if !ok {
		return ports.ExecutionProjection{State: records.ExecUnavailable, ExternalRef: task.ID}, &UnknownStatusError{Status: task.Status}
	}
	observed := task.CreatedAt
	if task.CompletedAt != nil && *task.CompletedAt > 0 {
		observed = *task.CompletedAt
	}
	return ports.ExecutionProjection{
		State:            state,
		ExternalRef:      task.ID,
		TargetObservedAt: epochToTimestamp(observed),
	}, nil
}

// GetExecution implements ports.Sink (E4-T4): the read-only public show
// mapped onto the portable execution axis. An accepted task whose
// execution cannot be derived reports unavailable — acceptance and
// execution stay separate (HER-008). The execution_status capability is
// required and never emulated; interim truth source is the frozen 0.20.5
// runtime-verified interface plus the eligibility probe (E11-T2's
// capability probe restores per-executable shape proof).
func (s *Sink) GetExecution(ctx context.Context, ref string) (ports.ExecutionProjection, error) {
	if _, err := s.adapter.Probe(ctx); err != nil {
		return ports.ExecutionProjection{}, err
	}
	task, err := s.adapter.client.Show(ctx, s.board, ref)
	if err != nil {
		var absent *UnknownTaskError
		if errors.As(err, &absent) {
			return ports.ExecutionProjection{State: records.ExecUnavailable, ExternalRef: ref}, err
		}
		return ports.ExecutionProjection{}, err
	}
	return MapExecution(task)
}
