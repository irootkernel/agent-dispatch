package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Reconcile fan-out sibling commits (E12 epic whole-review round 1,
// FAN-002): the per-lane commit loop AND its error policy live here in the
// app layer beside the Coordinator's arrival posture — the CLI keeps only
// wiring and envelope rendering. The occurrence's FIRST lane keeps
// committing through the reconciliation service's own single-intent
// transaction (CommitReconcileIntent / ResolveUncertainReconciliation,
// unchanged); this function owns exactly the sibling loop.

// ReconcileSiblingStore is the narrow durable surface the reconcile
// sibling commit needs (declared here so the policy is testable against a
// fake without standing up the whole coordination store).
type ReconcileSiblingStore interface {
	// CommitFanoutChild persists one additional child intent of an
	// occurrence whose shared lineage a sibling already committed.
	CommitFanoutChild(ctx context.Context, intent ports.IntentInput) error
}

// ReconcileLaneCommit is one sibling lane's reconcile fan-out commit
// outcome beneath the occurrence's shared aggregate.
type ReconcileLaneCommit struct {
	// DestinationID is the sibling's destination lane.
	DestinationID string
	// DispatchID is the sibling child's dispatch ID.
	DispatchID string
	// Committed reports the durable child commit.
	Committed bool
	// Note carries the slot-held skip (warning class): the lane keeps its
	// own coordination and the reconciliation child was not forced beside
	// it — the occurrence stays durable and the exit code stays zero.
	Note string
	// Err carries the non-slot-held failure (failure class): the
	// occurrence is NOT fully durable and the caller must surface it and
	// exit non-zero. Never silently dropped.
	Err error
}

// ReconcileSiblingJournal is the app-layer carrier of one reconcile
// occurrence's sibling outcomes (E12 epic whole-review round 3): the
// durable-first commit happens INSIDE the reconciliation service's intent
// builder, so the outcomes must survive from there to every CLI outcome
// shape — success, skip, and the Run-failure path. The journal owns that
// data at the app seam: the CLI holds a reference and renders, never a
// CLI-side stash of app-owned results.
type ReconcileSiblingJournal struct {
	commits []ReconcileLaneCommit
}

// CommitAll durably commits the siblings through the shared policy and
// records the outcomes on the journal.
func (j *ReconcileSiblingJournal) CommitAll(ctx context.Context, store ReconcileSiblingStore, siblings []ports.IntentInput) {
	j.commits = CommitReconcileSiblings(ctx, store, siblings)
}

// Outcomes returns the recorded lane commits (nil-safe: a zero journal
// has none).
func (j *ReconcileSiblingJournal) Outcomes() []ReconcileLaneCommit {
	if j == nil {
		return nil
	}
	return j.commits
}

// Failure returns the first non-slot-held sibling failure — the exit-code
// policy's pick — or nil when every sibling committed or skipped.
func (j *ReconcileSiblingJournal) Failure() *ReconcileLaneCommit {
	if j == nil {
		return nil
	}
	for i := range j.commits {
		if j.commits[i].Err != nil {
			return &j.commits[i]
		}
	}
	return nil
}

// CommitReconcileSiblings durably commits the reconcile fan-out's sibling
// children beneath the shared aggregate, one transaction per lane through
// the existing CommitFanoutChild surface (FAN-002/FAN-003).
//
// DURABLE-FIRST (E12 epic whole-review round 1): call this BEFORE the
// occurrence's first lane commits through the reconciliation transaction
// (the intent builder invokes it before returning the first intent). Every
// sibling is durable before the first lane activates, so the crash window
// between the two leaves READY sibling intents the existing
// `dispatches drain` submits — never a committed first lane whose remaining
// selection left no durable marker.
//
// A sibling's failure never blocks the others (CON-007): a lane that
// already holds its own active work skips with a note (its lane keeps its
// coordination), a lane whose IDENTICAL child already exists skips the
// same way (the duplicate idempotency key on a retry is the durable proof
// this lane's reconciliation already committed — the DAT-014 key is
// content-derived, so same key means same work, E12 epic whole-review
// round 2), any other failure is recorded in its entry for the caller to
// surface on the envelope and the exit code — the loop always runs to
// completion.
//
// Retry truthfulness (E12 epic whole-review round 2): a RETRY after a
// partial failure is a FRESH occurrence — the aggregate, decision, and
// dispatch identities all derive anew per run — so a retry is NOT a
// same-aggregate no-op. What makes the retry safe is the pair of
// constraints above: a lane whose earlier reconcile child already
// committed either still holds its lane slot (ErrRouteSlotHeld skip) or
// collides on the content-derived idempotency key
// (ErrIdempotencyConflict skip) — never a duplicate child — and only
// lanes still owed work get new children. There is no cross-run
// re-commit of "missing" siblings — the earlier committed child IS that
// lane's reconciliation.
func CommitReconcileSiblings(ctx context.Context, store ReconcileSiblingStore, siblings []ports.IntentInput) []ReconcileLaneCommit {
	out := make([]ReconcileLaneCommit, 0, len(siblings))
	for _, sibling := range siblings {
		commit := ReconcileLaneCommit{DestinationID: "", DispatchID: sibling.DispatchID}
		if sibling.Fanout == nil {
			commit.Err = fmt.Errorf("sibling %s carries no fanout block; its lane is unknowable", sibling.DispatchID)
			out = append(out, commit)
			continue
		}
		commit.DestinationID = sibling.Fanout.DestinationID
		switch err := store.CommitFanoutChild(ctx, sibling); {
		case err == nil:
			commit.Committed = true
		case errors.Is(err, ports.ErrRouteSlotHeld):
			commit.Note = "lane holds its own active work; the reconciliation child was not forced beside it"
		case errors.Is(err, ports.ErrIdempotencyConflict):
			commit.Note = "an identical child for this content already exists on the lane (duplicate idempotency key); nothing new was forced"
		default:
			commit.Err = err
		}
		out = append(out, commit)
	}
	return out
}
