package secretresolver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestE9T3SecretFileOwnershipChecked proves L-15: a secret file owned
// by another uid never resolves — a swapped or planted file is a
// configuration defect, not a secret to read. Same-uid resolution is
// already covered by TestResolveFile (tests run as the creating user).
func TestE9T3SecretFileOwnershipChecked(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("transferring file ownership to another uid requires root")
	}
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, 1, 1); err != nil {
		t.Skipf("cannot transfer ownership on this host: %v", err)
	}
	_, err := Resolve(context.Background(), ref(t, "file:"+path))
	if err == nil {
		t.Fatal("a secret file owned by another uid must never resolve")
	}
	if !strings.Contains(err.Error(), "owned by uid") {
		t.Fatalf("the refusal must name the ownership defect, got %v", err)
	}
}
