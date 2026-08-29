// Package stubhermes writes a stateful fake Hermes executable for
// integration tests: it answers the frozen --version first line,
// implements board-scoped kanban create with idempotency-key
// deduplication (the same key returns the original task), serves show
// with the frozen no-such-task absence behavior, and carries the
// read-only probe surfaces — assignees and list JSON, the create help
// text, and the rendered skill table (E11-T2). It is test
// infrastructure and never wired into production composition.
package stubhermes

import (
	"os"
	"path/filepath"
	"strings"
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

# Read-only probe surfaces first (argv: skills list --enabled-only,
# optionally -p <profile> skills list --enabled-only).
if [ "$1" = "skills" ] || [ "$1" = "-p" ]; then
  cat <<'TABLE'
                        Installed Skills
┏━━━━━━━━━━━━━━━━━━━━━━━┳━━━━━━━━━━━━━━━━━━━━━━┳━━━━━━━━━━┳━━━━━━━━━━┳━━━━━━━━━┓
┃ Name                  ┃ Category             ┃ Source   ┃ Trust    ┃ Status  ┃
┡━━━━━━━━━━━━━━━━━━━━━━╇━━━━━━━━━━━━━━━━━━━━━━╇━━━━━━━━━━╇━━━━━━━━━━╇━━━━━━━━━┩
│ llm-wiki              │                      │ builtin  │ builtin  │ enabled │
│ agent-dispatch-wiki-maintenance │ provenance │ builtin  │ builtin  │ enabled │
└───────────────────────┴──────────────────────┴──────────┴──────────┴─────────┘
TABLE
  exit 0
fi

# argv: kanban --board <b> <cmd> ... ; -h for the create surface.
cmd=$4
if [ "$5" = "-h" ]; then
  cat <<'HELP'
usage: hermes kanban create [-h] [--body BODY] [--assignee ASSIGNEE]
                            [--workspace WORKSPACE] [--mutex-key KEY]
                            [--max-runtime MAX_RUNTIME] [--max-retries N]
                            [--idempotency-key IDEMPOTENCY_KEY]
                            [--priority PRIORITY] [--created-by CREATED_BY]
                            [--skill SKILLS] [--json] title
HELP
  exit 0
fi
case "$cmd" in
  create)
    title=$5
    key=""
    assignee=""
    mode=""
    i=6
    while [ $i -le $# ]; do
      eval "a=\${$i}"
      if [ "$mode" = "key" ]; then key="$a"; mode=""; fi
      if [ "$mode" = "assignee" ]; then assignee="$a"; mode=""; fi
      if [ "$a" = "--idempotency-key" ]; then mode="key"; fi
      if [ "$a" = "--assignee" ]; then mode="assignee"; fi
      i=$((i+1))
    done
    if [ -n "$key" ] && [ -f "$DIR/key-$key" ]; then cat "$DIR/key-$key"; exit 0; fi
    n=$(cat "$DIR/count" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$DIR/count"
    id=$(printf 't_%08x' "$n")
    # The argv-derived title and assignee are untrusted interpolation into
    # a JSON document: backslash-escape the JSON string metacharacters
    # first (the fixed members stay single-quoted literals like the tables
    # above), so a quote or backslash in argv can never break the record.
    jtitle=$(printf '%s' "$title" | tr -d '[:cntrl:]' | sed 's/\\/\\\\/g; s/"/\\"/g')
    jassignee=$(printf '%s' "$assignee" | tr -d '[:cntrl:]' | sed 's/\\/\\\\/g; s/"/\\"/g')
    cat > "$DIR/id-$id" <<JSON
{"id":"$id","title":"$jtitle","status":"ready","created_at":1787142146,"assignee":"$jassignee","mutex_key":"wiki-publish","skills":["llm-wiki"]}
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
  assignees)
    printf '[{"name":"default","on_disk":true},{"name":"wiki-maintainer","on_disk":true}]'
    exit 0
    ;;
  list)
    printf '[]'
    exit 0
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

// WriteVersioned creates the stateful stub with a different version
// first line, for eligibility and same-path probe tests (TST-012).
func WriteVersioned(t *testing.T, versionLine string) string {
	t.Helper()
	bin := Write(t)
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(raw), "Hermes Agent v0.19.1 (2026.7.30)", versionLine, 1)
	if rewritten == string(raw) && versionLine != "Hermes Agent v0.19.1 (2026.7.30)" {
		t.Fatal("stub does not carry the frozen version line")
	}
	if err := os.WriteFile(bin, []byte(rewritten), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}
