package quarantine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rootkernel/jjukkumi/internal/ports"
)

// stubStore surfaces injected outcomes for the resolution surface.
type stubStore struct {
	ports.QuarantineStore
	releaseErr error
	discardErr error
}

func (s *stubStore) ReleaseQuarantine(ctx context.Context, quarantineID, actor, reason, now string) (ports.QuarantineRecord, error) {
	return ports.QuarantineRecord{}, s.releaseErr
}

func (s *stubStore) DiscardQuarantine(ctx context.Context, quarantineID, actor, reason, now string) (ports.QuarantineRecord, error) {
	return ports.QuarantineRecord{}, s.discardErr
}

func testNowString() string { return "2026-08-21T00:00:00Z" }

// TestResolutionErrorClassification proves the two arms of the error
// boundary: a durable-store failure wraps as the typed store error
// (storage, exit 20) while the typed domain outcomes pass through
// unwrapped for their registered codes.
func TestResolutionErrorClassification(t *testing.T) {
	ctx := context.Background()
	svc := &Service{Store: &stubStore{releaseErr: errors.New("disk I/O error"), discardErr: errors.New("disk I/O error")}, Now: testNowString}
	for name, call := range map[string]func() error{
		"release": func() error {
			_, err := svc.Release(ctx, "q-1", "operator", "reviewed")
			return err
		},
		"discard": func() error {
			_, err := svc.Discard(ctx, "q-1", "operator", "reviewed")
			return err
		},
	} {
		err := call()
		var storeErr *ports.StoreError
		if !errors.As(err, &storeErr) {
			t.Fatalf("%s: a store failure must wrap as the typed store error, got %v", name, err)
		}
		if !ports.IsQuarantineDomainOutcome(err) {
			// expected: the wrapped failure is not a domain outcome
		} else {
			t.Fatalf("%s: a store failure must not classify as a domain outcome", name)
		}
	}

	domain := &Service{Store: &stubStore{
		releaseErr: fmt.Errorf("%w: q-1 was resolved concurrently", ports.ErrQuarantineNotHeld),
		discardErr: fmt.Errorf("%w: q-1 does not exist", ports.ErrQuarantineNotFound),
	}, Now: testNowString}
	if _, err := domain.Release(ctx, "q-1", "operator", "reviewed"); !errors.Is(err, ports.ErrQuarantineNotHeld) {
		t.Fatalf("the not-held conflict must pass through, got %v", err)
	}
	var wrapped *ports.StoreError
	if _, err := domain.Release(ctx, "q-1", "operator", "reviewed"); errors.As(err, &wrapped) {
		t.Fatalf("a domain outcome must not be wrapped as storage: %v", err)
	}
	if _, err := domain.Discard(ctx, "q-1", "operator", "reviewed"); !errors.Is(err, ports.ErrQuarantineNotFound) {
		t.Fatalf("the not-found outcome must pass through, got %v", err)
	}
	if _, err := domain.Release(ctx, "q-1", "operator", "  "); !errors.Is(err, ports.ErrReasonRequired) {
		t.Fatalf("a missing reason is its own usage outcome, got %v", err)
	}
}
