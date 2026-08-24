package hermeswebhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/secretresolver"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// DefaultSubmitTimeout bounds one webhook submission when the operator
// configures nothing (SEC-009).
const DefaultSubmitTimeout = 30 * time.Second

// DefaultIdempotencyHeader is the idempotency header used when the
// target configuration omits one (WHK-005).
const DefaultIdempotencyHeader = "Idempotency-Key"

// DefaultMaximumRequestBytes bounds one rendered request body
// (sink-adapter-contract.md §3 limits; SEC-009).
const DefaultMaximumRequestBytes = 262144

// maxResponseCapture bounds the response evidence retained in the
// structured payload.
const maxResponseCapture = 4096

// declaredCapabilities is the versioned capability set tied to the
// frozen E0-T4 webhook evidence (§9 of the public interface report):
// the adapter transmits the core's idempotency key verbatim but cannot
// prove durable acceptance, look up prior submissions, or project
// execution, because the receiving platform exposes no such contract.
var declaredCapabilities = ports.Capabilities{
	DurableAcceptance:      false,
	SubmitIdempotencyKey:   true,
	LookupByIdempotencyKey: false,
	LookupByExternalRef:    false,
	ResourceMutex:          false,
	ExecutionStatus:        false,
	Cancellation:           false,
	ResultReceipt:          false,
	MaximumRequestBytes:    DefaultMaximumRequestBytes,
}

// AuthKind enumerates the authentication transports (configuration-spec
// §5). The secret is resolved outside SQLite immediately before the
// request (SEC-006) and redacted from every captured diagnostic
// (SEC-007).
type AuthKind string

const (
	AuthBearer AuthKind = "bearer"
	AuthHeader AuthKind = "header"
)

// Options configures one webhook sink; exactly the hermes-webhook
// target fields of configuration-spec §5.
type Options struct {
	TargetID             string
	Endpoint             string
	AuthType             string
	SecretRef            string
	AuthHeaderName       string
	IdempotencyHeader    string
	SubmitTimeout        time.Duration
	RequiredCapabilities []string
	// Client is the HTTP transport seam; nil builds the strict default
	// client. Tests inject a client whose transport trusts their
	// loopback certificate authority; redirect following is disabled on
	// the default client and must be on any injected one.
	Client HTTPClient
}

// HTTPClient is the transport seam the sink talks through; *http.Client
// satisfies it.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Sink is the explicit Hermes webhook target adapter.
type Sink struct {
	id                string
	endpoint          *url.URL
	authKind          AuthKind
	secretRef         *config.SecretRef
	authHeaderName    string
	idempotencyHeader string
	timeout           time.Duration
	client            HTTPClient
}

// NewSink constructs the webhook adapter and applies its fail-closed
// gates: the endpoint must be an https URL, the authentication shape
// and header names must be valid, and every required capability must be
// provided by the declared set (HER-005).
func NewSink(opts Options) (*Sink, error) {
	if strings.TrimSpace(opts.TargetID) == "" {
		return nil, &ConfigError{Detail: "target ID is empty"}
	}
	if opts.Endpoint == "" {
		return nil, &ConfigError{Detail: "endpoint is empty"}
	}
	endpoint, err := url.Parse(opts.Endpoint)
	if err != nil || endpoint.Host == "" {
		return nil, &ConfigError{Detail: fmt.Sprintf("endpoint %q is not a URL", opts.Endpoint)}
	}
	if endpoint.Scheme != "https" {
		return nil, &ConfigError{Detail: fmt.Sprintf("endpoint %q must use https", opts.Endpoint)}
	}
	if endpoint.User != nil {
		// Embedded userinfo would be persisted verbatim as the durable
		// target scope and could additionally surface as a Basic
		// Authorization header under auth.type header — both violate the
		// secret posture (SEC-006). Credentials belong in auth.secret_ref.
		return nil, &ConfigError{Detail: fmt.Sprintf("endpoint %q must not embed userinfo; put the credential in auth.secret_ref", opts.Endpoint)}
	}
	if opts.SecretRef == "" {
		return nil, &ConfigError{Detail: "auth.secret_ref is empty"}
	}
	ref, err := config.ParseSecretRef(opts.SecretRef)
	if err != nil {
		return nil, &ConfigError{Detail: err.Error()}
	}
	var authKind AuthKind
	headerName := ""
	switch AuthKind(opts.AuthType) {
	case AuthBearer:
		authKind = AuthBearer
	case AuthHeader:
		authKind = AuthHeader
		headerName = opts.AuthHeaderName
		if !validHeaderName(headerName) {
			return nil, &ConfigError{Detail: fmt.Sprintf("auth.type header requires a valid auth.header_name (got %q)", opts.AuthHeaderName)}
		}
	default:
		return nil, &ConfigError{Detail: fmt.Sprintf("auth.type %q is not bearer or header", opts.AuthType)}
	}
	idempotencyHeader := opts.IdempotencyHeader
	if idempotencyHeader == "" {
		idempotencyHeader = DefaultIdempotencyHeader
	}
	if !validHeaderName(idempotencyHeader) {
		return nil, &ConfigError{Detail: fmt.Sprintf("idempotency_header %q is not a valid header name", idempotencyHeader)}
	}
	// The idempotency header must not collide with the authentication
	// or transport headers: the later Set would silently overwrite the
	// key, breaking endpoint-side dedup (WHK-005).
	reserved := map[string]bool{"authorization": true, "content-type": true, "host": true, "content-length": true}
	collides := reserved[strings.ToLower(idempotencyHeader)] || (authKind == AuthHeader && strings.EqualFold(idempotencyHeader, headerName))
	if collides {
		return nil, &ConfigError{Detail: fmt.Sprintf("idempotency_header %q collides with the authentication or transport headers; choose a dedicated header", idempotencyHeader)}
	}
	timeout := opts.SubmitTimeout
	if timeout == 0 {
		timeout = DefaultSubmitTimeout
	}
	if timeout < 0 {
		return nil, &ConfigError{Detail: fmt.Sprintf("submit_timeout %s is negative", timeout)}
	}
	if err := missingCapabilities(opts.TargetID, opts.RequiredCapabilities); err != nil {
		return nil, err
	}
	client := opts.Client
	if client == nil {
		client = NewStrictClient(timeout)
	}
	return &Sink{
		id:                opts.TargetID,
		endpoint:          endpoint,
		authKind:          authKind,
		secretRef:         ref,
		authHeaderName:    headerName,
		idempotencyHeader: idempotencyHeader,
		timeout:           timeout,
		client:            client,
	}, nil
}

// ID returns the configured target identity (WHK-002: one explicit
// target, selected only by its ID).
func (s *Sink) ID() string { return s.id }

// Type returns the webhook sink type.
func (s *Sink) Type() ports.SinkType { return ports.SinkHermesWebhook }

// Probe returns the static, evidence-tied capability declaration. It
// performs no network I/O: the receiving webhook platform cannot be
// assumed running on the target host (E0-T4 §9), so target
// reachability is proven only by submission and its evidence.
func (s *Sink) Probe(ctx context.Context) (ports.Capabilities, error) {
	return declaredCapabilities, nil
}

// Submit delivers the immutable logical task request as structured
// JSON over one authenticated HTTPS POST (WHK-002..005). The response
// mapping is conservative: 2xx proves transport acceptance but never
// durable acceptance (no verified contract proves persistence, WHK-004);
// definite request-refusal statuses prove rejection; ambiguous statuses
// and mid-flight transport failures are unknown (DUR-005).
func (s *Sink) Submit(ctx context.Context, req ports.TaskRequest) (ports.SubmitResult, error) {
	// DAT-009 (E7-T8 round-1): only this build's exact task-request
	// contract is submittable; a same-family unknown major is refused
	// before any transport work, exactly like a foreign family.
	if req.ContractVersion != ports.TaskRequestContractVersion {
		return ports.SubmitResult{Classification: ports.SubmitDefiniteNotSubmitted}, fmt.Errorf("unsupported task-request contract %q (this build speaks %q)", req.ContractVersion, ports.TaskRequestContractVersion)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return ports.SubmitResult{}, fmt.Errorf("rendering task request: %w", err)
	}
	if int64(len(body)) > declaredCapabilities.MaximumRequestBytes {
		return definiteNotSubmitted(fmt.Sprintf("rendered request is %d bytes, over the %d-byte bound", len(body), declaredCapabilities.MaximumRequestBytes)), nil
	}
	secret, err := secretresolver.Resolve(ctx, s.secretRef)
	if err != nil {
		return definiteNotSubmitted(err.Error()), nil
	}
	if !validHeaderValue(secret) {
		// A credential the transport cannot carry would fail later with
		// an error quoting the value; refusing here keeps it out of every
		// diagnostic (SEC-007).
		return definiteNotSubmitted("resolved secret contains characters invalid in a header value"), nil
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return definiteNotSubmitted(fmt.Sprintf("building request: %v", err)), nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// The core owns the idempotency key; the adapter transmits it
	// verbatim so the same dispatch retry presents the same key
	// (sink-adapter-contract.md §7, WHK-005).
	httpReq.Header.Set(s.idempotencyHeader, req.IdempotencyKey)
	switch s.authKind {
	case AuthBearer:
		httpReq.Header.Set("Authorization", "Bearer "+secret)
	case AuthHeader:
		httpReq.Header.Set(s.authHeaderName, secret)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		// Client error text can quote request-derived material, so it is
		// redacted against the resolved secret before it becomes a
		// persisted diagnostic (SEC-007).
		return classifyTransportFailure(err, secret), nil
	}
	defer resp.Body.Close()
	captured, readErr := captureBody(resp.Body)
	return classifyStatus(resp.StatusCode, captured, readErr, secret), nil
}

// LookupByIdempotencyKey is unsupported: the receiving platform
// exposes no verified public lookup contract (E0-T4 §9). Unknown
// webhook dispatches are resolved by the operator, never by
// submitting through another sink (DUR-008).
func (s *Sink) LookupByIdempotencyKey(ctx context.Context, key string) (ports.LookupResult, error) {
	return ports.LookupResult{}, ports.ErrCapabilityUnsupported
}

// LookupByExternalRef is unsupported for the same reason as
// LookupByIdempotencyKey.
func (s *Sink) LookupByExternalRef(ctx context.Context, ref string) (ports.LookupResult, error) {
	return ports.LookupResult{}, ports.ErrCapabilityUnsupported
}

// GetExecution is unsupported: webhook delivery carries no execution
// projection contract.
func (s *Sink) GetExecution(ctx context.Context, ref string) (ports.ExecutionProjection, error) {
	return ports.ExecutionProjection{}, ports.ErrCapabilityUnsupported
}

// definiteRejectStatuses are the HTTP statuses that prove the endpoint
// refused to process the request: resending the identical request
// cannot succeed, so the attempt is a definite rejection with the
// status as structured evidence.
var definiteRejectStatuses = map[int]bool{
	http.StatusBadRequest:            true, // malformed request
	http.StatusUnauthorized:          true,
	http.StatusForbidden:             true,
	http.StatusNotFound:              true, // no such route
	http.StatusMethodNotAllowed:      true,
	http.StatusNotAcceptable:         true,
	http.StatusGone:                  true,
	http.StatusRequestEntityTooLarge: true,
	http.StatusRequestURITooLong:     true,
	http.StatusUnsupportedMediaType:  true,
	http.StatusUnprocessableEntity:   true,
}

// classifyStatus maps one received HTTP status onto the adapter result
// classification. The structured payload carries only captured
// endpoint bytes as the body — never adapter-fabricated text — and a
// read error leaves an explicit read_error marker with truncated set,
// so incomplete evidence is never presented as complete. Every
// retained byte is redacted against the resolved secret before it
// becomes evidence (SEC-007).
func classifyStatus(status int, body []byte, readErr error, secret string) ports.SubmitResult {
	payload := map[string]any{
		"status":    status,
		"body":      redact(secret, string(body)),
		"truncated": readErr != nil,
	}
	if readErr != nil {
		payload["read_error"] = redact(secret, readErr.Error())
	}
	structured, _ := json.Marshal(payload)
	diagnostic := "http status " + strconv.Itoa(status)
	switch {
	case status >= 200 && status < 300:
		// Transport acceptance only: no verified response contract
		// proves durable task acceptance (WHK-004, E0-T4 §9).
		return ports.SubmitResult{
			Classification:    ports.SubmitAccepted,
			Durable:           ports.DurableFalse,
			StructuredPayload: structured,
			Diagnostic:        diagnostic,
		}
	case definiteRejectStatuses[status]:
		return ports.SubmitResult{
			Classification:    ports.SubmitRejected,
			Durable:           ports.DurableFalse,
			StructuredPayload: structured,
			Diagnostic:        diagnostic,
		}
	case status >= 300 && status < 400:
		// Redirects are not followed; the endpoint declined delivery at
		// the configured URL, which is a definite routing rejection.
		return ports.SubmitResult{
			Classification:    ports.SubmitRejected,
			Durable:           ports.DurableFalse,
			StructuredPayload: structured,
			Diagnostic:        diagnostic + " (redirect not followed)",
		}
	default:
		// 408, 409, 429, and 5xx cannot prove acceptance or
		// non-acceptance without a documented contract (DUR-005).
		return ports.SubmitResult{
			Classification:    ports.SubmitUnknown,
			Durable:           ports.DurableUnknown,
			StructuredPayload: structured,
			Diagnostic:        diagnostic,
		}
	}
}

// classifyTransportFailure maps one client error onto the provable
// phases: failures before any request bytes were transmitted (DNS,
// dial, TLS handshake) are definite non-submission; everything else —
// mid-request cancellation, resets after transmission, read timeouts —
// is unknown (sink-adapter-contract.md §5). The diagnostic is redacted
// because Go error text can echo request-derived values (SEC-007).
func classifyTransportFailure(err error, secret string) ports.SubmitResult {
	if provableNoSend(err) {
		return ports.SubmitResult{
			Classification: ports.SubmitDefiniteNotSubmitted,
			Durable:        ports.DurableFalse,
			Diagnostic:     "transport failure before request transmission: " + redact(secret, err.Error()),
		}
	}
	return ports.SubmitResult{
		Classification: ports.SubmitUnknown,
		Durable:        ports.DurableUnknown,
		Diagnostic:     "transport failure after possible transmission: " + redact(secret, err.Error()),
	}
}

// definiteNotSubmitted builds the local, provably-not-sent result.
func definiteNotSubmitted(detail string) ports.SubmitResult {
	return ports.SubmitResult{
		Classification: ports.SubmitDefiniteNotSubmitted,
		Durable:        ports.DurableFalse,
		Diagnostic:     detail,
	}
}

// captureBody reads at most maxResponseCapture bytes of the response
// body and reports the read error alongside the bytes: any read
// error — including one after partial data — leaves the payload marked
// truncated with an explicit read_error, and adapter text is never
// fabricated into the captured body.
func captureBody(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxResponseCapture+1))
	if err != nil {
		return data, err
	}
	if len(data) > maxResponseCapture {
		return data[:maxResponseCapture], fmt.Errorf("response body exceeded the %d-byte capture bound", maxResponseCapture)
	}
	return data, nil
}

// redact removes every occurrence of the secret from text; an empty
// secret redacts nothing.
func redact(secret, text string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redacted]")
}

// validHeaderName accepts RFC 9110 field-name tokens: every rune must be
// a tchar (ALPHA, DIGIT, or one of !#$%&'*+-.^_`|~). The complete
// allowlist rejects every other separator, quote, backslash, space,
// control character, and non-ASCII rune before the configuration can
// reach a submission attempt (E9-T7, D-023 F3); the configuration
// schema carries the equivalent pattern.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !isTchar(r) {
			return false
		}
	}
	return true
}

func isTchar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// validHeaderValue accepts RFC 9110 field-value content: visible
// bytes, horizontal tab, and obs-text; a credential outside this set
// cannot be transmitted and must never reach an error message.
func validHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b == '\t' || (b >= 0x20 && b != 0x7f) {
			continue
		}
		return false
	}
	return true
}
