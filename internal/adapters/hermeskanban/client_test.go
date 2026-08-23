package hermeskanban

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testClient(bin string) *Client {
	return NewClient(bin, ProcessLimits{
		SubmitTimeout: 30 * time.Second, LookupTimeout: 30 * time.Second, MaxOutputBytes: 1 << 20,
	})
}

// TestDecodeFrozenFixtures proves typed parsing against the immutable
// E0-T4 corpus: create (task object), show ({task} envelope), list
// (array), assignees (array). Only typed parsing of these shapes may
// decide an outcome.
func TestDecodeFrozenFixtures(t *testing.T) {
	load := func(name string) []byte {
		raw, err := os.ReadFile(fixtureDir + "/" + name)
		if err != nil {
			t.Fatalf("read frozen fixture %s: %v", name, err)
		}
		return raw
	}
	task, err := decodeCreate(load("create-response.json"))
	if err != nil {
		t.Fatalf("create fixture must decode: %v", err)
	}
	if task.ID != "t_6253023d" || task.Status != "ready" || task.CreatedAt != 1787142146 {
		t.Fatalf("create fixture decoded wrong: %+v", task)
	}
	if task.MutexKey == nil || *task.MutexKey != "jjukkumi-vault-maintenance" {
		t.Fatalf("mutex echo missing: %+v", task)
	}

	shown, err := decodeShow(load("show-response.json"))
	if err != nil {
		t.Fatalf("show fixture must decode: %v", err)
	}
	if shown.ID != task.ID {
		t.Fatalf("show/create identity mismatch: %q vs %q", shown.ID, task.ID)
	}

	tasks, err := decodeList(load("list-response.json"))
	if err != nil {
		t.Fatalf("list fixture must decode: %v", err)
	}
	if len(tasks) < 2 {
		t.Fatalf("list fixture tasks = %d", len(tasks))
	}

	assignees, err := decodeAssignees(load("assignees-default-board.json"))
	if err != nil {
		t.Fatalf("assignees fixture must decode: %v", err)
	}
	if len(assignees) == 0 || !assignees[0].OnDisk {
		t.Fatalf("assignees fixture decoded wrong: %+v", assignees)
	}

	// The unvalidated-assignee fixture is still a well-formed acceptance
	// record: create-time profile validation is the adapter's job, not
	// Hermes's (E0-T4 §7).
	unvalidated, err := decodeCreate(load("create-assignee-unvalidated.json"))
	if err != nil {
		t.Fatalf("unvalidated-assignee fixture must decode: %v", err)
	}
	if unvalidated.Assignee == nil || *unvalidated.Assignee != "no-such-profile-xyz" {
		t.Fatalf("assignee echo missing: %+v", unvalidated)
	}

	// The duplicate-dedup fixture returns the ORIGINAL task for a
	// repeated idempotency key: identical id and title to the first
	// create.
	dup, err := decodeCreate(load("create-duplicate-dedup.json"))
	if err != nil {
		t.Fatalf("dedup fixture must decode: %v", err)
	}
	if dup.ID != task.ID || dup.Title != task.Title {
		t.Fatalf("dedup must return the original task, got %+v", dup)
	}
}

// TestCreateRejectsMalformedAcceptance proves a create response missing
// the acceptance-proof members is malformed, never accepted: no human
// text parsing decides acceptance (E4-T1 acceptance).
func TestCreateRejectsMalformedAcceptance(t *testing.T) {
	malformed := []string{
		`{"title": "no id"}`,
		`{"id": "t_6253023d", "title": "no status"}`,
		`{"id": "not-the-form", "status": "ready", "created_at": 1}`,
		`{"id": "t_6253023d", "status": "ready", "created_at": 0}`,
		`[]`,
	}
	for _, body := range malformed {
		if _, err := decodeCreate([]byte(body)); err == nil {
			t.Fatalf("malformed create response %q must not decode as acceptance", body)
		}
	}
}

// TestClientClassification runs the frozen error behaviors
// (error-cases.txt) through a stub binary.
func TestClientClassification(t *testing.T) {
	t.Run("unknown task", func(t *testing.T) {
		bin := newStubHermes(t, `echo 'no such task: t_nonexistent00' >&2; exit 1`)
		_, err := testClient(bin).Show(context.Background(), "b", "t_nonexistent00")
		var unknown *UnknownTaskError
		if !errors.As(err, &unknown) || unknown.Ref != "t_nonexistent00" {
			t.Fatalf("frozen no-such-task behavior must classify as UnknownTaskError, got %v", err)
		}
	})
	t.Run("unknown board", func(t *testing.T) {
		bin := newStubHermes(t, `echo "kanban: board 'agent-dispatch-no-such-board' does not exist. Create it with a boards create command." >&2; exit 1`)
		_, err := testClient(bin).List(context.Background(), "agent-dispatch-no-such-board", ListOptions{})
		var unknown *UnknownBoardError
		if !errors.As(err, &unknown) || unknown.Board != "agent-dispatch-no-such-board" {
			t.Fatalf("frozen unknown-board behavior must classify as UnknownBoardError, got %v", err)
		}
	})
	t.Run("argument rejected", func(t *testing.T) {
		bin := newStubHermes(t, `echo 'hermes: error: unrecognized arguments: --json' >&2; exit 2`)
		_, err := testClient(bin).List(context.Background(), "b", ListOptions{})
		var rejected *ArgumentRejectedError
		if !errors.As(err, &rejected) {
			t.Fatalf("argparse exit 2 must classify as ArgumentRejectedError, got %v", err)
		}
	})
	t.Run("other failure stays generic", func(t *testing.T) {
		bin := newStubHermes(t, `echo 'internal explosion' >&2; exit 7`)
		_, err := testClient(bin).Show(context.Background(), "b", "t_6253023d")
		var failed *CommandFailedError
		if !errors.As(err, &failed) || failed.ExitCode != 7 {
			t.Fatalf("unknown exit behavior must stay CommandFailedError, got %v", err)
		}
		if !strings.Contains(failed.Detail, "internal explosion") {
			t.Fatalf("bounded stderr detail missing: %v", failed)
		}
	})
	t.Run("timeout is ambiguous", func(t *testing.T) {
		bin := newStubHermes(t, `sleep 30`)
		client := NewClient(bin, ProcessLimits{SubmitTimeout: 200 * time.Millisecond, LookupTimeout: 200 * time.Millisecond})
		_, err := client.Create(context.Background(), "b", CreateOptions{Title: "x"})
		var timeout *TimeoutError
		if !errors.As(err, &timeout) {
			t.Fatalf("timeout must classify as TimeoutError (ambiguous outcome), got %v", err)
		}
	})
	t.Run("malformed output", func(t *testing.T) {
		bin := newStubHermes(t, `echo '{"status": "ok", "task_i'`)
		_, err := testClient(bin).Create(context.Background(), "b", CreateOptions{Title: "x"})
		var malformed *MalformedOutputError
		if !errors.As(err, &malformed) {
			t.Fatalf("garbage on exit 0 must classify as MalformedOutputError, got %v", err)
		}
	})
}

// TestClientCreateTypedRoundTrip proves a successful create parses the
// typed record from a stub emitting the frozen response shape.
func TestClientCreateTypedRoundTrip(t *testing.T) {
	fixture, err := os.ReadFile(fixtureDir + "/create-response.json")
	if err != nil {
		t.Fatal(err)
	}
	stubDir := t.TempDir()
	response := filepath.Join(stubDir, "create.json")
	if err := os.WriteFile(response, fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := newStubHermes(t, `cat "`+response+`"`)
	client := testClient(bin)
	task, err := client.Create(context.Background(), "agent-dispatch-probe", CreateOptions{
		Title:          "Agent Dispatch E0-T4 capability probe 1",
		Body:           "b",
		Assignee:       "wiki-maintainer",
		Skills:         []string{"llm-wiki"},
		Workspace:      "dir:/tmp/vault",
		MutexKey:       "wiki-publish",
		MaxRuntime:     "30m",
		MaxRetries:     2,
		IdempotencyKey: "agent-dispatch:v1:sha256:abc",
		Priority:       5,
		CreatedBy:      "agent-dispatch",
	})
	if err != nil {
		t.Fatalf("typed create round trip: %v", err)
	}
	if task.ID != "t_6253023d" || task.Status != "ready" {
		t.Fatalf("task record %q/%q", task.ID, task.Status)
	}
}

// TestCreateArgvDocumentedOrder proves the rendered argument array stays
// inside the E0-T4-verified flag surface, in a deterministic order, with
// the idempotency key transmitted verbatim.
func TestCreateArgvDocumentedOrder(t *testing.T) {
	argv := CreateOptions{
		Title:          "T",
		Body:           "B",
		Assignee:       "P",
		Skills:         []string{"s1", "s2"},
		Workspace:      "scratch",
		MutexKey:       "M",
		MaxRuntime:     "30m",
		MaxRetries:     2,
		IdempotencyKey: "KEY",
		Priority:       3,
		CreatedBy:      "agent-dispatch",
	}.argv("board-1")
	want := []string{
		"kanban", "--board", "board-1", "create", "T",
		"--body", "B", "--assignee", "P",
		"--skill", "s1", "--skill", "s2",
		"--workspace", "scratch", "--mutex-key", "M",
		"--max-runtime", "30m", "--max-retries", "2",
		"--idempotency-key", "KEY", "--priority", "3",
		"--created-by", "agent-dispatch", "--json",
	}
	if len(argv) != len(want) {
		t.Fatalf("argv length %d want %d: %v", len(argv), len(want), argv)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q want %q (full: %v)", i, argv[i], want[i], argv)
		}
	}
}

// TestProfileOnDisk covers the E0-T4 §7 profile validation: present and
// on disk, present but not on disk, and absent all classify distinctly.
func TestProfileOnDisk(t *testing.T) {
	fixture, err := os.ReadFile(fixtureDir + "/assignees-default-board.json")
	if err != nil {
		t.Fatal(err)
	}
	stubDir := t.TempDir()
	response := filepath.Join(stubDir, "assignees.json")
	if err := os.WriteFile(response, fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := newStubHermes(t, `cat "`+response+`"`)
	client := testClient(bin)
	onDisk, err := client.ProfileOnDisk(context.Background(), "b", "profile-a")
	if err != nil || !onDisk {
		t.Fatalf("profile-a must be on disk: %v %v", onDisk, err)
	}
	absent, err := client.ProfileOnDisk(context.Background(), "b", "no-such-profile")
	if err != nil || absent {
		t.Fatalf("absent profile must return false without error: %v %v", absent, err)
	}
	// A name collision prefix must not match a different profile.
	prefix, err := client.ProfileOnDisk(context.Background(), "b", "profile")
	if err != nil || prefix {
		t.Fatalf("prefix-only name must not match: %v %v", prefix, err)
	}
}

// TestHostileValuesTravelVerbatim proves option values containing shell
// metacharacters and leading-dash-safe content reach the child as single
// verbatim argv elements: no shell, no interpolation, no word splitting
// (HER-003, SEC-005).
func TestHostileValuesTravelVerbatim(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	bin := newStubHermes(t, `for a in "$@"; do printf '<%s>' "$a"; done > `+argsFile)
	hostile := CreateOptions{
		Title:          "t; rm -rf /",
		Body:           "$(echo no)\n`id`\nnewline",
		Assignee:       "wiki|maintainer & co",
		IdempotencyKey: "agent-dispatch:v1:sha256:*",
		CreatedBy:      "attacker\"; quoted",
	}
	// The stub emits no JSON, so the typed parse fails after the child
	// ran; the verbatim-argv assertion only needs the captured args.
	_, _ = testClient(bin).Create(context.Background(), "board x; y", hostile)
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		"<t; rm -rf />", "<$(echo no)\n`id`\nnewline>", "<wiki|maintainer & co>",
		"<agent-dispatch:v1:sha256:*>", "<attacker\"; quoted>", "<board x; y>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("verbatim argument %q missing from child argv capture %q", want, got)
		}
	}
}

// TestCreateRejectsOptionLikeValues proves the leading-dash guard: a
// value that would be parsed as a flag is refused before any invocation.
func TestCreateRejectsOptionLikeValues(t *testing.T) {
	cases := []CreateOptions{
		{Title: "--board=other"},
		{Title: "fine", Body: "-x"},
		{Title: "fine", Assignee: "--json"},
		{Title: "fine", Skills: []string{"-evil"}},
		{Title: "fine", MutexKey: "--flag"},
		{Title: "fine", IdempotencyKey: "-key"},
	}
	for _, opts := range cases {
		bin := newStubHermes(t, `exit 0`)
		if _, err := testClient(bin).Create(context.Background(), "b", opts); err == nil {
			t.Fatalf("option-like value %+v must be refused", opts)
		}
	}
}

// TestLookupTimeoutIsAmbiguous proves the lookup surface classifies its
// own deadline as TimeoutError too, not only create.
func TestLookupTimeoutIsAmbiguous(t *testing.T) {
	bin := newStubHermes(t, `sleep 30`)
	client := NewClient(bin, ProcessLimits{SubmitTimeout: 5 * time.Second, LookupTimeout: 200 * time.Millisecond})
	_, err := client.Show(context.Background(), "b", "t_6253023d")
	var timeout *TimeoutError
	if !errors.As(err, &timeout) || !strings.Contains(timeout.Error(), "200ms") {
		t.Fatalf("lookup timeout must classify as TimeoutError naming the deadline, got %v", err)
	}
}

// TestClientExcessiveOutput proves the client surfaces the write-side
// bound as the explicit ambiguous excessive-output error.
func TestClientExcessiveOutput(t *testing.T) {
	bin := newStubHermes(t, `yes 'x' | head -c 100000`)
	client := NewClient(bin, ProcessLimits{SubmitTimeout: 5 * time.Second, LookupTimeout: 5 * time.Second, MaxOutputBytes: 2048})
	_, err := client.List(context.Background(), "b", ListOptions{})
	var excessive *ExcessiveOutputError
	if !errors.As(err, &excessive) || excessive.Bound != 2048 {
		t.Fatalf("excessive output must classify as ExcessiveOutputError with the bound, got %v", err)
	}
}

// TestSecretValuesRedactedFromDiagnostics proves the values of
// allowlisted environment variables are scrubbed from embedded child
// output before it enters error text (error-model §6).
func TestSecretValuesRedactedFromDiagnostics(t *testing.T) {
	const token = "super-secret-token-value"
	t.Setenv("HERMES_API_TOKEN", token)
	bin := newStubHermes(t, `echo "auth failed for $HERMES_API_TOKEN" >&2; exit 3`)
	client := NewClient(bin, ProcessLimits{
		SubmitTimeout: time.Second, LookupTimeout: time.Second, MaxOutputBytes: 4096,
		EnvironmentAllowlist: []string{"HERMES_API_TOKEN"},
	})
	_, err := client.Show(context.Background(), "b", "t_6253023d")
	var failed *CommandFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("expected CommandFailedError, got %v", err)
	}
	if strings.Contains(failed.Detail, token) {
		t.Fatalf("allowlisted secret leaked into diagnostic: %q", failed.Detail)
	}
	if !strings.Contains(failed.Detail, "<redacted:HERMES_API_TOKEN>") {
		t.Fatalf("redaction marker missing: %q", failed.Detail)
	}
}

// TestDiagnosticsRedacted proves embedded child output in every typed
// error is bounded (error-model §6: no unrestricted subprocess output).
func TestDiagnosticsRedacted(t *testing.T) {
	huge := strings.Repeat("x", 5000)
	bin := newStubHermes(t, `echo '`+huge+`' >&2; exit 3`)
	_, err := testClient(bin).Show(context.Background(), "b", "t_6253023d")
	var failed *CommandFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("expected CommandFailedError, got %v", err)
	}
	if len(failed.Detail) > diagnosticBound+16 {
		t.Fatalf("diagnostic %d bytes exceeds the bound", len(failed.Detail))
	}
	raw, _ := json.Marshal(failed.Error())
	if len(raw) > diagnosticBound*4 {
		t.Fatalf("serialized error too large: %d", len(raw))
	}
}

// TestLookupSurfacesRejectOptionLikeValues proves the leading-dash
// guard covers the lookup transport too: board, task reference, status,
// and sort values beginning with "-" are refused before any invocation.
func TestLookupSurfacesRejectOptionLikeValues(t *testing.T) {
	client := testClient(newStubHermes(t, `exit 0`))
	ctx := context.Background()
	if _, err := client.Show(ctx, "--json", "t_6253023d"); err == nil {
		t.Fatal("option-like board must be refused by Show")
	}
	if _, err := client.Show(ctx, "board", "--flag"); err == nil {
		t.Fatal("option-like task reference must be refused by Show")
	}
	if _, err := client.List(ctx, "board", ListOptions{Status: "-x"}); err == nil {
		t.Fatal("option-like status must be refused by List")
	}
	if _, err := client.List(ctx, "board", ListOptions{Sort: "--json"}); err == nil {
		t.Fatal("option-like sort must be refused by List")
	}
	if _, err := client.Assignees(ctx, "-board"); err == nil {
		t.Fatal("option-like board must be refused by Assignees")
	}
}

// TestVersionParseDiagnosticRedacted proves a child that echoes a secret
// into its --version output cannot leak it through the parse diagnostic.
func TestVersionParseDiagnosticRedacted(t *testing.T) {
	const token = "super-secret-token-value"
	t.Setenv("HERMES_API_TOKEN", token)
	bin := newStubHermes(t, `printf 'Hermes Agent %s\n' "$HERMES_API_TOKEN"`)
	client := NewClient(bin, ProcessLimits{
		SubmitTimeout: time.Second, LookupTimeout: time.Second, MaxOutputBytes: 4096,
		EnvironmentAllowlist: []string{"HERMES_API_TOKEN"},
	})
	_, err := client.DiscoverVersion(context.Background())
	var malformed *MalformedOutputError
	if !errors.As(err, &malformed) {
		t.Fatalf("expected MalformedOutputError, got %v", err)
	}
	if strings.Contains(malformed.Reason, token) {
		t.Fatalf("allowlisted secret leaked through the version diagnostic: %q", malformed.Reason)
	}
}

// TestCreateRejectsOptionLikeBoard proves the create surface guards its
// board argument like every other rendered value slot: the refusal
// happens before any child process runs and names the guard reason.
func TestCreateRejectsOptionLikeBoard(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "invoked")
	bin := newStubHermes(t, `printf x >> "`+counter+`"; exit 0`)
	client := testClient(bin)
	_, err := client.Create(context.Background(), "--json", CreateOptions{Title: "t"})
	if err == nil || !strings.Contains(err.Error(), "begins with '-'") {
		t.Fatalf("option-like board must be refused by the guard, got %v", err)
	}
	if raw, rerr := os.ReadFile(counter); rerr == nil && len(raw) > 0 {
		t.Fatal("the guard must refuse before any child invocation")
	}
}

// TestUnknownBoardNameBounded proves the stderr-derived board name in
// the frozen unknown-board classification is bounded like every other
// child-output diagnostic.
func TestUnknownBoardNameBounded(t *testing.T) {
	huge := strings.Repeat("x", 5000)
	bin := newStubHermes(t, `printf "kanban: board '%s' does not exist." '`+huge+`' >&2; exit 1`)
	_, err := testClient(bin).List(context.Background(), "b", ListOptions{})
	var unknown *UnknownBoardError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected UnknownBoardError, got %v", err)
	}
	if len(unknown.Board) > 64+16 {
		t.Fatalf("board name %d bytes exceeds the bound", len(unknown.Board))
	}
}

// TestUnknownTaskRefBounded proves the stderr-derived task reference in
// the frozen no-such-task classification is bounded like every other
// child-output diagnostic.
func TestUnknownTaskRefBounded(t *testing.T) {
	huge := strings.Repeat("x", 5000)
	bin := newStubHermes(t, `printf 'no such task: `+huge+`' >&2; exit 1`)
	_, err := testClient(bin).Show(context.Background(), "b", "t_6253023d")
	var unknown *UnknownTaskError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected UnknownTaskError, got %v", err)
	}
	if len(unknown.Ref) > diagnosticBound+16 { // bound plus the truncation marker
		t.Fatalf("ref %d bytes exceeds the diagnostic bound", len(unknown.Ref))
	}
}
