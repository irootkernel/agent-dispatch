package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"context"
	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// cliStoreFixture builds one real store behind a config that points the
// CLI at it, with one ready intent seeded through the public surface.
func cliStoreFixture(t *testing.T) (configPath string, dispatchID string) {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(filepath.Join(stateDir, "backups")); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterRoute(nil, "wiki", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := db.InitializeRouteState(nil, "wiki"); err != nil {
		t.Fatal(err)
	}
	lin := ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-1", SchemaVersion: "jjukkumi.source-observation/v1", SourceType: "watchman",
			SourceID: "watchman-main", TriggerName: "trig", ResourceID: "vault-main",
			ObservedAt: "2026-08-20T01:00:00Z", ReceivedAt: "2026-08-20T01:00:00Z",
			RawPayloadDigest: "sha256:" + rep64('a'), IngestStatus: "accepted",
		},
		Batch: ports.BatchInput{
			BatchID: "batch-1", RouteID: "wiki", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: "2026-08-20T01:00:00Z",
			ContentFingerprint: "sha256:" + rep64('c'), ObservationIDs: []string{"obs-1"},
		},
		Decision: ports.DecisionInput{
			DecisionID: "decision-1", BatchID: "batch-1", RouteID: "wiki",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch",
			Classification: "normal", CreatedAt: "2026-08-20T01:00:00Z", Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: "dispatch-1", DecisionID: "decision-1", RouteID: "wiki",
			RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
			ResourceID: "vault-main", Generation: 1, IdempotencyKey: "jjukkumi:v1:sha256:" + rep64('1'),
			ContentFingerprint: "sha256:" + rep64('c'), ManifestDigest: "sha256:" + rep64('d'),
			RequestVersion: "jjukkumi.hermes-task/v1", RequestJSON: "{}", CreatedAt: "2026-08-20T01:00:00Z",
		},
	}
	if err := db.CommitLineage(context.Background(), lin); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "jjukkumi.yaml")
	base, _ := planFixture(t)
	raw, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(raw), "  id: plan-test\n", "  id: plan-test\n  state_dir: "+stateDir+"\n", 1)
	if patched == string(raw) {
		t.Fatal("could not inject state_dir into the fixture configuration")
	}
	if err := os.WriteFile(cfgPath, []byte(patched), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, "dispatch-1"
}

func rep64(ch byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

func TestDispatchesListAndShow(t *testing.T) {
	cfgPath, dispatchID := cliStoreFixture(t)
	var out, errb bytes.Buffer
	code := Run([]string{"dispatches", "list", "--config", cfgPath, "--route", "wiki"}, &out, &errb)
	if code != 0 {
		t.Fatalf("list: %d %s", code, errb.String())
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil || env.Command != "dispatches list" {
		t.Fatalf("list envelope: %v %s", err, out.String())
	}
	raw, err := json.Marshal(env.Result)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Count != 1 {
		t.Fatalf("list count: %v %s", err, out.String())
	}
	out.Reset()
	code = Run([]string{"dispatches", "show", "--config", cfgPath, dispatchID}, &out, &errb)
	if code != 0 {
		t.Fatalf("show: %d %s", code, errb.String())
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil || env.Command != "dispatches show" {
		t.Fatalf("show envelope: %v %s", err, out.String())
	}
	// A missing dispatch is the documented usage error.
	out.Reset()
	code = Run([]string{"dispatches", "show", "--config", cfgPath, "missing"}, &out, &errb)
	if code != 4 {
		t.Fatalf("missing dispatch must exit 4, got %d: %s", code, errb.String())
	}
}

func TestRouteEnableListShowDisable(t *testing.T) {
	cfgPath, _ := cliStoreFixture(t)
	var out, errb bytes.Buffer
	// Enable requires the production gate flags.
	if code := Run([]string{"route", "enable", "--config", cfgPath, "--route", "wiki"}, &out, &errb); code != 2 {
		t.Fatalf("enable without gate flags must be usage: %d", code)
	}
	out.Reset()
	code := Run([]string{"route", "enable", "--config", cfgPath, "--route", "wiki", "--acknowledge-production-gate", "--yes"}, &out, &errb)
	if code != 0 {
		t.Fatalf("enable: %d %s", code, errb.String())
	}
	out.Reset()
	code = Run([]string{"route", "list", "--config", cfgPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("list: %d %s", code, errb.String())
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	code = Run([]string{"route", "show", "--config", cfgPath, "--route", "wiki"}, &out, &errb)
	if code != 0 {
		t.Fatalf("show: %d %s", code, errb.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`"activation_state":"enabled"`)) {
		t.Fatalf("enabled route must show: %s", out.String())
	}
	out.Reset()
	code = Run([]string{"route", "disable", "--config", cfgPath, "--route", "wiki", "--reason", "maintenance window"}, &out, &errb)
	if code != 0 {
		t.Fatalf("disable: %d %s", code, errb.String())
	}
	out.Reset()
	code = Run([]string{"route", "show", "--config", cfgPath, "--route", "wiki"}, &out, &errb)
	if code != 0 || !bytes.Contains(out.Bytes(), []byte(`"activation_state":"disabled"`)) {
		t.Fatalf("disabled route must show: %d %s", code, out.String())
	}
}
