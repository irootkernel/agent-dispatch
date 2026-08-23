package hermeskanban

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// machineReport is the real E0-T4 capability report frozen in the docs
// package; the adapter's capability authority is exactly this document.
const machineReport = "../../../docs/integrations/hermes-capability-report.json"

func TestLoadFrozenReport(t *testing.T) {
	report, err := LoadReport(machineReport)
	if err != nil {
		t.Fatalf("frozen report must load: %v", err)
	}
	if report.SchemaVersion != ReportSchemaVersion {
		t.Fatalf("schema version %q", report.SchemaVersion)
	}
	if report.Interface != "public_cli" {
		t.Fatalf("interface %q", report.Interface)
	}
	caps := report.PortCapabilities()
	// E0-T4 §10: every capability the durable Kanban contract requires is
	// present on 0.19.1 with no reduced guarantee.
	want := ports.Capabilities{
		DurableAcceptance:      true,
		SubmitIdempotencyKey:   true,
		LookupByIdempotencyKey: false,
		LookupByExternalRef:    true,
		ResourceMutex:          true,
		ExecutionStatus:        true,
		Cancellation:           true,
		ResultReceipt:          true,
	}
	if caps != want {
		t.Fatalf("frozen report capabilities = %+v want %+v", caps, want)
	}
	// maximum_request_bytes is null in the report (E0-T4 §8): Hermes
	// documents no public request-size limit and the adapter enforces its
	// own bound.
	if caps.MaximumRequestBytes != 0 {
		t.Fatalf("null maximum_request_bytes must map to 0 (unset), got %d", caps.MaximumRequestBytes)
	}
}

func TestLoadReportFailsClosed(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cases := []struct {
		name string
		body string
	}{
		{"not json", "not json"},
		{"wrong schema version", `{"schema_version":"agent-dispatch.hermes-capabilities/v2","probed_at":"2026-08-19T21:25:24+09:00","hermes_version":"0.19.1 (2026.7.30)","interface":"public_cli","capabilities":{},"evidence":[]}`},
		{"wrong interface", `{"schema_version":"agent-dispatch.hermes-capabilities/v1","probed_at":"2026-08-19T21:25:24+09:00","hermes_version":"0.19.1","interface":"public_webhook","capabilities":{},"evidence":[]}`},
		{"missing version", `{"schema_version":"agent-dispatch.hermes-capabilities/v1","probed_at":"2026-08-19T21:25:24+09:00","interface":"public_cli","capabilities":{},"evidence":[]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := LoadReport(write(c.name, c.body)); err == nil {
				t.Fatal("report must fail closed")
			}
		})
	}
	if _, err := LoadReport(filepath.Join(dir, "absent.json")); err == nil {
		t.Fatal("missing report file must fail closed")
	}
}

func TestReportVersionMatch(t *testing.T) {
	report, err := LoadReport(machineReport)
	if err != nil {
		t.Fatal(err)
	}
	if !report.VersionMatchsWith(Version{0, 19, 1, "2026.7.30"}) {
		t.Fatal("0.19.1 must match the frozen report")
	}
	if report.VersionMatchsWith(Version{0, 20, 0, "2026.8.10"}) {
		t.Fatal("0.20.0 must not match the frozen report")
	}
}

func TestValidateRequired(t *testing.T) {
	all := ports.Capabilities{
		DurableAcceptance:      true,
		SubmitIdempotencyKey:   true,
		LookupByIdempotencyKey: false,
		LookupByExternalRef:    true,
		ResourceMutex:          true,
		ExecutionStatus:        true,
		Cancellation:           true,
		ResultReceipt:          true,
	}
	// The honest capability set excludes the port-level key lookup (the
	// public CLI has no read-only query; E8-T3), so requiring every name
	// is the negative case below and the honest set validates.
	requireHonest := []string{"durable_acceptance", "submit_idempotency_key", "lookup_by_external_ref", "resource_mutex", "execution_status", "cancellation", "result_receipt"}
	if err := ValidateRequired("t", all, requireHonest); err != nil {
		t.Fatalf("all honest capabilities present must validate: %v", err)
	}
	if err := ValidateRequired("t", all, ports.CapabilityNames); err == nil {
		t.Fatal("requiring the unsupported key lookup must fail closed (E8-T3)")
	}
	if err := ValidateRequired("t", all, nil); err != nil {
		t.Fatalf("no requirements must validate: %v", err)
	}

	// HER-005: a missing capability fails closed with the typed error.
	withoutMutex := all
	withoutMutex.ResourceMutex = false
	err := ValidateRequired("wiki", withoutMutex, []string{"durable_acceptance", "resource_mutex"})
	var missing *CapabilityError
	if !errors.As(err, &missing) {
		t.Fatalf("missing capability must surface CapabilityError, got %v", err)
	}
	if len(missing.Missing) != 1 || missing.Missing[0] != "resource_mutex" {
		t.Fatalf("missing list = %v", missing.Missing)
	}
	if missing.Remediation() == "" {
		t.Fatal("CapabilityError must carry remediation")
	}

	// An unknown name is a configuration defect, never a reduced
	// guarantee.
	err = ValidateRequired("t", all, []string{"teleportation"})
	if err == nil {
		t.Fatal("unknown capability name must fail closed")
	}
}
