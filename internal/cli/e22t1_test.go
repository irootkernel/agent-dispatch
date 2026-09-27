package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

// The test binary stands in for the installed CLI only for this exact
// reconciliation invocation. The grandchild shares its process group.
func init() {
	mode := os.Getenv("AGENT_DISPATCH_E22_RECONCILE_HELPER")
	if mode == "" {
		return
	}
	if mode == "grandchild" {
		for {
			time.Sleep(time.Hour)
		}
	}
	if (mode != "reconcile" && mode != "success" && mode != "adopt-once" && mode != "excess-output") || len(os.Args) != 6 || os.Args[1] != "sync" || os.Args[2] != "reconcile" {
		os.Exit(2)
	}
	if mode == "excess-output" {
		fmt.Fprint(os.Stdout, e22t1OversizedReconcileResult())
		os.Exit(0)
	}
	if mode == "adopt-once" {
		marker := os.Getenv("AGENT_DISPATCH_E22_RECONCILE_RECORD")
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			if err := os.WriteFile(marker, []byte("adopted"), 0o600); err != nil {
				os.Exit(2)
			}
			fmt.Fprint(os.Stdout, `{"result":{"state":"membership_adopted"}}`)
			os.Exit(0)
		}
		fmt.Fprintf(os.Stdout, `{"result":{"state":"no_change","target_commit":"%s"}}`, strings.Repeat("a", 40))
		os.Exit(0)
	}
	if mode == "success" {
		fmt.Fprintf(os.Stdout, `{"result":{"state":"no_change","target_commit":"%s"}}`, strings.Repeat("a", 40))
		os.Exit(0)
	}
	if err := os.Setenv("AGENT_DISPATCH_E22_RECONCILE_HELPER", "grandchild"); err != nil {
		os.Exit(2)
	}
	child := exec.Command(os.Args[0], "e22-grandchild")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	record := struct {
		PID        int      `json:"pid"`
		Grandchild int      `json:"grandchild"`
		Args       []string `json:"args"`
		ConfigPath string   `json:"config_path"`
	}{os.Getpid(), child.Process.Pid, os.Args[1:], os.Getenv("AGENT_DISPATCH_CONFIG")}
	raw, err := json.Marshal(record)
	if err != nil || os.WriteFile(os.Getenv("AGENT_DISPATCH_E22_RECONCILE_RECORD"), raw, 0o600) != nil {
		_ = child.Process.Kill()
		os.Exit(2)
	}
	_ = child.Wait()
	os.Exit(0)
}

func e22t1OversizedReconcileResult() string {
	return fmt.Sprintf(`{"result":{"state":"no_change","target_commit":"%s"}}`, strings.Repeat("a", 40)) + strings.Repeat(" ", 2*1024*1024)
}

func e22t1Service(t *testing.T) (*peerService, func()) {
	t.Helper()
	isolateSyncServiceForTest(t)
	cfg, err := config.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sync.Enabled = true
	cfg.Sync.Bounds.Queue = 1
	cfg.Instance.StateDir = t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_DISPATCH_CONFIG", configPath)
	store, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.TempDir()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	revision, _ := config.SyncRevision(cfg)
	if _, err := store.EnsureSyncControl(context.Background(), cfg.Sync.GroupID, revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		store.Close()
		t.Fatal(err)
	}
	head := strings.Repeat("1", 40)
	service := &peerService{
		cfg: cfg, configPath: configPath, store: store,
		slots: make(chan struct{}, 2), rate: map[string]peerRate{},
		members: func(context.Context) (string, [2]syncrecords.ActiveMember, error) {
			return head, [2]syncrecords.ActiveMember{
				{InstanceID: "node-b", Endpoint: cfg.Sync.Nodes[1].Endpoint},
				{InstanceID: "workstation-main", Endpoint: cfg.Sync.Nodes[0].Endpoint},
			}, nil
		},
	}
	t.Setenv("SYNC_NODE_B_TO_A", "test-inbound-credential")
	return service, func() { _ = store.Close() }
}

func isolateSyncServiceForTest(t *testing.T) {
	t.Helper()
	oldPlatform, oldLaunchDir, oldSystemdDir := syncServicePlatform, launchAgentsDir, systemdUserDir
	oldLaunchctl, oldSystemctl := launchctlRun, systemctlRun
	root := t.TempDir()
	syncServicePlatform = func() string { return "darwin" }
	launchAgentsDir = func() string { return filepath.Join(root, "LaunchAgents") }
	systemdUserDir = func() string { return filepath.Join(root, "systemd") }
	launchctlRun = func(...string) (string, error) { return "Could not find service", os.ErrNotExist }
	systemctlRun = func(...string) (string, error) { return "inactive\n", os.ErrNotExist }
	t.Cleanup(func() {
		syncServicePlatform, launchAgentsDir, systemdUserDir = oldPlatform, oldLaunchDir, oldSystemdDir
		launchctlRun, systemctlRun = oldLaunchctl, oldSystemctl
	})
}

func e22t1Nudge() syncrecords.Nudge {
	return syncrecords.Nudge{
		SchemaVersion: syncrecords.NudgeSchema, GroupID: "wiki-pair",
		PublicationID: "publication-one", Sender: "node-b", Receiver: "workstation-main",
		MembershipRevision: strings.Repeat("1", 40), ContentRef: "refs/heads/wiki-sync",
		TargetCommit: strings.Repeat("2", 40),
	}
}

func e22t1Request(t *testing.T, svc *peerService, path string, body []byte, token string, alter func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	if alter != nil {
		alter(r)
	}
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, r)
	return w
}

func TestE22T1NudgeAdmissionIsDurableIdempotentAndBounded(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	svc.wake = make(chan struct{}, 1)
	nudge := e22t1Nudge()
	raw, err := syncrecords.CanonicalNudge(nudge)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		w := e22t1Request(t, svc, "/v1/sync/nudges", raw, "test-inbound-credential", nil)
		if w.Code != http.StatusAccepted || w.Body.Len() != 0 {
			t.Fatalf("admission %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	select {
	case <-svc.wake:
	default:
		t.Fatal("committed nudge did not wake the inbox worker")
	}
	// JSON formatting does not change the logical replay key.
	reformatted, err := json.MarshalIndent(nudge, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if w := e22t1Request(t, svc, "/v1/sync/nudges", reformatted, "test-inbound-credential", nil); w.Code != http.StatusAccepted {
		t.Fatalf("reformatted replay = %d", w.Code)
	}
	rows, err := svc.store.LoadPendingPeerNudges(context.Background(), "wiki-pair", 10)
	if err != nil || len(rows) != 1 || rows[0].PublicationID != nudge.PublicationID {
		t.Fatalf("pending inbox = %v, %v", rows, err)
	}
	changed := nudge
	changed.TargetCommit = strings.Repeat("3", 40)
	changedRaw, _ := syncrecords.CanonicalNudge(changed)
	if w := e22t1Request(t, svc, "/v1/sync/nudges", changedRaw, "test-inbound-credential", nil); w.Code != http.StatusConflict {
		t.Fatalf("conflicting replay = %d", w.Code)
	}
	changed.PublicationID = "publication-two"
	changedRaw, _ = syncrecords.CanonicalNudge(changed)
	if w := e22t1Request(t, svc, "/v1/sync/nudges", changedRaw, "test-inbound-credential", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("full inbox = %d", w.Code)
	}
}

func TestE22T1NudgeRejectsWrongAuthorityAndParserAmbiguity(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	base := e22t1Nudge()
	cases := []struct {
		name   string
		mutate func(*syncrecords.Nudge)
		token  string
	}{
		{"wrong group", func(n *syncrecords.Nudge) { n.GroupID = "other" }, "test-inbound-credential"},
		{"wrong receiver", func(n *syncrecords.Nudge) { n.Receiver = "other" }, "test-inbound-credential"},
		{"wrong sender", func(n *syncrecords.Nudge) { n.Sender = "other" }, "test-inbound-credential"},
		{"stale membership", func(n *syncrecords.Nudge) { n.MembershipRevision = strings.Repeat("3", 40) }, "test-inbound-credential"},
		{"wrong credential", func(n *syncrecords.Nudge) {}, "wrong"},
		{"outbound credential cannot authorize inbound", func(n *syncrecords.Nudge) {}, "test-outbound-credential"},
	}
	t.Setenv("SYNC_NODE_A_TO_B", "test-outbound-credential")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := base
			tc.mutate(&n)
			raw, _ := syncrecords.CanonicalNudge(n)
			if w := e22t1Request(t, svc, "/v1/sync/nudges", raw, tc.token, nil); w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d", w.Code)
			}
		})
	}
	raw, _ := syncrecords.CanonicalNudge(base)
	for _, tc := range []struct {
		body []byte
		want int
	}{
		{append([]byte(`{"schema_version":"agent-dispatch.sync-nudge/v1",`), raw[1:]...), http.StatusBadRequest},
		{append(append([]byte{}, raw...), []byte(` {}`)...), http.StatusBadRequest},
		{append(raw[:len(raw)-1], []byte(`,"path":"/tmp/note.md"}`)...), http.StatusBadRequest},
		{bytes.Repeat([]byte("x"), (256<<10)+1), http.StatusRequestEntityTooLarge},
	} {
		if w := e22t1Request(t, svc, "/v1/sync/nudges", tc.body, "test-inbound-credential", nil); w.Code != tc.want {
			t.Fatalf("malformed payload status = %d, want %d", w.Code, tc.want)
		}
	}
	if w := e22t1Request(t, svc, "/v1/sync/nudges", raw, "test-inbound-credential", func(r *http.Request) {
		r.Header.Add("Authorization", "Bearer other")
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous authorization status = %d", w.Code)
	}
	if w := e22t1Request(t, svc, "/v1/sync/nudges", raw, "test-inbound-credential", func(r *http.Request) {
		r.TransferEncoding = []string{"chunked"}
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("transfer ambiguity status = %d", w.Code)
	}
	for _, tc := range []struct {
		name, path string
		alter      func(*http.Request)
		want       int
	}{
		{"method", "/v1/sync/nudges", func(r *http.Request) { r.Method = http.MethodGet }, http.StatusNotFound},
		{"query", "/v1/sync/nudges?path=note", nil, http.StatusNotFound},
		{"content type", "/v1/sync/nudges", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, http.StatusBadRequest},
		{"content encoding", "/v1/sync/nudges", func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }, http.StatusBadRequest},
		{"trailer", "/v1/sync/nudges", func(r *http.Request) { r.Trailer = http.Header{"X-Extra": {"value"}} }, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := e22t1Request(t, svc, tc.path, raw, "test-inbound-credential", tc.alter); w.Code != tc.want {
				t.Fatalf("request shape status = %d, want %d", w.Code, tc.want)
			}
		})
	}
	if w := e22t1Request(t, svc, "/v1/sync/status", bytes.Repeat([]byte("x"), (16<<10)+1), "test-inbound-credential", nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status status = %d", w.Code)
	}
	svc.slots <- struct{}{}
	svc.slots <- struct{}{}
	if w := e22t1Request(t, svc, "/v1/sync/nudges", raw, "test-inbound-credential", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("full concurrency bound status = %d", w.Code)
	}
	<-svc.slots
	<-svc.slots
	rows, err := svc.store.LoadPendingPeerNudges(context.Background(), "wiki-pair", 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected traffic affected inbox: %v, %v", rows, err)
	}
}

func TestE22T1NudgeRefusesHeldOrStaleControl(t *testing.T) {
	for _, tc := range []struct {
		name, field, value string
	}{
		{"blocked", "state", "blocked"},
		{"emergency membership", "membership_mode", "blocked_emergency"},
		{"stale configuration", "config_revision", "sha256:" + strings.Repeat("0", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, closeStore := e22t1Service(t)
			defer closeStore()
			update := "UPDATE sync_controls SET " + tc.field + "=? WHERE group_id=?"
			if tc.name == "blocked" {
				update = "UPDATE sync_controls SET state=?,reason='trust_failure' WHERE group_id=?"
			}
			if _, err := svc.store.Exec(update, tc.value, svc.cfg.Sync.GroupID); err != nil {
				t.Fatal(err)
			}
			raw, _ := syncrecords.CanonicalNudge(e22t1Nudge())
			if w := e22t1Request(t, svc, "/v1/sync/nudges", raw, "test-inbound-credential", nil); w.Code != http.StatusServiceUnavailable {
				t.Fatalf("held control admission = %d", w.Code)
			}
			rows, err := svc.store.LoadPendingPeerNudges(context.Background(), svc.cfg.Sync.GroupID, 1)
			if err != nil || len(rows) != 0 {
				t.Fatalf("held control changed inbox: %v %v", rows, err)
			}
		})
	}
}

func TestE22T1NudgeRefusesDisabledLiveConfiguration(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	current := *svc.cfg
	syncConfig := *svc.cfg.Sync
	syncConfig.Enabled = false
	current.Sync = &syncConfig
	rawConfig, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.configPath, rawConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := syncrecords.CanonicalNudge(e22t1Nudge())
	if w := e22t1Request(t, svc, "/v1/sync/nudges", raw, "test-inbound-credential", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled service admission = %d", w.Code)
	}
}

func TestE22T1StaleControlWarnsOnAdmissionAndDelivery(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	var warnings bytes.Buffer
	svc.stderr = &warnings
	if _, err := svc.store.Exec(`UPDATE sync_controls SET config_revision=? WHERE group_id=?`, "sha256:"+strings.Repeat("0", 64), svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	incoming := e22t1Nudge()
	raw, _ := syncrecords.CanonicalNudge(incoming)
	if w := e22t1Request(t, svc, "/v1/sync/nudges", raw, "test-inbound-credential", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale control admission = %d", w.Code)
	}
	outgoing := incoming
	outgoing.Sender, outgoing.Receiver = outgoing.Receiver, outgoing.Sender
	raw, _ = syncrecords.CanonicalNudge(outgoing)
	if _, _, err := svc.store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{
		JobID: "delivery-stale-control", GroupID: outgoing.GroupID, Kind: "delivery", LogicalKey: outgoing.PublicationID,
		InitialState: "pending", PayloadJSON: string(raw), QueueLimit: 1, Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: e22t1RoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("stale control must prevent delivery")
		return nil, errors.New("unexpected dial")
	})}
	if err := svc.deliverPending(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warnings.String(), "sync control configuration binding is stale") {
		t.Fatalf("missing stale control warning: %q", warnings.String())
	}
	if !strings.Contains(warnings.String(), "sync control prevents delivery") {
		t.Fatalf("missing delivery control warning: %q", warnings.String())
	}
}

func TestE22T1ServiceRejectsAccessibleSigningRefs(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	if err := verifyPeerSignerIsolation(svc.cfg.Sync); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYNC_PUBLISHER_SIGNING_KEY", "must-never-enter-service")
	if err := verifyPeerSignerIsolation(svc.cfg.Sync); err == nil {
		t.Fatal("present signing environment allowed")
	}
	_ = os.Unsetenv("SYNC_PUBLISHER_SIGNING_KEY")
	svc.cfg.Sync.PublisherSigningKeyRef = "file:/tmp/key"
	if err := verifyPeerSignerIsolation(svc.cfg.Sync); err == nil {
		t.Fatal("same-user signing file allowed")
	}
}

func TestE22T1ServeStartupUsesRegisteredTrustError(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	t.Setenv("SYNC_PUBLISHER_SIGNING_KEY", "fixture-signing-value")
	var stdout, stderr bytes.Buffer
	code := runSyncServe([]string{"--group", svc.cfg.Sync.GroupID}, &stdout, &stderr)
	if code != 30 || !strings.Contains(stderr.String(), `"code":"sync_trust_failed"`) || !strings.Contains(stderr.String(), `"category":"security"`) || stdout.Len() != 0 {
		t.Fatalf("signer startup refusal: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestE22T1AuthenticatedRateLimitKeepsInboxBounded(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	raw, _ := syncrecords.CanonicalNudge(e22t1Nudge())
	for i := 0; i < 120; i++ {
		if w := e22t1Request(t, svc, "/v1/sync/nudges", raw, "test-inbound-credential", nil); w.Code != http.StatusAccepted {
			t.Fatalf("request %d = %d", i, w.Code)
		}
	}
	if w := e22t1Request(t, svc, "/v1/sync/nudges", raw, "test-inbound-credential", nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("over limit = %d", w.Code)
	}
	rows, err := svc.store.LoadPendingPeerNudges(context.Background(), "wiki-pair", 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rate limit changed obligations: %v, %v", rows, err)
	}
}

func TestE22T1UnauthenticatedAttemptsUseCheapSenderBudget(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	raw, _ := syncrecords.CanonicalNudge(e22t1Nudge())
	for i := 0; i < 120; i++ {
		if w := e22t1Request(t, svc, "/v1/sync/nudges", raw, "incorrect-credential", nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	if w := e22t1Request(t, svc, "/v1/sync/nudges", raw, "incorrect-credential", nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("unbounded unauthenticated attempt: %d", w.Code)
	}
	rows, err := svc.store.LoadPendingPeerNudges(context.Background(), svc.cfg.Sync.GroupID, 1)
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed authentication admitted work: %v %v", rows, err)
	}
}

type e22t1RoundTrip func(*http.Request) (*http.Response, error)

func (f e22t1RoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestE22T1DeliveryUsesConfiguredPeerAndSettlesOnlyOn202(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	svc.cfg.Sync.Nodes[1].Endpoint = "https://node-b.example.ts.net:8448"
	t.Setenv("SYNC_NODE_A_TO_B", "test-outbound-credential")
	nudge := e22t1Nudge()
	nudge.Sender, nudge.Receiver = nudge.Receiver, nudge.Sender
	raw, _ := syncrecords.CanonicalNudge(nudge)
	_, _, err := svc.store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{
		JobID: "delivery-one", GroupID: "wiki-pair", Kind: "delivery", LogicalKey: nudge.PublicationID,
		InitialState: "pending", PayloadJSON: string(raw), QueueLimit: 1, Now: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	client := &http.Client{Transport: e22t1RoundTrip(func(r *http.Request) (*http.Response, error) {
		called++
		if r.URL.String() != "https://node-b.example.ts.net:8448/v1/sync/nudges" || r.Header.Get("Authorization") != "Bearer test-outbound-credential" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("outbound request drift: %s %v", r.URL, r.Header)
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	if err := svc.deliverPending(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	job, err := svc.store.LoadLatestSyncJob(context.Background(), "wiki-pair", "delivery")
	if err != nil || called != 1 || job.State != "accepted" || job.ResolvedAt == "" {
		t.Fatalf("delivery not settled after 202: call=%d job=%+v err=%v", called, job, err)
	}
}

func TestE22T1LostDeliveryResponseRetainsAndReplaysIdentity(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	t.Setenv("SYNC_NODE_A_TO_B", "test-outbound-credential")
	nudge := e22t1Nudge()
	nudge.Sender, nudge.Receiver = nudge.Receiver, nudge.Sender
	raw, _ := syncrecords.CanonicalNudge(nudge)
	_, _, err := svc.store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{
		JobID: "delivery-lost", GroupID: "wiki-pair", Kind: "delivery", LogicalKey: nudge.PublicationID,
		InitialState: "pending", PayloadJSON: string(raw), QueueLimit: 1, Now: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	lost := &http.Client{Transport: e22t1RoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("response lost after possible commit")
	})}
	if err := svc.deliverPending(context.Background(), lost); err != nil {
		t.Fatal(err)
	}
	job, err := svc.store.LoadLatestSyncJob(context.Background(), "wiki-pair", "delivery")
	if err != nil || job.State != "unknown" || job.ResolvedAt != "" {
		t.Fatalf("lost response falsely settled: %+v %v", job, err)
	}
	if err := svc.deliverPending(context.Background(), lost); err != nil {
		t.Fatal(err)
	}
	job, err = svc.store.LoadLatestSyncJob(context.Background(), "wiki-pair", "delivery")
	if err != nil || job.State != "retryable" || job.LogicalKey != nudge.PublicationID || job.PayloadJSON == "" {
		t.Fatalf("idempotent replay not prepared: %+v %v", job, err)
	}
}

func TestE22T1DeliveryResponseClassesRetainCorrectObligations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		want     string
		resolved bool
	}{
		{"rate limit", 429, "retryable", false},
		{"unavailable", 503, "retryable", false},
		{"bad gateway", 502, "retryable", false},
		{"gateway timeout", 504, "retryable", false},
		{"other server error", 500, "retryable", false},
		{"identity conflict", 409, "refused", true},
		{"unauthorized", 401, "refused", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, closeStore := e22t1Service(t)
			defer closeStore()
			t.Setenv("SYNC_NODE_A_TO_B", "test-outbound-credential")
			nudge := e22t1Nudge()
			nudge.Sender, nudge.Receiver = nudge.Receiver, nudge.Sender
			raw, _ := syncrecords.CanonicalNudge(nudge)
			_, _, err := svc.store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{
				JobID: "delivery-status", GroupID: nudge.GroupID, Kind: "delivery", LogicalKey: nudge.PublicationID,
				InitialState: "pending", PayloadJSON: string(raw), QueueLimit: 1, Now: time.Now().UTC().Format(time.RFC3339Nano),
			})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			client := &http.Client{Transport: e22t1RoundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			})}
			if err := svc.deliverPending(context.Background(), client); err != nil {
				t.Fatal(err)
			}
			job, err := svc.store.LoadLatestSyncJob(context.Background(), nudge.GroupID, "delivery")
			if err != nil || calls != 1 || job.State != tc.want || (job.ResolvedAt != "") != tc.resolved {
				t.Fatalf("classification: calls=%d job=%+v err=%v", calls, job, err)
			}
			journal, err := svc.store.LoadSyncJournals(context.Background(), job.JobID)
			if err != nil || len(journal) != 1 || journal[0].Outcome != tc.want {
				t.Fatalf("journal: %+v %v", journal, err)
			}
		})
	}
}

func TestE22T1DeliveryRejectsWrongPayloadWithoutDial(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	nudge := e22t1Nudge() // incoming direction cannot be sent by this node
	raw, _ := syncrecords.CanonicalNudge(nudge)
	_, _, err := svc.store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{
		JobID: "delivery-refused", GroupID: nudge.GroupID, Kind: "delivery", LogicalKey: nudge.PublicationID,
		InitialState: "pending", PayloadJSON: string(raw), QueueLimit: 1, Now: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: e22t1RoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid delivery must not dial")
		return nil, errors.New("unexpected dial")
	})}
	if err := svc.deliverPending(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	job, err := svc.store.LoadLatestSyncJob(context.Background(), nudge.GroupID, "delivery")
	if err != nil || job.State != "refused" || job.ResolvedAt == "" {
		t.Fatalf("invalid payload resolution: %+v %v", job, err)
	}
}

func TestE22T1DeliveryRefusesEndpointDriftWithoutDial(t *testing.T) {
	for _, tc := range []struct{ configured, signed string }{
		{"https://node-b.example.ts.net", "https://node-b.example.ts.net:8448"},
		{"https://node-b.example.ts.net:8448", "https://node-b.example.ts.net"},
		{"https://node-b.example.ts.net:8448/", "https://node-b.example.ts.net:8448"},
		{"https://user@node-b.example.ts.net:8448", "https://user@node-b.example.ts.net:8448"},
	} {
		t.Run(tc.configured+"/"+tc.signed, func(t *testing.T) {
			svc, closeStore := e22t1Service(t)
			defer closeStore()
			t.Setenv("SYNC_NODE_A_TO_B", "test-outbound-credential")
			svc.cfg.Sync.Nodes[1].Endpoint = tc.configured
			svc.members = func(context.Context) (string, [2]syncrecords.ActiveMember, error) {
				return strings.Repeat("1", 40), [2]syncrecords.ActiveMember{{InstanceID: "node-b", Endpoint: tc.signed}, {InstanceID: "workstation-main", Endpoint: svc.cfg.Sync.Nodes[0].Endpoint}}, nil
			}
			nudge := e22t1Nudge()
			nudge.Sender, nudge.Receiver = nudge.Receiver, nudge.Sender
			raw, err := syncrecords.CanonicalNudge(nudge)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: e22t1RoundTrip(func(*http.Request) (*http.Response, error) {
				t.Fatal("endpoint drift must not dial")
				return nil, errors.New("unexpected dial")
			})}
			state, _, _ := svc.sendNudge(context.Background(), client, sqlite.SyncJobRow{PayloadJSON: string(raw)})
			if state != "refused" {
				t.Fatalf("endpoint drift state = %s", state)
			}
		})
	}
}

func TestE22T1DeliveryBackoffIsBounded(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		attempts int
		wait     time.Duration
	}{
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{6, 5 * time.Minute},
	} {
		job := sqlite.SyncJobRow{Attempts: tc.attempts, UpdatedAt: now.Format(time.RFC3339Nano)}
		if deliveryDue(job, now.Add(tc.wait-time.Nanosecond)) || !deliveryDue(job, now.Add(tc.wait)) {
			t.Fatalf("attempt %d did not honor wait %s", tc.attempts, tc.wait)
		}
	}
}

func TestE22T1PeerClientNeverFollowsRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://wrong.example.invalid/")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client := peerHTTPClient()
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("redirect status = %d", response.StatusCode)
	}
}

func TestE22T1PeerClientRejectsUntrustedAndWrongHostCertificates(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := peerHTTPClient()
	defer client.CloseIdleConnections()
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatal("peer transport must use direct, verified TLS 1.2 or newer")
	}
	if _, err := client.Get(server.URL); err == nil {
		t.Fatal("untrusted peer certificate was accepted")
	} else {
		var authorityErr x509.UnknownAuthorityError
		if !errors.As(err, &authorityErr) {
			t.Fatalf("untrusted certificate error = %v", err)
		}
	}
	certificate, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	transport.TLSClientConfig.RootCAs = roots
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	if _, err := client.Get("https://wrong-host.ts.net/"); err == nil {
		t.Fatal("wrong-host peer certificate was accepted")
	} else {
		var hostErr x509.HostnameError
		if !errors.As(err, &hostErr) {
			t.Fatalf("wrong-host certificate error = %v", err)
		}
	}
}

func TestE22T1StatusReportsUnknownUntilPairVerification(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "wiki-sync"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.invalid"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "note.md"}, {"commit", "-q", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	resource := svc.cfg.Resources[svc.cfg.Sync.Resource]
	resource.Root = repo
	svc.cfg.Resources[svc.cfg.Sync.Resource] = resource
	rawConfig, _ := json.Marshal(svc.cfg)
	if err := os.WriteFile(svc.configPath, rawConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	revision, _ := config.SyncRevision(svc.cfg)
	if _, err := svc.store.Exec(`UPDATE sync_controls SET config_revision=? WHERE group_id=?`, revision, svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	request := syncrecords.StatusRequest{
		SchemaVersion: syncrecords.StatusRequestSchema, GroupID: "wiki-pair", Sender: "node-b",
		Receiver: "workstation-main", MembershipRevision: strings.Repeat("1", 40),
		ContentRef: "refs/heads/wiki-sync", TargetCommit: strings.Repeat("2", 40),
		ScopeDigest:    config.SyncScopeDigest(svc.cfg, svc.cfg.Sync.Resource),
		ContractDigest: config.SyncContractDigest(), Nonce: "0123456789abcdef",
	}
	raw, _ := json.Marshal(request)
	t.Setenv("SYNC_NODE_A_TO_B", "test-outbound-credential")
	if w := e22t1Request(t, svc, "/v1/sync/status", raw, "test-outbound-credential", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("outbound credential authorized inbound status: %d", w.Code)
	}
	w := e22t1Request(t, svc, "/v1/sync/status", raw, "test-inbound-credential", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var result syncrecords.StatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "unknown" || !result.Uncertain || !result.GovernedDirty || !result.PendingWork || result.MembershipCurrent || result.Nonce != request.Nonce || result.TargetCommit == request.TargetCommit || result.EvidenceAgeSeconds != 0 {
		t.Fatalf("unsafe status evidence: %+v", result)
	}
	wrongScope := request
	wrongScope.ScopeDigest = "sha256:" + strings.Repeat("0", 64)
	wrongRaw, _ := json.Marshal(wrongScope)
	if w := e22t1Request(t, svc, "/v1/sync/status", wrongRaw, "test-inbound-credential", nil); w.Code != http.StatusConflict {
		t.Fatalf("wrong scope = %d", w.Code)
	}
	wrongRef := request
	wrongRef.ContentRef = "refs/heads/other"
	wrongRaw, _ = json.Marshal(wrongRef)
	if w := e22t1Request(t, svc, "/v1/sync/status", wrongRaw, "test-inbound-credential", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong ref = %d", w.Code)
	}
	wrongNonce := request
	wrongNonce.Nonce = "short"
	wrongRaw, _ = json.Marshal(wrongNonce)
	if w := e22t1Request(t, svc, "/v1/sync/status", wrongRaw, "test-inbound-credential", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("short nonce = %d", w.Code)
	}
}

func TestE22T1ShutdownPreservesCommittedInbox(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	svc.wake = make(chan struct{}, 1)
	reconcileStarted := make(chan struct{}, 1)
	svc.reconcile = func(ctx context.Context) (bool, string) {
		reconcileStarted <- struct{}{}
		<-ctx.Done()
		return false, ""
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- servePeer(ctx, listener, svc) }()
	defer cancel()
	raw, _ := syncrecords.CanonicalNudge(e22t1Nudge())
	request, err := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+"/v1/sync/nudges", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-inbound-credential")
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("before shutdown = %d", response.StatusCode)
	}
	select {
	case <-reconcileStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("committed nudge did not wake reconciliation")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown exceeded test bound")
	}
	rows, err := svc.store.LoadPendingPeerNudges(context.Background(), "wiki-pair", 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("committed inbox lost during shutdown: %v %v", rows, err)
	}
	second, _ := http.NewRequest(http.MethodPost, request.URL.String(), bytes.NewReader(raw))
	second.Header = request.Header.Clone()
	if response, err := client.Do(second); err == nil {
		_ = response.Body.Close()
		t.Fatalf("listener still admitted after shutdown: %d", response.StatusCode)
	}
}

func TestE22T1ReconcileCancellationKillsChildProcessGroup(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	readyFile := filepath.Join(t.TempDir(), "reconcile-child.json")
	t.Setenv("AGENT_DISPATCH_E22_RECONCILE_HELPER", "reconcile")
	t.Setenv("AGENT_DISPATCH_E22_RECONCILE_RECORD", readyFile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct {
		settled bool
		target  string
	}, 1)
	go func() {
		settled, target := svc.runReconcile(ctx)
		done <- struct {
			settled bool
			target  string
		}{settled, target}
	}()
	var record struct {
		PID        int      `json:"pid"`
		Grandchild int      `json:"grandchild"`
		Args       []string `json:"args"`
		ConfigPath string   `json:"config_path"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(readyFile)
		if err == nil && json.Unmarshal(raw, &record) == nil && record.Grandchild > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if record.PID == 0 || record.Grandchild == 0 {
		t.Fatal("reconcile helper did not start its grandchild")
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = syscall.Kill(record.PID, syscall.SIGKILL)
			_ = syscall.Kill(record.Grandchild, syscall.SIGKILL)
		}
	}()
	wantArgs := []string{"sync", "reconcile", "--state-dir=" + stateDirOf(svc.cfg), "--group", svc.cfg.Sync.GroupID}
	if !slices.Equal(record.Args, wantArgs) || record.ConfigPath != svc.configPath {
		t.Fatalf("reconcile child binding: args=%q config=%q", record.Args, record.ConfigPath)
	}
	if group, err := syscall.Getpgid(record.PID); err != nil || group != record.PID {
		t.Fatalf("reconcile child group=%d err=%v pid=%d", group, err, record.PID)
	}
	cancel()
	select {
	case result := <-done:
		if result.settled || result.target != "" {
			t.Fatalf("cancelled reconcile falsely settled: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile child did not return after cancellation")
	}
	for _, pid := range []int{record.PID, record.Grandchild} {
		until := time.Now().Add(5 * time.Second)
		for time.Now().Before(until) {
			if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Fatalf("cancelled reconcile process %d remains: %v", pid, err)
		}
	}
	cleanup = false
}

func TestE22T1ReconcileChildSuccessReturnsTarget(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	t.Setenv("AGENT_DISPATCH_E22_RECONCILE_HELPER", "success")
	settled, target := svc.runReconcile(context.Background())
	if !settled || target != strings.Repeat("a", 40) {
		t.Fatalf("child result: settled=%v target=%q", settled, target)
	}
}

func TestE22T1ReconcileChildOutputLimitDefersSettlement(t *testing.T) {
	state, candidate := parseReconcileResult([]byte(e22t1OversizedReconcileResult()))
	if settled, target := settledReconcileResult(state, candidate); !settled || target != strings.Repeat("a", 40) {
		t.Fatalf("oversized fixture must settle without an output limit: settled=%v target=%q", settled, target)
	}
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	t.Setenv("AGENT_DISPATCH_E22_RECONCILE_HELPER", "excess-output")
	settled, target := svc.runReconcile(context.Background())
	if settled || target != "" {
		t.Fatalf("oversized child result: settled=%v target=%q", settled, target)
	}
}

func TestE22T1OlderNudgeCoverageWalksConfiguredLocalHistory(t *testing.T) {
	repo := t.TempDir()
	commands := [][]string{
		{"init", "-q", "-b", "wiki-sync"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.invalid"},
	}
	for _, args := range commands {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	commit := func(body string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "note.md"}, {"commit", "-q", "-m", body}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repo
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	first := commit("first")
	second := commit("second")
	client, err := gitlocal.New(repo, gitlocal.Limits{Timeout: 5 * time.Second, MaxOutput: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, target string
		bound        int
		want         bool
	}{
		{"current", second, 2, true},
		{"older", first, 2, true},
		{"over bound", first, 1, false},
		{"untrusted object", strings.Repeat("f", 40), 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := localHistoryCoverage{client: client, current: second, remaining: tc.bound, covered: make(map[string]bool)}
			covered, err := history.contains(context.Background(), tc.target)
			if err != nil || covered != tc.want {
				t.Fatalf("covered=%v want=%v err=%v", covered, tc.want, err)
			}
		})
	}
	history := localHistoryCoverage{client: client, current: second, remaining: 2, covered: make(map[string]bool)}
	for _, target := range []string{second, first, strings.Repeat("f", 40), second, first} {
		covered, err := history.contains(context.Background(), target)
		if err != nil || covered != (target != strings.Repeat("f", 40)) {
			t.Fatalf("shared history target %s: covered=%v err=%v", target, covered, err)
		}
	}
}

func TestE22T1StaleNudgeDoesNotBlockLaterInboxRow(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "wiki-sync"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	commit := func(body string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "note.md"}, {"commit", "-q", "-m", body}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repo
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	old := commit("old")
	current := commit("current")
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	svc.cfg.Sync.Bounds.Queue = 3
	svc.cfg.Sync.Bounds.HistoryCommits = 1
	resource := svc.cfg.Resources[svc.cfg.Sync.Resource]
	resource.Root = repo
	svc.cfg.Resources[svc.cfg.Sync.Resource] = resource
	rawConfig, err := json.Marshal(svc.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.configPath, rawConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	revision, _ := config.SyncRevision(svc.cfg)
	if _, err := svc.store.Exec(`UPDATE sync_controls SET config_revision=? WHERE group_id=?`, revision, svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	svc.reconcile = func(context.Context) (bool, string) { return true, old }
	if _, err := svc.store.AdmitPeerNudge(context.Background(), sqlite.PeerNudgeInput{
		GroupID: svc.cfg.Sync.GroupID, PublicationID: "invalid-persisted", Fingerprint: "sha256:invalid-persisted",
		PayloadJSON: `{}`, ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano), QueueLimit: 3,
	}); err != nil {
		t.Fatal(err)
	}
	for i, target := range []string{old, current} {
		nudge := e22t1Nudge()
		nudge.PublicationID = fmt.Sprintf("publication-%d", i)
		nudge.TargetCommit = target
		raw, err := syncrecords.CanonicalNudge(nudge)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
		if _, err := svc.store.AdmitPeerNudge(context.Background(), sqlite.PeerNudgeInput{
			GroupID: nudge.GroupID, PublicationID: nudge.PublicationID, Fingerprint: fingerprint,
			PayloadJSON: string(raw), ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano), QueueLimit: 3,
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := svc.store.LoadPendingPeerNudges(context.Background(), svc.cfg.Sync.GroupID, 3)
	if err != nil || len(rows) != 3 {
		t.Fatalf("pending: %v %v", rows, err)
	}
	e22t1RunScheduledInbox(t, svc)
	deferred, err := svc.store.LoadPeerNudgeBacklog(context.Background(), svc.cfg.Sync.GroupID)
	if err != nil || deferred.Pending != 3 || deferred.Failed != 3 || deferred.OldestReason != "local_ref_unavailable" {
		t.Fatalf("local ref mismatch did not retain inbox: %+v %v", deferred, err)
	}
	if failed, found, err := svc.store.LoadSyncRecoverySchedule(context.Background(), svc.cfg.Sync.GroupID); err != nil || !found || failed.LastReason != "local_ref_unavailable" || failed.ConsecutiveFailures != 1 {
		t.Fatalf("local ref mismatch schedule: %+v found=%v err=%v", failed, found, err)
	}
	svc.reconcile = func(context.Context) (bool, string) { return true, current }
	e22t1RunScheduledInbox(t, svc)
	backlog, err := svc.store.LoadPeerNudgeBacklog(context.Background(), svc.cfg.Sync.GroupID)
	if err != nil || backlog.Pending != 0 {
		t.Fatalf("backlog: %+v %v", backlog, err)
	}
	schedule, found, err := svc.store.LoadSyncRecoverySchedule(context.Background(), svc.cfg.Sync.GroupID)
	if err != nil || !found || schedule.LastSuccessAt == "" || schedule.LastReason != "none" {
		t.Fatalf("scheduled inbox settlement was not recorded: %+v found=%v err=%v", schedule, found, err)
	}
	var invalidResolution string
	if err := svc.store.QueryRowContext(context.Background(), `SELECT resolution FROM sync_peer_nudges WHERE publication_id='invalid-persisted'`).Scan(&invalidResolution); err != nil || invalidResolution != "invalid_payload" {
		t.Fatalf("invalid row resolution: %q %v", invalidResolution, err)
	}
	for i, expected := range []string{"superseded", "covered"} {
		var resolution string
		if err := svc.store.QueryRowContext(context.Background(), `SELECT resolution FROM sync_peer_nudges WHERE publication_id=?`, fmt.Sprintf("publication-%d", i)).Scan(&resolution); err != nil || resolution != expected {
			t.Fatalf("row %d resolution: %q %v", i, resolution, err)
		}
	}
	// A non-linear configured head prevents bounded causal coverage. The
	// scheduled worker must keep the nudge and record that distinct failure.
	tree := gitTestOutput(t, "git", repo, "rev-parse", current+"^{tree}")
	merge := gitTestOutput(t, "git", repo, "commit-tree", tree, "-p", old, "-p", current)
	gitTestRun(t, "git", repo, "update-ref", svc.cfg.Sync.ContentRef, merge, current)
	svc.reconcile = func(context.Context) (bool, string) { return true, merge }
	nudge := e22t1Nudge()
	nudge.PublicationID = "nonlinear-history"
	nudge.TargetCommit = old
	raw, err := syncrecords.CanonicalNudge(nudge)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.AdmitPeerNudge(context.Background(), sqlite.PeerNudgeInput{
		GroupID: nudge.GroupID, PublicationID: nudge.PublicationID,
		Fingerprint: fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), PayloadJSON: string(raw),
		ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano), QueueLimit: 3,
	}); err != nil {
		t.Fatal(err)
	}
	e22t1RunScheduledInbox(t, svc)
	schedule, found, err = svc.store.LoadSyncRecoverySchedule(context.Background(), svc.cfg.Sync.GroupID)
	if err != nil || !found || schedule.LastReason != "inbox_unsettled" || schedule.ConsecutiveFailures != 1 {
		t.Fatalf("non-linear history did not persist inbox failure: %+v found=%v err=%v", schedule, found, err)
	}
	backlog, err = svc.store.LoadPeerNudgeBacklog(context.Background(), svc.cfg.Sync.GroupID)
	if err != nil || backlog.Pending != 1 || backlog.Failed != 1 || backlog.OldestReason != "coverage_unavailable" {
		t.Fatalf("non-linear history lost the pending nudge: %+v err=%v", backlog, err)
	}
}

func TestE22T1InboxRetainsFailedReconcile(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	nudge := e22t1Nudge()
	raw, _ := syncrecords.CanonicalNudge(nudge)
	fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	if _, err := svc.store.AdmitPeerNudge(context.Background(), sqlite.PeerNudgeInput{
		GroupID: nudge.GroupID, PublicationID: nudge.PublicationID, Fingerprint: fingerprint,
		PayloadJSON: string(raw), ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano), QueueLimit: 1,
	}); err != nil {
		t.Fatal(err)
	}
	called := false
	svc.reconcile = func(context.Context) (bool, string) { called = true; return false, "" }
	e22t1RunScheduledInbox(t, svc)
	backlog, err := svc.store.LoadPeerNudgeBacklog(context.Background(), nudge.GroupID)
	if err != nil || !called || backlog.Pending != 1 || backlog.Failed != 1 || backlog.OldestReason != "reconcile_failed" {
		t.Fatalf("failed reconcile obligation: called=%v backlog=%+v err=%v", called, backlog, err)
	}
	if schedule, found, err := svc.store.LoadSyncRecoverySchedule(context.Background(), nudge.GroupID); err != nil || !found || schedule.LastReason != "reconcile_failed" || schedule.ConsecutiveFailures != 1 {
		t.Fatalf("failed reconcile schedule: %+v found=%v err=%v", schedule, found, err)
	}
}

func TestE22T1PausedInboxRetainsWorkWithoutFailure(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	nudge := e22t1Nudge()
	raw, _ := syncrecords.CanonicalNudge(nudge)
	if _, err := svc.store.AdmitPeerNudge(context.Background(), sqlite.PeerNudgeInput{
		GroupID: nudge.GroupID, PublicationID: nudge.PublicationID,
		Fingerprint: fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), PayloadJSON: string(raw),
		ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano), QueueLimit: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.Exec(`UPDATE sync_controls SET state='paused',reason='operator_pause' WHERE group_id=?`, nudge.GroupID); err != nil {
		t.Fatal(err)
	}
	svc.reconcile = func(context.Context) (bool, string) {
		t.Fatal("paused inbox must not reconcile")
		return false, ""
	}
	e22t1RunScheduledInbox(t, svc)
	backlog, err := svc.store.LoadPeerNudgeBacklog(context.Background(), nudge.GroupID)
	if err != nil || backlog.Pending != 1 || backlog.Failed != 0 {
		t.Fatalf("paused inbox: %+v %v", backlog, err)
	}
}

func TestE22T1ReconcileOutcomeRequiresSettledTarget(t *testing.T) {
	for _, tc := range []struct {
		state, target string
		settled       bool
	}{
		{"no_change", strings.Repeat("a", 40), true},
		{"applied", strings.Repeat("b", 40), true},
		{"recovered", strings.Repeat("c", 40), true},
		{"deferred", strings.Repeat("d", 40), false},
		{"applied", "", false},
	} {
		raw, _ := json.Marshal(map[string]any{"result": map[string]any{"state": tc.state, "target_commit": tc.target}})
		settled, target := settledReconcileResult(parseReconcileResult(raw))
		if settled != tc.settled || (settled && target != tc.target) {
			t.Fatalf("state=%s target=%s: settled=%v actual=%s", tc.state, tc.target, settled, target)
		}
	}
}

func e22t1RunScheduledInbox(t *testing.T, svc *peerService) {
	t.Helper()
	// Drive an existing failed reservation due; the production worker still
	// owns loading, dispatching, settlement, and durable completion.
	if _, err := svc.store.ExecContext(context.Background(), `UPDATE sync_recovery_schedule SET next_due_at=? WHERE group_id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	svc.recoverScheduled(context.Background(), true)
}

func TestE22T1CurrentMembersRejectsUnsignedAndNonAdvancingHistory(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	commit := func(body string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "note.md"}, {"commit", "-q", "-m", body}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repo
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	old, newer := commit("old"), commit("new")
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	resource := svc.cfg.Resources[svc.cfg.Sync.Resource]
	resource.Root = repo
	svc.cfg.Resources[svc.cfg.Sync.Resource] = resource
	cmd := exec.Command("git", "update-ref", svc.cfg.Sync.MembershipRef, old)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("membership ref: %v %s", err, out)
	}
	if _, _, err := svc.currentMembers(context.Background()); err == nil {
		t.Fatal("unsigned local membership history was accepted")
	}
	svc.memberHead = newer
	if _, _, err := svc.currentMembers(context.Background()); err == nil || !strings.Contains(err.Error(), "did not advance linearly") {
		t.Fatalf("non-advancing membership accepted: %v", err)
	}
}

func TestE22T1ConfigurationDriftIsVisibleToOperator(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	var warnings bytes.Buffer
	svc.stderr = &warnings
	changed := *svc.cfg
	syncChanged := *svc.cfg.Sync
	syncChanged.Bounds.Queue++
	changed.Sync = &syncChanged
	raw, err := json.Marshal(&changed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	e22t1RunScheduledInbox(t, svc)
	if err := svc.deliverPending(context.Background(), peerHTTPClient()); err == nil {
		t.Fatal("drift must defer delivery")
	}
	if got := strings.Count(warnings.String(), "configuration changed"); got != 1 {
		t.Fatalf("configuration warning count = %d: %q", got, warnings.String())
	}
}

func TestE22T1OwnerOnlySocketRejectsSquatAndRecoversStalePath(t *testing.T) {
	root, err := os.MkdirTemp(os.TempDir(), "adp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	listener, path, err := listenPeerSocket(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket permissions: %v %v", info, err)
	}
	if _, _, err := listenPeerSocket(root); err == nil {
		t.Fatal("active socket must refuse a second service")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, _, err := listenPeerSocket(root)
	if err != nil {
		t.Fatalf("stale socket recovery: %v", err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := listenPeerSocket(root); err == nil {
		t.Fatal("ordinary file must not be removed")
	}
}

func TestE22T1SyncStatusExposesPeerInboxBacklog(t *testing.T) {
	configPath := enabledSyncConfig(t)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openStateStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	status := func() map[string]any {
		t.Helper()
		code, envelope, stderr := syncResult(t, "status", "--group", cfg.Sync.GroupID, "--config", configPath, "--output", "json")
		if code != 0 {
			t.Fatalf("status: %d %s", code, stderr)
		}
		return envelope["result"].(map[string]any)
	}
	initial := status()
	if initial["peer_inbox_pending"] != float64(0) || initial["peer_inbox_failed"] != float64(0) || initial["peer_inbox_retained"] != float64(0) || initial["peer_inbox_retention_limit"] != float64(sqlite.PeerNudgeLedgerLimit) || initial["peer_inbox_oldest_received_at"] != "" || initial["peer_inbox_oldest_reason"] != "" {
		t.Fatalf("initial inbox status: %v", initial)
	}
	in := sqlite.PeerNudgeInput{
		GroupID: cfg.Sync.GroupID, PublicationID: "status-nudge", Fingerprint: "sha256:status-nudge",
		PayloadJSON: `{"publication_id":"status-nudge"}`, ReceivedAt: "2026-09-25T01:00:00Z", QueueLimit: 2,
	}
	if _, err := store.AdmitPeerNudge(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPeerNudgeFailure(context.Background(), in.GroupID, in.PublicationID, in.Fingerprint, "reconcile_failed"); err != nil {
		t.Fatal(err)
	}
	active := status()
	if active["peer_inbox_pending"] != float64(1) || active["peer_inbox_failed"] != float64(1) || active["peer_inbox_retained"] != float64(1) || active["peer_inbox_retention_limit"] != float64(sqlite.PeerNudgeLedgerLimit) || active["peer_inbox_oldest_received_at"] != in.ReceivedAt || active["peer_inbox_oldest_reason"] != "reconcile_failed" {
		t.Fatalf("retained inbox status: %v", active)
	}
}
