package workreceipt

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

func testNow() time.Time { return time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC) }

// failingStore surfaces injected durable-store failures and counts audit
// appends: a storage error must surface as a typed store error and never
// as invalid-receipt evidence (E5 audit: no false rejection audit).
type failingStore struct {
	Store
	audits int
	fail   map[string]error
}

func (f *failingStore) LoadIntent(ctx context.Context, dispatchID string) (ports.IntentSnapshot, error) {
	if err := f.fail["LoadIntent"]; err != nil {
		return ports.IntentSnapshot{}, err
	}
	return ports.IntentSnapshot{}, ports.ErrIntentNotFound
}

func (f *failingStore) LoadWorkReceipt(ctx context.Context, dispatchID, runID string) (ports.WorkReceiptView, error) {
	if err := f.fail["LoadWorkReceipt"]; err != nil {
		return ports.WorkReceiptView{}, err
	}
	return ports.WorkReceiptView{}, ports.ErrWorkReceiptNotFound
}

func (f *failingStore) AuditWorkReceipt(ctx context.Context, transitionID, dispatchID, fromState, toState, recordedAt, contextJSON string) error {
	f.audits++
	return nil
}

// TestStoreFailuresAreNotRejectionEvidence proves a transient durable
// store failure classifies as storage, never as a receipt rejection:
// the invalid-receipt audit is reserved for evaluated submissions.
func TestStoreFailuresAreNotRejectionEvidence(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		method string
		call   func(s *Service) error
	}{
		{"begin lineage read", "LoadIntent", func(s *Service) error {
			_, err := s.Begin(ctx, BeginInput{DispatchID: "disp-1", RunID: "run-1"})
			return err
		}},
		{"begin replay check", "LoadWorkReceipt", func(s *Service) error {
			_, err := s.Begin(ctx, BeginInput{DispatchID: "disp-1", RunID: "run-1"})
			return err
		}},
		{"complete lineage read", "LoadIntent", func(s *Service) error {
			_, err := s.Complete(ctx, CompleteInput{DispatchID: "disp-1", RunID: "run-1", ManifestJSON: `[]`})
			return err
		}},
		{"fail lineage read", "LoadIntent", func(s *Service) error {
			_, err := s.Fail(ctx, FailInput{DispatchID: "disp-1", RunID: "run-1", FailureCode: "agent_error"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &failingStore{fail: map[string]error{tc.method: errors.New("disk I/O error")}}
			svc := &Service{Store: store, Now: testNow}
			err := tc.call(svc)
			var storeErr *ports.StoreError
			if !errors.As(err, &storeErr) {
				t.Fatalf("a store failure must classify as storage, got %v", err)
			}
			var invalid *InvalidError
			if errors.As(err, &invalid) {
				t.Fatalf("a store failure must not become rejection evidence: %v", err)
			}
			if store.audits != 0 {
				t.Fatalf("no invalid-receipt audit may be written for a store failure: %d", store.audits)
			}
		})
	}
}
