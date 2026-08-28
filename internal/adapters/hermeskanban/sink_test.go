package hermeskanban

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// sinkFixture builds a gated sink over the stateful stub with the
// eligibility floor and a rendered golden request.
func sinkFixture(t *testing.T, bin string) *Sink {
	t.Helper()
	sink, err := NewSink("hermes-main", bin, "", "agent-dispatch-test", ProcessLimits{SubmitTimeout: 30 * time.Second, LookupTimeout: 30 * time.Second}, 262144)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	if _, err := sink.Probe(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
	return sink
}

func goldenRequestMut(t *testing.T, mut func(*ports.TaskRequest)) ports.TaskRequest {
	t.Helper()
	req := loadGoldenRequest(t)
	if mut != nil {
		mut(&req)
	}
	return req
}

// TestSinkSubmitAcceptedDurable proves the primary contract: a normal
// submission is durably accepted with the external reference, target
// observation time, and bounded structured evidence (HER-001, DUR
// acceptance).
func TestSinkSubmitAcceptedDurable(t *testing.T) {
	sink := sinkFixture(t, stubhermes.Write(t))
	res, err := sink.Submit(context.Background(), goldenRequestMut(t, nil))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res.Classification != ports.SubmitAccepted || res.Durable != ports.DurableTrue {
		t.Fatalf("classification %q durable %q", res.Classification, res.Durable)
	}
	if !taskRef.MatchString(res.ExternalRef) {
		t.Fatalf("external ref %q not the public form", res.ExternalRef)
	}
	if res.TargetObservedAt == "" {
		t.Fatal("target observed at missing")
	}
	var payload map[string]any
	if err := json.Unmarshal(res.StructuredPayload, &payload); err != nil {
		t.Fatalf("structured payload not JSON: %v", err)
	}
	if payload["id"] != res.ExternalRef || payload["status"] != "ready" {
		t.Fatalf("payload disagrees with the result: %v", payload)
	}
}

// TestSinkDuplicateKeyResolvesToOriginal proves AC-302 at the sink
// level: re-submitting the same idempotency key returns the original
// task — same external reference, no second task created.
func TestSinkDuplicateKeyResolvesToOriginal(t *testing.T) {
	bin := stubhermes.Write(t)
	sink := sinkFixture(t, bin)
	req := goldenRequestMut(t, nil)
	first, err := sink.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sink.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Classification != ports.SubmitAccepted || second.ExternalRef != first.ExternalRef {
		t.Fatalf("duplicate key must resolve to the original: first %q second %q (%s)",
			first.ExternalRef, second.ExternalRef, second.Classification)
	}
}

// TestSinkClassificationTable drives the frozen failure modes through
// the sink classification (TST-006 subset over the real transport
// mapping).
func TestSinkClassificationTable(t *testing.T) {
	// versionOK prefixes the frozen --version answer so the sink's
	// construction gate passes and the create path reaches the scripted
	// failure.
	versionOK := `if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.19.1 (2026.7.30)\n'; exit 0; fi
`
	cases := []struct {
		name        string
		script      string
		want        ports.SubmitClassification
		wantDurable ports.DurableStatus
	}{
		{"timeout is unknown", versionOK + `sleep 30`, ports.SubmitUnknown, ports.DurableUnknown},
		{"malformed output is unknown", versionOK + `echo '{"id": "t_12345678"'`, ports.SubmitUnknown, ports.DurableUnknown},
		{"argument rejection is definite not submitted", versionOK + `echo 'hermes: error: unrecognized arguments: --json' >&2; exit 2`, ports.SubmitDefiniteNotSubmitted, ports.DurableFalse},
		{"unknown board is definite not submitted", versionOK + `echo "kanban: board 'nope' does not exist. Create it." >&2; exit 1`, ports.SubmitDefiniteNotSubmitted, ports.DurableFalse},
		{"generic failure is unknown", versionOK + `echo 'internal explosion' >&2; exit 7`, ports.SubmitUnknown, ports.DurableUnknown},
		{"excessive output is unknown", versionOK + `yes 'x' | head -c 100000`, ports.SubmitUnknown, ports.DurableUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := newStubHermes(t, c.script)
			sink := sinkFixture(t, bin)
			res, err := sink.Submit(context.Background(), goldenRequestMut(t, nil))
			if err != nil {
				t.Fatalf("sink error: %v", err)
			}
			if res.Classification != c.want || res.Durable != c.wantDurable {
				t.Fatalf("classification %q/%q want %q/%q", res.Classification, res.Durable, c.want, c.wantDurable)
			}
			if res.Diagnostic == "" {
				t.Fatal("every failure must carry a diagnostic")
			}
		})
	}
}

// TestSinkOversizedManifestRejectedAtSubmit proves the rendering policy
// rejection surfaces as a definite_not_submitted with nothing sent.
func TestSinkOversizedManifestRejectedAtSubmit(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "invoked")
	bin := newStubHermes(t, `if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.19.1 (2026.7.30)\n'; exit 0; fi
printf x >> "`+counter+`"; exit 0`)
	sink, err := NewSink("t", bin, "", "b", ProcessLimits{}, 64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := sink.Submit(context.Background(), goldenRequestMut(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Classification != ports.SubmitDefiniteNotSubmitted {
		t.Fatalf("oversized manifest must be definite_not_submitted, got %q", res.Classification)
	}
	if raw, rerr := os.ReadFile(counter); rerr == nil && len(raw) > 0 {
		t.Fatal("policy rejection must happen before any child invocation")
	}
}

// TestSinkLookupByExternalRef proves the read-only reference lookup:
// found with acceptance evidence, absent via the frozen no-such-task
// behavior (DUR-006), and transport failures stay errors the
// reconciliation treats as ambiguous.
func TestSinkLookupByExternalRef(t *testing.T) {
	bin := stubhermes.Write(t)
	sink := sinkFixture(t, bin)
	res, err := sink.Submit(context.Background(), goldenRequestMut(t, nil))
	if err != nil {
		t.Fatal(err)
	}

	found, err := sink.LookupByExternalRef(context.Background(), res.ExternalRef)
	if err != nil {
		t.Fatalf("lookup found: %v", err)
	}
	if found.Status != ports.LookupFound || found.Acceptance != "accepted" || found.ExternalRef != res.ExternalRef || !found.FoundDurable {
		t.Fatalf("found lookup wrong: %+v", found)
	}

	absent, err := sink.LookupByExternalRef(context.Background(), "t_deadbeef")
	if err != nil {
		t.Fatalf("lookup absent: %v", err)
	}
	if absent.Status != ports.LookupAbsent {
		t.Fatalf("absent lookup wrong: %+v", absent)
	}
}

// TestSinkLookupByKeyUnsupported proves the honest capability boundary:
// no read-only key query exists on the public CLI and the sink never
// emulates one (sink-adapter-contract §2, E0-T4 §5).
func TestSinkLookupByKeyUnsupported(t *testing.T) {
	sink := sinkFixture(t, stubhermes.Write(t))
	_, err := sink.LookupByIdempotencyKey(context.Background(), "agent-dispatch:v1:sha256:xyz")
	if err == nil || !errors.Is(err, ports.ErrCapabilityUnsupported) {
		t.Fatalf("by-key lookup must be capability_unsupported, got %v", err)
	}
}

// TestSinkGetExecution proves the execution projection through the
// read-only show: the queued projection for a fresh task (the
// done/succeeded and full mapping table cases are covered by
// TestMapExecutionTable) and the honest unsupported boundary when the
// capability is absent.
func TestSinkGetExecution(t *testing.T) {
	sink := sinkFixture(t, stubhermes.Write(t))
	accepted, err := sink.Submit(context.Background(), goldenRequestMut(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	projection, err := sink.GetExecution(context.Background(), accepted.ExternalRef)
	if err != nil {
		t.Fatalf("queued projection: %v", err)
	}
	if projection.State != "queued" || projection.ExternalRef != accepted.ExternalRef {
		t.Fatalf("queued projection wrong: %+v", projection)
	}

	// A below-floor target fails the eligibility gate before the lookup;
	// the capability-absent boundary returns with the E11-T2 probe's
	// per-executable shape evidence.
	belowFloor := newStubHermes(t, `if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.18.0 (2026.5.1)
'; exit 0; fi; exit 3`)
	plain, err := NewSink("t", belowFloor, "", "b", ProcessLimits{}, 262144)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plain.GetExecution(context.Background(), "t_00000001")
	if err == nil {
		t.Fatal("a below-floor target must fail the eligibility gate before any lookup")
	}
}

// TestSinkConstructionValidation proves board and bound are required and
// the report is validated against the requirements at construction,
// before any process runs.
func TestSinkConstructionValidation(t *testing.T) {
	bin := stubhermes.Write(t)
	if _, err := NewSink("t", bin, "", "", ProcessLimits{}, 1024); err == nil {
		t.Fatal("missing board must fail construction")
	}
	if _, err := NewSink("t", bin, "", "b", ProcessLimits{}, 0); err == nil {
		t.Fatal("missing manifest bound must fail construction")
	}
	if _, err := NewSink("t", bin, "not-a-version", "b", ProcessLimits{}, 1024); err == nil {
		t.Fatal("an unparseable eligibility floor must fail construction")
	}
}

// TestSinkTargetObservedAtRendering proves the created_at epoch maps to
// the canonical RFC 3339 observation timestamp.
func TestSinkTargetObservedAtRendering(t *testing.T) {
	sink := sinkFixture(t, stubhermes.Write(t))
	res, err := sink.Submit(context.Background(), goldenRequestMut(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Unix(1787142146, 0).UTC().Format(time.RFC3339)
	if res.TargetObservedAt != want {
		t.Fatalf("observed at %q want %q", res.TargetObservedAt, want)
	}
}

// TestSinkLookupTransportFailureIsError proves a transport failure in
// the reference lookup surfaces as the typed error (which the
// reconciliation service conservatively treats as ambiguous) rather
// than a found or absent proof.
func TestSinkLookupTransportFailureIsError(t *testing.T) {
	versionOK := `if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.19.1 (2026.7.30)\n'; exit 0; fi
`
	bin := newStubHermes(t, versionOK+`sleep 30`)
	sink := sinkFixture(t, bin)
	_, err := sink.LookupByExternalRef(context.Background(), "t_6253023d")
	var timeout *TimeoutError
	if err == nil || !errors.As(err, &timeout) {
		t.Fatalf("transport failure must surface the typed error for ambiguous treatment, got %v", err)
	}
}

// TestSinkEvidenceAlwaysValidJSON proves the persisted structured
// evidence is always parseable JSON, including the oversized case.
func TestSinkEvidenceAlwaysValidJSON(t *testing.T) {
	task := TaskRecord{ID: "t_6253023d", Status: "ready", CreatedAt: 1787142146}
	raw := acceptanceEvidenceOf(task)
	if json.Valid(raw) != true {
		t.Fatal("typed evidence must be valid JSON")
	}
	truncated := boundedPayload(make([]byte, 8192))
	if !json.Valid(truncated) {
		t.Fatalf("oversized evidence projection must remain valid JSON, got %s", truncated)
	}
	var marker struct {
		Truncated bool `json:"truncated"`
		Bytes     int  `json:"bytes"`
	}
	if err := json.Unmarshal(truncated, &marker); err != nil || !marker.Truncated || marker.Bytes != 8192 {
		t.Fatalf("truncation marker wrong: %+v err=%v", marker, err)
	}
}

// TestSinkSubmitEligibilityGuardDefinite proves a below-floor Hermes is
// a provable pre-invocation failure (definite), never an ambiguous
// unknown (HER-011 posture at the submit boundary).
func TestSinkSubmitEligibilityGuardDefinite(t *testing.T) {
	bin := newStubHermes(t, `if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.18.0 (2026.5.1)
'; exit 0; fi; exit 3`)
	sink, err := NewSink("t", bin, "", "b", ProcessLimits{}, 262144)
	if err != nil {
		t.Fatalf("construction against the floor must pass: %v", err)
	}
	res, err := sink.Submit(context.Background(), goldenRequestMut(t, nil))
	if err != nil {
		t.Fatalf("eligibility guard must classify, not error: %v", err)
	}
	if res.Classification != ports.SubmitDefiniteNotSubmitted {
		t.Fatalf("a below-floor version must be definite_not_submitted, got %q", res.Classification)
	}
}

// TestSinkExecutableSwapDetectedAtSubmission proves eligibility is
// re-proven per invocation: an executable replaced by a below-floor one
// between construction and submission classifies
// definite_not_submitted (a construction-time snapshot would have
// wrongly proceeded). The full capability-fingerprint binding arrives
// with E11-T2 (HER-018).
func TestSinkExecutableSwapDetectedAtSubmission(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hermes")
	good := `#!/bin/sh
if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.19.1 (2026.7.30)
'; exit 0; fi; exit 3`
	bad := `#!/bin/sh
if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.18.0 (2026.5.1)
'; exit 0; fi; exit 3`
	if err := os.WriteFile(exe, []byte(good), 0o755); err != nil {
		t.Fatal(err)
	}
	sink, err := NewSink("t", exe, "", "b", ProcessLimits{}, 262144)
	if err != nil {
		t.Fatalf("construction against the eligible executable must pass: %v", err)
	}
	if err := os.WriteFile(exe, []byte(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := sink.Submit(context.Background(), goldenRequestMut(t, nil))
	if err != nil {
		t.Fatalf("swapped executable must classify, not error: %v", err)
	}
	if res.Classification != ports.SubmitDefiniteNotSubmitted {
		t.Fatalf("eligibility lost after construction must be definite_not_submitted, got %q", res.Classification)
	}
}

// TestE8T2ExecStartFailureIsDefiniteNotSubmitted pins M-5: a fork/exec
// failure (the argv-length family) proves the child never started, so
// the create failure is definite not-submitted — never an unknown
// dead-letter of provably unsubmitted work.
func TestE8T2ExecStartFailureIsDefiniteNotSubmitted(t *testing.T) {
	s := &Sink{}
	res := s.classifyCreateFailure(&fs.PathError{Op: "fork/exec", Path: "hermes", Err: syscall.E2BIG})
	if res.Classification != ports.SubmitDefiniteNotSubmitted {
		t.Fatalf("a fork/exec failure must classify definite not-submitted, got %v", res.Classification)
	}
	// A post-start file error stays unknown: it cannot prove the child
	// never ran.
	res = s.classifyCreateFailure(&fs.PathError{Op: "write", Path: "pipe", Err: syscall.EPIPE})
	if res.Classification != ports.SubmitUnknown {
		t.Fatalf("a post-start failure must stay unknown, got %v", res.Classification)
	}
}
