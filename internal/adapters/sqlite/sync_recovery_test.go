package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPreSignaturePublicationsUsesTimeAndClaimLiveness(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 0, 0, 1, 0, time.UTC)
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg", now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, created, owner, expiry string }{
		{"older", "2026-09-26T00:00:00.5Z", "", ""},
		{"younger", "2026-09-26T00:00:00.52Z", "", ""},
		{"active", "2026-09-25T00:00:00Z", "publisher", now.Add(time.Hour).Format(time.RFC3339Nano)},
		{"expired", "2026-09-26T00:00:00.53Z", "publisher", now.Add(-time.Hour).Format(time.RFC3339Nano)},
		{"corrupt", "2026-09-26T00:00:00.54Z", "publisher", "not-a-timestamp"},
	} {
		var owner, expiry any
		if row.owner != "" {
			owner, expiry = row.owner, row.expiry
		}
		if _, err := s.ExecContext(ctx, `INSERT INTO sync_jobs(job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,claim_owner,claim_expires_at,created_at,updated_at) VALUES (?,'wiki-pair','publication',?,?,'prepared','{}',?,?,?,?)`, row.id, row.id, "sha256:"+row.id, owner, expiry, row.created, row.created); err != nil {
			t.Fatal(err)
		}
	}
	count, oldest, err := s.PreSignaturePublications(ctx, "wiki-pair", now)
	if err != nil || count != 4 || oldest != "2026-09-26T00:00:00.5Z" {
		t.Fatalf("pre-signature obligations: count=%d oldest=%q err=%v", count, oldest, err)
	}
}

func TestSyncRecoverySchedulePersistsBackoffAndFencesCompletion(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const group = "wiki-pair"
	start := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	if _, err := s.EnsureSyncControl(ctx, group, "cfg", start.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if due, err := s.ReserveSyncRecovery(ctx, group, "first", start, true); err != nil || !due {
		t.Fatalf("startup reservation: due=%v err=%v", due, err)
	}
	if due, err := s.ReserveSyncRecovery(ctx, group, "overlap", start.Add(time.Second), true); err != nil || due {
		t.Fatalf("live reservation must exclude another worker: due=%v err=%v", due, err)
	}
	if err := s.CompleteSyncRecovery(ctx, group, "first", start, false, "reconcile_failed"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteSyncRecovery(ctx, group, "first", start, true, "none"); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("stale completion changed schedule: %v", err)
	}
	if due, err := s.ReserveSyncRecovery(ctx, group, "wake", start.Add(29*time.Second), true); err != nil || due {
		t.Fatalf("wake bypassed failed-attempt backoff: due=%v err=%v", due, err)
	}
	if due, err := s.ReserveSyncRecovery(ctx, group, "second", start.Add(30*time.Second), false); err != nil || !due {
		t.Fatalf("retry not due after backoff: due=%v err=%v", due, err)
	}
	if err := s.CompleteSyncRecovery(ctx, group, "second", start.Add(30*time.Second), false, "local_ref_unavailable"); err != nil {
		t.Fatal(err)
	}
	state, found, err := s.LoadSyncRecoverySchedule(ctx, group)
	if err != nil || !found || state.ConsecutiveFailures != 2 || state.LastReason != "local_ref_unavailable" || state.NextDueAt != start.Add(90*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("durable exponential backoff: %+v found=%v err=%v", state, found, err)
	}
	if due, err := s.ReserveSyncRecovery(ctx, group, "third", start.Add(90*time.Second), false); err != nil || !due {
		t.Fatalf("second retry: due=%v err=%v", due, err)
	}
	if err := s.CompleteSyncRecovery(ctx, group, "third", start.Add(90*time.Second), true, "none"); err != nil {
		t.Fatal(err)
	}
	state, found, err = s.LoadSyncRecoverySchedule(ctx, group)
	if err != nil || !found || state.ConsecutiveFailures != 0 || state.LastReason != "none" || state.LastSuccessAt == "" || state.NextDueAt != start.Add(390*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("successful periodic timer: %+v found=%v err=%v", state, found, err)
	}
	if due, err := s.ReserveSyncRecovery(ctx, group, "healthy-wake", start.Add(91*time.Second), true); err != nil || !due {
		t.Fatalf("healthy nudge did not advance timer: due=%v err=%v", due, err)
	}
}

func TestSyncRecoveryScheduleDeadOwnerExpires(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const group = "wiki-pair"
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	if _, err := s.EnsureSyncControl(ctx, group, "cfg", now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if due, err := s.ReserveSyncRecovery(ctx, group, "dead", now, true); err != nil || !due {
		t.Fatalf("first reservation: due=%v err=%v", due, err)
	}
	if due, err := s.ReserveSyncRecovery(ctx, group, "survivor", now.Add(30*time.Second), false); err != nil || !due {
		t.Fatalf("dead owner's reservation never expired: due=%v err=%v", due, err)
	}
	if err := s.CompleteSyncRecovery(ctx, group, "dead", now.Add(31*time.Second), true, "none"); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("dead owner completed over successor: %v", err)
	}
}

func TestSyncRecoveryScheduleFailureCaps(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const group = "wiki-pair"
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	if _, err := s.EnsureSyncControl(ctx, group, "cfg", now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 18; i++ {
		attempt := "failed-" + string(rune('a'+i))
		if due, err := s.ReserveSyncRecovery(ctx, group, attempt, now, false); err != nil || !due {
			t.Fatalf("reservation %d: due=%v err=%v", i, due, err)
		}
		if err := s.CompleteSyncRecovery(ctx, group, attempt, now, false, "reconcile_failed"); err != nil {
			t.Fatal(err)
		}
		state, found, err := s.LoadSyncRecoverySchedule(ctx, group)
		if err != nil || !found {
			t.Fatalf("load %d: found=%v err=%v", i, found, err)
		}
		if i >= 4 && state.NextDueAt != now.Add(5*time.Minute).Format(time.RFC3339Nano) {
			t.Fatalf("backoff exceeded cap at %d: %+v", i, state)
		}
		now = now.Add(5 * time.Minute)
	}
	state, _, err := s.LoadSyncRecoverySchedule(ctx, group)
	if err != nil || state.ConsecutiveFailures != 16 {
		t.Fatalf("failure count exceeded cap: %+v err=%v", state, err)
	}
}
