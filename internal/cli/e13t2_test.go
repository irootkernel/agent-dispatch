package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeswebhook"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// E13-T2 CLI coverage: the notifications test, list, retry, and drain
// surface end to end over the E13-T1 outbox — the transport probe
// creates nothing (NTF-008), drain delivers bounded attempts with the
// stable idempotency identity across ambiguous retries (NTF-007), the
// refused→retry re-arm path, sink isolation, drift evaluation exactly
// once per appearance (OPS-013), and the status projection.

// e13t2WebhookFixture extends the two-destination fixture with a log
// sink and a webhook sink pointed at one loopback HTTPS endpoint whose
// behavior the test flips; the CLI's client factory is overridden to
// trust the endpoint's certificate and enforce a short deadline. The
// capture records both headers and payload bytes (E13-T2 and G9 share
// this scaffolding).
type e13t2WebhookFixture struct {
	configPath string
	vault      string
	server     *httptest.Server
	mu         sync.Mutex
	status     int
	delay      time.Duration
	requests   []http.Header
	payloads   []string
}

func newE13T2WebhookFixture(t *testing.T) *e13t2WebhookFixture {
	return newNotificationWebhookFixture(t, "E13T2_NOTIFICATION_TOKEN", "e13t2-secret-token", 400*time.Millisecond)
}

// newNotificationWebhookFixture builds the two-destination fixture with
// both notification sinks wired to one loopback HTTPS capture endpoint
// under the named token environment variable.
func newNotificationWebhookFixture(t *testing.T, tokenEnv, tokenValue string, clientTimeout time.Duration) *e13t2WebhookFixture {
	t.Helper()
	f := &e13t2WebhookFixture{status: http.StatusOK}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		status, delay := f.status, f.delay
		f.requests = append(f.requests, r.Header.Clone())
		f.payloads = append(f.payloads, string(body))
		f.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(f.server.Close)

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
	block := "\n    notifications:\n      sinks:\n        - id: ops-log\n          type: log\n        - id: ops-webhook\n          type: webhook\n          endpoint: " + f.server.URL + "\n          auth:\n            type: bearer\n            secret_ref: env:" + tokenEnv
	updated = updated[:at] + block + updated[at:]
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(tokenEnv, tokenValue)

	pool := x509.NewCertPool()
	if leaf := f.server.Certificate(); leaf != nil {
		pool.AddCert(leaf)
	}
	loopback := &http.Client{
		Timeout: clientTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	original := webhookClientFactory
	webhookClientFactory = func(time.Duration) hermeswebhook.HTTPClient { return loopback }
	t.Cleanup(func() { webhookClientFactory = original })
	f.configPath, f.vault = configPath, vault
	return f
}

// deliveredPayloads returns the captured webhook payload bytes.
func (f *e13t2WebhookFixture) deliveredPayloads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.payloads...)
}

func (f *e13t2WebhookFixture) setStatus(status int, delay time.Duration) {
	f.mu.Lock()
	f.status, f.delay = status, delay
	f.mu.Unlock()
}

func (f *e13t2WebhookFixture) capturedHeaders() []http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]http.Header(nil), f.requests...)
}

// e13t2CompleteWork drives one lane to a clean completion and returns
// the completing dispatch id (the E13-T1 proven path).
func e13t2CompleteWork(t *testing.T, configPath, vault, lane, runSuffix string) string {
	t.Helper()
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	e12t2Enable(t, configPath)
	os.WriteFile(strings.Join([]string{vault, "Inbox", "e13t2-" + runSuffix + ".md"}, "/"), []byte("note"), 0o644)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/e13t2-`+runSuffix+`.md","exists":true,"new":true,"size":4,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch: %s", errb.String())
		}
	})
	store := e5t1Store(t, configPath)
	defer store.Close()
	var dispatchID string
	if err := store.QueryRow(`SELECT dispatch_id FROM child_dispatches WHERE destination_id = ?`, lane).Scan(&dispatchID); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-e13t2-" + runSuffix}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-e13t2-" + runSuffix, "--manifest", "-"}, &out, &errb); code != 0 {
			t.Fatalf("work complete: %s", errb.String())
		}
	})
	return dispatchID
}

// e13t2Notifications opens the fixture store and returns the counted
// notification rows by state.
func e13t2NotificationCount(t *testing.T, configPath, where string) int {
	t.Helper()
	store := e5t1Store(t, configPath)
	defer store.Close()
	var n int
	if err := store.QueryRow(`SELECT COUNT(*) FROM notification_events WHERE ` + where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestE13T2NotificationsTestCreatesNothing(t *testing.T) {
	f := newE13T2WebhookFixture(t)
	e4t3RegisterRoute(t, f.configPath)
	e12t2Enable(t, f.configPath)
	var out, errb bytes.Buffer
	if code := Run([]string{"notifications", "test", "--route", "wiki", "--sink", "ops-log", "--config", f.configPath}, &out, &errb); code != 0 {
		t.Fatalf("notifications test: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["outcome"] != "delivered" || res["created_nothing"] != true {
		t.Fatalf("the probe must deliver and declare its nothing-created posture: %v", res)
	}
	if n := e13t2NotificationCount(t, f.configPath, "1=1"); n != 0 {
		t.Fatalf("the probe must create no notification intent (NTF-008): %d", n)
	}
	// The webhook probe reaches the endpoint with the stable probe key.
	var out2, errb2 bytes.Buffer
	if code := Run([]string{"notifications", "test", "--route", "wiki", "--sink", "ops-webhook", "--config", f.configPath}, &out2, &errb2); code != 0 {
		t.Fatalf("notifications test webhook: %s", errb2.String())
	}
	headers := f.capturedHeaders()
	if len(headers) != 1 {
		t.Fatalf("the webhook probe must reach the endpoint once: %d", len(headers))
	}
	if got := headers[0].Get("X-Agent-Dispatch-Notification-Key"); got == "" {
		t.Fatal("the probe must carry the stable idempotency header")
	}
	if n := e13t2NotificationCount(t, f.configPath, "1=1"); n != 0 {
		t.Fatalf("the webhook probe must still create nothing: %d", n)
	}
}

func TestE13T2DrainDeliversLogSinkAndStatusProjects(t *testing.T) {
	configPath, vault := e13t1NotificationsFixture(t)
	e13t2CompleteWork(t, configPath, vault, "main", "drain-log")
	if n := e13t2NotificationCount(t, configPath, "state = 'pending'"); n != 1 {
		t.Fatalf("one pending intent after the completion: %d", n)
	}
	// The status surface projects the pending delivery work (OPS-013).
	var out, errb bytes.Buffer
	if code := Run([]string{"status", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("status: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	notifications, _ := res["notifications"].(map[string]any)
	if notifications == nil || notifications["pending"] != float64(1) {
		t.Fatalf("status must project the pending notification: %v", res["notifications"])
	}
	// Drain delivers the log sink's intent once.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "drain", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	res = decodeEnvelope(t, &out)
	drain, _ := res["drain"].(map[string]any)
	// The explicit drain never evaluates drift (v0.1.6 §4: the drift
	// evaluation rides the scheduled runner), so the pass delivers only
	// the work intent on the log sink.
	if drain["delivered"] != float64(1) || res["pending"] != false {
		t.Fatalf("the drain must deliver the work intent: %v", res)
	}
	// The listing carries the attempt projection; a second drain is a
	// no-op (nothing pending).
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "list", "--state", "delivered", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("list: %s", errb.String())
	}
	res = decodeEnvelope(t, &out)
	rows, _ := res["notifications"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the delivered intent must list: %v", res["notifications"])
	}
	for _, rawRow := range rows {
		row, _ := rawRow.(map[string]any)
		if row["attempt_count"] != float64(1) || row["last_outcome"] != "delivered" {
			t.Fatalf("the attempt projection must join: %v", row)
		}
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "drain", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("second drain: %s", errb.String())
	}
	res = decodeEnvelope(t, &out)
	drain, _ = res["drain"].(map[string]any)
	if drain["delivered"] != float64(0) {
		t.Fatalf("the second drain must find nothing pending: %v", res)
	}
}

func TestE13T2AmbiguousRetryKeepsStableKeyAndRefusedRetryRearms(t *testing.T) {
	f := newE13T2WebhookFixture(t)
	e13t2CompleteWork(t, f.configPath, f.vault, "main", "ambig")
	if n := e13t2NotificationCount(t, f.configPath, "sink_id = 'ops-webhook' AND state = 'pending'"); n != 1 {
		t.Fatalf("one pending webhook intent: %d", n)
	}
	// The endpoint stalls past the client deadline: the delivery is
	// ambiguous (request bytes may have left), never a refusal.
	f.setStatus(http.StatusOK, 600*time.Millisecond)
	var out, errb bytes.Buffer
	if code := Run([]string{"notifications", "drain", "--config", f.configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	drain, _ := res["drain"].(map[string]any)
	// The stalled endpoint classifies the webhook work intent ambiguous
	// (the drift evaluation no longer rides the explicit drain); the log
	// sink's pair delivers.
	if drain["ambiguous"] != float64(1) || drain["delivered"] != float64(1) {
		t.Fatalf("the stalled endpoint must classify its intent ambiguous: %v", res)
	}
	if n := e13t2NotificationCount(t, f.configPath, "sink_id = 'ops-webhook' AND state = 'pending'"); n != 1 {
		t.Fatalf("the ambiguous notification must stay pending: %d", n)
	}
	// The endpoint recovers; every webhook retry presents its own SAME
	// idempotency key (NTF-007, AC-903 shape) and the source state never
	// changed: each notification's key appears exactly twice — once per
	// attempt.
	store := e5t1Store(t, f.configPath)
	keys, err := store.Query(`SELECT idempotency_key FROM notification_events WHERE sink_id = 'ops-webhook'`)
	if err != nil {
		t.Fatal(err)
	}
	var webhookKeys []string
	for keys.Next() {
		var k string
		if err := keys.Scan(&k); err != nil {
			t.Fatal(err)
		}
		webhookKeys = append(webhookKeys, k)
	}
	keys.Close()
	store.Close()
	f.setStatus(http.StatusOK, 0)
	// E16-T2: the ambiguous outcomes carry a persisted backoff deadline,
	// so an immediate second drain selects no webhook work. The explicit
	// operator retry is the sole bypass that makes them immediately due.
	store2 := e5t1Store(t, f.configPath)
	rows, err := store2.Query(`SELECT notification_id FROM notification_events WHERE sink_id = 'ops-webhook' AND state = 'pending'`)
	if err != nil {
		t.Fatal(err)
	}
	var pendingIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		pendingIDs = append(pendingIDs, id)
	}
	rows.Close()
	store2.Close()
	for _, id := range pendingIDs {
		out.Reset()
		errb.Reset()
		if code := Run([]string{"notifications", "retry", id, "--config", f.configPath}, &out, &errb); code != 0 {
			t.Fatalf("retry %s: %s", id, errb.String())
		}
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "drain", "--config", f.configPath}, &out, &errb); code != 0 {
		t.Fatalf("second drain: %s", errb.String())
	}
	headers := f.capturedHeaders()
	if len(headers) != 2 {
		t.Fatalf("both attempts of the notification must reach the endpoint: %d", len(headers))
	}
	for _, want := range webhookKeys {
		seen := 0
		for _, h := range headers {
			if h.Get("X-Agent-Dispatch-Notification-Key") == want {
				seen++
			}
		}
		if seen != 2 {
			t.Fatalf("each notification's stable key must appear exactly once per attempt: key seen %d times", seen)
		}
	}
	if n := e13t2NotificationCount(t, f.configPath, "sink_id = 'ops-webhook' AND state = 'delivered'"); n != 1 {
		t.Fatal("the recovered endpoint must resolve the notification delivered")
	}

	// The refused path: a definite refusal resolves refused; the explicit
	// retry re-arms and delivers once the endpoint accepts.
	e13t2CompleteWork(t, f.configPath, f.vault, "review", "refused")
	f.setStatus(http.StatusForbidden, 0)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "drain", "--config", f.configPath}, &out, &errb); code != 0 {
		t.Fatalf("third drain: %s", errb.String())
	}
	if n := e13t2NotificationCount(t, f.configPath, "sink_id = 'ops-webhook' AND state = 'refused'"); n != 1 {
		t.Fatal("the definite refusal must resolve the notification refused")
	}
	f.setStatus(http.StatusOK, 0)
	refusedStore := e5t1Store(t, f.configPath)
	var refusedID string
	if err := refusedStore.QueryRow(`SELECT notification_id FROM notification_events WHERE sink_id = 'ops-webhook' AND event = 'work_completed' AND state = 'refused'`).Scan(&refusedID); err != nil {
		t.Fatal(err)
	}
	refusedStore.Close()
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "retry", refusedID, "--config", f.configPath}, &out, &errb); code != 0 {
		t.Fatalf("retry: %s", errb.String())
	}
	res = decodeEnvelope(t, &out)
	if res["outcome"] != "delivered" || res["state"] != "delivered" {
		t.Fatalf("the re-armed retry must deliver: %v", res)
	}
	// A delivered notification never re-sends.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "retry", refusedID, "--config", f.configPath}, &out, &errb); code != 4 {
		t.Fatalf("retrying a delivered notification must exit 4, got %d (%s)", code, errb.String())
	}
}

func TestE13T2SinkIsolationAndDriftEvaluation(t *testing.T) {
	f := newE13T2WebhookFixture(t)
	e13t2CompleteWork(t, f.configPath, f.vault, "main", "isolate")
	// The webhook endpoint definitely refuses while the log sink works:
	// each sink's notification resolves independently.
	f.setStatus(http.StatusForbidden, 0)
	var out, errb bytes.Buffer
	if code := Run([]string{"notifications", "drain", "--config", f.configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	if n := e13t2NotificationCount(t, f.configPath, "sink_id = 'ops-log' AND state = 'delivered'"); n != 1 {
		t.Fatal("the log sink must deliver its intent independently of the refusing webhook")
	}
	if n := e13t2NotificationCount(t, f.configPath, "sink_id = 'ops-webhook' AND state = 'refused'"); n != 1 {
		t.Fatal("the webhook notification must resolve refused independently")
	}

	// Drift evaluation (OPS-013) rides the scheduled runner — its only
	// automatic surface since E16 (v0.1.6 §4) — and never the explicit
	// drain: the runner enqueues watchman_drift once per sink for the
	// enabled route's missing Watchman binding, and the second runner
	// pass's identical finding deduplicates. The route stays in manual
	// drain mode, so the enqueued drift intents stay pending for the
	// operator's explicit pass.
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "run", "--route", "wiki", "--config", f.configPath}, &out, &errb); code != 0 {
		t.Fatalf("schedule run with drift: %s", errb.String())
	}
	if n := e13t2NotificationCount(t, f.configPath, "event = 'watchman_drift'"); n != 2 {
		t.Fatalf("the drift intent must exist once per sink: %d", n)
	}
	if n := e13t2NotificationCount(t, f.configPath, "event = 'watchman_drift' AND state = 'pending'"); n != 2 {
		t.Fatalf("a manual-mode route's drift intents wait for the explicit drain: %d", n)
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "run", "--route", "wiki", "--config", f.configPath}, &out, &errb); code != 0 {
		t.Fatalf("dedup schedule run: %s", errb.String())
	}
	if n := e13t2NotificationCount(t, f.configPath, "event = 'watchman_drift'"); n != 2 {
		t.Fatalf("a persisting drift must never re-notify: %d", n)
	}
}

// TestE13T2PayloadAndLogsStaySecretFree pins SEC-011 end to end: the
// vault's hostile note body and the resolved webhook secret appear in
// neither any stored payload nor the command's ordinary stdout.
func TestE13T2PayloadAndLogsStaySecretFree(t *testing.T) {
	f := newE13T2WebhookFixture(t)
	e13t2CompleteWork(t, f.configPath, f.vault, "main", "secret")
	f.setStatus(http.StatusOK, 0)
	var out, errb bytes.Buffer
	if code := Run([]string{"notifications", "drain", "--config", f.configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	store := e5t1Store(t, f.configPath)
	defer store.Close()
	rows, err := store.Query(`SELECT payload_json FROM notification_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	payloads := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, p)
	}
	if len(payloads) == 0 {
		t.Fatal("the completion must have created notifications")
	}
	for _, p := range payloads {
		if strings.Contains(p, "e13t2-secret-token") || strings.Contains(p, "note") {
			t.Fatalf("a payload leaked secret or content material: %s", p)
		}
	}
	if strings.Contains(out.String(), "e13t2-secret-token") || strings.Contains(errb.String(), "e13t2-secret-token") {
		t.Fatal("the ordinary command output must never contain the resolved secret")
	}
	var attemptDiagnostic int
	if err := store.QueryRow(`SELECT COUNT(*) FROM notification_attempts WHERE error_code LIKE '%e13t2-secret-token%'`).Scan(&attemptDiagnostic); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if attemptDiagnostic != 0 {
		t.Fatal("no attempt diagnostic may retain the resolved secret")
	}
	// The envelope's drain report is JSON-clean for the token.
	var probe map[string]any
	if err := json.Unmarshal(out.Bytes(), &probe); err != nil {
		t.Fatalf("the drain envelope must stay valid JSON: %v", err)
	}
}

// TestE13T2DefectiveSinkDeclarationDoesNotBlockHealthySinks pins the
// round-1 remediation: a sink declaration that fails closed (plain-http
// endpoint) records its own notifications as retryable attempts while
// the healthy log sink still delivers — one broken declaration never
// aborts the pass.
func TestE13T2DefectiveSinkDeclarationDoesNotBlockHealthySinks(t *testing.T) {
	f := newE13T2WebhookFixture(t)
	e13t2CompleteWork(t, f.configPath, f.vault, "main", "defective")
	// The stale-declaration scenario: the webhook sink is REMOVED from
	// the configuration after its notifications exist. Config
	// validation catches malformed declarations up front (plain-http
	// exits 3 at load), so the delivery-time construction failure that
	// reaches the drain is a notification whose sink the configuration
	// no longer declares.
	raw, err := os.ReadFile(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	cut := strings.Index(string(raw), "        - id: ops-webhook")
	if cut < 0 {
		t.Fatal("fixture no longer carries the webhook sink block")
	}
	stripped := string(raw)[:cut] + strings.Join(strings.Split(string(raw)[cut:], "\n")[6:], "\n")
	if err := os.WriteFile(f.configPath, []byte(stripped), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"notifications", "drain", "--config", f.configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain must survive a defective declaration: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	drain, _ := res["drain"].(map[string]any)
	// The explicit drain carries no drift evaluation (v0.1.6 §4), so the
	// stale webhook carries exactly its work notification as retryable
	// evidence and the log sink delivers its single work intent.
	if drain["delivered"] != float64(1) || drain["retryable"] != float64(1) {
		t.Fatalf("the log sink must deliver while the stale webhook records retryable: %v", res)
	}
	// The webhook notifications stayed pending with the unresolvable
	// evidence; the log notifications resolved delivered.
	if n := e13t2NotificationCount(t, f.configPath, "sink_id = 'ops-log' AND state = 'delivered'"); n != 1 {
		t.Fatalf("the healthy sink must deliver its intent: %d", n)
	}
	if n := e13t2NotificationCount(t, f.configPath, "sink_id = 'ops-webhook' AND state = 'pending'"); n != 1 {
		t.Fatalf("the stale sink's intent must stay pending: %d", n)
	}
	store := e5t1Store(t, f.configPath)
	defer store.Close()
	var code string
	if err := store.QueryRow(`SELECT error_code FROM notification_attempts a JOIN notification_events e ON e.notification_id = a.notification_id WHERE e.sink_id = 'ops-webhook' LIMIT 1`).Scan(&code); err != nil || code != "sink_unresolvable" {
		t.Fatalf("the unresolvable declaration must leave its evidence: %q %v", code, err)
	}
	// The log sink's payload lines landed on stderr (the structured
	// stream), keeping stdout's envelope clean.
	if !strings.Contains(errb.String(), "notification-event/v1") {
		t.Fatalf("the log delivery lines belong on stderr: %.200s", errb.String())
	}
}

// TestE13T2RetryPendingAndUnknown covers the retry command's remaining
// arms: a pending notification delivers without a re-arm, an unknown ID
// exits 4, and --limit above the listing bound is a usage error.
func TestE13T2RetryPendingAndUnknown(t *testing.T) {
	configPath, vault := e13t1NotificationsFixture(t)
	e13t2CompleteWork(t, configPath, vault, "main", "pending")
	var out, errb bytes.Buffer
	if code := Run([]string{"notifications", "retry", "ntf-does-not-exist", "--config", configPath}, &out, &errb); code != 4 {
		t.Fatalf("an unknown notification must exit 4: %d %s", code, errb.String())
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var pendingID string
	if err := store.QueryRow(`SELECT notification_id FROM notification_events WHERE state = 'pending' LIMIT 1`).Scan(&pendingID); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "retry", pendingID, "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("retry pending: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["outcome"] != "delivered" || res["state"] != "delivered" {
		t.Fatalf("the pending retry must deliver: %v", res)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "list", "--limit", "501", "--config", configPath}, &out, &errb); code != 2 {
		t.Fatalf("a limit above the listing bound must be a usage error: %d", code)
	}
}

// TestE13T2ProbeIdempotencyAndDialRefusal pins two adapter postures at
// the CLI boundary: repeated probes present the SAME idempotency key
// (the endpoint can deduplicate them), and a connection-refused
// endpoint classifies retryable (provable pre-delivery failure).
func TestE13T2ProbeIdempotencyAndDialRefusal(t *testing.T) {
	f := newE13T2WebhookFixture(t)
	e4t3RegisterRoute(t, f.configPath)
	e12t2Enable(t, f.configPath)
	for i := 0; i < 2; i++ {
		var out, errb bytes.Buffer
		if code := Run([]string{"notifications", "test", "--route", "wiki", "--sink", "ops-webhook", "--config", f.configPath}, &out, &errb); code != 0 {
			t.Fatalf("probe %d: %s", i+1, errb.String())
		}
	}
	headers := f.capturedHeaders()
	if len(headers) != 2 {
		t.Fatalf("both probes must reach the endpoint: %d", len(headers))
	}
	if headers[0].Get("X-Agent-Dispatch-Notification-Key") != headers[1].Get("X-Agent-Dispatch-Notification-Key") {
		t.Fatal("repeated probes must present the same stable key")
	}
	// A closed port is a provable pre-delivery failure: retryable, and
	// the diagnostic carries neither the secret nor the endpoint URL.
	closed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	raw, err := os.ReadFile(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	repointed := strings.Replace(string(raw), f.server.URL, closedURL, 1)
	if err := os.WriteFile(f.configPath, []byte(repointed), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"notifications", "test", "--route", "wiki", "--sink", "ops-webhook", "--config", f.configPath}, &out, &errb); code != 0 {
		t.Fatalf("probe against closed endpoint: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["outcome"] != "retryable" {
		t.Fatalf("a refused connection must classify retryable: %v", res)
	}
	if strings.Contains(out.String(), closedURL) || strings.Contains(errb.String(), closedURL) {
		t.Fatal("the endpoint URL must not persist into ordinary output")
	}
}

// TestE13T2DrainReportsPendingBeyondThePassBound pins the round-1
// remediation of the epic validation: the envelope's pending truth is
// the store's post-pass pending set, never just this pass's outcomes —
// a pass bound must not hide a backlog.
func TestE13T2DrainReportsPendingBeyondThePassBound(t *testing.T) {
	configPath, vault := e13t1NotificationsFixture(t)
	e13t2CompleteWork(t, configPath, vault, "main", "bound-a")
	e13t2CompleteWork(t, configPath, vault, "review", "bound-b")
	if n := e13t2NotificationCount(t, configPath, "state = 'pending'"); n != 2 {
		t.Fatalf("two pending work intents: %d", n)
	}
	// The bounded pass delivers one of the two pending work
	// notifications (the explicit drain adds no drift intents since
	// v0.1.6 §4) and must report the one that remains.
	var out, errb bytes.Buffer
	if code := Run([]string{"notifications", "drain", "--limit", "1", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	drain, _ := res["drain"].(map[string]any)
	if drain["delivered"] != float64(1) {
		t.Fatalf("the bounded pass must deliver exactly one: %v", res["drain"])
	}
	if res["pending"] != true || res["pending_remaining"] != float64(1) {
		t.Fatalf("the envelope must report the beyond-bound backlog: %v", res)
	}
	// An unbounded pass drains the remainder and reports a clean set.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "drain", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("second drain: %s", errb.String())
	}
	res = decodeEnvelope(t, &out)
	if res["pending"] != false || res["pending_remaining"] != float64(0) {
		t.Fatalf("the drained set must report no pending remainder: %v", res)
	}
}

// TestE13T2ScheduleRunAbortsWhenDriftEnqueueFails pins the storage
// posture of the drift evaluation's new automatic surface: a
// drift-enqueue storage failure inside the scheduled runner is the
// storage class (exit 20), never a silent dry pass.
func TestE13T2ScheduleRunAbortsWhenDriftEnqueueFails(t *testing.T) {
	configPath, _ := e13t1NotificationsFixture(t)
	e4t3RegisterRoute(t, configPath)
	e12t2Enable(t, configPath)
	original := driftEnqueue
	driftEnqueue = func(context.Context, *sqlite.Store, string, records.NotificationEventKind, string, map[string]string, string) (int, error) {
		return 0, errors.New("drift enqueue storage failure")
	}
	t.Cleanup(func() { driftEnqueue = original })
	var out, errb bytes.Buffer
	markInvocationStart()
	code := Run([]string{"schedule", "run", "--route", "wiki", "--config", configPath}, &out, &errb)
	if code != 20 {
		t.Fatalf("a drift-enqueue storage failure must exit 20, got %d (%s)", code, errb.String())
	}
	if !strings.Contains(errb.String(), "sqlite_query_failed") {
		t.Fatalf("the failure must carry the storage error code: %s", errb.String())
	}
	if strings.Contains(out.String(), "drift enqueue storage failure") {
		t.Fatal("a storage failure must not ride stdout as data")
	}
}
