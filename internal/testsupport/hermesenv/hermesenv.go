// Package hermesenv guards environment-dependent tests that need the
// real installed Hermes inside the runtime-verified support set.
package hermesenv

import (
	"os/exec"
	"strings"
	"testing"
)

// SkipUnlessSupportedHermes resolves the real `hermes` binary on PATH
// and skips the test as a TST-007 environment-dependent evidence gap
// when it is absent, unprobeable, or rejected by the caller's
// supported-set predicate over the first `hermes --version` line. The
// version judgment stays with the owning adapter package; widening the
// verified set is a fresh E0-T4 probe, not a test override.
func SkipUnlessSupportedHermes(t testing.TB, supported func(firstVersionLine string) bool) {
	bin, err := exec.LookPath("hermes")
	if err != nil {
		t.Skip("hermes binary not available")
	}
	verOut, verr := exec.Command(bin, "--version").Output()
	if verr != nil {
		t.Skipf("hermes --version unavailable: %v", verr)
	}
	firstLine := strings.SplitN(strings.TrimSpace(string(verOut)), "\n", 2)[0]
	if !supported(firstLine) {
		t.Skipf("installed hermes (%s) is outside the verified support set (environment-dependent evidence gap, TST-007)", firstLine)
	}
}
