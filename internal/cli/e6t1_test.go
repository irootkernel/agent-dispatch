package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeswebhook"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
)

// e6t1Capture records one inbound webhook delivery.
type e6t1Capture struct {
	mu       sync.Mutex
	requests []struct {
		idempotencyKey string
		authorization  string
		body           string
	}
}

func (c *e6t1Capture) add(r *http.Request) {
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, struct {
		idempotencyKey string
		authorization  string
		body           string
	}{r.Header.Get("Idempotency-Key"), r.Header.Get("Authorization"), body.String()})
}

func (c *e6t1Capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func (c *e6t1Capture) at(i int) (key, auth, body string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.requests[i]
	return r.idempotencyKey, r.authorization, r.body
}

// e6t1Fixture builds a vault, one TLS webhook endpoint whose handler is
// driven by the test, and a one-route configuration pointing at it
// through an explicit hermes-webhook target.
func e6t1Fixture(t *testing.T, handler http.HandlerFunc) (configPath, vault string, server *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	vault = filepath.Join(dir, "vault")
	if err := os.MkdirAll(filepath.Join(vault, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "new.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_DISPATCH_WEBHOOK_TOKEN", "cli-secret-1")
	server = e6t1TLSServer(t, handler)
	cfg := `version: 1
instance:
  id: e6t1-test
  state_dir: ` + stateDir + `
resources:
  vault-main:
    type: directory
    root: ` + vault + `
    file_scope: markdown
    git:
      mode: disabled
targets:
  hook-main:
    type: hermes-webhook
    endpoint: ` + server.URL + `
    auth:
      type: bearer
      secret_ref: env:AGENT_DISPATCH_WEBHOOK_TOKEN
    submit_timeout: 5s
    idempotency_header: Idempotency-Key
routes:
  wiki:
    enabled: true
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: agent-dispatch.wiki.test
      include: ["**/*.md"]
      exclude: [".obsidian/workspace*.json"]
    batching:
      automatic_threshold: 25
      hard_limit: 100
      max_manifest_bytes: 262144
    policy:
      protected: []
      immutable: []
      bulk_action: quarantine
      overflow_action: reconcile
      fresh_instance_action: reconcile
      unsafe_path_action: quarantine
    dispatch:
      target: hook-main
      profile: wiki-maintainer
      skills: [llm-wiki]
      mutex_key: wiki-publish
      latest_state: true
      submission_retry:
        max_attempts: 3
        initial_backoff: 1s
        max_backoff: 2s
        multiplier: 2.0
        jitter_fraction: 0.0
      execution_hints:
        max_runtime: 30m
        max_attempts: 2
      failure_budget: 2
      active_stale_after: 2h
    reconciliation:
      initial: true
      daily_expected: true
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, vault, server
}

// e6t1RegisterRoute seeds the route runtime state for the webhook
// target, mirroring the e4t3 registration helper.
func e6t1RegisterRoute(t *testing.T, configPath, endpoint string) {
	t.Helper()
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	stateDir := platformpaths.ResolveStateDir(cfg.Instance.StateDir)
	store, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(filepath.Join(stateDir, "backups")); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterRoute(nil, "wiki", "route-rev-1", "policy-rev-1", "vault-main", "hook-main", "{}", "2026-08-22T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	var exists int
	if err := store.QueryRowContext(context.Background(), `SELECT 1 FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&exists); err == nil {
		// Already initialized: registration is idempotent for repeated
		// fixture seeding.
	} else {
		if err := store.InitializeRouteState(nil, "wiki"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetRouteActivation(context.Background(), "wiki", "enabled", "route-rev-1", "2026-08-22T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
}

// e6t1Dispatch drives one dispatch through the CLI and returns the
// envelope result plus the exit code.
func e6t1Dispatch(t *testing.T, configPath, vault string) (map[string]any, int) {
	t.Helper()
	setPlanEnv(t, vault, false)
	e6t1RegisterRoute(t, configPath, "")
	var out, errb bytes.Buffer
	var code int
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("dispatch failed (exit %d): %s", code, errb.String())
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("not an envelope: %s", out.String())
	}
	raw, _ := json.Marshal(env.Result)
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return res, code
}

// e6t1Cert is the one self-signed loopback certificate shared by every
// webhook fixture: the CLI builds the production strict client (system
// roots, no bypass), so each fixture swaps the test-only
// webhookClientFactory seam for a client whose root pool verifies
// exactly this certificate. The certificate is generated once and
// reused so every fixture's client trusts the same key material.
var (
	e6t1CertOnce sync.Once
	e6t1Cert     tls.Certificate
)

func e6t1LoadCert(t *testing.T) tls.Certificate {
	t.Helper()
	e6t1CertOnce.Do(func() {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
		if err != nil {
			t.Fatal(err)
		}
		template := x509.Certificate{
			SerialNumber: serial,
			Subject:      pkix.Name{CommonName: "agent-dispatch-e6t1-loopback"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
			DNSNames:     []string{"localhost"},
		}
		der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		e6t1Cert = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	})
	return e6t1Cert
}

// e6t1TLSServer starts one loopback HTTPS endpoint with the shared
// certificate and swaps the CLI's webhook client factory for one that
// verifies exactly that certificate, keeping the strict policy (one
// deadline, no redirects). The production factory is restored on test
// cleanup.
func e6t1TLSServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	cert := e6t1LoadCert(t)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	previous := webhookClientFactory
	webhookClientFactory = func(timeout time.Duration) hermeswebhook.HTTPClient {
		return &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	t.Cleanup(func() { webhookClientFactory = previous })
	return server
}

// TestDispatchSubmitsThroughWebhookSink proves the E6-T1 wiring end to
// end: one Watchman batch becomes a durable intent whose target scope
// is the webhook endpoint, the explicit webhook sink delivers the same
// logical task contract with the core's idempotency key and the bearer
// secret, and the accepted state is persisted (WHK-002, WHK-005,
// SEC-006).
func TestDispatchSubmitsThroughWebhookSink(t *testing.T) {
	capture := &e6t1Capture{}
	configPath, vault, server := e6t1Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		capture.add(r)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accepted": true}`))
	})
	res, _ := e6t1Dispatch(t, configPath, vault)
	if res["state"] != "accepted" || res["submitted"] != true {
		t.Fatalf("dispatch result wrong: %v", res)
	}
	if capture.count() != 1 {
		t.Fatalf("endpoint invocations = %d, want 1", capture.count())
	}
	key, auth, body := capture.at(0)
	if !strings.HasPrefix(key, "agent-dispatch:v1:sha256:") {
		t.Fatalf("idempotency header = %q, want the core key", key)
	}
	if auth != "Bearer cli-secret-1" {
		t.Fatalf("authorization = %q", auth)
	}
	if !strings.Contains(body, `"contract_version":"agent-dispatch.hermes-task/v1"`) {
		t.Fatalf("body does not carry the logical task contract: %s", body)
	}
	// The durable intent records the webhook endpoint as its target
	// scope — the identity reconciliation re-verifies.
	dispatchID, _ := res["dispatch_id"].(string)
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	stateDir := platformpaths.ResolveStateDir(cfg.Instance.StateDir)
	store, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snap, err := store.LoadIntent(context.Background(), dispatchID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.TargetType != "hermes-webhook" || snap.TargetScope != server.URL {
		t.Fatalf("intent target = %q scope = %q, want hermes-webhook %q", snap.TargetType, snap.TargetScope, server.URL)
	}
}

// TestWebhookUnknownNeverFallsBack proves the no-fallback posture at
// the CLI level: an ambiguous webhook response leaves the intent
// unknown, drain reconciles it honestly (the webhook declares no
// lookup, so it dead-letters for the operator), and no second delivery
// happens without operator action (WHK-002, WHK-004, DUR-008).
func TestWebhookUnknownNeverFallsBack(t *testing.T) {
	capture := &e6t1Capture{}
	configPath, vault, _ := e6t1Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		capture.add(r)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	res, _ := e6t1Dispatch(t, configPath, vault)
	if res["state"] != "unknown" {
		t.Fatalf("ambiguous submission must record unknown, got %v", res["state"])
	}
	dispatchID, _ := res["dispatch_id"].(string)

	var drainOut, drainErr bytes.Buffer
	code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &drainOut, &drainErr)
	if code != 0 {
		t.Fatalf("drain failed: %s", drainErr.String())
	}
	drainEnv := decodeEnvelope(t, &drainOut)
	reconciled, _ := drainEnv["reconciled"].([]any)
	if len(reconciled) != 1 {
		t.Fatalf("one unknown dispatch must be reconciled: %v", drainEnv["reconciled"])
	}
	entry, _ := reconciled[0].(map[string]any)
	if entry["state"] != "dead_lettered" {
		t.Fatalf("webhook unknown without lookup must dead-letter: %v", entry)
	}
	if entry["dispatch_id"] != dispatchID {
		t.Fatalf("reconciled dispatch = %v", entry["dispatch_id"])
	}
	if capture.count() != 1 {
		t.Fatalf("endpoint invocations = %d, want exactly the original submission", capture.count())
	}
}

// TestWebhookRetryReusesIdempotencyKey proves WHK-005 end to end: the
// operator retry and the drain resubmission present the same
// idempotency key the original submission carried.
func TestWebhookRetryReusesIdempotencyKey(t *testing.T) {
	capture := &e6t1Capture{}
	var respondAtomic sync.Map // first delivery: 500, later: 200
	respondAtomic.Store("fails", true)
	configPath, vault, _ := e6t1Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		capture.add(r)
		if v, _ := respondAtomic.Load("fails"); v == true {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	res, _ := e6t1Dispatch(t, configPath, vault)
	if res["state"] != "unknown" {
		t.Fatalf("first submission must be unknown, got %v", res["state"])
	}
	dispatchID, _ := res["dispatch_id"].(string)

	// The unknown dispatch dead-letters through drain (the webhook
	// declares no lookup), which makes it retryable.
	var deadOut, deadErr bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &deadOut, &deadErr); code != 0 {
		t.Fatalf("dead-letter drain failed: %s", deadErr.String())
	}

	var retryOut, retryErr bytes.Buffer
	if code := Run([]string{"dispatches", "retry", "--config", configPath, dispatchID, "--reason", "operator resolved"}, &retryOut, &retryErr); code != 0 {
		t.Fatalf("retry failed: %s", retryErr.String())
	}
	respondAtomic.Store("fails", false)
	var drainOut, drainErr bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &drainOut, &drainErr); code != 0 {
		t.Fatalf("drain failed: %s", drainErr.String())
	}
	if capture.count() != 2 {
		t.Fatalf("endpoint invocations = %d, want 2", capture.count())
	}
	first, _, _ := capture.at(0)
	second, _, _ := capture.at(1)
	if first == "" || first != second {
		t.Fatalf("idempotency keys differ across retry: %q vs %q", first, second)
	}
	var showOut, showErr bytes.Buffer
	if code := Run([]string{"dispatches", "show", "--config", configPath, dispatchID}, &showOut, &showErr); code != 0 {
		t.Fatalf("show failed: %s", showErr.String())
	}
	if !strings.Contains(showOut.String(), `"state": "accepted"`) && !strings.Contains(showOut.String(), `"state":"accepted"`) {
		t.Fatalf("recovered dispatch must be accepted: %s", showOut.String())
	}
}

// TestWebhookCapabilityGateFailsClosed proves a webhook target required
// to provide an undeclared capability fails validation with the stable
// configuration code (HER-005, WHK-004 honesty).
func TestWebhookCapabilityGateFailsClosed(t *testing.T) {
	configPath, vault, _ := e6t1Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(raw, []byte("    idempotency_header: Idempotency-Key"), []byte("    idempotency_header: Idempotency-Key\n    required_capabilities: [durable_acceptance]"), 1)
	if err := os.WriteFile(configPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, vault, false)
	e6t1RegisterRoute(t, configPath, "")
	var out, errb bytes.Buffer
	var code int
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 3 {
		t.Fatalf("capability mismatch must exit 3, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "config_capability_missing") {
		t.Fatalf("stable code missing: %s", errb.String())
	}
}

// TestConfigValidateProbesWebhookTarget proves the offline probe
// summary for webhook targets: available with the declared, honest
// capability set and no endpoint network I/O at validation time.
func TestConfigValidateProbesWebhookTarget(t *testing.T) {
	configPath, _, _ := e6t1Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--config", configPath, "--probe-targets", "--output", "json"}, &out, &errb)
	if code != 0 {
		t.Fatalf("validate failed: %s", errb.String())
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(env.Result)
	var res struct {
		ProbeTargets []map[string]any `json:"probe_targets"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.ProbeTargets) != 1 {
		t.Fatalf("probe entries = %v", res.ProbeTargets)
	}
	entry := res.ProbeTargets[0]
	if entry["type"] != "hermes-webhook" || entry["state"] != "available" {
		t.Fatalf("probe entry wrong: %v", entry)
	}
	caps, _ := entry["capabilities"].(map[string]any)
	if caps == nil || caps["durable_acceptance"] != false || caps["submit_idempotency_key"] != true {
		t.Fatalf("declared capabilities wrong: %v", caps)
	}
}

// TestConfigValidateProbeWebhookConfigError proves a webhook target
// whose submit timeout is schema-pattern-valid but unparseable (an
// int64 overflow) is a configuration defect through the probe surface
// too: exit 3, config_invalid — the same class dispatch reports.
func TestConfigValidateProbeWebhookConfigError(t *testing.T) {
	configPath, _, _ := e6t1Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(raw, []byte("    submit_timeout: 5s"), []byte("    submit_timeout: 99999999999999999999s"), 1)
	if err := os.WriteFile(configPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--config", configPath, "--probe-targets", "--output", "json"}, &out, &errb)
	if code != 3 {
		t.Fatalf("unparseable timeout must exit 3, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "config_invalid") {
		t.Fatalf("stable code missing: %s", errb.String())
	}
}

// TestConfigValidateProbeWebhookCapabilityMismatch proves the probe
// surface reports the HER-005 gate with its own stable code: a webhook
// target requiring durable_acceptance fails validation with
// config_capability_missing and exit 3.
func TestConfigValidateProbeWebhookCapabilityMismatch(t *testing.T) {
	configPath, _, _ := e6t1Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(raw, []byte("    idempotency_header: Idempotency-Key"), []byte("    idempotency_header: Idempotency-Key\n    required_capabilities: [durable_acceptance]"), 1)
	if err := os.WriteFile(configPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--config", configPath, "--probe-targets", "--output", "json"}, &out, &errb)
	if code != 3 {
		t.Fatalf("capability mismatch must exit 3, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "config_capability_missing") {
		t.Fatalf("stable code missing: %s", errb.String())
	}
}

// TestWebhookConfigErrorFailsClosedAtDispatch proves the dispatch
// surface reports an unparseable webhook submit_timeout as the stable
// configuration class (exit 3, config_invalid), matching the probe
// surface.
func TestWebhookConfigErrorFailsClosedAtDispatch(t *testing.T) {
	configPath, vault, _ := e6t1Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(raw, []byte("    submit_timeout: 5s"), []byte("    submit_timeout: 99999999999999999999s"), 1)
	if err := os.WriteFile(configPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, vault, false)
	e6t1RegisterRoute(t, configPath, "")
	var out, errb bytes.Buffer
	var code int
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 3 {
		t.Fatalf("unparseable timeout must exit 3 at dispatch, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "config_invalid") {
		t.Fatalf("stable code missing: %s", errb.String())
	}
}

// TestWebhookRerunRecordsEndpointScope proves the rerun path's scope
// resolver carries the webhook endpoint as the durable target scope,
// mirroring dispatch and retry. The predecessor is persisted without
// submission: rerun requires ready or dead-lettered work (E7-T2/B-2).
func TestWebhookRerunRecordsEndpointScope(t *testing.T) {
	capture := &e6t1Capture{}
	configPath, vault, server := e6t1Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		capture.add(r)
		w.WriteHeader(http.StatusOK)
	})
	setPlanEnv(t, vault, false)
	e6t1RegisterRoute(t, configPath, "")
	var dispatchOut, dispatchErr bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &dispatchOut, &dispatchErr)
	})
	res := decodeEnvelope(t, &dispatchOut)
	dispatchID, _ := res["dispatch_id"].(string)

	var out, errb bytes.Buffer
	code := Run([]string{"dispatches", "rerun", "--config", configPath, dispatchID, "--reason", "operator rerun", "--yes"}, &out, &errb)
	if code != 0 {
		t.Fatalf("rerun failed: %s", errb.String())
	}
	env := decodeEnvelope(t, &out)
	newID, _ := env["dispatch_id"].(string)
	if newID == "" || newID == dispatchID {
		t.Fatalf("rerun must create a new dispatch: %v", env)
	}
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	stateDir := platformpaths.ResolveStateDir(cfg.Instance.StateDir)
	store, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snap, err := store.LoadIntent(context.Background(), newID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.TargetScope != server.URL {
		t.Fatalf("rerun intent scope = %q, want the endpoint %q", snap.TargetScope, server.URL)
	}
}
