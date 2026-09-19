package synccontractcheck

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryBundle(t *testing.T) {
	if err := Check("../../docs/contracts/sync-provider-v1"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifySumsDetectsDrift(t *testing.T) {
	dir := t.TempDir()
	body := []byte("contract")
	if err := os.WriteFile(filepath.Join(dir, "a.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(fmt.Sprintf("%x  a.json\n", sum)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(dir, []string{"a.json"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte("drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(dir, []string{"a.json"}); err == nil || !strings.Contains(err.Error(), "checksum drift") {
		t.Fatalf("expected drift, got %v", err)
	}
}

func TestCheckRejectsArtifactOmission(t *testing.T) {
	dir := copyRepositoryBundle(t)
	path := filepath.Join(dir, "bundle.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "    \"trust-fixtures.json\",\n", "", 1))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Check(dir); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("expected allowlist failure, got %v", err)
	}
}

func TestCheckRejectsPeerDescriptorDriftWithUpdatedChecksum(t *testing.T) {
	dir := copyRepositoryBundle(t)
	path := filepath.Join(dir, "peer.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "/v1/sync/nudges", "/v1/sync/changed", 1))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	rewriteChecksum(t, dir, "peer.json")
	if err := Check(dir); err == nil || !strings.Contains(err.Error(), "frozen descriptor") {
		t.Fatalf("expected peer drift failure, got %v", err)
	}
}

func TestCheckRejectsHybridResultWithUpdatedChecksum(t *testing.T) {
	dir := copyRepositoryBundle(t)
	path := filepath.Join(dir, "results.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"state":"disabled"`, `"state":"disabled","proof":"contradiction"`, 1))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	rewriteChecksum(t, dir, "results.json")
	if err := Check(dir); err == nil || !strings.Contains(err.Error(), "result contract mismatch") {
		t.Fatalf("expected result drift failure, got %v", err)
	}
}

func TestCheckRejectsSchemaIdentityDriftWithUpdatedChecksum(t *testing.T) {
	dir := copyRepositoryBundle(t)
	rel := "../../schemas/sync-nudge.schema.json"
	path := filepath.Join(dir, rel)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "urn:agent-dispatch:schema:sync-nudge:v1", "urn:agent-dispatch:schema:changed:v1", 1))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	rewriteChecksum(t, dir, rel)
	if err := Check(dir); err == nil || !strings.Contains(err.Error(), "schema link identity mismatch") {
		t.Fatalf("expected schema-link failure, got %v", err)
	}
}

func copyRepositoryBundle(t *testing.T) string {
	t.Helper()
	source := "../../docs/contracts/sync-provider-v1"
	docs := filepath.Join(t.TempDir(), "docs")
	dir := filepath.Join(docs, "contracts", "sync-provider-v1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range append([]string{"bundle.json", "SHA256SUMS"}, expectedArtifacts...) {
		src := filepath.Join(source, filepath.FromSlash(rel))
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func rewriteChecksum(t *testing.T, dir, rel string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	sumsPath := filepath.Join(dir, "SHA256SUMS")
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	for i, line := range lines {
		if strings.HasSuffix(line, "  "+rel) {
			lines[i] = fmt.Sprintf("%x  %s", sum, rel)
		}
	}
	if err := os.WriteFile(sumsPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
