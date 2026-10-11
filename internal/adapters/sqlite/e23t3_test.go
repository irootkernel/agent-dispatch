package sqlite

import (
	"context"
	"errors"
	"fmt"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestE23T3MigrationLockDeadline(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lock := s.Path() + ".migration-lock"
	if err := os.WriteFile(lock, []byte("999999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = s.MigrateContext(ctx, filepath.Join(dir, "backups"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline not honored: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("migration lock outlived invocation deadline")
	}
	data, err := os.ReadFile(lock)
	if err != nil || string(data) != "999999\n" {
		t.Fatal("live holder lock was changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
		t.Fatal("backup started while lock was unavailable")
	}
}

func TestE23T3ReadOnlyState(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "not-created", "state.db")
	if _, err := OpenReadOnly(context.Background(), missing); !os.IsNotExist(err) {
		t.Fatalf("missing state should refuse: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatal("read-only open created parent")
	}
	path := filepath.Join(dir, "question?hash#state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(filepath.Join(dir, "backups")); err != nil {
		t.Fatal(err)
	}
	// Keep the writer open so lookup must observe committed WAL contents.
	if _, err := s.Exec(`INSERT INTO schema_migrations(version,name,checksum) VALUES(29,'future','unsupported')`); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	defer s.Close()
	var tooNew *ErrSchemaTooNewType
	if err := ro.CheckManagementSchema(context.Background()); !errors.As(err, &tooNew) {
		t.Fatalf("WAL future row not observed/refused: %v", err)
	}
	if _, err := ro.Exec(`DELETE FROM schema_migrations WHERE version=29`); err == nil {
		t.Fatal("read-only handle allowed a write")
	}
}

func TestE23T3WriteLockUsesRemainingDeadline(t *testing.T) {
	for _, boundedOpen := range []bool{false, true} {
		t.Run(fmt.Sprintf("bounded-open-%t", boundedOpen), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.db")
			holder, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close()
			if err := holder.Migrate(filepath.Join(dir, "backups")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			var requester *Store
			if boundedOpen {
				requester, err = OpenContext(ctx, path)
			} else {
				requester, err = Open(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer requester.Close()
			tx, err := holder.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`UPDATE sync_management_requests SET state=state`); err != nil {
				t.Fatal(err)
			}
			deadline, _ := ctx.Deadline()
			time.Sleep(max(0, time.Until(deadline)-50*time.Millisecond))
			start := time.Now()
			_, _, err = requester.PrepareManagementRequest(ctx, "mgr_"+strings.Repeat("9", 32), syncmanagement.Binding{GroupID: "wiki", InstanceID: "local", StateIncarnationID: "local-state-001", Action: "sync-now", PolicyRevision: "sha256:" + strings.Repeat("a", 64), ConfigRevision: "sha256:" + strings.Repeat("b", 64)}, time.Now())
			if err == nil {
				t.Fatal("held writer admitted a request")
			}
			if time.Since(start) > 200*time.Millisecond {
				t.Fatalf("write waited beyond remaining invocation deadline: %s: %v", time.Since(start), err)
			}

			var busyMS int
			if err := requester.QueryRow(`PRAGMA busy_timeout`).Scan(&busyMS); err != nil {
				t.Fatal(err)
			}
			want := 5000
			if boundedOpen {
				want = 0
			}
			if busyMS != want {
				t.Fatalf("connection setting leaked: got %d, want %d", busyMS, want)
			}
		})
	}
}

func TestE23T3AdmissionExpiresWhileWaitingForWriter(t *testing.T) {
	for _, action := range []string{"sync-now", "pause"} {
		t.Run(action, func(t *testing.T) {
			holder := openTestStore(t)
			requester, err := Open(holder.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer requester.Close()
			ctx := context.Background()
			if syncmanagement.IsControl(action) {
				if _, err := holder.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", managementTime(time.Now())); err != nil {
					t.Fatal(err)
				}
			}
			created := time.Now().Add(-599 * time.Second)
			r, _, err := holder.PrepareManagementRequest(ctx, managementHandle(1), managementBinding(action), created)
			if err != nil {
				t.Fatal(err)
			}
			before := managementLedgerSnapshot(t, holder)
			tx, err := holder.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`UPDATE sync_management_requests SET state=state`); err != nil {
				t.Fatal(err)
			}
			admissionTime := time.Now()
			if !admissionTime.Before(mustManagementTime(r.ExpiresAt)) {
				t.Fatal("fixture expired before admission began")
			}
			released := make(chan error, 1)
			timer := time.AfterFunc(time.Until(mustManagementTime(r.ExpiresAt))+20*time.Millisecond, func() {
				released <- tx.Rollback()
			})
			defer timer.Stop()
			_, duplicate, admissionErr := requester.AcceptManagementRequest(ctx, ManagementAdmission{
				GroupID: r.GroupID, RequestID: r.RequestID, CurrentBinding: r.Binding,
				Authorized: true, QueueLimit: 2, ControlSyncRevision: "cfg-1",
			})
			if err := <-released; err != nil {
				t.Fatal(err)
			}
			if !errors.Is(admissionErr, ErrManagementExpired) || duplicate {
				t.Fatalf("expired while waiting: duplicate=%v error=%v", duplicate, admissionErr)
			}
			if after := managementLedgerSnapshot(t, holder); after != before {
				t.Fatal("expired admission changed request, acceptance, control or job state")
			}
		})
	}
}

func TestE23T3AdmissionClockAfterWriterReservation(t *testing.T) {
	for _, action := range []string{"sync-now", "pause"} {
		t.Run(action, func(t *testing.T) {
			holder := openTestStore(t)
			requester, err := Open(holder.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer requester.Close()
			ctx := context.Background()
			if syncmanagement.IsControl(action) {
				if _, err := holder.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", managementTime(managementT0)); err != nil {
					t.Fatal(err)
				}
			}
			r := prepareManagement(t, holder, 1, managementBinding(action))
			tx, err := holder.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`UPDATE sync_management_requests SET state=state`); err != nil {
				t.Fatal(err)
			}
			unlocked := make(chan struct{})
			released := make(chan error, 1)
			timer := time.AfterFunc(50*time.Millisecond, func() {
				close(unlocked)
				released <- tx.Rollback()
			})
			defer timer.Stop()
			calls := 0
			admittedAt := managementT0.Add(time.Second)
			accepted, duplicate, admissionErr := requester.AcceptManagementRequest(ctx, ManagementAdmission{
				GroupID: r.GroupID, RequestID: r.RequestID, CurrentBinding: r.Binding,
				Authorized: true, QueueLimit: 2, ControlSyncRevision: "cfg-1",
				Clock: func() time.Time {
					calls++
					select {
					case <-unlocked:
						return admittedAt
					default:
						return managementT0
					}
				},
			})
			if err := <-released; err != nil {
				t.Fatal(err)
			}
			if admissionErr != nil || duplicate || calls != 1 || accepted.AcceptedAt == nil || *accepted.AcceptedAt != managementTime(admittedAt) {
				t.Fatalf("fresh admission clock: result=%+v duplicate=%v calls=%d error=%v", accepted, duplicate, calls, admissionErr)
			}
		})
	}
}
