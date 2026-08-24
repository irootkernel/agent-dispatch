package hermeskanban

import (
	"regexp"
	"strings"
	"testing"
)

// TestE9T4SkillRendererCrossCheck pins L-23: the rendered task body and
// the surfaces it instructs the worker to use cannot drift apart. Every
// assigned skill id is referenced by the trusted instruction in its
// space-normalized form, and every agent-dispatch command line the
// receipt instructions embed carries exactly the flags the work
// command surface accepts — a renamed skill or flag breaks the render
// contract visibly instead of silently instructing the worker wrongly.
func TestE9T4SkillRendererCrossCheck(t *testing.T) {
	req := loadGoldenRequest(t)
	rendered, err := Render(req, RenderOptions{MaxManifestBytes: 1 << 20, ResourceMutexSupported: true})
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(rendered.Body)

	// The trusted instruction names every assigned skill: the id's
	// kebab-case form appears with the word "skill" in the instruction
	// text (llm-wiki -> "llm wiki skill").
	if req.Assignment == nil || len(req.Assignment.Skills) == 0 {
		t.Fatal("golden request must carry assigned skills for this test")
	}
	for _, skill := range req.Assignment.Skills {
		spaceForm := strings.ReplaceAll(strings.ToLower(skill), "-", " ")
		if !strings.Contains(body, spaceForm+" skill") && !strings.Contains(body, "skill "+spaceForm) {
			t.Fatalf("the trusted instruction must reference the assigned skill %q in its space form", skill)
		}
	}

	// The receipt instruction lines carry only flags the work command
	// surface actually accepts; the worker copies them verbatim.
	workFlag := regexp.MustCompile(`agent-dispatch work (begin|complete|fail)([^<\n]*)`)
	accepted := map[string]bool{
		"--dispatch-id": true, "--run-id": true, "--external-task-id": true,
		"--manifest": true, "--failure-code": true, "--config": true,
	}
	for _, line := range workFlag.FindAllStringSubmatch(rendered.Body, -1) {
		for _, field := range strings.Fields(line[2]) {
			if !strings.HasPrefix(field, "--") {
				continue
			}
			name := field
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			if !accepted[name] {
				t.Fatalf("the receipt instructions reference %q, which the work command surface does not accept", name)
			}
		}
	}
	// The command identities the body teaches are exactly the work verbs
	// the renderer emits (begin and complete), and a verb the surface
	// does not have never appears.
	for _, verb := range []string{"work begin", "work complete"} {
		if !strings.Contains(rendered.Body, "agent-dispatch "+verb) {
			t.Fatalf("the receipt instructions must teach %q", verb)
		}
	}
	if strings.Contains(rendered.Body, "agent-dispatch work retry") {
		t.Fatal("the receipt instructions must not teach a work verb that does not exist")
	}
}
