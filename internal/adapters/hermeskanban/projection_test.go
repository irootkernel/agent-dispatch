package hermeskanban

import (
	"testing"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
)

// TestMapExecutionTable is the documented version-tested mapping from
// the frozen public status enum onto the portable execution axis, plus
// the malformed-status unknown projection.
func TestMapExecutionTable(t *testing.T) {
	completed := int64(1787149999)
	cases := []struct {
		status string
		want   records.ExecutionState
	}{
		{"done", records.ExecSucceeded},
		{"running", records.ExecRunning},
		{"ready", records.ExecQueued},
		{"scheduled", records.ExecQueued},
		{"todo", records.ExecQueued},
		{"triage", records.ExecQueued},
		{"review", records.ExecQueued},
		{"blocked", records.ExecQueued},
		{"archived", records.ExecCanceled},
	}
	for _, c := range cases {
		task := TaskRecord{ID: "t_6253023d", Status: c.status, CreatedAt: 1787142146, CompletedAt: &completed}
		projection, err := MapExecution(task)
		if err != nil || projection.State != c.want {
			t.Fatalf("status %q mapped to %s (%v), want %s", c.status, projection.State, err, c.want)
		}
	}
	// done observes the completed timestamp, not creation.
	task := TaskRecord{ID: "t_6253023d", Status: "done", CreatedAt: 1787142146, CompletedAt: &completed}
	projection, _ := MapExecution(task)
	if projection.TargetObservedAt != "2026-08-19T14:33:19Z" {
		t.Fatalf("done must observe completed_at: %q", projection.TargetObservedAt)
	}

	// Malformed statuses become the unknown projection, never success.
	for _, malformed := range []string{"", "DONE", "exploded", "succeeded", "ready "} {
		task := TaskRecord{ID: "t_6253023d", Status: malformed, CreatedAt: 1787142146}
		projection, err := MapExecution(task)
		var unknown *UnknownStatusError
		if err == nil || projection.State != records.ExecUnavailable {
			t.Fatalf("malformed status %q must project unavailable with the typed error, got %+v err=%v", malformed, projection, err)
		}
		if !asUnknownStatus(err, &unknown) {
			t.Fatalf("malformed status must surface UnknownStatusError, got %T", err)
		}
	}
}

func asUnknownStatus(err error, target **UnknownStatusError) bool {
	e, ok := err.(*UnknownStatusError)
	if ok {
		*target = e
	}
	return ok
}
