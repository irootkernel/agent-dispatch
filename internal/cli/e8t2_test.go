package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E8-T2 regression evidence (H-6, M-1): the trigger-path recovery of
// expired submitting leases and the submit-timeout-derived lease TTL.

// TestE8T2TriggerRecoversExpiredSubmitting proves the head-of-entry
// recovery on the Watchman-triggered dispatch path (H-6): a process that
// died mid-submit heals on the next trigger — no manual drain — and the
// healed unknown is reconciled while the arriving burst merges into the
// dirty generation.
func TestE8T2TriggerRecoversExpiredSubmitting(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	dispatchID := e7t2NoSubmitDispatch(t, configPath, vault)

	// The dead submitter: an attempt lease that committed long ago and
	// expired, leaving the intent wedged in submitting exactly as a
	// mid-submit process death does.
	store := e5t1Store(t, configPath)
	if _, err := store.AcquireAttempt(requestCtx(), ports.AcquireAttempt{
		DispatchID: dispatchID, AttemptID: "attempt-victim", Owner: "victim",
		Now: "2026-08-20T00:00:00Z", LeaseExpiresAt: "2026-08-20T00:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	// The next trigger carries a new burst: the head-of-entry sweep must
	// recover the expired lease before the arrival is evaluated.
	var out, errb bytes.Buffer
	setPlanEnv(t, vault, false)
	withStdin(t, `[{"name":"Notes/next-trigger.md","exists":true,"new":true,"size":4,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("trigger dispatch over a wedged submitter: %s", errb.String())
	}
	healed := e5t1Store(t, configPath)
	defer healed.Close()
	// The recovery itself is pinned on the transition history: the wedged
	// intent left submitting toward unknown with the lease-expired
	// evidence (any further reconciliation moves it on from there).
	var recoveryReason string
	if err := healed.QueryRow(`SELECT context_json FROM state_transitions WHERE entity_id = ? AND from_state = 'submitting' AND to_state = 'unknown' ORDER BY rowid DESC LIMIT 1`, dispatchID).Scan(&recoveryReason); err != nil {
		t.Fatalf("the trigger alone must recover the expired submitting lease: %v", err)
	}
	if !strings.Contains(recoveryReason, "lease_expired") {
		t.Fatalf("the recovery transition must carry the lease-expired evidence: %s", recoveryReason)
	}
}

// TestE8T2ScheduledReconcileRecoversExpiredSubmitting proves the second
// H-6 site: the scheduled `reconcile --submit` path heals a wedged
// submitter before any submission, without a manual drain.
func TestE8T2ScheduledReconcileRecoversExpiredSubmitting(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	dispatchID := e7t2NoSubmitDispatch(t, configPath, vault)
	store := e5t1Store(t, configPath)
	if _, err := store.AcquireAttempt(requestCtx(), ports.AcquireAttempt{
		DispatchID: dispatchID, AttemptID: "attempt-victim", Owner: "victim",
		Now: "2026-08-20T00:00:00Z", LeaseExpiresAt: "2026-08-20T00:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "scheduled", "--submit"}, &out, &errb); code != 0 {
		t.Fatalf("scheduled reconcile --submit: %s", errb.String())
	}
	healed := e5t1Store(t, configPath)
	defer healed.Close()
	var n int
	if err := healed.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_id = ? AND from_state = 'submitting' AND to_state = 'unknown'`, dispatchID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the scheduled path must recover the expired submitting lease: %d %v", n, err)
	}
}

// TestE8T2LeaseTTLOutlivesSubmitTimeout pins the M-1 derivation: the
// lease TTL is the configured submit timeout plus a fixed margin, so a
// live submitter inside the operator-approved window can never have its
// lease stolen by a recovery sweep; the default configuration keeps the
// shipped one-minute lease.
func TestE8T2LeaseTTLOutlivesSubmitTimeout(t *testing.T) {
	cases := []struct {
		configured string
		want       time.Duration
	}{
		{"", time.Minute},
		{"30s", time.Minute},
		{"10s", 40 * time.Second},
		{"5m", 5*time.Minute + 30*time.Second},
		{"garbage", time.Minute},
	}
	for _, tc := range cases {
		if got := leaseTTLFor(tc.configured); got != tc.want {
			t.Errorf("leaseTTLFor(%q) = %v, want %v", tc.configured, got, tc.want)
		}
	}
}
