package notificationsink

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strings"
)

// redact removes every occurrence of the resolved secret from a text
// before it becomes a persisted diagnostic (SEC-007 posture, SEC-011).
func redact(secret, text string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "«redacted»")
}

// redactText replaces every occurrence of one literal (for example the
// configured endpoint URL) with a placeholder so URL-embedded material
// never persists into diagnostics.
func redactText(needle, placeholder, text string) string {
	if needle == "" {
		return text
	}
	return strings.ReplaceAll(text, needle, placeholder)
}

// digestOf digests one bounded diagnostic byte slice for the attempt's
// response evidence.
func digestOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// provableNoSend reports whether one client error proves no request
// bytes were transmitted: name resolution, connection dialing, and the
// TLS handshake all precede HTTP transmission, so their failures are
// retryable pre-delivery failures; every other failure leaves the
// delivery outcome unprovable (ambiguous).
func provableNoSend(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
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
