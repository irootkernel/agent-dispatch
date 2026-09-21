package resourceguard

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireWaitSerializesParticipatingWriter(t *testing.T) {
	stateDir, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := Acquire(stateDir, "vault-main")
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan *Guard, 1)
	errs := make(chan error, 1)
	go func() {
		guard, err := AcquireWait(stateDir, "vault-main")
		if err != nil {
			errs <- err
			return
		}
		acquired <- guard
	}()
	select {
	case guard := <-acquired:
		_ = guard.Close()
		t.Fatal("waiting writer acquired a live resource guard")
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case guard := <-acquired:
		if err := guard.Close(); err != nil {
			t.Fatal(err)
		}
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("waiting writer did not acquire the released resource guard")
	}
}
