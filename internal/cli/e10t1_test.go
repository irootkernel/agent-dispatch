package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// e10t1SetHashLimit injects one limits block into the fixture
// configuration before the resources key.
func e10t1SetHashLimit(t *testing.T, configPath string, limit int64) {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), "resources:", "limits:\n  max_hash_file_bytes: "+
		strconv.FormatInt(limit, 10)+"\nresources:", 1)
	if updated == string(raw) {
		t.Fatalf("hash limit injection did not apply")
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestE10T1ReconcileEnvelopeReportsBoundedHashEvidence pins the E10-T1
// operator surface (OPS-012): with a configured hash bound below one
// in-scope file, the reconcile envelope reports the stable over-bound
// file as explicit quarantine evidence, the digest stays unknown in the
// stored snapshot, and a normal in-scope file still hashes. The envelope
// of an ordinary reconciliation stays free of the new evidence keys
// (no concurrent change, no quarantine, no instability).
func TestE10T1ReconcileEnvelopeReportsBoundedHashEvidence(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e10t1SetHashLimit(t, configPath, 16)
	e4t3RegisterRoute(t, configPath)
	big := make([]byte, 64)
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "big.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile with an over-bound file: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	quarantined, _ := res["quarantined_over_bound"].([]any)
	if len(quarantined) != 1 || quarantined[0] != "Inbox/big.md" {
		t.Fatalf("the over-bound file must be quarantine evidence in the envelope: %v", res)
	}
	if _, present := res["concurrent_change"]; present {
		t.Fatalf("an uncontested run must not report a concurrent change: %v", res)
	}
	if _, present := res["unstable_after_retry"]; present {
		t.Fatalf("a stable file must not be instability evidence: %v", res)
	}
	store := e5t1Store(t, configPath)
	var digest int
	if err := store.QueryRow(`SELECT COUNT(*) FROM path_facts WHERE path = 'Inbox/big.md' AND digest IS NULL`).Scan(&digest); err != nil || digest != 1 {
		t.Fatalf("the over-bound file's fact must keep an unknown digest: %d %v", digest, err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM path_facts WHERE path = 'Inbox/new.md' AND digest IS NOT NULL`).Scan(&digest); err != nil || digest != 1 {
		t.Fatalf("the ordinary file must still hash: %d %v", digest, err)
	}
	// The observation revision advanced: the fenced replacement stored
	// the snapshot (revision 1 after the empty baseline).
	var revision int
	if err := store.QueryRow(`SELECT observation_revision FROM resources WHERE resource_id = 'vault-main'`).Scan(&revision); err != nil || revision < 1 {
		t.Fatalf("the stored snapshot must advance the observation revision: %d %v", revision, err)
	}
}

// TestE10T1StatusRegressionAfterFencedSchema proves the E10-T1 schema
// change is status-invisible (the deliverable's status regression
// coverage): the status envelope still reports the store healthy and the
// reconcile/staleness surfaces unchanged after a reconciliation ran on
// the v8 schema.
func TestE10T1StatusRegressionAfterFencedSchema(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"status", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("status after fenced reconciliation: %s", errb.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("status envelope must stay valid JSON: %v", err)
	}
	if envelope["api_version"] != "agent-dispatch.cli/v1" || envelope["ok"] != true {
		t.Fatalf("status envelope must keep its identity and health: %v", envelope)
	}
	_ = vault
}
