package cli

import (
	"bytes"
	"strings"
	"testing"
)

// E7-T8 regression suite: payload versioning (M-8) and the HER-006
// rendering completion (M-9).

// TestReceiptPayloadVersionNeverNull proves the acceptance receipt
// carries the task-request contract version (M-8).
func TestReceiptPayloadVersionNeverNull(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("dispatch: %s", errb.String())
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var nulls int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_receipts WHERE payload_version IS NULL OR payload_version = ''`).Scan(&nulls); err != nil || nulls != 0 {
		t.Fatalf("every receipt must carry its payload version: %d %v", nulls, err)
	}
}

// TestUnknownStoredRequestVersionFailsClosed proves M-8: an intent whose
// stored request version names an unknown contract major is refused on
// read instead of being mis-rendered at submit time.
func TestUnknownStoredRequestVersionFailsClosed(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	id, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	store := e5t1Store(t, configPath)
	if _, err := store.Exec(`UPDATE dispatch_intents SET request_version = 'agent-dispatch.hermes-task/v9' WHERE dispatch_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	store.Close()
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "show", "--config", configPath, id}, &out, &errb); code == 0 || !strings.Contains(errb.String(), "fail closed") {
		t.Fatalf("an unknown stored request version must fail closed on read, got %d: %s", code, errb.String())
	}
}

// The HER-006 rendering completion (acceptance criteria and the
// manifest-existence sentence) is pinned by the regenerated renderer
// golden (TestRenderGolden) and asserted in the hermeskanban package.

// TestPersistedObservationValidatesAgainstSchema proves M-11 end to
// end: the durable observation's flags and position conform to the
// published source-observation schema.
func TestPersistedObservationValidatesAgainstSchema(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	store := e5t1Store(t, configPath)
	defer store.Close()
	var flags, position string
	if err := store.QueryRow(`SELECT flags_json, COALESCE(position_json, '') FROM source_observations ORDER BY observed_at DESC LIMIT 1`).Scan(&flags, &position); err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"Overflow", "FreshInstance", "HasRelative", "RelativeRoot", "has_relative"} {
		if strings.Contains(flags, banned) {
			t.Fatalf("persisted flags must be schema-conformant snake_case without %q: %s", banned, flags)
		}
	}
	for _, want := range []string{"overflow", "fresh_instance"} {
		if !strings.Contains(flags, want) {
			t.Fatalf("persisted flags must carry %q: %s", want, flags)
		}
	}
	if !strings.Contains(position, `"clock"`) {
		t.Fatalf("the verbatim source position (clock) must persist: %s", position)
	}
}
