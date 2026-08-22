package hermeswebhook

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"
)

// NewStrictClient builds the production HTTP client: the full submit
// deadline as one end-to-end timeout, system root certificate
// verification (no verification bypass exists in this build), no proxy
// (the configured endpoint is the only declared destination), and
// redirect following disabled so a redirecting endpoint is surfaced as
// a definite routing rejection instead of silently re-delivering the
// authenticated request elsewhere (SEC-007: credentials must not leak
// to an undeclared redirect target).
func NewStrictClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:           nil,
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// provableNoSend reports whether one client error proves no request
// bytes were transmitted: name resolution, connection dialing, and the
// TLS handshake all precede HTTP transmission, so their failures are
// definite non-submission. Every other failure — cancellation or reset
// after the request started, response read timeouts — leaves the
// delivery state unprovable (sink-adapter-contract.md §5, DUR-005).
func provableNoSend(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	// Handshake failures precede HTTP transmission: the request was
	// never delivered when certificate verification, hostname checks,
	// record framing, or TLS alerts end the handshake.
	var certVerify *tls.CertificateVerificationError
	if errors.As(err, &certVerify) {
		return true
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return true
	}
	var hostname x509.HostnameError
	if errors.As(err, &hostname) {
		return true
	}
	var recordHeader tls.RecordHeaderError
	if errors.As(err, &recordHeader) {
		return true
	}
	var alert tls.AlertError
	return errors.As(err, &alert)
}
