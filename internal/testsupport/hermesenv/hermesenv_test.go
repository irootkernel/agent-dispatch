package hermesenv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSandboxIsolatesHermesState(t *testing.T) {
	operatorHome := t.TempDir()
	operatorHermes := filepath.Join(operatorHome, ".hermes")
	operatorBoard := filepath.Join(operatorHermes, "kanban", "boards", "operator", "board.json")
	if err := os.MkdirAll(filepath.Dir(operatorBoard), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(operatorHermes, "active_profile"), []byte("operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(operatorBoard, []byte("operator-board\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	bin := filepath.Join(binDir, "hermes")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf 'Hermes Agent v0.20.5 (test)\n'
  exit 0
fi
printf '%s\n' "$HOME" "$HERMES_HOME" "$HERMES_KANBAN_HOME" "$HERMES_KANBAN_DB" "$HERMES_KANBAN_BOARD" "$HERMES_KANBAN_WORKSPACES_ROOT"
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", operatorHome)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HERMES_KANBAN_DB", filepath.Join(operatorHermes, "kanban.db"))
	t.Setenv("HERMES_KANBAN_BOARD", "operator")
	t.Setenv("HERMES_KANBAN_WORKSPACES_ROOT", filepath.Join(operatorHermes, "workspaces"))

	sandbox := NewSandbox(t, func(firstLine string) bool {
		return strings.Contains(firstLine, "v0.20.5")
	})
	out, err := sandbox.CommandContext(context.Background(), "print-env").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		sandbox.Home,
		sandbox.HermesHome,
		sandbox.HermesHome,
		"",
		"",
		"",
	}, "\n") + "\n"
	if string(out) != want {
		t.Fatalf("isolated Hermes environment mismatch\nwant: %q\n got: %q", want, out)
	}
	if got := strings.Join(sandbox.EnvironmentAllowlist(), ","); got != "PATH,HOME,HERMES_HOME,HERMES_KANBAN_HOME" {
		t.Fatalf("test target allowlist mismatch: %s", got)
	}
	active, err := os.ReadFile(filepath.Join(operatorHermes, "active_profile"))
	if err != nil || string(active) != "operator\n" {
		t.Fatalf("operator active profile changed: %q %v", active, err)
	}
	board, err := os.ReadFile(operatorBoard)
	if err != nil || string(board) != "operator-board\n" {
		t.Fatalf("operator board changed: %q %v", board, err)
	}
}
