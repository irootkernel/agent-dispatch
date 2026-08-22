package cli

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/app/workreceipt"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// TestQuarantineErrClassification pins every arm of the quarantine error
// boundary: registered domain codes keep their exits, typed store
// failures are storage, and anything else is an internal defect — never
// a blanket storage relabel.
func TestQuarantineErrClassification(t *testing.T) {
	cases := []struct {
		name string
		sub  string
		err  error
		code string
		want int
	}{
		{"not found", "show", ports.ErrQuarantineNotFound, "quarantine_not_found", 4},
		{"re-release denied", "release", fmt.Errorf("%w: q-1 is released", ports.ErrQuarantineNotHeld), "quarantine_release_denied", 14},
		{"re-discard conflict", "discard", fmt.Errorf("%w: q-1 is released", ports.ErrQuarantineNotHeld), "transition_invalid", 14},
		{"store failure", "show", ports.WrapStore(errors.New("disk I/O error")), "sqlite_query_failed", 20},
		{"defect", "show", errors.New("unexpected service defect"), "internal_unclassified", 40},
	}
	for _, tc := range cases {
		var errb bytes.Buffer
		code := quarantineErr(&errb, "quarantine "+tc.sub, tc.sub, tc.err)
		if code != tc.want || !bytes.Contains(errb.Bytes(), []byte(tc.code)) {
			t.Fatalf("%s: want exit %d with %s, got %d: %s", tc.name, tc.want, tc.code, code, errb.String())
		}
	}
}

// TestWrapQuarantineReadError pins the read path's typed wrap: the
// shared domain-outcome classification passes through unwrapped, every
// other failure becomes the typed store error.
func TestWrapQuarantineReadError(t *testing.T) {
	domain := fmt.Errorf("%w: q-1 missing", ports.ErrQuarantineNotFound)
	if err := wrapQuarantineReadError(domain); !errors.Is(err, ports.ErrQuarantineNotFound) {
		t.Fatalf("a domain outcome must pass through, got %v", err)
	}
	var storeErr *ports.StoreError
	if errors.As(wrapQuarantineReadError(domain), &storeErr) {
		t.Fatalf("a domain outcome must not be wrapped as storage: %v", domain)
	}
	wrapped := wrapQuarantineReadError(errors.New("disk I/O error"))
	if !errors.As(wrapped, &storeErr) {
		t.Fatalf("a read failure must wrap as the typed store error, got %v", wrapped)
	}
}

// TestReconcileErrClassification pins the full-reconciliation boundary:
// every lost-race arm is a conflict (14), typed store failures are
// storage (20), anything else is internal (40).
func TestReconcileErrClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
		want int
	}{
		{"eligibility refusal", fmt.Errorf("%w: route wiki is IDLE, not uncertain", ports.ErrStateNotEligible), "transition_invalid", 14},
		{"generation fence", fmt.Errorf("%w: route wiki changed during reconciliation", ports.ErrGenerationConflict), "transition_invalid", 14},
		{"idempotency conflict", fmt.Errorf("%w: duplicate key", ports.ErrIdempotencyConflict), "transition_invalid", 14},
		{"slot held", fmt.Errorf("%w: reservation in the way", ports.ErrRouteSlotHeld), "transition_invalid", 14},
		{"wrapped conflict", ports.WrapStore(fmt.Errorf("%w: route wiki changed during reconciliation", ports.ErrGenerationConflict)), "transition_invalid", 14},
		{"store failure", ports.WrapStore(errors.New("disk I/O error")), "sqlite_query_failed", 20},
		{"defect", errors.New("enumeration exploded"), "internal_unclassified", 40},
	}
	for _, tc := range cases {
		var errb bytes.Buffer
		code := reconcileErr(&errb, "reconcile", tc.err)
		if code != tc.want || !bytes.Contains(errb.Bytes(), []byte(tc.code)) {
			t.Fatalf("%s: want exit %d with %s, got %d: %s", tc.name, tc.want, tc.code, code, errb.String())
		}
	}
}

// TestWorkReceiptErrClassification pins the receipt boundary's storage
// and defect arms (the rejection and conflict arms are covered by the
// e5t1 command suites).
func TestWorkReceiptErrClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
		want int
	}{
		{"store failure", ports.WrapStore(errors.New("disk I/O error")), "sqlite_query_failed", 20},
		{"defect", errors.New("unexpected service defect"), "internal_unclassified", 40},
		{"invalid receipt", &workreceipt.InvalidError{Reasons: []string{"note body"}}, "work_receipt_invalid", 4},
		{"state conflict", fmt.Errorf("%w: route needs a follow-up", ports.ErrStateNotEligible), "transition_invalid", 14},
		{"generation conflict", fmt.Errorf("%w: generation moved", ports.ErrGenerationConflict), "transition_invalid", 14},
	}
	for _, tc := range cases {
		var errb bytes.Buffer
		code := workReceiptErr(&errb, "work complete", tc.err)
		if code != tc.want || !bytes.Contains(errb.Bytes(), []byte(tc.code)) {
			t.Fatalf("%s: want exit %d with %s, got %d: %s", tc.name, tc.want, tc.code, code, errb.String())
		}
	}
}
