package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestE12T1ReconcilePathPersistsChildAndAggregate pins the reconcile
// creation path's child linkage (E12-T1): a `reconcile --submit` that
// reaches acceptance leaves one child dispatch beneath one origin
// `reconcile` aggregate event, with the destination revision persisted
// from the live certified lane (review round 1, testing finding 2).
func TestE12T1ReconcilePathPersistsChildAndAggregate(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	os.WriteFile(filepath.Join(vault, "Inbox", "extra.md"), []byte("extra"), 0o644)
	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial", "--submit"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile --submit: %s", errb.String())
	}
	store := e5t1Store(t, configPath)
	var dispatchID string
	if err := store.QueryRow(`SELECT dispatch_id FROM dispatch_intents ORDER BY created_at DESC LIMIT 1`).Scan(&dispatchID); err != nil {
		t.Fatal(err)
	}
	child, err := store.LoadChildDispatch(requestCtx(), dispatchID)
	if err != nil {
		t.Fatalf("the reconcile intent must be child-linked: %v", err)
	}
	if child.DestinationID == "" || child.DestinationRevision == "" || child.Workstream == "" {
		t.Fatalf("the reconcile child must carry the certified lane: %+v", child)
	}
	agg, err := store.LoadAggregateEvent(requestCtx(), child.AggregateID)
	if err != nil || agg.Origin != "reconcile" {
		t.Fatalf("the reconcile aggregate must persist with origin reconcile: %+v %v", agg, err)
	}
	var revisions int
	if err := store.QueryRow(`SELECT COUNT(*) FROM destination_revisions WHERE destination_id = ?`, child.DestinationID).Scan(&revisions); err != nil || revisions != 1 {
		t.Fatalf("the reconcile path must persist the referenced destination revision: %d %v", revisions, err)
	}
	// The dispatches list surface exposes the same linkage beside the
	// request document (review round 1: list drifted from show).
	outs := &bytes.Buffer{}
	errs := &bytes.Buffer{}
	if code := Run([]string{"dispatches", "list", "--config", configPath, "--output", "json"}, outs, errs); code != 0 {
		t.Fatalf("dispatches list: %s", errs.String())
	}
	listed := decodeEnvelope(t, outs)
	rows, ok := listed["dispatches"].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("list must return the intents array: %v", listed)
	}
	row, _ := rows[0].(map[string]any)
	if row["aggregate_id"] != child.AggregateID || row["destination_id"] != child.DestinationID {
		t.Fatalf("list must surface the child linkage: %v", row)
	}
}
