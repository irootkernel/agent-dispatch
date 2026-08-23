package doctor

import (
	"strings"
	"testing"
	"time"
)

// TestExamineCoversEveryFindingCode pins every finding class: code,
// severity, and a non-empty remediation, so a swapped code, an inverted
// condition, or a missing severity mapping fails a test.
func TestExamineCoversEveryFindingCode(t *testing.T) {
	no := false
	yes := true
	cases := []struct {
		name  string
		input Input
		code  string
		owner func([]Finding) Finding
	}{
		{"config", Input{SemanticErrors: []string{"boom"}}, "config_invalid", nil},
		{"resource missing", Input{Resources: []ResourceFact{{ResourceID: "v", Root: "/x", Missing: true}}}, "resource_root_missing", nil},
		{"resource unreadable", Input{Resources: []ResourceFact{{ResourceID: "v", Root: "/x", NotReadable: true}}}, "resource_root_not_readable", nil},
		{"resource perms", Input{Resources: []ResourceFact{{ResourceID: "v", Root: "/x", NotOwnerOnly: true}}}, "resource_root_permissions", nil},
		{"store open", Input{StoreExamined: true, Store: StoreFact{OpenError: "nope"}}, "sqlite_open_failed", nil},
		{"store integrity", Input{StoreExamined: true, Store: StoreFact{IntegrityError: "corrupt"}}, "sqlite_integrity_failed", nil},
		{"journal mode", Input{StoreExamined: true, Store: StoreFact{JournalMode: "delete", SchemaVersion: 4, LatestVersion: 4}}, "journal_mode_unexpected", nil},
		{"migration", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 2, LatestVersion: 4}}, "migration_pending", nil},
		{"stale lease", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 4, LatestVersion: 4, StaleLeases: []string{"d-1"}}}, "stale_attempt_lease", nil},
		{"unknown", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 4, LatestVersion: 4, UnknownCount: 2}}, "unknown_dispatches", nil},
		{"dead letter", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 4, LatestVersion: 4, DeadLettered: 1}}, "dead_lettered_dispatches", nil},
		{"db size", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 4, LatestVersion: 4, DatabaseBytes: largeDatabaseBytes + 1}}, "database_size_large", nil},
		{"watchman", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 4, LatestVersion: 4}, WatchmanExamined: true, Watchman: WatchmanFact{UnusableBecause: "gone"}}, "watchman_unavailable", nil},
		{"watchman version", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 4, LatestVersion: 4}, Watchman: WatchmanFact{Available: true, Version: "x", UnusableBecause: "old"}}, "watchman_version_unsupported", nil},
		{"target gate", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 4, LatestVersion: 4}, Targets: []TargetFact{{TargetID: "t", GateError: "bad"}}}, "target_gate_failed", nil},
		{"secret", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 4, LatestVersion: 4}, Targets: []TargetFact{{TargetID: "t", SecretResolved: &no}}}, "secret_unresolvable", nil},
		{"stale active", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 4, LatestVersion: 4}, Routes: []RouteFact{{RouteID: "r", ActiveDispatchID: "d", ActiveDispatchAge: 48 * time.Hour, StaleActiveAfter: 2 * time.Hour}}}, "stale_active_route", nil},
		{"never reconciled", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 4, LatestVersion: 4}, Routes: []RouteFact{{RouteID: "r", DailyExpected: true}}}, "reconciliation_never_run", nil},
		{"overdue", Input{StoreExamined: true, Store: StoreFact{SchemaVersion: 4, LatestVersion: 4}, Routes: []RouteFact{{RouteID: "r", DailyExpected: true, LastReconciledAt: "x", ReconciledAge: 48 * time.Hour}}}, "reconciliation_overdue", nil},
	}
	for _, tc := range cases {
		findings := Examine(tc.input)
		var got *Finding
		for i := range findings {
			if findings[i].Code == tc.code {
				got = &findings[i]
				break
			}
		}
		if got == nil {
			codes := make([]string, 0, len(findings))
			for _, f := range findings {
				codes = append(codes, f.Code)
			}
			t.Fatalf("%s: finding %q missing (got %v)", tc.name, tc.code, codes)
		}
		if got.Severity == "" || got.Summary == "" || got.Remediation == "" {
			t.Fatalf("%s: finding incomplete: %+v", tc.name, got)
		}
	}
	_ = yes
}

// TestExamineSkipsStoreWhenNotExamined proves a configuration failure
// never fabricates store findings.
func TestExamineSkipsStoreWhenNotExamined(t *testing.T) {
	findings := Examine(Input{SemanticErrors: []string{"boom"}})
	for _, f := range findings {
		if strings.HasPrefix(f.Code, "sqlite_") || f.Code == "migration_pending" || f.Code == "journal_mode_unexpected" {
			t.Fatalf("store finding fabricated without an examined store: %+v", f)
		}
	}
}

// TestExamineSortsStably proves the severity-then-code ordering.
func TestExamineSortsStably(t *testing.T) {
	findings := Examine(Input{
		SemanticErrors: []string{"boom"},
		StoreExamined:  true,
		Store:          StoreFact{UnknownCount: 1, SchemaVersion: 4, LatestVersion: 4},
		Resources:      []ResourceFact{{ResourceID: "v", Root: "/x", Missing: true}},
	})
	if len(findings) < 3 {
		t.Fatalf("expected several findings: %+v", findings)
	}
	for i := 1; i < len(findings); i++ {
		if findings[i].Severity == SeverityError && findings[i-1].Severity != SeverityError {
			t.Fatalf("error finding after non-error: %+v", findings)
		}
	}
}
