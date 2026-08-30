package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// E14-T2 CLI proof (CLI-017, AC-1004): the public --baseline-only
// operation, its usage and state guards, and the zero-production-row
// guarantee observed through the command surface.

// e14t2Decode reads the reconcile envelope's result object.
func e14t2Decode(t *testing.T, out *bytes.Buffer) map[string]any {
	t.Helper()
	var env struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("the envelope must decode: %v %s", err, out.String())
	}
	return env.Result
}

// TestE14T2BaselineOnlyEstablishesDisabledBaseline proves the public
// command end to end: a clean host baselines the disabled route, the
// envelope carries the baseline evidence, and the state store contains
// the baseline row and zero production rows.
func TestE14T2BaselineOnlyEstablishesDisabledBaseline(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Resources["vault-main"].Root, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Resources["vault-main"].Root, "Inbox", "a.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, "/tmp", false)
	var out, errb bytes.Buffer
	code := Run([]string{"reconcile", "--route", "wiki", "--reason", "initial", "--baseline-only", "--config", configPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("the clean-host baseline must succeed: %d %s", code, errb.String())
	}
	result := e14t2Decode(t, &out)
	if result["baseline_only"] != true || result["snapshot_stored"] != true {
		t.Fatalf("the envelope must report the stored baseline: %v", result)
	}
	if _, ok := result["snapshot_sha256"].(string); !ok || result["snapshot_sha256"] == "" {
		t.Fatalf("the envelope must carry the baseline digest: %v", result)
	}
	// The state store holds the baseline and no production row.
	store, err := openUnmigratedStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	rec, err := store.LoadRouteBaseline(ctx, "wiki")
	if err != nil || rec == nil {
		t.Fatalf("the baseline row must exist: %v %v", rec, err)
	}
	for _, table := range []string{"policy_decisions", "dispatch_intents", "dispatch_receipts", "work_receipts", "route_runtime_state", "notification_events"} {
		var n int
		if err := store.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("the baseline command must create zero rows in %s: %d", table, n)
		}
	}
}

// TestE14T2BaselineOnlyRefusesSubmitCombination proves the no-submit
// contract: --baseline-only with --submit is a usage error.
func TestE14T2BaselineOnlyRefusesSubmitCombination(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	setPlanEnv(t, "/tmp", false)
	var out, errb bytes.Buffer
	code := Run([]string{"reconcile", "--route", "wiki", "--reason", "initial", "--baseline-only", "--submit", "--config", configPath}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "no submit path") {
		t.Fatalf("the combination must be a usage error: %d %s", code, errb.String())
	}
}

// TestE14T2BaselineOnlyRefusesEnabledStates proves both guard halves at
// the command surface: a YAML-enabled route and a runtime-enabled route
// both refuse with the transition-invalid exit (CLI-017).
func TestE14T2BaselineOnlyRefusesEnabledStates(t *testing.T) {
	bin := stubhermes.Write(t)

	t.Run("configuration enabled", func(t *testing.T) {
		configPath := e11t2HermesConfig(t, bin)
		raw, _ := os.ReadFile(configPath)
		if err := os.WriteFile(configPath, bytes.Replace(raw, []byte("enabled: false"), []byte("enabled: true"), 1), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(cfg.Resources["vault-main"].Root, 0o755); err != nil {
			t.Fatal(err)
		}
		setPlanEnv(t, "/tmp", false)
		var out, errb bytes.Buffer
		code := Run([]string{"reconcile", "--route", "wiki", "--reason", "initial", "--baseline-only", "--config", configPath}, &out, &errb)
		if code != 14 || !strings.Contains(errb.String(), "enabled in configuration") {
			t.Fatalf("an enabled configuration key must refuse at exit 14: %d %s", code, errb.String())
		}
	})

	t.Run("runtime enabled", func(t *testing.T) {
		configPath := e11t2HermesConfig(t, bin)
		cfg, err := config.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(cfg.Resources["vault-main"].Root, 0o755); err != nil {
			t.Fatal(err)
		}
		setPlanEnv(t, "/tmp", false)
		// Materialize the route rows, then flip the runtime half on.
		e4t3RegisterRoute(t, configPath)
		store, err := openUnmigratedStore(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetRouteActivation(context.Background(), "wiki", "enabled", "route-rev-1", "", "2026-08-31T00:00:00Z"); err != nil {
			store.Close()
			t.Fatal(err)
		}
		store.Close()
		var out, errb bytes.Buffer
		code := Run([]string{"reconcile", "--route", "wiki", "--reason", "initial", "--baseline-only", "--config", configPath}, &out, &errb)
		if code != 14 || !strings.Contains(errb.String(), "disabled-route operation") {
			t.Fatalf("an enabled runtime half must refuse at exit 14: %d %s", code, errb.String())
		}
	})
}

// TestE14T2BaselineOnlyHelpDocumentsOperation proves CLI-017's
// documented-disabled-route-operation clause in the command help.
func TestE14T2BaselineOnlyHelpDocumentsOperation(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "-h"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile help: %d", code)
	}
	help := out.String()
	for _, want := range []string{"--baseline-only", "disabled-route operation", "baseline record", "rerunnable"} {
		if !strings.Contains(help, want) {
			t.Fatalf("the reconcile help must document %q: %s", want, help)
		}
	}
}
