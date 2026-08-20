// Package stubhermes writes a stateful fake Hermes executable for
// integration tests: it answers the frozen --version first line,
// implements board-scoped kanban create with idempotency-key
// deduplication (the same key returns the original task), and serves
// show with the frozen no-such-task absence behavior. It is test
// infrastructure and never wired into production composition.
package stubhermes

import (
	"os"
	"path/filepath"
	"testing"
)

// Write creates the stub binary and returns its path. Task ids are
// deterministic (t_00000001, t_00000002, ...) in creation order.
func Write(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
DIR="` + filepath.Join(dir, "state") + `"
mkdir -p "$DIR"
if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.19.1 (2026.7.30)\n'; exit 0; fi
# argv: kanban --board <b> <cmd> ...
cmd=$4
case "$cmd" in
  create)
    title=$5
    key=""
    mode=""
    i=6
    while [ $i -le $# ]; do
      eval "a=\${$i}"
      if [ "$mode" = "key" ]; then key="$a"; mode=""; fi
      if [ "$a" = "--idempotency-key" ]; then mode="key"; fi
      i=$((i+1))
    done
    if [ -n "$key" ] && [ -f "$DIR/key-$key" ]; then cat "$DIR/key-$key"; exit 0; fi
    n=$(cat "$DIR/count" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$DIR/count"
    id=$(printf 't_%08x' "$n")
    cat > "$DIR/id-$id" <<JSON
{"id":"$id","title":"$title","status":"ready","created_at":1787142146,"mutex_key":"wiki-publish","skills":["llm-wiki"]}
JSON
    if [ -n "$key" ]; then cp "$DIR/id-$id" "$DIR/key-$key"; fi
    cat "$DIR/id-$id"
    exit 0
    ;;
  show)
    id=$5
    if [ -f "$DIR/id-$id" ]; then printf '{"task":'; cat "$DIR/id-$id"; printf '}'; exit 0; fi
    echo "no such task: $id" >&2; exit 1
    ;;
  *)
    echo "stub: unsupported command $cmd" >&2; exit 2
    ;;
esac
`
	bin := filepath.Join(dir, "hermes")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}
