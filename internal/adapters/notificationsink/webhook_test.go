package notificationsink

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E13-T2 webhook sink coverage (SEC-011..013, NTF-006/NTF-007,
// TST-014): the four-way outcome classification, the stable
// idempotency header, transport strictness, and payload secrecy.

// webhookTestFixture is one loopback HTTPS endpoint plus the notification
// sink pointed at it; the client verifies the server's own certificate
// through an explicit root pool (the production verification path, not a
// bypass).
type webhookTestFixture struct {
	server   *httptest.Server
	sink     *WebhookSink
	requests []capturedDelivery
	mu       sync.Mutex
}

type capturedDelivery struct {
	body   string
	header http.Header
}

func newWebhookTestFixture(t *testing.T, handler http.HandlerFunc) *webhookTestFixture {
	t.Helper()
	f := &webhookTestFixture{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, capturedDelivery{body: string(body), header: r.Header.Clone()})
		f.mu.Unlock()
		if handler != nil {
			handler(w, r)
		}
	}))
	f.server = server
	t.Cleanup(server.Close)
	sink, err := NewWebhookSink(WebhookOptions{
		SinkID:    "ops-webhook",
		Endpoint:  server.URL,
		AuthType:  "bearer",
		SecretRef: "env:NOTIFICATION_TEST_TOKEN",
		Timeout:   5 * time.Second,
		Client:    loopbackDeliveryClient(server, 5*time.Second),
	})
	if err != nil {
		t.Fatalf("NewWebhookSink: %v", err)
	}
	f.sink = sink
	t.Setenv("NOTIFICATION_TEST_TOKEN", "sekrit-token-value")
	return f
}

func loopbackDeliveryClient(server *httptest.Server, timeout time.Duration) *http.Client {
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

func (f *webhookTestFixture) deliveries() []capturedDelivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]capturedDelivery(nil), f.requests...)
}

func sampleDelivery(key string) ports.NotificationDelivery {
	return ports.NotificationDelivery{
		NotificationID: "ntf-" + strings.Repeat("a", 64),
		RouteID:        "wiki-maintenance",
		Event:          records.EventWorkCompleted,
		SinkID:         "ops-webhook",
		IdempotencyKey: key,
		PayloadJSON:    `{"schema_version":"agent-dispatch.notification-event/v1","notification_id":"ntf-` + strings.Repeat("a", 64) + `","route_id":"wiki-maintenance","event":"work_completed","sink_id":"ops-webhook","created_at":"2026-08-30T09:00:00Z","source":{"dispatch_id":"dsp-1"}}`,
	}
}

func TestWebhookSinkClassifiesStatuses(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		outcome records.NotificationAttemptOutcome
	}{
		{"accepted", http.StatusOK, records.NotificationDeliveredOutcome},
		{"created", http.StatusCreated, records.NotificationDeliveredOutcome},
		{"refused", http.StatusForbidden, records.NotificationRefusedOutcome},
		{"unauthorized", http.StatusUnauthorized, records.NotificationRefusedOutcome},
		{"gone", http.StatusGone, records.NotificationRefusedOutcome},
		{"server error", http.StatusInternalServerError, records.NotificationRetryableOutcome},
		{"throttled", http.StatusTooManyRequests, records.NotificationRetryableOutcome},
		{"redirect", http.StatusFound, records.NotificationRefusedOutcome},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newWebhookTestFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			})
			attempt := f.sink.Deliver(context.Background(), sampleDelivery("ntfidem-x"))
			if attempt.Outcome != tc.outcome {
				t.Fatalf("status %d must classify %s, got %s (%s)", tc.status, tc.outcome, attempt.Outcome, attempt.ErrorCode)
			}
		})
	}
}

func TestWebhookSinkSendsPayloadWithStableIdempotencyKey(t *testing.T) {
	f := newWebhookTestFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	delivery := sampleDelivery("ntfidem-stable")
	first := f.sink.Deliver(context.Background(), delivery)
	if first.Outcome != records.NotificationDeliveredOutcome {
		t.Fatalf("first delivery must succeed: %+v", first)
	}
	// The retry of an ambiguous outcome presents the SAME key (NTF-007):
	// the endpoint sees two requests with identical headers and body.
	second := f.sink.Deliver(context.Background(), delivery)
	if second.Outcome != records.NotificationDeliveredOutcome {
		t.Fatalf("second delivery must succeed: %+v", second)
	}
	captured := f.deliveries()
	if len(captured) != 2 {
		t.Fatalf("both attempts must reach the endpoint: %d", len(captured))
	}
	for i, c := range captured {
		if got := c.header.Get(DefaultIdempotencyHeader); got != "ntfidem-stable" {
			t.Fatalf("attempt %d must carry the stable idempotency key: %q", i+1, got)
		}
		if got := c.header.Get("Authorization"); got != "Bearer sekrit-token-value" {
			t.Fatalf("attempt %d must authenticate: %q", i+1, got)
		}
		if c.body != delivery.PayloadJSON {
			t.Fatalf("attempt %d must deliver the stored payload verbatim: %s", i+1, c.body)
		}
	}
}

func TestWebhookSinkRedactsSecretFromDiagnostics(t *testing.T) {
	f := newWebhookTestFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		// The endpoint echoes the credential back: no captured byte may
		// retain it (SEC-011/SEC-007 posture).
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("token rejected: sekrit-token-value"))
	})
	attempt := f.sink.Deliver(context.Background(), sampleDelivery("ntfidem-redact"))
	if attempt.Outcome != records.NotificationRetryableOutcome {
		t.Fatalf("a 500 must classify retryable: %+v", attempt)
	}
	if strings.Contains(attempt.ErrorCode, "sekrit-token-value") {
		t.Fatalf("the error code must not retain the secret: %q", attempt.ErrorCode)
	}
	if attempt.ResponseDigest == "" {
		t.Fatal("the response digest must bind the captured endpoint bytes")
	}
}

func TestWebhookSinkFailClosedConstruction(t *testing.T) {
	if _, err := NewWebhookSink(WebhookOptions{SinkID: "s", Endpoint: "http://notify.example.invalid", AuthType: "bearer", SecretRef: "env:X"}); err == nil {
		t.Fatal("a plain-http endpoint must fail closed (SEC-013)")
	}
	if _, err := NewWebhookSink(WebhookOptions{SinkID: "s", Endpoint: "https://u:p@notify.example.invalid", AuthType: "bearer", SecretRef: "env:X"}); err == nil {
		t.Fatal("embedded userinfo must fail closed")
	}
	if _, err := NewWebhookSink(WebhookOptions{SinkID: "s", Endpoint: "https://notify.example.invalid", AuthType: "bearer"}); err == nil {
		t.Fatal("a missing secret reference must fail closed")
	}
	if _, err := NewWebhookSink(WebhookOptions{SinkID: "s", Endpoint: "https://notify.example.invalid", AuthType: "bearer", SecretRef: "env:X", IdempotencyHeader: "Authorization"}); err == nil {
		t.Fatal("an idempotency header colliding with the transport headers must fail closed")
	}
}

func TestWebhookSinkUnresolvableSecretIsRetryable(t *testing.T) {
	f := newWebhookTestFixture(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	t.Setenv("NOTIFICATION_TEST_TOKEN", "") // env: resolves empty → unresolvable
	attempt := f.sink.Deliver(context.Background(), sampleDelivery("ntfidem-secret"))
	if attempt.Outcome != records.NotificationRetryableOutcome || attempt.ErrorCode != "secret_reference_unresolvable" {
		t.Fatalf("an unresolvable secret must be a retryable pre-delivery failure: %+v", attempt)
	}
}

func TestLogSinkDeliversPayloadLine(t *testing.T) {
	var b strings.Builder
	sink := &LogSink{ID: "ops-log", Out: &b}
	delivery := sampleDelivery("ntfidem-log")
	attempt := sink.Deliver(context.Background(), delivery)
	if attempt.Outcome != records.NotificationDeliveredOutcome {
		t.Fatalf("the log sink must deliver: %+v", attempt)
	}
	if !strings.HasPrefix(b.String(), delivery.PayloadJSON) || !strings.HasSuffix(b.String(), "\n") {
		t.Fatalf("the log sink must append the payload as one JSON line: %q", b.String())
	}
	broken := &LogSink{ID: "ops-log"}
	if attempt := broken.Deliver(context.Background(), delivery); attempt.Outcome != records.NotificationRetryableOutcome {
		t.Fatalf("an unwritable log sink must classify retryable: %+v", attempt)
	}
}

func TestWebhookSinkResponseDigestIsDeterministic(t *testing.T) {
	f := newWebhookTestFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	first := f.sink.Deliver(context.Background(), sampleDelivery("ntfidem-digest"))
	second := f.sink.Deliver(context.Background(), sampleDelivery("ntfidem-digest"))
	if first.ResponseDigest == "" || first.ResponseDigest != second.ResponseDigest {
		t.Fatalf("the response digest must be deterministic: %q vs %q", first.ResponseDigest, second.ResponseDigest)
	}
}
