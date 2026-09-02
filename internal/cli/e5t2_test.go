package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/testsupport/hermesenv"
)

// e5t2SkillDir is the packaged production skill inside the docs tree.
var e5t2SkillDir = filepath.Join("..", "..", "docs", "skills", "agent-dispatch-wiki-maintenance")

// e5t2Hermes runs one public hermes command against a disposable root so
// the validation never touches the real Hermes profile.
func e5t2Hermes(t *testing.T, sandbox *hermesenv.Sandbox, argv ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := sandbox.CommandContext(ctx, argv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestHermesCompanionSkillPackaged proves the production skill exists in
// the docs package with the required rules and no permission surface
// (FBK-006, BND-003, BND-004).
func TestHermesCompanionSkillPackaged(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(e5t2SkillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("packaged skill missing: %v", err)
	}
	skill := string(body)
	for _, required := range []string{
		"name: agent-dispatch-wiki-maintenance", // frontmatter identity
		"latest",                                // latest-state rule
		"untrusted data",                        // untrusted-data rule
		"agent-dispatch work begin",             // receipt CLI use
		"agent-dispatch work complete",
		"agent-dispatch work fail",
		"No-Receipt Fallback", // fallback behavior
		"DISPATCH_ID",         // task-variable mapping
		"RUN_ID",
		"not a Hermes plugin", // boundary statement
	} {
		if !strings.Contains(skill, required) {
			t.Fatalf("packaged skill must contain %q", required)
		}
	}
	// The skill must not grant permissions or instruct plugin installation.
	lower := strings.ToLower(skill)
	for _, forbidden := range []string{"--yolo", "sudo", "hermes plugins install", "chmod"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("packaged skill must not contain %q", forbidden)
		}
	}
	// Installation instructions exist beside the skill.
	if _, err := os.Stat(filepath.Join(e5t2SkillDir, "INSTALL.md")); err != nil {
		t.Fatalf("installation instructions missing: %v", err)
	}
}

// TestHermesCompanionSkillValidated proves the packaged skill through the
// public Hermes skill mechanism in a disposable profile: it is listed and
// inspectable, a disposable board task selects it through the public
// --skill flag, the durable task record carries it, and the board is
// deleted afterwards. The real Hermes profile is never touched.
func TestHermesCompanionSkillValidated(t *testing.T) {
	sandbox := hermesenv.NewSandbox(t, func(firstLine string) bool {
		return strings.Contains(firstLine, "v0.20.5")
	})

	// Disposable profile: install the skill by local copy (the documented
	// public mechanism, INSTALL.md option A).
	skillsDir := filepath.Join(sandbox.HermesHome, "skills", "productivity")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := filepath.Abs(e5t2SkillDir)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-R", src, skillsDir).CombinedOutput(); err != nil {
		t.Fatalf("installing the skill into the disposable profile: %v: %s", err, out)
	}

	listing, err := e5t2Hermes(t, sandbox, "skills", "list")
	if err != nil {
		t.Fatalf("skills list: %v: %s", err, listing)
	}
	if !strings.Contains(listing, "agent-dispatch-wiki-maintenance") {
		t.Fatalf("disposable profile must list the installed skill: %s", listing)
	}
	inspected, err := e5t2Hermes(t, sandbox, "skills", "inspect", "agent-dispatch-wiki-maintenance")
	if err != nil || !strings.Contains(inspected, "agent-dispatch-wiki-maintenance") {
		t.Fatalf("skills inspect failed: %v: %s", err, inspected)
	}

	// Disposable board task selecting the skill through the public flag.
	board := fmt.Sprintf("agent-dispatch-e5t2-skill-%d", time.Now().UnixNano())
	if out, err := e5t2Hermes(t, sandbox, "kanban", "boards", "create", board); err != nil {
		t.Fatalf("boards create: %v: %s", err, out)
	}
	defer func() {
		if out, err := e5t2Hermes(t, sandbox, "kanban", "boards", "rm", board, "--delete"); err != nil {
			t.Logf("board cleanup: %v: %s", err, out)
		}
	}()
	created, err := e5t2Hermes(t, sandbox, "kanban", "--board", board, "create",
		"--skill", "agent-dispatch-wiki-maintenance", "--json", "Agent Dispatch companion skill validation")
	if err != nil {
		t.Fatalf("kanban create with the skill: %v: %s", err, created)
	}
	var task struct {
		ID     string   `json:"id"`
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(created)), &task); err != nil {
		t.Fatalf("create response is not JSON: %v: %s", err, created)
	}
	if task.ID == "" {
		t.Fatal("created task id missing")
	}
	found := false
	for _, s := range task.Skills {
		if s == "agent-dispatch-wiki-maintenance" {
			found = true
		}
	}
	if !found {
		t.Fatalf("durable task record must carry the selected skill: %+v", task)
	}

	// The durable record still carries it on the public show surface.
	shown, err := e5t2Hermes(t, sandbox, "kanban", "--board", board, "show", task.ID, "--json")
	if err != nil || !strings.Contains(shown, "agent-dispatch-wiki-maintenance") {
		t.Fatalf("task show must carry the skill: %v: %s", err, shown)
	}
}
