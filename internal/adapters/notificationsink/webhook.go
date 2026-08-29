package notificationsink

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeswebhook"
	"github.com/irootkernel/agent-dispatch/internal/adapters/secretresolver"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Webhook defaults (SEC-013): one bounded request, one bounded
// response capture, and one bounded execution time. The endpoint is
// HTTPS-only, redirects are surfaced as definite routing refusals
// (never followed), and ambient proxies are ignored — the strict client
// is shared with the hermes-webhook target transport.
const (
	// DefaultDeliveryTimeout bounds one webhook notification delivery.
	DefaultDeliveryTimeout = 15 * time.Second
	// DefaultIdempotencyHeader carries the stable notification delivery
	// identity (NTF-007) when the sink declares no dedicated header.
	DefaultIdempotencyHeader = "X-Agent-Dispatch-Notification-Key"
	// maxPayloadBytes bounds one delivered notification payload; the
	// stored projection is already bounded far below this.
	maxPayloadBytes = 65536
	// maxResponseCapture bounds the response evidence retained in the
	// attempt's diagnostic fields.
	maxResponseCapture = 4096
)

// ConfigError is the fail-closed construction failure class: the sink
// declaration itself is defective (exit 3 at the CLI boundary).
type ConfigError struct{ Detail string }

func (e *ConfigError) Error() string { return e.Detail }

// WebhookOptions configures one notification webhook sink; exactly the
// webhook sink fields of the route's notifications block
// (configuration-spec §14).
type WebhookOptions struct {
	SinkID            string
	Endpoint          string
	AuthType          string
	SecretRef         string
	AuthHeaderName    string
	IdempotencyHeader string
	Timeout           time.Duration
	// Client is the HTTP transport seam; nil builds the strict default
	// (system roots, one deadline, no proxy, redirects refused). Tests
	// inject a loopback-CA client.
	Client hermeswebhook.HTTPClient
}

// WebhookSink is the authenticated HTTPS notification sink: one POST of
// the stored notification-event/v1 payload per attempt, the stable
// idempotency key in a dedicated header, the secret resolved only at
// send time and redacted from every captured diagnostic (SEC-011,
// SEC-012, SEC-013).
type WebhookSink struct {
	id                string
	endpoint          *url.URL
	authKind          string
	secretRef         *config.SecretRef
	authHeaderName    string
	idempotencyHeader string
	timeout           time.Duration
	client            hermeswebhook.HTTPClient
}

// NewWebhookSink constructs the webhook adapter and applies its
// fail-closed gates: an https endpoint without embedded userinfo, a
// valid authentication reference, and a dedicated idempotency header
// that collides with neither the authentication nor the transport
// headers.
func NewWebhookSink(opts WebhookOptions) (*WebhookSink, error) {
	if strings.TrimSpace(opts.SinkID) == "" {
		return nil, &ConfigError{Detail: "sink id is empty"}
	}
	if opts.Endpoint == "" {
		return nil, &ConfigError{Detail: "sink endpoint is empty"}
	}
	endpoint, err := url.Parse(opts.Endpoint)
	if err != nil || endpoint.Host == "" {
		return nil, &ConfigError{Detail: fmt.Sprintf("endpoint %q is not a URL", opts.Endpoint)}
	}
	if endpoint.Scheme != "https" {
		return nil, &ConfigError{Detail: fmt.Sprintf("endpoint %q must use https (SEC-013)", opts.Endpoint)}
	}
	if endpoint.User != nil {
		return nil, &ConfigError{Detail: fmt.Sprintf("endpoint %q must not embed userinfo; put the credential in auth.secret_ref", opts.Endpoint)}
	}
	if opts.SecretRef == "" {
		return nil, &ConfigError{Detail: "sink auth.secret_ref is empty"}
	}
	ref, err := config.ParseSecretRef(opts.SecretRef)
	if err != nil {
		return nil, &ConfigError{Detail: err.Error()}
	}
	authHeaderName := ""
	switch opts.AuthType {
	case "bearer":
	case "header":
		authHeaderName = opts.AuthHeaderName
		if authHeaderName == "" || strings.ContainsAny(authHeaderName, " \t,;:") || strings.Contains(authHeaderName, "(") {
			return nil, &ConfigError{Detail: fmt.Sprintf("auth.type header requires a valid auth.header_name (got %q)", opts.AuthHeaderName)}
		}
	default:
		return nil, &ConfigError{Detail: fmt.Sprintf("auth.type %q is not bearer or header", opts.AuthType)}
	}
	idempotencyHeader := opts.IdempotencyHeader
	if idempotencyHeader == "" {
		idempotencyHeader = DefaultIdempotencyHeader
	}
	reserved := map[string]bool{"authorization": true, "content-type": true, "host": true, "content-length": true}
	if reserved[strings.ToLower(idempotencyHeader)] || (authHeaderName != "" && strings.EqualFold(idempotencyHeader, authHeaderName)) {
		return nil, &ConfigError{Detail: fmt.Sprintf("the idempotency header %q collides with the authentication or transport headers; choose a dedicated header", idempotencyHeader)}
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultDeliveryTimeout
	}
	if timeout < 0 {
		return nil, &ConfigError{Detail: fmt.Sprintf("delivery timeout %s is negative", timeout)}
	}
	client := opts.Client
	if client == nil {
		client = hermeswebhook.NewStrictClient(timeout)
	}
	return &WebhookSink{
		id: opts.SinkID, endpoint: endpoint, authKind: opts.AuthType, secretRef: ref,
		authHeaderName: authHeaderName, idempotencyHeader: idempotencyHeader,
		timeout: timeout, client: client,
	}, nil
}

// Deliver performs one bounded HTTPS POST of the notification payload
// with the stable idempotency key and classifies the result onto the
// four-way notification outcome contract.
func (s *WebhookSink) Deliver(ctx context.Context, in ports.NotificationDelivery) ports.NotificationAttemptInput {
	out := ports.NotificationAttemptInput{NotificationID: in.NotificationID}
	if int64(len(in.PayloadJSON)) > maxPayloadBytes {
		out.Outcome = records.NotificationRetryableOutcome
		out.ErrorCode = "payload_over_bound"
		return out
	}
	secret, err := secretresolver.Resolve(ctx, s.secretRef)
	if err != nil {
		// The secret reference could not be resolved before any bytes
		// left this host: a retryable pre-delivery failure whose text
		// quotes no credential material.
		out.Outcome = records.NotificationRetryableOutcome
		out.ErrorCode = "secret_reference_unresolvable"
		return out
	}
	body := []byte(in.PayloadJSON)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		out.Outcome = records.NotificationRetryableOutcome
		out.ErrorCode = "request_build_failed"
		return out
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// Every attempt of one notification carries the same stable delivery
	// identity (NTF-007): the endpoint can deduplicate the at-least-once
	// retries of an ambiguous transport outcome.
	httpReq.Header.Set(s.idempotencyHeader, in.IdempotencyKey)
	switch s.authKind {
	case "bearer":
		httpReq.Header.Set("Authorization", "Bearer "+secret)
	case "header":
		httpReq.Header.Set(s.authHeaderName, secret)
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	resp, err := s.client.Do(httpReq.WithContext(deliveryCtx))
	if err != nil {
		return classifyTransportFailure(err, secret, s.endpoint.String(), out)
	}
	defer resp.Body.Close()
	captured, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseCapture))
	return classifyStatus(resp.StatusCode, captured, readErr, secret, out)
}

// definiteRefusalStatuses are the HTTP statuses that prove the endpoint
// refused this notification: resending the identical request cannot
// succeed, so the notification resolves refused and stays inspectable.
var definiteRefusalStatuses = map[int]bool{
	http.StatusBadRequest:            true,
	http.StatusUnauthorized:          true,
	http.StatusForbidden:             true,
	http.StatusNotFound:              true,
	http.StatusMethodNotAllowed:      true,
	http.StatusNotAcceptable:         true,
	http.StatusGone:                  true,
	http.StatusRequestEntityTooLarge: true,
	http.StatusRequestURITooLong:     true,
	http.StatusUnsupportedMediaType:  true,
	http.StatusUnprocessableEntity:   true,
}

// classifyStatus maps one received webhook status onto the notification
// outcome contract: 2xx is a definite success, the refusal set is a
// definite refusal, redirects are definite routing refusals (the
// authenticated request never follows them), and everything else —
// throttling, server errors, odd statuses — is retryable. The response
// digest binds the captured endpoint bytes; every retained byte is
// redacted against the resolved secret (SEC-007 posture, SEC-011).
func classifyStatus(status int, body []byte, readErr error, secret string, out ports.NotificationAttemptInput) ports.NotificationAttemptInput {
	out.ErrorCode = "http_status_" + strconv.Itoa(status)
	if len(body) > 0 {
		out.ResponseDigest = digestOf(redact(secret, string(body)))
	}
	switch {
	case status >= 200 && status < 300:
		out.Outcome = records.NotificationDeliveredOutcome
		out.ErrorCode = ""
	case status >= 300 && status < 400:
		// The strict client never follows redirects; a redirecting
		// endpoint is a definite routing refusal, never an re-delivery
		// elsewhere with the credential attached (SEC-013).
		out.Outcome = records.NotificationRefusedOutcome
		out.ErrorCode = "http_redirect_refused"
	case definiteRefusalStatuses[status]:
		out.Outcome = records.NotificationRefusedOutcome
	default:
		out.Outcome = records.NotificationRetryableOutcome
	}
	if readErr != nil {
		out.Outcome = records.NotificationAmbiguousOutcome
		out.ErrorCode = "response_read_failed"
	}
	return out
}

// classifyTransportFailure maps one client error: failures provably
// before any request bytes left this host (name resolution, dialing,
// the TLS handshake) are retryable pre-delivery failures; every other
// failure leaves the delivery state unprovable — ambiguous, retried
// under the same idempotency key (NTF-007, DUR-005 posture). The error
// text is redacted against the resolved secret AND the configured
// endpoint URL before it becomes a diagnostic: a URL carrying query
// material must not persist into notification evidence (SEC-007/SEC-011
// posture).
func classifyTransportFailure(err error, secret, endpoint string, out ports.NotificationAttemptInput) ports.NotificationAttemptInput {
	redacted := redactText(endpoint, "(endpoint)", redact(secret, err.Error()))
	// The resolved address can stand in for the endpoint after DNS: the
	// host portion redacts too, so no network detail of the configured
	// destination persists into notification evidence.
	if host := endpointHost(endpoint); host != "" {
		redacted = redactText(host, "(endpoint-host)", redacted)
	}
	if provableNoSend(err) {
		out.Outcome = records.NotificationRetryableOutcome
		out.ErrorCode = boundedCode(redacted)
		return out
	}
	out.Outcome = records.NotificationAmbiguousOutcome
	out.ErrorCode = boundedCode(redacted)
	return out
}

// endpointHost extracts the scheme-less host of the configured endpoint
// for address redaction.
func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

// boundedCode keeps one bounded, secret-free error class marker,
// truncated on a rune boundary so no partial multi-byte sequence
// persists.
func boundedCode(redacted string) string {
	code := strings.TrimSpace(redacted)
	if len(code) > 128 {
		var b strings.Builder
		for _, r := range code {
			if b.Len()+utf8.RuneLen(r) > 128 {
				break
			}
			b.WriteRune(r)
		}
		code = b.String()
	}
	if code == "" {
		return "transport_failed"
	}
	return code
}
