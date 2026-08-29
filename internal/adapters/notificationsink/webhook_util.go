package notificationsink

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeswebhook"
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

// provableNoSend delegates to the shared strict-transport
// classification the hermes-webhook target adapter owns (E13 epic
// audit: one classification, not two verbatim copies).
func provableNoSend(err error) bool {
	return hermeswebhook.ProvableNoSend(err)
}
