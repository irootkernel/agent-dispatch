package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func TestE22T3LocalObservationRequiresCleanCurrentPairEvidence(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "wiki-sync"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}} {
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
	client, err := membershipGitClient(svc.cfg, svc.cfg.Sync)
	if err != nil {
		t.Fatal(err)
	}
	head, err := client.ResolveRef(context.Background(), svc.cfg.Sync.ContentRef)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "update-ref", svc.cfg.Sync.MembershipRef, head)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("membership ref: %v %s", err, out)
	}
	revision, _ := config.SyncRevision(svc.cfg)
	if _, err := svc.store.Exec(`UPDATE sync_controls SET config_revision=? WHERE group_id=?`, revision, svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	request := syncrecords.StatusRequest{
		SchemaVersion: syncrecords.StatusRequestSchema, GroupID: svc.cfg.Sync.GroupID,
		Sender: "node-b", Receiver: svc.cfg.Sync.LocalInstanceID, MembershipRevision: head,
		ContentRef: svc.cfg.Sync.ContentRef, TargetCommit: head,
		ScopeDigest: config.SyncScopeDigest(svc.cfg, svc.cfg.Sync.Resource), ContractDigest: config.SyncContractDigest(), Nonce: "0123456789abcdef",
	}
	clean := observeSyncPeer(context.Background(), svc.cfg, svc.store, client, request)
	if clean.State != "applied" || clean.GovernedDirty || clean.PendingWork || !clean.MembershipCurrent || clean.Uncertain || clean.Validate() != nil {
		t.Fatalf("clean local observation: %+v", clean)
	}
	configBytes, err := json.Marshal(svc.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.configPath, configBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	svc.members = func(context.Context) (string, [2]syncrecords.ActiveMember, error) {
		return head, [2]syncrecords.ActiveMember{
			{InstanceID: request.Sender, StateIncarnationID: svc.cfg.Sync.Nodes[1].StateIncarnationID},
			{InstanceID: request.Receiver, StateIncarnationID: svc.cfg.Sync.Nodes[0].StateIncarnationID},
		}, nil
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	serveStatus := func() syncrecords.StatusResponse {
		t.Helper()
		w := e22t1Request(t, svc, "/v1/sync/status", requestBytes, "test-inbound-credential", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status response: %d %s", w.Code, w.Body.String())
		}
		response, err := syncrecords.DecodeStatusResponse(w.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	firstStatus := serveStatus()
	if firstStatus.State != "applied" {
		t.Fatalf("serve did not report clean state: %+v", firstStatus)
	}
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("serve edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondStatus := serveStatus()
	if secondStatus.State != "local_dirty" || !secondStatus.GovernedDirty || secondStatus.EvidenceGeneration <= firstStatus.EvidenceGeneration {
		t.Fatalf("serve reused prior observation: first=%+v second=%+v", firstStatus, secondStatus)
	}
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"checkout", "-q", "--detach", "HEAD"}, {"checkout", "-q", "wiki-sync"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		if args[2] == "--detach" {
			detached := observeSyncPeer(context.Background(), svc.cfg, svc.store, client, request)
			if detached.State != "unknown" || !detached.Uncertain {
				t.Fatalf("detached HEAD appeared current: %+v", detached)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty := observeSyncPeer(context.Background(), svc.cfg, svc.store, client, request)
	if dirty.State != "local_dirty" || !dirty.GovernedDirty {
		t.Fatalf("governed edit passed clean status: %+v", dirty)
	}
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(repo, ".AGENT-DISPATCH-SYNC")
	if err := os.Mkdir(alias, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alias, "pending.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	reserved := observeSyncPeer(context.Background(), svc.cfg, svc.store, client, request)
	if !reserved.GovernedDirty || !reserved.Uncertain || reserved.State != "unknown" {
		t.Fatalf("reserved path alias appeared clean: %+v", reserved)
	}
	if err := os.RemoveAll(alias); err != nil {
		t.Fatal(err)
	}
	protectedDir := filepath.Join(repo, "raw")
	if err := os.Mkdir(protectedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protectedDir, "note.md"), []byte("protected edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	protected := observeSyncPeer(context.Background(), svc.cfg, svc.store, client, request)
	if !protected.GovernedDirty || !protected.Uncertain || protected.State != "unknown" {
		t.Fatalf("protected path appeared clean: %+v", protected)
	}
	if err := os.RemoveAll(protectedDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "ignored.md"), []byte("unpublished content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ignored := observeSyncPeer(context.Background(), svc.cfg, svc.store, client, request)
	if !ignored.GovernedDirty || ignored.State != "local_dirty" {
		t.Fatalf("ignored governed file appeared clean: %+v", ignored)
	}
	for _, name := range []string{".gitignore", "ignored.md"} {
		if err := os.Remove(filepath.Join(repo, name)); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err = svc.store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{JobID: "delivery-pending", GroupID: svc.cfg.Sync.GroupID, Kind: "delivery", LogicalKey: "delivery-pending", InitialState: "pending", PayloadJSON: `{}`, QueueLimit: 1, Now: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	pending := observeSyncPeer(context.Background(), svc.cfg, svc.store, client, request)
	if pending.State != "applied" || !pending.PendingWork {
		t.Fatalf("pending delivery was hidden: %+v", pending)
	}
	local := syncrecords.VerificationNodeFromStatus(clean)
	verification := syncrecords.Verification{SchemaVersion: syncrecords.VerificationSchema, VerificationID: "verification-test", GroupID: request.GroupID, MembershipRevision: head, ContentRef: request.ContentRef, TargetCommit: head, ScopeDigest: request.ScopeDigest, ContractDigest: request.ContractDigest, Phase: "finished", Result: "complete", ExpectedNodes: []syncrecords.ExpectedNode{{InstanceID: clean.Responder, StateIncarnationID: clean.StateIncarnationID}, {InstanceID: "node-b", StateIncarnationID: svc.cfg.Sync.Nodes[1].StateIncarnationID}}, Nodes: []syncrecords.VerificationNode{local}, Fence: 1}
	if verification.Validate() == nil {
		t.Fatal("one-node verification falsely passed")
	}
}

func TestE22T3LiveDeadlinePrecedesInterruptedExpiry(t *testing.T) {
	if verificationCommandDeadline <= 0 || verificationCommandDeadline >= sqlite.InterruptedVerificationExpiryAge {
		t.Fatalf("live deadline %s must precede interrupted expiry %s", verificationCommandDeadline, sqlite.InterruptedVerificationExpiryAge)
	}
}

func TestE22T3FinalObservationRejectsChangedLocalState(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		changed, uncertain, control, local, pair bool
		result, reason                           string
	}{
		{"clean", false, false, true, true, true, "complete", ""},
		{"target changed", true, false, true, true, true, "target_changed", "target_changed"},
		{"recheck unavailable", false, true, true, true, true, "incomplete", "target_recheck_unavailable"},
		{"control paused", false, false, false, true, true, "incomplete", "local_changed_during_verification"},
		{"local binding changed", false, false, true, false, true, "incomplete", "local_changed_during_verification"},
		{"local became dirty", false, false, true, true, false, "incomplete", "local_changed_during_verification"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, reason := finalVerificationDisposition(tc.changed, tc.uncertain, tc.control, tc.local, tc.pair)
			if result != tc.result || reason != tc.reason {
				t.Fatalf("disposition = (%s,%s), want (%s,%s)", result, reason, tc.result, tc.reason)
			}
		})
	}
}

type e22t3RoundTrip func(*http.Request) (*http.Response, error)

func (f e22t3RoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestE22T3PeerResponseBindsNonceAndRetainsAge(t *testing.T) {
	cfg, err := config.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sync.Enabled = true
	t.Setenv("SYNC_NODE_A_TO_B", "outbound-secret")
	request := syncrecords.StatusRequest{SchemaVersion: syncrecords.StatusRequestSchema, GroupID: cfg.Sync.GroupID, Sender: cfg.Sync.LocalInstanceID, Receiver: cfg.Sync.Nodes[1].InstanceID, MembershipRevision: strings.Repeat("1", 40), ContentRef: cfg.Sync.ContentRef, TargetCommit: strings.Repeat("2", 40), ScopeDigest: config.SyncScopeDigest(cfg, cfg.Sync.Resource), ContractDigest: config.SyncContractDigest(), Nonce: "0123456789abcdef"}
	response := syncrecords.StatusResponse{SchemaVersion: syncrecords.StatusResponseSchema, GroupID: request.GroupID, Responder: request.Receiver, StateIncarnationID: cfg.Sync.Nodes[1].StateIncarnationID, MembershipRevision: request.MembershipRevision, ContentRef: request.ContentRef, TargetCommit: request.TargetCommit, ScopeDigest: request.ScopeDigest, ContractDigest: request.ContractDigest, Nonce: request.Nonce, EvidenceGeneration: time.Now().Add(-301 * time.Second).UnixNano(), State: "applied", MembershipCurrent: true}
	transport := e22t3RoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/sync/status" || r.Header.Get("Authorization") != "Bearer outbound-secret" {
			t.Fatalf("unbound peer query: %s %q", r.URL, r.Header.Get("Authorization"))
		}
		var got syncrecords.StatusRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got != request {
			t.Fatalf("peer request: %+v %v", got, err)
		}
		raw, _ := json.Marshal(response)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(raw)), Header: http.Header{}}, nil
	})
	client := &http.Client{Transport: transport}
	observed, err := querySyncPeer(context.Background(), cfg, client, cfg.Sync.Nodes[1].Endpoint, request)
	if err != nil || observed.EvidenceAgeSeconds < 301 || syncrecords.VerificationNodeFromStatus(observed).EvidenceFresh {
		t.Fatalf("cached evidence became fresh: %+v %v", observed, err)
	}
	response.Nonce = "wrong-nonce-value"
	observed, err = querySyncPeer(context.Background(), cfg, client, cfg.Sync.Nodes[1].Endpoint, request)
	if err != nil || syncResponseMatches(observed, request, syncrecords.ActiveMember{InstanceID: request.Receiver, StateIncarnationID: response.StateIncarnationID}) {
		t.Fatalf("wrong nonce bound to verifier: %+v %v", observed, err)
	}
	response.Nonce = request.Nonce
	for _, generation := range []int64{time.Now().Add(6 * time.Second).UnixNano(), time.Now().Unix()} {
		response.EvidenceGeneration = generation
		if _, err := querySyncPeer(context.Background(), cfg, client, cfg.Sync.Nodes[1].Endpoint, request); err == nil {
			t.Fatalf("invalid peer generation %d was accepted", generation)
		}
	}
	for _, endpoint := range []string{"http://node-b.example.ts.net", "https://node-b.example.ts.net:8443", "https://node-b.example.ts.net?ref=evil"} {
		if _, err := querySyncPeer(context.Background(), cfg, client, endpoint, request); err == nil {
			t.Fatalf("invalid peer endpoint %q was accepted", endpoint)
		}
	}
}

// Reuse the signed-membership/checkpoint fixture to exercise the actual CLI
// verification path against a nonce-correlated peer, without contacting a host.
func e22t3AssertPairVerification(t *testing.T, cfg *config.Config, contentState, membershipState string) {
	t.Helper()
	t.Setenv("SYNC_NODE_A_TO_B", "outbound-secret")
	var peerOverride func(*syncrecords.StatusResponse)
	transport := e22t3RoundTrip(func(r *http.Request) (*http.Response, error) {
		var request syncrecords.StatusRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path != "/v1/sync/status" || r.Header.Get("Authorization") != "Bearer outbound-secret" || request.Receiver != cfg.Sync.Nodes[1].InstanceID {
			t.Fatalf("peer query binding: %+v %s", request, r.URL)
		}
		response := syncrecords.StatusResponse{SchemaVersion: syncrecords.StatusResponseSchema, GroupID: request.GroupID,
			Responder: request.Receiver, StateIncarnationID: cfg.Sync.Nodes[1].StateIncarnationID,
			MembershipRevision: request.MembershipRevision, ContentRef: request.ContentRef,
			TargetCommit: request.TargetCommit, ScopeDigest: request.ScopeDigest, ContractDigest: request.ContractDigest,
			Nonce: request.Nonce, EvidenceGeneration: time.Now().UnixNano(), State: "applied", MembershipCurrent: true}
		if peerOverride != nil {
			peerOverride(&response)
		}
		raw, _ := json.Marshal(response)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(raw)), Header: http.Header{}}, nil
	})
	interruptedStore, err := openStateStore(resolveConfigPath(""))
	if err != nil {
		t.Fatal(err)
	}
	const interruptedID = "t3-interrupted-verification"
	_, _, err = interruptedStore.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{
		JobID: interruptedID, GroupID: cfg.Sync.GroupID, Kind: "verification", LogicalKey: interruptedID,
		InitialState: "planned", PayloadJSON: `{}`, QueueLimit: cfg.Sync.Bounds.Queue,
		Now: time.Now().Add(-6 * time.Minute).UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		interruptedStore.Close()
		t.Fatal(err)
	}
	interruptedStore.Close()
	statusCode, status, statusErr := syncResult(t, "status", "--group", cfg.Sync.GroupID, "--output", "json")
	if statusCode != 0 || status["result"].(map[string]any)["latest_verification"].(map[string]any)["state"] != "planned" {
		t.Fatalf("interrupted plan not visible before verification: code=%d status=%v err=%s", statusCode, status, statusErr)
	}
	var out, errOut bytes.Buffer
	if code := runSyncVerifyWithHTTP([]string{"--group", cfg.Sync.GroupID, "--output", "json"}, &out, &errOut, &http.Client{Transport: transport}); code != 0 {
		t.Fatalf("pair verification: %d %s", code, errOut.String())
	}
	var envelope struct {
		Result   syncrecords.Verification `json:"result"`
		Warnings []string                 `json:"warnings"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Result.Result != "complete" || len(envelope.Result.Nodes) != 2 || envelope.Result.Validate() != nil {
		t.Fatalf("pair did not converge: %+v", envelope.Result)
	}
	interruptedStore, err = openStateStore(resolveConfigPath(""))
	if err != nil {
		t.Fatal(err)
	}
	interrupted, found, err := interruptedStore.FindSyncJob(context.Background(), cfg.Sync.GroupID, "verification", interruptedID)
	if err != nil || !found || interrupted.State != "expired" {
		interruptedStore.Close()
		t.Fatalf("interrupted plan was not expired by verify: %+v found=%v err=%v", interrupted, found, err)
	}
	interruptedStore.Close()
	statusCode, status, statusErr = syncResult(t, "status", "--group", cfg.Sync.GroupID, "--output", "json")
	if statusCode != 0 {
		t.Fatalf("status after verification: %d %s", statusCode, statusErr)
	}
	result := status["result"].(map[string]any)
	if result["latest_verification"].(map[string]any)["state"] != "complete" || result["latest_delivery"].(map[string]any)["present"] != false {
		t.Fatalf("verification obscured delivery status: %v", result)
	}
	for _, test := range []struct {
		name string
		edit func(*syncrecords.StatusResponse)
		want string
	}{
		{"obsolete incarnation", func(r *syncrecords.StatusResponse) { r.StateIncarnationID = "node-b-old1" }, "peer_binding_mismatch"},
		{"stale peer", func(r *syncrecords.StatusResponse) {
			r.EvidenceGeneration = time.Now().Add(-301 * time.Second).UnixNano()
		}, "peer_incomplete"},
	} {
		peerOverride = test.edit
		out.Reset()
		errOut.Reset()
		if code := runSyncVerifyWithHTTP([]string{"--group", cfg.Sync.GroupID, "--output", "json"}, &out, &errOut, &http.Client{Transport: transport}); code != 0 || json.Unmarshal(out.Bytes(), &envelope) != nil || envelope.Result.Result != "incomplete" || !containsString(envelope.Warnings, test.want) {
			t.Fatalf("%s passed verification: code=%d result=%+v warnings=%v err=%s", test.name, code, envelope.Result, envelope.Warnings, errOut.String())
		}
	}
	peerOverride = nil
	assertLocalIncomplete := func(label string) {
		t.Helper()
		out.Reset()
		errOut.Reset()
		if code := runSyncVerifyWithHTTP([]string{"--group", cfg.Sync.GroupID, "--output", "json"}, &out, &errOut, &http.Client{Transport: transport}); code != 0 || json.Unmarshal(out.Bytes(), &envelope) != nil || envelope.Result.Result != "incomplete" || !containsString(envelope.Warnings, "local_incomplete") {
			t.Fatalf("%s passed verification: code=%d result=%+v warnings=%v err=%s", label, code, envelope.Result, envelope.Warnings, errOut.String())
		}
	}
	notePath := filepath.Join(cfg.Resources[cfg.Sync.Resource].Root, "note.md")
	originalNote, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notePath, []byte("unpublished edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertLocalIncomplete("governed local edit")
	if err := os.WriteFile(notePath, originalNote, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := openStateStore(resolveConfigPath(""))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, _, err = store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{JobID: "t3-pending-delivery", GroupID: cfg.Sync.GroupID, Kind: "delivery", LogicalKey: "t3-pending-delivery", InitialState: "pending", PayloadJSON: `{}`, QueueLimit: cfg.Sync.Bounds.Queue, Now: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	assertLocalIncomplete("pending local delivery")
	if _, err := store.Exec(`DELETE FROM sync_jobs WHERE job_id='t3-pending-delivery'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE sync_controls SET state='paused',reason='operator_pause' WHERE group_id=?`, cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	assertLocalIncomplete("paused local control")
	if _, err := store.Exec(`UPDATE sync_controls SET state='active',reason='none' WHERE group_id=?`, cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	offline := &http.Client{Transport: e22t3RoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("peer offline")
	})}
	out.Reset()
	errOut.Reset()
	if code := runSyncVerifyWithHTTP([]string{"--group", cfg.Sync.GroupID, "--output", "json"}, &out, &errOut, offline); code != 0 || json.Unmarshal(out.Bytes(), &envelope) != nil || envelope.Result.Result != "incomplete" || len(envelope.Result.Nodes) != 1 || len(envelope.Result.ExpectedNodes) != 2 || !containsString(envelope.Warnings, "peer_unavailable") {
		t.Fatalf("offline peer passed verification: code=%d result=%+v err=%s", code, envelope.Result, errOut.String())
	}
	statusCode, status, statusErr = syncResult(t, "status", "--group", cfg.Sync.GroupID, "--output", "json")
	if statusCode != 0 || len(status["result"].(map[string]any)["expected_nodes"].([]any)) != 2 || status["result"].(map[string]any)["latest_verification"].(map[string]any)["state"] != "incomplete" || !e22t3HasReasonCode(status["result"].(map[string]any)["latest_verification"].(map[string]any)["reason_codes"], "peer_unavailable") {
		t.Fatalf("offline peer vanished from status: code=%d status=%v err=%s", statusCode, status, statusErr)
	}
	for _, refState := range []string{contentState, membershipState} {
		original, err := os.ReadFile(refState)
		if err != nil {
			t.Fatal(err)
		}
		moving := &http.Client{Transport: e22t3RoundTrip(func(r *http.Request) (*http.Response, error) {
			if err := os.WriteFile(refState, []byte(strings.Repeat("3", 40)+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var request syncrecords.StatusRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			response := syncrecords.StatusResponse{SchemaVersion: syncrecords.StatusResponseSchema, GroupID: request.GroupID,
				Responder: request.Receiver, StateIncarnationID: cfg.Sync.Nodes[1].StateIncarnationID,
				MembershipRevision: request.MembershipRevision, ContentRef: request.ContentRef,
				TargetCommit: request.TargetCommit, ScopeDigest: request.ScopeDigest, ContractDigest: request.ContractDigest,
				Nonce: request.Nonce, EvidenceGeneration: time.Now().UnixNano(), State: "applied", MembershipCurrent: true}
			raw, _ := json.Marshal(response)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(raw)), Header: http.Header{}}, nil
		})}
		out.Reset()
		errOut.Reset()
		code := runSyncVerifyWithHTTP([]string{"--group", cfg.Sync.GroupID, "--output", "json"}, &out, &errOut, moving)
		if err := os.WriteFile(refState, original, 0o600); err != nil {
			t.Fatal(err)
		}
		if code != 0 || json.Unmarshal(out.Bytes(), &envelope) != nil || envelope.Result.Result != "target_changed" || !containsString(envelope.Warnings, "target_changed") {
			t.Fatalf("ref change %s passed verification: code=%d result=%+v err=%s", refState, code, envelope.Result, errOut.String())
		}
	}
	originalContentState, err := os.ReadFile(contentState)
	if err != nil {
		t.Fatal(err)
	}
	unavailableRecheck := &http.Client{Transport: e22t3RoundTrip(func(r *http.Request) (*http.Response, error) {
		if err := os.Remove(contentState); err != nil {
			t.Fatal(err)
		}
		return transport.RoundTrip(r)
	})}
	out.Reset()
	errOut.Reset()
	code := runSyncVerifyWithHTTP([]string{"--group", cfg.Sync.GroupID, "--output", "json"}, &out, &errOut, unavailableRecheck)
	if err := os.WriteFile(contentState, originalContentState, 0o600); err != nil {
		t.Fatal(err)
	}
	if code != 0 || json.Unmarshal(out.Bytes(), &envelope) != nil || envelope.Result.Result != "incomplete" || !containsString(envelope.Warnings, "target_recheck_unavailable") {
		t.Fatalf("unavailable target recheck passed verification: code=%d result=%+v warnings=%v err=%s", code, envelope.Result, envelope.Warnings, errOut.String())
	}
	repo := cfg.Resources[cfg.Sync.Resource].Root
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	originalMembership := git("rev-parse", cfg.Sync.MembershipRef)
	localMove := &http.Client{Transport: e22t3RoundTrip(func(r *http.Request) (*http.Response, error) {
		git("update-ref", cfg.Sync.MembershipRef, strings.TrimSpace(string(originalContentState)))
		return transport.RoundTrip(r)
	})}
	out.Reset()
	errOut.Reset()
	code = runSyncVerifyWithHTTP([]string{"--group", cfg.Sync.GroupID, "--output", "json"}, &out, &errOut, localMove)
	git("update-ref", cfg.Sync.MembershipRef, originalMembership)
	if code != 0 || json.Unmarshal(out.Bytes(), &envelope) != nil || envelope.Result.Result != "target_changed" || !containsString(envelope.Warnings, "local_binding_mismatch") {
		t.Fatalf("local membership movement hid binding warning: code=%d result=%+v warnings=%v err=%s", code, envelope.Result, envelope.Warnings, errOut.String())
	}
	configPath := os.Getenv("AGENT_DISPATCH_CONFIG")
	originalConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	changedCfg := *cfg
	changedSync := *cfg.Sync
	changedSync.Bounds.Queue--
	if changedSync.Bounds.Queue < 1 {
		changedSync.Bounds.Queue = cfg.Sync.Bounds.Queue + 1
	}
	changedCfg.Sync = &changedSync
	changedConfig, err := json.Marshal(&changedCfg)
	if err != nil {
		t.Fatal(err)
	}
	movingConfig := &http.Client{Transport: e22t3RoundTrip(func(r *http.Request) (*http.Response, error) {
		if err := os.WriteFile(configPath, changedConfig, 0o600); err != nil {
			t.Fatal(err)
		}
		var request syncrecords.StatusRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		response := syncrecords.StatusResponse{SchemaVersion: syncrecords.StatusResponseSchema, GroupID: request.GroupID,
			Responder: request.Receiver, StateIncarnationID: cfg.Sync.Nodes[1].StateIncarnationID,
			MembershipRevision: request.MembershipRevision, ContentRef: request.ContentRef,
			TargetCommit: request.TargetCommit, ScopeDigest: request.ScopeDigest, ContractDigest: request.ContractDigest,
			Nonce: request.Nonce, EvidenceGeneration: time.Now().UnixNano(), State: "applied", MembershipCurrent: true}
		raw, _ := json.Marshal(response)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(raw)), Header: http.Header{}}, nil
	})}
	out.Reset()
	errOut.Reset()
	code = runSyncVerifyWithHTTP([]string{"--group", cfg.Sync.GroupID, "--output", "json"}, &out, &errOut, movingConfig)
	if err := os.WriteFile(configPath, originalConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if code != 0 || json.Unmarshal(out.Bytes(), &envelope) != nil || envelope.Result.Result != "target_changed" || !containsString(envelope.Warnings, "target_changed") {
		t.Fatalf("config movement passed verification: code=%d result=%+v warnings=%v err=%s", code, envelope.Result, envelope.Warnings, errOut.String())
	}
}

func e22t3HasReasonCode(value any, want string) bool {
	codes, ok := value.([]any)
	if !ok {
		return false
	}
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}

func TestE22T3DecisionRefreshRejectsAgedPeer(t *testing.T) {
	now := time.Now()
	verification := syncrecords.Verification{Nodes: []syncrecords.VerificationNode{
		{State: "applied", MembershipCurrent: true, EvidenceFresh: true, EvidenceAgeSeconds: 299, EvidenceGeneration: now.Add(-301 * time.Second).UnixNano()},
		{State: "applied", MembershipCurrent: true, EvidenceFresh: true, EvidenceAgeSeconds: 1, EvidenceGeneration: now.UnixNano()},
	}}
	if !verification.CleanPair() {
		t.Fatal("setup should have a pair fresh at HTTP receipt")
	}
	refreshVerificationAges(&verification, now)
	if verification.CleanPair() || verification.Nodes[0].EvidenceFresh || verification.Nodes[0].EvidenceAgeSeconds < 301 {
		t.Fatalf("decision reused stale peer age: %+v", verification.Nodes[0])
	}
}
