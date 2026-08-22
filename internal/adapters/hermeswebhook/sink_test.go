package hermeswebhook

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootkernel/jjukkumi/internal/ports"
)

// webhookFixture is one loopback HTTPS endpoint plus the sink pointed
// at it. The sink's client verifies the server's self-signed
// certificate through an explicit root pool, so the production
// verification path is exercised, not bypassed.
type webhookFixture struct {
	server  *httptest.Server
	sink    *Sink
	request []capturedRequest
	mu      sync.Mutex
}

type capturedRequest struct {
	method string
	path   string
	body   string
	header http.Header
}

// newWebhookFixture starts one TLS endpoint served by handler and wires
// the sink against it with the given authentication.
func newWebhookFixture(t *testing.T, authType, secretRef, headerName, idempotencyHeader string, handler http.HandlerFunc) *webhookFixture {
	t.Helper()
	f := &webhookFixture{}
	server := httptest.NewTLSServer(f.capture(handler))
	f.server = server
	t.Cleanup(server.Close)
	sink, err := NewSink(Options{
		TargetID:          "hook-main",
		Endpoint:          server.URL,
		AuthType:          authType,
		SecretRef:         secretRef,
		AuthHeaderName:    headerName,
		IdempotencyHeader: idempotencyHeader,
		SubmitTimeout:     5 * time.Second,
		Client:            loopbackClient(server, 5*time.Second),
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	f.sink = sink
	return f
}

// loopbackClient verifies the test server's own certificate while
// keeping the strict client policy (deadline, no redirects).
func loopbackClient(server *httptest.Server, timeout time.Duration) *http.Client {
	pool := x509.NewCertPool()
	if leaf := server.Certificate(); leaf != nil {
		pool.AddCert(leaf)
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// capture records one incoming request for assertions.
func (f *webhookFixture) capture(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.request = append(f.request, capturedRequest{method: r.Method, path: r.URL.Path, body: string(body), header: r.Header.Clone()})
		f.mu.Unlock()
		if next != nil {
			next(w, r)
		}
	}
}

func (f *webhookFixture) requests() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]capturedRequest(nil), f.request...)
}

// sampleRequest is one minimal logical task request.
func sampleRequest(key string) ports.TaskRequest {
	return ports.TaskRequest{
		ContractVersion: ports.TaskRequestContractVersion,
		DispatchID:      "dispatch-1",
		IdempotencyKey:  key,
		Route:           ports.TaskRouteRef{ID: "wiki", Revision: "rev-1"},
		Resource:        ports.TaskResource{ID: "vault-main", Workspace: "dir:/srv/vault"},
		Activation: ports.TaskActivation{
			Mode:               "latest_state",
			Generation:         1,
			ContentFingerprint: "sha256:0000",
			Manifest:           []ports.TaskManifestItem{{Path: "Inbox/new.md", Operation: "create"}},
			Flags:              []string{},
		},
		AcceptanceCriteria: []string{"Evaluate the current vault state."},
	}
}

// TestAcceptedNonDurable proves the WHK-004 boundary: a 2xx is
// acceptance of the transmission, never durable task acceptance,
// because no verified response contract proves persistence.
func TestAcceptedNonDurable(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accepted": true}`))
	})
	res, err := f.sink.Submit(context.Background(), sampleRequest("key-1"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Classification != ports.SubmitAccepted {
		t.Fatalf("classification = %q, want accepted", res.Classification)
	}
	if res.Durable != ports.DurableFalse {
		t.Fatalf("durable = %q, want false (transport acceptance only)", res.Durable)
	}
	var payload map[string]any
	if err := json.Unmarshal(res.StructuredPayload, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload["status"] != float64(200) {
		t.Fatalf("payload status = %v", payload["status"])
	}
}

// TestSameLogicalContractBytes proves the webhook sends exactly the
// marshaled logical task contract (hermes-integration §10).
func TestSameLogicalContractBytes(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	req := sampleRequest("key-2")
	if _, err := f.sink.Submit(context.Background(), req); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	want, _ := json.Marshal(req)
	got := f.requests()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	if got[0].body != string(want) {
		t.Fatalf("body mismatch:\n got %s\nwant %s", got[0].body, want)
	}
	if got[0].header.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %q", got[0].header.Get("Content-Type"))
	}
	if got[0].method != http.MethodPost {
		t.Fatalf("method = %q", got[0].method)
	}
}

// TestIdempotencyHeaderVerbatimAndStable proves WHK-005: the core's
// idempotency key is transmitted verbatim under the configured header,
// and the same dispatch retry presents the same key.
func TestIdempotencyHeaderVerbatimAndStable(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "X-Idem", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := sampleRequest("jjukkumi:v1:sha256:abc")
	for i := 0; i < 2; i++ {
		if _, err := f.sink.Submit(context.Background(), req); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}
	got := f.requests()
	if len(got) != 2 {
		t.Fatalf("requests = %d, want 2", len(got))
	}
	for i, r := range got {
		if r.header.Get("X-Idem") != "jjukkumi:v1:sha256:abc" {
			t.Fatalf("request %d idempotency header = %q", i, r.header.Get("X-Idem"))
		}
	}
}

// TestDefaultIdempotencyHeader proves the default header name applies
// when the target configures none.
func TestDefaultIdempotencyHeader(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if _, err := f.sink.Submit(context.Background(), sampleRequest("key-3")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got := f.requests()[0].header.Get(DefaultIdempotencyHeader); got != "key-3" {
		t.Fatalf("default header value = %q", got)
	}
}

// TestAuthShapes proves bearer and custom-header authentication
// transmit the resolved secret only on the wire (SEC-006).
func TestAuthShapes(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if _, err := f.sink.Submit(context.Background(), sampleRequest("k")); err != nil {
		t.Fatalf("bearer Submit: %v", err)
	}
	if got := f.requests()[0].header.Get("Authorization"); got != "Bearer secret-value-1" {
		t.Fatalf("authorization = %q", got)
	}

	t.Setenv("HOOK_TOKEN2", "secret-value-2")
	f2 := newWebhookFixture(t, "header", "env:HOOK_TOKEN2", "X-Hook-Auth", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if _, err := f2.sink.Submit(context.Background(), sampleRequest("k")); err != nil {
		t.Fatalf("header Submit: %v", err)
	}
	if got := f2.requests()[0].header.Get("X-Hook-Auth"); got != "secret-value-2" {
		t.Fatalf("custom auth header = %q", got)
	}
	if got := f2.requests()[0].header.Get("Authorization"); got != "" {
		t.Fatalf("header auth must not set Authorization, got %q", got)
	}
}

// TestSecretRedactedFromEvidence proves SEC-007: an endpoint that
// echoes the credential back never lands it in the captured payload.
func TestSecretRedactedFromEvidence(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"echo": "` + r.Header.Get("Authorization") + `"}`))
	})
	res, err := f.sink.Submit(context.Background(), sampleRequest("k"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if strings.Contains(string(res.StructuredPayload), "secret-value-1") {
		t.Fatalf("structured payload leaks the secret: %s", res.StructuredPayload)
	}
	if !strings.Contains(string(res.StructuredPayload), "[redacted]") {
		t.Fatalf("payload does not show the redaction marker: %s", res.StructuredPayload)
	}
	if strings.Contains(res.Diagnostic, "secret-value-1") {
		t.Fatalf("diagnostic leaks the secret: %s", res.Diagnostic)
	}
}

// TestStatusClassificationTable pins the conservative HTTP mapping
// (WHK-004, DUR-005): definite refusals reject, ambiguous statuses stay
// unknown, redirects are not followed.
func TestStatusClassificationTable(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	cases := []struct {
		status int
		want   ports.SubmitClassification
	}{
		{http.StatusOK, ports.SubmitAccepted},
		{http.StatusCreated, ports.SubmitAccepted},
		{http.StatusNoContent, ports.SubmitAccepted},
		{http.StatusBadRequest, ports.SubmitRejected},
		{http.StatusUnauthorized, ports.SubmitRejected},
		{http.StatusForbidden, ports.SubmitRejected},
		{http.StatusNotFound, ports.SubmitRejected},
		{http.StatusMethodNotAllowed, ports.SubmitRejected},
		{http.StatusUnprocessableEntity, ports.SubmitRejected},
		{http.StatusRequestEntityTooLarge, ports.SubmitRejected},
		{http.StatusMovedPermanently, ports.SubmitRejected},
		{http.StatusFound, ports.SubmitRejected},
		{http.StatusRequestTimeout, ports.SubmitUnknown},
		{http.StatusConflict, ports.SubmitUnknown},
		{http.StatusTooManyRequests, ports.SubmitUnknown},
		{http.StatusInternalServerError, ports.SubmitUnknown},
		{http.StatusServiceUnavailable, ports.SubmitUnknown},
	}
	for _, tc := range cases {
		f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(tc.status)
		})
		res, err := f.sink.Submit(context.Background(), sampleRequest("k"))
		if err != nil {
			t.Fatalf("status %d: %v", tc.status, err)
		}
		if res.Classification != tc.want {
			t.Fatalf("status %d classification = %q, want %q", tc.status, res.Classification, tc.want)
		}
		// Redirect statuses additionally prove the credential-bearing
		// request was delivered exactly once: a client that followed the
		// redirect would have re-sent it to the redirect target
		// (SEC-007).
		if tc.status >= 300 && tc.status < 400 && len(f.requests()) != 1 {
			t.Fatalf("status %d: endpoint invocations = %d, want exactly one", tc.status, len(f.requests()))
		}
	}
}

// TestDefinitePreSubmitDialFailure proves a closed endpoint is a
// definite non-submission: nothing was transmitted (connection
// refused during dial).
func TestDefinitePreSubmitDialFailure(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := server.URL
	server.Close()
	sink, err := NewSink(Options{
		TargetID: "hook-main", Endpoint: endpoint,
		AuthType: "bearer", SecretRef: "env:HOOK_TOKEN",
		Client: loopbackClient(server, 5*time.Second),
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	res, err := sink.Submit(context.Background(), sampleRequest("k"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Classification != ports.SubmitDefiniteNotSubmitted {
		t.Fatalf("classification = %q, want definite_not_submitted", res.Classification)
	}
}

// TestUntrustedCertificateIsDefinite proves a TLS handshake failure
// against an untrusted endpoint transmits nothing: the strict client
// rejects the certificate before any request byte (SEC posture).
func TestUntrustedCertificateIsDefinite(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	// A client with an empty root pool trusts nothing, standing in for
	// a hostile endpoint certificate under the production policy.
	strict := &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	sink, err := NewSink(Options{
		TargetID: "hook-main", Endpoint: server.URL,
		AuthType: "bearer", SecretRef: "env:HOOK_TOKEN",
		Client: strict,
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	res, err := sink.Submit(context.Background(), sampleRequest("k"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Classification != ports.SubmitDefiniteNotSubmitted {
		t.Fatalf("classification = %q, want definite_not_submitted (handshake precedes transmission)", res.Classification)
	}
}

// TestTimeoutAfterPossibleWrite proves an endpoint that accepts the
// connection but never answers within the deadline is unknown: the
// request may have been processed (DUR-005).
func TestTimeoutAfterPossibleWrite(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	})
	f.sink.timeout = 300 * time.Millisecond
	f.sink.client = loopbackClient(f.server, 300*time.Millisecond)
	res, err := f.sink.Submit(context.Background(), sampleRequest("k"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Classification != ports.SubmitUnknown {
		t.Fatalf("classification = %q, want unknown", res.Classification)
	}
	if res.Durable != ports.DurableUnknown {
		t.Fatalf("durable = %q, want unknown", res.Durable)
	}
}

// TestUnresolvedSecretIsDefinite proves a missing secret provably sends
// nothing and never leaks a value (SEC-006).
func TestUnresolvedSecretIsDefinite(t *testing.T) {
	f := newWebhookFixture(t, "bearer", "env:JJUKKUMI_MISSING_SECRET_XYZ", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	res, err := f.sink.Submit(context.Background(), sampleRequest("k"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Classification != ports.SubmitDefiniteNotSubmitted {
		t.Fatalf("classification = %q, want definite_not_submitted", res.Classification)
	}
	if len(f.requests()) != 0 {
		t.Fatalf("unresolved secret must not invoke the endpoint")
	}
}

// TestOversizedRequestRefusedLocally proves the bounded-request policy
// (SEC-009): an over-bound request is refused before any transmission.
func TestOversizedRequestRefusedLocally(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := sampleRequest("k")
	big := strings.Repeat("a", int(DefaultMaximumRequestBytes))
	req.Activation.Manifest = []ports.TaskManifestItem{{Path: big, Operation: "create"}}
	res, err := f.sink.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Classification != ports.SubmitDefiniteNotSubmitted {
		t.Fatalf("classification = %q, want definite_not_submitted", res.Classification)
	}
	if len(f.requests()) != 0 {
		t.Fatalf("oversized request must not invoke the endpoint")
	}
}

// TestLookupsUnsupported proves the honest capability boundary
// (HER-004, sink-adapter-contract §2): lookups and execution
// projection are typed-unsupported, never emulated.
func TestLookupsUnsupported(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {})
	if _, err := f.sink.LookupByIdempotencyKey(context.Background(), "k"); !errors.Is(err, ports.ErrCapabilityUnsupported) {
		t.Fatalf("LookupByIdempotencyKey err = %v, want capability unsupported", err)
	}
	if _, err := f.sink.LookupByExternalRef(context.Background(), "ref"); !errors.Is(err, ports.ErrCapabilityUnsupported) {
		t.Fatalf("LookupByExternalRef err = %v, want capability unsupported", err)
	}
	if _, err := f.sink.GetExecution(context.Background(), "ref"); !errors.Is(err, ports.ErrCapabilityUnsupported) {
		t.Fatalf("GetExecution err = %v, want capability unsupported", err)
	}
}

// TestProbeStatic proves the declaration is offline and conservative
// (E0-T4 §9: durable acceptance unproven, idempotency transmitted).
func TestProbeStatic(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {})
	caps, err := f.sink.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(f.requests()) != 0 {
		t.Fatalf("Probe must not touch the network")
	}
	if caps.DurableAcceptance {
		t.Fatalf("durable acceptance must not be declared without a verified contract")
	}
	if !caps.SubmitIdempotencyKey {
		t.Fatalf("idempotency key submission is declared and transmitted")
	}
	if caps.LookupByIdempotencyKey || caps.LookupByExternalRef || caps.ExecutionStatus {
		t.Fatalf("lookup/execution capabilities must not be declared: %+v", caps)
	}
}

// TestNewSinkGates pins the fail-closed construction checks.
func TestNewSinkGates(t *testing.T) {
	base := Options{TargetID: "hook", AuthType: "bearer", SecretRef: "env:X"}
	cases := []struct {
		name string
		opts func(Options) Options
	}{
		{"plain http endpoint", func(o Options) Options { o.Endpoint = "http://example.invalid/hook"; return o }},
		{"missing endpoint", func(o Options) Options { return o }},
		{"unparseable endpoint", func(o Options) Options { o.Endpoint = "https://"; return o }},
		{"endpoint with userinfo", func(o Options) Options { o.Endpoint = "https://user:pass@example.invalid/hook"; return o }},
		{"negative submit timeout", func(o Options) Options {
			o.Endpoint = "https://example.invalid/hook"
			o.SubmitTimeout = -time.Second
			return o
		}},
		{"bad auth type", func(o Options) Options { o.Endpoint = "https://example.invalid/hook"; o.AuthType = "digest"; return o }},
		{"header auth without name", func(o Options) Options { o.Endpoint = "https://example.invalid/hook"; o.AuthType = "header"; return o }},
		{"bad idempotency header", func(o Options) Options {
			o.Endpoint = "https://example.invalid/hook"
			o.IdempotencyHeader = "Bad Header:"
			return o
		}},
		{"bad secret ref", func(o Options) Options {
			o.Endpoint = "https://example.invalid/hook"
			o.SecretRef = "not-a-ref"
			return o
		}},
	}
	for _, tc := range cases {
		_, err := NewSink(tc.opts(base))
		var configErr *ConfigError
		if err == nil || !errors.As(err, &configErr) {
			t.Fatalf("%s: error %v is not a ConfigError", tc.name, err)
		}
	}
	_, err := NewSink(func(o Options) Options {
		o.Endpoint = "https://example.invalid/hook"
		o.RequiredCapabilities = []string{"durable_acceptance"}
		return o
	}(base))
	var capabilityErr *CapabilityError
	if err == nil || !errors.As(err, &capabilityErr) {
		t.Fatalf("missing capability: error %v is not a CapabilityError", err)
	}
}

// TestExcessiveResponseOutputBounded proves a huge response body is
// captured only up to the bound with an explicit truncation marker.
func TestExcessiveResponseOutputBounded(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseCapture*2)))
	})
	res, err := f.sink.Submit(context.Background(), sampleRequest("k"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Classification != ports.SubmitAccepted {
		t.Fatalf("classification = %q", res.Classification)
	}
	var payload map[string]any
	if err := json.Unmarshal(res.StructuredPayload, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload["truncated"] != true {
		t.Fatalf("payload truncation marker missing: %v", payload["truncated"])
	}
	if len(res.StructuredPayload) > maxResponseCapture+2048 {
		t.Fatalf("captured payload too large: %d", len(res.StructuredPayload))
	}
}

// TestUserinfoEndpointRefused pins the SEC-006 defense: an endpoint
// embedding credentials in the URL is rejected at construction so the
// userinfo can never be persisted as the durable target scope or
// surface as a Basic authorization header.
func TestUserinfoEndpointRefused(t *testing.T) {
	_, err := NewSink(Options{
		TargetID:  "hook",
		Endpoint:  "https://user:secret@example.invalid/hook",
		AuthType:  "bearer",
		SecretRef: "env:X",
	})
	var configErr *ConfigError
	if err == nil || !errors.As(err, &configErr) {
		t.Fatalf("userinfo endpoint must be a ConfigError, got %v", err)
	}
}

// TestSecretHeaderValueValidation proves a credential the transport
// cannot carry is refused before any transmission and never appears in
// the diagnostic (SEC-007).
func TestSecretHeaderValueValidation(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "bad\nsecret")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	res, err := f.sink.Submit(context.Background(), sampleRequest("k"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Classification != ports.SubmitDefiniteNotSubmitted {
		t.Fatalf("classification = %q, want definite_not_submitted", res.Classification)
	}
	if strings.Contains(res.Diagnostic, "bad\nsecret") {
		t.Fatalf("diagnostic leaks secret-derived text: %s", res.Diagnostic)
	}
	if len(f.requests()) != 0 {
		t.Fatalf("invalid secret must not invoke the endpoint")
	}
}

// TestMidBodyAbortMarksTruncated proves a response whose body dies
// mid-stream is marked truncated: partial evidence is never presented
// as complete.
func TestMidBodyAbortMarksTruncated(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"partial": `))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Aborting the handler mid-body closes the connection after the
		// partial bytes, producing a mid-stream read error on the client.
		panic("simulated mid-body abort")
	})
	res, err := f.sink.Submit(context.Background(), sampleRequest("k"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Classification != ports.SubmitAccepted {
		t.Fatalf("classification = %q", res.Classification)
	}
	var payload map[string]any
	if err := json.Unmarshal(res.StructuredPayload, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload["truncated"] != true {
		t.Fatalf("mid-body abort must mark the capture truncated: %v", payload)
	}
}

// TestZeroDataBodyAbortRecordsReadError proves the zero-byte body
// failure shape: no adapter text is fabricated as the body, the capture
// is marked truncated, and the read error is recorded explicitly.
func TestZeroDataBodyAbortRecordsReadError(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	f := newWebhookFixture(t, "bearer", "env:HOOK_TOKEN", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Push the headers out, then die before any body byte: the
		// client receives the status and its body read fails with zero
		// bytes captured.
		if c := http.NewResponseController(w); c != nil {
			_ = c.Flush()
		} else if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		panic("simulated pre-body abort")
	})
	res, err := f.sink.Submit(context.Background(), sampleRequest("k"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Classification != ports.SubmitAccepted {
		t.Fatalf("classification = %q", res.Classification)
	}
	var payload map[string]any
	if err := json.Unmarshal(res.StructuredPayload, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload["truncated"] != true {
		t.Fatalf("zero-data abort must mark the capture truncated: %v", payload)
	}
	if _, ok := payload["read_error"]; !ok {
		t.Fatalf("read_error marker missing: %v", payload)
	}
	if body, _ := payload["body"].(string); body != "" {
		t.Fatalf("no endpoint byte was captured, body must be empty: %q", body)
	}
}

// TestTransportDiagnosticRedacted proves the transport-failure
// diagnostics redact the resolved secret (SEC-007): a client error
// echoing request-derived material cannot leak the credential into the
// persisted diagnostic.
func TestTransportDiagnosticRedacted(t *testing.T) {
	t.Setenv("HOOK_TOKEN", "secret-value-1")
	secretive := &erroringClient{message: `net/http: invalid header field value "Bearer secret-value-1" for key Authorization`}
	sink, err := NewSink(Options{
		TargetID: "hook-main", Endpoint: "https://example.invalid/hook",
		AuthType: "bearer", SecretRef: "env:HOOK_TOKEN",
		Client: secretive,
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	res, err := sink.Submit(context.Background(), sampleRequest("k"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Classification != ports.SubmitUnknown {
		t.Fatalf("classification = %q, want unknown", res.Classification)
	}
	if strings.Contains(res.Diagnostic, "secret-value-1") {
		t.Fatalf("transport diagnostic leaks the secret: %s", res.Diagnostic)
	}
	if !strings.Contains(res.Diagnostic, "[redacted]") {
		t.Fatalf("diagnostic lacks the redaction marker: %s", res.Diagnostic)
	}
}

// erroringClient is a transport stub whose every request fails with a
// fixed error text.
type erroringClient struct{ message string }

func (c *erroringClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New(c.message)
}
