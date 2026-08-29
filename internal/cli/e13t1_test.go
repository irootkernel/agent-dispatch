package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
)

// E13-T1 CLI coverage: the transactional notification outbox wired end
// to end — a real `work complete` through the CLI stack commits its
// work_completed intents inside the completion transaction under the
// route's effective policy (DUR-016, NTF-001..NTF-003), with the
// channel-neutral payload structurally free of vault content (SEC-011).

// e13t1NotificationsFixture writes the two-destination fixture with a
// one-sink notification policy whose event list is deliberately omitted:
// the NTF-002 defaults govern, exactly the declaration shape E13 ships.
func e13t1NotificationsFixture(t *testing.T) (string, string) {
	t.Helper()
	configPath, vault := e12t2TwoDestinationFixture(t)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(raw)
	marker := "\n    submission_retry:"
	at := strings.Index(updated, marker)
	if at < 0 {
		t.Fatal("fixture no longer carries the submission_retry block")
	}
	block := "\n    notifications:\n      sinks:\n        - id: ops-log\n          type: log"
	updated = updated[:at] + block + updated[at:]
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, vault
}

// TestE13T1WorkCompleteCommitsNotificationIntentsEndToEnd pins the
// transactional join through the whole CLI stack: `work begin` and
// `work complete` on one lane of the two-destination route leave exactly
// one pending work_completed intent per configured sink, scoped to the
// completing lane, carrying the effective policy's revision digest, with
// a payload that never contains the vault's note body (SEC-011).
func TestE13T1WorkCompleteCommitsNotificationIntentsEndToEnd(t *testing.T) {
	configPath, vault := e13t1NotificationsFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	e12t2Enable(t, configPath)
	// A hostile note body: whatever the vault carries, none of it may
	// reach a notification payload.
	os.WriteFile(strings.Join([]string{vault, "Inbox", "e13t1.md"}, "/"),
		[]byte("SECRET-TOKEN-1234 note body never for notification"), 0o644)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/e13t1.md","exists":true,"new":true,"size":8,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch: %s", errb.String())
		}
	})
	store := e5t1Store(t, configPath)
	defer store.Close()
	var mainChild string
	if err := store.QueryRow(`SELECT dispatch_id FROM child_dispatches WHERE destination_id = 'main'`).Scan(&mainChild); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-e13t1"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-e13t1", "--manifest", "-"}, &out, &errb); code != 0 {
			t.Fatalf("work complete: %s", errb.String())
		}
	})

	// The intent committed with the completion: one per sink (the fixture
	// declares one log sink), pending, scoped to the completed lane.
	var count int
	if err := store.QueryRow(`SELECT COUNT(*) FROM notification_events`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("the completion must commit exactly one intent per sink: %d %v", count, err)
	}
	var event, destination, state, policyRevision, payload string
	if err := store.QueryRow(`SELECT event, COALESCE(destination_id, ''), state, policy_revision, payload_json
		FROM notification_events`).Scan(&event, &destination, &state, &policyRevision, &payload); err != nil {
		t.Fatal(err)
	}
	if event != "work_completed" || state != "pending" || destination != "main" {
		t.Fatalf("the intent must be a pending work_completed scoped to the lane: %q %q %q", event, state, destination)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := config.NotificationPolicyRevision(cfg.Routes["wiki"]); policyRevision != want {
		t.Fatalf("the intent must carry the effective policy revision: %q want %q", policyRevision, want)
	}
	if strings.Contains(payload, "SECRET-TOKEN-1234") || strings.Contains(payload, "note body") {
		t.Fatalf("the payload must never carry vault content: %s", payload)
	}
	if !strings.Contains(payload, `"dispatch_id":"`+mainChild+`"`) {
		t.Fatalf("the payload must carry the completing dispatch identity: %s", payload)
	}

	// The sibling lane's completion is its own occurrence: completing it
	// adds its own single intent, never a duplicate of the first.
	var reviewChild string
	if err := store.QueryRow(`SELECT dispatch_id FROM child_dispatches WHERE destination_id = 'review'`).Scan(&reviewChild); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", reviewChild, "--run-id", "run-e13t1-r"}, &out, &errb); code != 0 {
		t.Fatalf("work begin review: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", reviewChild, "--run-id", "run-e13t1-r", "--manifest", "-"}, &out, &errb); code != 0 {
			t.Fatalf("work complete review: %s", errb.String())
		}
	})
	if err := store.QueryRow(`SELECT COUNT(*) FROM notification_events`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("two lane completions must leave two intents: %d %v", count, err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM notification_events WHERE destination_id = 'review' AND event = 'work_completed'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("the sibling completion must carry its own lane-scoped intent: %d %v", count, err)
	}
}
