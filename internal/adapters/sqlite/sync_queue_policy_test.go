package sqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestSyncQueuePolicyLeavesImportCapacity(t *testing.T) {
	for _, limit := range []int{2, 8} {
		t.Run(fmt.Sprintf("queue=%d", limit), func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()
			peerF003Control(t, s, "wiki-pair")
			for i := 0; i < limit-1; i++ {
				if _, err := s.AdmitPeerNudge(ctx, peerF003Nudge(fmt.Sprintf("hint-%d", i), limit)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.AdmitPeerNudge(ctx, peerF003Nudge("extra-hint", limit)); !errors.Is(err, ErrSyncQueueFull) {
				t.Fatalf("nudge consumed the reserved work slot: %v", err)
			}
			job := peerF003Job("import", "import", "validated", limit)
			if _, _, err := s.AdmitSyncJob(ctx, job); err != nil {
				t.Fatalf("full nudge quota must still admit import: %v", err)
			}
			peerF003Counts(t, s, 1, limit-1)
			if _, _, err := s.AdmitSyncJob(ctx, peerF003Job("extra-job", "verification", "planned", limit)); !errors.Is(err, ErrSyncQueueFull) {
				t.Fatalf("combined bound must still apply: %v", err)
			}
			if duplicate, err := s.AdmitPeerNudge(ctx, peerF003Nudge("hint-0", limit)); err != nil || !duplicate {
				t.Fatalf("replay at combined bound: duplicate=%v err=%v", duplicate, err)
			}
			peerF003Counts(t, s, 1, limit-1)
		})
	}
}

func TestSyncQueuePolicyRefusesOneSlot(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.AdmitPeerNudge(context.Background(), peerF003Nudge("one-slot", 1)); err == nil {
		t.Fatal("one-slot peer queue must be rejected before admission")
	}
	peerF003Counts(t, s, 0, 0)
}
