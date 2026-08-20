package hermeskanban

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Client is the typed transport over the verified public Hermes kanban
// CLI (E0-T4). Every method builds an argument array — never a shell
// string (HER-003, SEC-005) — and decides outcomes only from exit 0 plus
// successfully typed --json output. Frozen exit-1 stderr shapes
// (error-cases.txt) classify failures for lookup semantics; human text
// never decides acceptance.
type Client struct {
	runner *runner
}

// NewClient returns a client for the configured executable under the
// given process limits.
func NewClient(executable string, limits ProcessLimits) *Client {
	return &Client{runner: &runner{executable: executable, limits: limits.withDefaults()}}
}

// Limits exposes the effective (defaulted) process limits.
func (c *Client) Limits() ProcessLimits { return c.runner.limits }

// DiscoverVersion invokes `hermes --version` and parses the documented
// first line (E0-T4 §2). It does not gate; call CheckVersionSupported
// for the route-validation gate.
func (c *Client) DiscoverVersion(ctx context.Context) (Version, error) {
	res, err := c.run(ctx, "version", c.runner.limits.LookupTimeout, []string{"--version"})
	if err != nil {
		return Version{}, err
	}
	v, perr := ParseVersionOutput(string(res.Stdout))
	if perr != nil {
		return Version{}, &MalformedOutputError{Command: "version", Reason: c.runner.redactSecrets(perr.Error())}
	}
	return v, nil
}

// CreateOptions is the runtime-verified public create surface (E0-T4
// §3): title, body, assignee, skills, workspace, mutex key, runtime and
// retry hints, idempotency key, priority, and created-by attribution.
// Values arrive only from trusted route configuration and the logical
// task contract; nothing here is derived from untrusted event data.
type CreateOptions struct {
	Title          string
	Body           string
	Assignee       string
	Skills         []string
	Workspace      string // scratch | worktree | worktree:<path> | dir:<path>
	MutexKey       string
	MaxRuntime     string // seconds or duration text
	MaxRetries     int
	IdempotencyKey string
	Priority       int
	CreatedBy      string
}

// argv renders the create argument array in the documented flag order.
func (o CreateOptions) argv(board string) []string {
	argv := []string{"kanban", "--board", board, "create", o.Title}
	if o.Body != "" {
		argv = append(argv, "--body", o.Body)
	}
	if o.Assignee != "" {
		argv = append(argv, "--assignee", o.Assignee)
	}
	for _, s := range o.Skills {
		argv = append(argv, "--skill", s)
	}
	if o.Workspace != "" {
		argv = append(argv, "--workspace", o.Workspace)
	}
	if o.MutexKey != "" {
		argv = append(argv, "--mutex-key", o.MutexKey)
	}
	if o.MaxRuntime != "" {
		argv = append(argv, "--max-runtime", o.MaxRuntime)
	}
	if o.MaxRetries > 0 {
		argv = append(argv, "--max-retries", fmt.Sprint(o.MaxRetries))
	}
	if o.IdempotencyKey != "" {
		argv = append(argv, "--idempotency-key", o.IdempotencyKey)
	}
	if o.Priority != 0 {
		argv = append(argv, "--priority", fmt.Sprint(o.Priority))
	}
	if o.CreatedBy != "" {
		argv = append(argv, "--created-by", o.CreatedBy)
	}
	return append(argv, "--json")
}

// optionValues returns every value rendered into a flag-argument slot,
// so the leading-dash guard covers the whole surface.
func (o CreateOptions) optionValues() []string {
	values := []string{o.Title, o.Body, o.Assignee, o.Workspace, o.MutexKey, o.MaxRuntime, o.IdempotencyKey, o.CreatedBy}
	values = append(values, o.Skills...)
	return values
}

// validate rejects option-like values: an argument beginning with "-"
// in a value slot could be parsed by the child CLI as a flag and
// silently change the invocation's meaning. The guard keeps the
// trusted-value invariant enforced at the transport boundary instead of
// depending on every future caller (the E4-T2 renderer included).
func (o CreateOptions) validate() error {
	if err := guardOptionValues(o.optionValues()); err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if strings.TrimSpace(o.Title) == "" {
		return fmt.Errorf("create requires a title")
	}
	return nil
}

// guardOptionValues refuses option-like values before any invocation.
// It applies to every value slot the transport renders — create options
// and the lookup surfaces (board, task reference, status, sort) alike —
// so the transport-boundary invariant is uniform.
func guardOptionValues(values []string) error {
	for _, v := range values {
		if strings.HasPrefix(v, "-") {
			return fmt.Errorf("option value %q begins with '-' and would be parsed as a flag; refusing the invocation", truncate(v, 60))
		}
	}
	return nil
}

// Create submits one task and returns the typed record. A zero exit with
// a typed task object is the only durable-acceptance evidence path.
func (c *Client) Create(ctx context.Context, board string, opts CreateOptions) (TaskRecord, error) {
	if err := opts.validate(); err != nil {
		return TaskRecord{}, err
	}
	if err := guardOptionValues([]string{board}); err != nil {
		return TaskRecord{}, fmt.Errorf("create: %w", err)
	}
	res, err := c.run(ctx, "create", c.runner.limits.SubmitTimeout, opts.argv(board))
	if err != nil {
		return TaskRecord{}, err
	}
	task, perr := decodeCreate(res.Stdout)
	if perr != nil {
		return TaskRecord{}, &MalformedOutputError{Command: "create", Reason: c.runner.redactSecrets(perr.Error())}
	}
	return task, nil
}

// ListOptions bounds one list call to the verified public surface
// (E0-T4 §6: --status and --sort only).
type ListOptions struct {
	Status string // one of PublicStatuses; empty lists all
	Sort   string
}

// List reads the bounded task array (E0-T4 §6). It is read-only.
func (c *Client) List(ctx context.Context, board string, opts ListOptions) ([]TaskRecord, error) {
	if err := guardOptionValues([]string{board, opts.Status, opts.Sort}); err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	argv := []string{"kanban", "--board", board, "list"}
	if opts.Status != "" {
		argv = append(argv, "--status", opts.Status)
	}
	if opts.Sort != "" {
		argv = append(argv, "--sort", opts.Sort)
	}
	argv = append(argv, "--json")
	res, err := c.run(ctx, "list", c.runner.limits.LookupTimeout, argv)
	if err != nil {
		return nil, err
	}
	tasks, perr := decodeList(res.Stdout)
	if perr != nil {
		return nil, &MalformedOutputError{Command: "list", Reason: c.runner.redactSecrets(perr.Error())}
	}
	return tasks, nil
}

// Show reads one task by its public t_<8 hex> reference (E0-T4 §6).
// The frozen exit-1 `no such task` behavior is a deterministic absence
// proof surfaced as UnknownTaskError.
func (c *Client) Show(ctx context.Context, board, taskID string) (TaskRecord, error) {
	if err := guardOptionValues([]string{board, taskID}); err != nil {
		return TaskRecord{}, fmt.Errorf("show: %w", err)
	}
	res, err := c.run(ctx, "show", c.runner.limits.LookupTimeout,
		[]string{"kanban", "--board", board, "show", taskID, "--json"})
	if err != nil {
		return TaskRecord{}, err
	}
	task, perr := decodeShow(res.Stdout)
	if perr != nil {
		return TaskRecord{}, &MalformedOutputError{Command: "show", Reason: c.runner.redactSecrets(perr.Error())}
	}
	return task, nil
}

// Assignees enumerates the board's profiles with their on_disk existence
// flag (E0-T4 §7). Routes validate the configured profile this way before
// enabling, because --assignee is not validated at create time.
func (c *Client) Assignees(ctx context.Context, board string) ([]Assignee, error) {
	if err := guardOptionValues([]string{board}); err != nil {
		return nil, fmt.Errorf("assignees: %w", err)
	}
	res, err := c.run(ctx, "assignees", c.runner.limits.LookupTimeout,
		[]string{"kanban", "--board", board, "assignees", "--json"})
	if err != nil {
		return nil, err
	}
	out, perr := decodeAssignees(res.Stdout)
	if perr != nil {
		return nil, &MalformedOutputError{Command: "assignees", Reason: c.runner.redactSecrets(perr.Error())}
	}
	return out, nil
}

// ProfileOnDisk reports whether the configured profile exists for the
// board (E0-T4 §7 profile validation).
func (c *Client) ProfileOnDisk(ctx context.Context, board, profile string) (bool, error) {
	assignees, err := c.Assignees(ctx, board)
	if err != nil {
		return false, err
	}
	for _, a := range assignees {
		if a.Name == profile {
			return a.OnDisk, nil
		}
	}
	return false, nil
}

// run executes one bounded invocation and translates failures onto the
// typed error set. The frozen exit-1 stderr shapes classify absence;
// everything else stays a bounded, redacted generic failure whose
// outcome classification belongs to the submit orchestration (DUR-005),
// not the transport.
func (c *Client) run(ctx context.Context, command string, timeout time.Duration, argv []string) (runResult, error) {
	res, err := c.runner.run(ctx, timeout, argv)
	if err == nil {
		return res, nil
	}
	// Child output embedded into error text is scrubbed of allowlisted
	// environment values and bounded (error-model §6).
	stderr := c.runner.redactSecrets(string(res.Stderr))
	switch {
	case errors.Is(err, errExcessiveOutput):
		return res, &ExcessiveOutputError{Command: command, Bound: c.runner.limits.MaxOutputBytes}
	case errors.Is(err, errDeadline):
		return res, &TimeoutError{Command: command, After: timeout.String()}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		switch {
		case code == 2:
			return res, &ArgumentRejectedError{Command: command, Detail: truncate(stderr, diagnosticBound)}
		case code == 1 && strings.HasPrefix(stderr, "no such task: "):
			return res, &UnknownTaskError{Ref: truncate(strings.TrimSpace(strings.TrimPrefix(stderr, "no such task: ")), diagnosticBound)}
		case code == 1 && strings.HasPrefix(stderr, "kanban: board ") && strings.Contains(stderr, "does not exist"):
			return res, &UnknownBoardError{Board: boardFromStderr(stderr)}
		default:
			return res, &CommandFailedError{Command: command, ExitCode: code, Detail: truncate(stderr, diagnosticBound)}
		}
	}
	var missing *ExecutableMissingError
	if errors.As(err, &missing) {
		return res, missing
	}
	return res, fmt.Errorf("hermes %s could not run: %v", command, truncate(err.Error(), diagnosticBound))
}

// boardFromStderr extracts the quoted board slug from the frozen
// unknown-board message without executing untrusted content; the
// stderr-derived value is bounded like every other child-output
// diagnostic (error-model §6).
func boardFromStderr(stderr string) string {
	s := strings.TrimPrefix(stderr, "kanban: board ")
	if i := strings.IndexByte(s, '\''); i >= 0 {
		s = s[i+1:]
		if j := strings.IndexByte(s, '\''); j >= 0 {
			s = s[:j]
		}
	}
	return truncate(s, 64)
}
