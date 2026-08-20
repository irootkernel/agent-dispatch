// Package reconcile implements the unknown-resolution workflow (E3-T3,
// DUR-006): an unknown dispatch must attempt lookup by idempotency key or
// external reference before any other submission, and an ambiguous result
// never falls over to another target (DUR-008).
package reconcile

import (
	"context"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// Store is the durable surface reconciliation needs: the unknown
// workflow plus intent loading.
type Store interface {
	ports.ReconcileStore
	ports.DispatchStore
}

// Service orchestrates one unknown dispatch's reconciliation.
type Service struct {
	Store Store
	Sink  ports.Sink
	// Now renders the canonical audit timestamp.
	Now func() string
}

// Result reports what one reconciliation proved.
type Result struct {
	DispatchID string
	From       records.IntentState
	To         records.IntentState
	Lookup     ports.LookupStatus
}

// Reconcile resolves one unknown dispatch through the target's lookup
// interfaces: by idempotency key first, then by external reference when
// one is known. A durable proof of acceptance or non-acceptance resolves
// the dispatch; an ambiguous or absent-with-exhausted-budget outcome
// dead-letters it for operator action (DUR-009).
func (s *Service) Reconcile(ctx context.Context, dispatchID, actor string, attemptsExhausted bool) (Result, error) {
	var out Result
	out.DispatchID = dispatchID
	out.From = records.IntentUnknown

	// DUR-006: lookup by idempotency key before any other submission.
	intent, err := s.intent(ctx, dispatchID)
	if err != nil {
		return out, err
	}
	lookup, err := s.Sink.LookupByIdempotencyKey(ctx, intent.IdempotencyKey)
	if err != nil {
		// An unavailable or malformed lookup proves nothing; the dispatch
		// is dead-lettered as unresolved, never retried blindly.
		lookup = ports.LookupResult{Status: ports.LookupAmbiguous}
	}
	if lookup.Status == ports.LookupAmbiguous && intent.ExternalRef != "" {
		byRef, err := s.Sink.LookupByExternalRef(ctx, intent.ExternalRef)
		if err == nil {
			lookup = byRef
		}
	}
	out.Lookup = lookup.Status
	to, err := s.Store.ReconcileUnknown(ctx, dispatchID, actor, lookup, attemptsExhausted, s.Now())
	if err != nil {
		return out, err
	}
	out.To = to
	return out, nil
}

// intentSnapshot is the minimal durable fact set the lookup order needs.
type intentSnapshot struct {
	IdempotencyKey string
	ExternalRef    string
}

func (s *Service) intent(ctx context.Context, dispatchID string) (intentSnapshot, error) {
	var snap intentSnapshot
	loaded, err := s.Store.LoadIntent(ctx, dispatchID)
	if err != nil {
		return snap, err
	}
	snap.IdempotencyKey = loaded.IdempotencyKey
	snap.ExternalRef = loaded.ExternalRef
	return snap, nil
}
