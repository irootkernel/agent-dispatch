package watchman

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Lifecycle defaults (SEC-004): bounded output, short timeout, minimal
// environment. The watchman server is local; ten seconds is generous.
const (
	DefaultSubprocessTimeout  = 10 * time.Second
	DefaultSubprocessOutBytes = 1 << 20
	minimumSupportedVersion   = "2026.07.27.00"
)

// LifecycleError carries the structured watchman response error member;
// the server can exit 0 while reporting an error (E0-T5 §8), so every
// response is branched on `error` first.
type LifecycleError struct {
	Command string
	Message string
}

func (e *LifecycleError) Error() string {
	return fmt.Sprintf("watchman %s: %s", e.Command, e.Message)
}

// UnavailableError reports that the watchman binary or server cannot be
// used at all, with an actionable remediation for doctor/config validate.
type UnavailableError struct {
	Detail string
}

func (e *UnavailableError) Error() string {
	return "watchman is not usable: " + e.Detail
}

// Remediation returns the actionable operator guidance required by the
// acceptance criteria (E2-T5: absence produces actionable output).
func (e *UnavailableError) Remediation() string {
	return "install Watchman (for example `brew install watchman`), ensure `watchman --version` succeeds, and rerun; see docs/integrations/watchman-public-interface-report.md for the supported version range"
}

// Client operates the public Watchman trigger lifecycle through the
// contract-grade JSON array interface only (E0-T5: the positional forms
// silently mis-parse and must not be used).
type Client struct {
	binary    string
	timeout   time.Duration
	maxOut    int
	allowlist []string
}

// NewClient returns a client for the given binary path (default
// "watchman" resolved through PATH).
func NewClient(binary string) *Client {
	if binary == "" {
		binary = "watchman"
	}
	return &Client{
		binary:  binary,
		timeout: DefaultSubprocessTimeout,
		maxOut:  DefaultSubprocessOutBytes,
		// Deliberately minimal (SEC-004): PATH to resolve the binary
		// and HOME because the watchman server locates its state
		// directory and socket through it; nothing else is inherited.
		allowlist: []string{"PATH", "HOME", "WATCHMAN_SOCK", "TMPDIR"},
	}
}

// TriggerDefinition is the managed trigger definition (normalized form
// used for comparison against trigger-list output). RelativeRoot carries
// the configured-root-relative subtree constraint (SRC-011): when the
// actual watch root is an ancestor of the configured resource root, the
// definition installs with relative_root so only the configured subtree
// can invoke the managed command.
type TriggerDefinition struct {
	Name         string   `json:"name"`
	Command      []string `json:"command"`
	AppendFiles  bool     `json:"append_files"`
	StdinFields  []string `json:"stdin"`
	Expression   []any    `json:"expression"`
	RelativeRoot string   `json:"relative_root,omitempty"`
}

// Canonical renders the definition deterministically for comparison.
func (d TriggerDefinition) Canonical() (string, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Equal compares two definitions on their normalized content.
func (d TriggerDefinition) Equal(other TriggerDefinition) bool {
	a, err1 := d.Canonical()
	b, err2 := other.Canonical()
	return err1 == nil && err2 == nil && a == b
}

// ManagedTrigger builds the managed trigger definition for one route
// (SRC-007: one explicit unique trigger name per route; append_files
// false so the payload arrives on stdin; the verified stdin field set;
// and a coarse regular-file prefilter — the trusted pattern engine, not
// the expression, remains the include/exclude authority). A non-empty
// relativeRoot (E10-T2, SRC-011) constrains the trigger to the
// configured resource subtree inside the actual — possibly ancestral —
// watch root, so the delivered payload and WATCHMAN_RELATIVE_ROOT are
// configured-root-relative.
func ManagedTrigger(name string, command []string, relativeRoot string) TriggerDefinition {
	def := TriggerDefinition{
		Name:        name,
		Command:     command,
		AppendFiles: false,
		StdinFields: []string{"name", "exists", "new", "size", "type"},
		Expression:  []any{"type", "f"},
	}
	if relativeRoot != "" && relativeRoot != "." {
		def.RelativeRoot = relativeRoot
	}
	return def
}

// response is the common envelope of every watchman reply.
type response struct {
	Version      string              `json:"version"`
	Error        string              `json:"error"`
	Disposition  string              `json:"disposition"`
	TriggerID    string              `json:"triggerid"`
	Deleted      *bool               `json:"deleted"`
	WatchDeleted bool                `json:"watch-del"`
	Trigger      string              `json:"trigger"`
	Triggers     []TriggerDefinition `json:"triggers"`
	Watch        string              `json:"watch"`
	Watcher      string              `json:"watcher"`
	Clock        string              `json:"clock"`
	Warning      string              `json:"warning"`
}

// run executes one `-j` array command and parses the response, branching
// on the `error` member first (E0-T5 §8).
func (c *Client) run(ctx context.Context, argv []any) (*response, error) {
	var resp response
	if err := c.runInto(ctx, argv, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// runInto executes one `-j` array command and decodes the response into
// out after branching on the `error` member first.
func (c *Client) runInto(ctx context.Context, argv []any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	payload, err := json.Marshal(argv)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, c.binary, "--no-pretty", "-j")
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = c.env()
	// Capture through a real file: the child writes directly (no pipe
	// copy races) and the read is bounded to maxOut (SEC-004).
	outFile, err := os.CreateTemp("", "agent-dispatch-watchman-*.json")
	if err != nil {
		return fmt.Errorf("creating capture file: %w", err)
	}
	defer os.Remove(outFile.Name())
	defer outFile.Close()
	cmd.Stdout = outFile
	cmd.Stderr = outFile
	runErr := cmd.Run()
	_, _ = outFile.Seek(0, 0)
	raw, _ := io.ReadAll(io.LimitReader(outFile, int64(c.maxOut)))
	body := bytes.TrimSpace(raw)
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &UnavailableError{Detail: fmt.Sprintf("command timed out after %s", c.timeout)}
		}
		return &UnavailableError{Detail: fmt.Sprintf("cannot run %q: %v%s", c.binary, runErr, hint(string(body)))}
	}
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return &LifecycleError{Command: fmt.Sprint(argv[0]), Message: "unparseable response: " + truncate(string(body), 300)}
	}
	if envelope.Error != "" {
		return &LifecycleError{Command: fmt.Sprint(argv[0]), Message: envelope.Error}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &LifecycleError{Command: fmt.Sprint(argv[0]), Message: "unparseable response: " + truncate(string(body), 300)}
	}
	return nil
}

func hint(stderr string) string {
	s := strings.TrimSpace(stderr)
	if s == "" {
		return ""
	}
	return " (" + truncate(s, 300) + ")"
}

// truncate keeps embedded child output bounded in error text.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func (c *Client) env() []string {
	var out []string
	for _, k := range c.allowlist {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// SetTimeoutForTest overrides the subprocess timeout (tests only).
func (c *Client) SetTimeoutForTest(d time.Duration) { c.timeout = d }

// Version returns the server version and verifies it against the
// supported baseline.
func (c *Client) Version(ctx context.Context) (string, error) {
	resp, err := c.run(ctx, []any{"version"})
	if err != nil {
		return "", err
	}
	if resp.Version == "" {
		return "", &UnavailableError{Detail: "server reported no version"}
	}
	return resp.Version, nil
}

// CheckVersionSupported compares a version against the frozen baseline
// numerically over the four `YYYY.MM.DD.NN` components; an unparseable
// version fails closed.
func CheckVersionSupported(version string) error {
	if compareVersions(version, minimumSupportedVersion) < 0 {
		return &LifecycleError{Command: "version", Message: fmt.Sprintf("watchman %s is older than the supported baseline %s", version, minimumSupportedVersion)}
	}
	return nil
}

func compareVersions(a, b string) int {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	if !okA || !okB {
		return -1 // unparseable is unsupported
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVersion(v string) ([4]int, bool) {
	var out [4]int
	parts := strings.Split(v, ".")
	if len(parts) != 4 {
		return out, false
	}
	for i, p := range parts {
		n := 0
		if p == "" || len(p) > 9 {
			return out, false // absurd components are unparseable
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return out, false
			}
			n = n*10 + int(r-'0')
		}
		out[i] = n
	}
	return out, true
}

// EnsureWatch makes sure the root is watched and returns the canonical
// watch root (macOS /tmp → /private/tmp), which callers must use for all
// further commands and for binding validation.
func (c *Client) EnsureWatch(ctx context.Context, root string) (string, error) {
	resp, err := c.run(ctx, []any{"watch-project", root})
	if err != nil {
		return "", err
	}
	if resp.Watch == "" {
		return "", &LifecycleError{Command: "watch-project", Message: "no watch root reported"}
	}
	return resp.Watch, nil
}

// IsWatched reports whether the given root is currently watched, so
// status and remove can inspect state without creating a watch
// (EnsureWatch has the side effect of starting one). A root nested under
// a watched ancestor is watched (E10-T2): the server covers it through
// the ancestor's watch even though the nested root itself never appears
// in watch-list.
func (c *Client) IsWatched(ctx context.Context, root string) (bool, error) {
	roots, err := c.WatchList(ctx)
	if err != nil {
		return false, err
	}
	want := filepath.Clean(root)
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = resolved
	}
	for _, r := range roots {
		cleaned := filepath.Clean(r)
		if cleaned == want || coversRoot(cleaned, want) {
			return true, nil
		}
	}
	return false, nil
}

// coversRoot reports whether a watched root covers want as an ancestor
// (round-1 review: a filesystem-root watch "/" must cover every absolute
// path, which a naive separator-append spelling missed).
func coversRoot(watched, want string) bool {
	if watched == string(filepath.Separator) {
		return filepath.IsAbs(want)
	}
	return strings.HasPrefix(want, watched+string(filepath.Separator))
}

// WatchDelete drops the server's watch on one root. The managed remove
// command never calls it (the operator owns watch roots, SRC-012); the
// test harnesses use it so disposable trees do not accumulate FSEvent
// streams across repeated runs.
func (c *Client) WatchDelete(ctx context.Context, root string) error {
	resp, err := c.run(ctx, []any{"watch-del", root})
	if err != nil {
		return err
	}
	// watch-del confirms through its namesake member (the frozen
	// interface reports {"watch-del": true}), not a "deleted" member.
	if !resp.WatchDeleted {
		return &LifecycleError{Command: "watch-del", Message: "watch was not deleted"}
	}
	return nil
}

// WatchList returns every root the server currently watches (E10-T2,
// SRC-012): the removal proof searches all of them for the managed
// trigger identity, not only the configured root.
func (c *Client) WatchList(ctx context.Context) ([]string, error) {
	var wl struct {
		Roots []string `json:"roots"`
	}
	if err := c.runInto(ctx, []any{"watch-list"}, &wl); err != nil {
		return nil, err
	}
	return wl.Roots, nil
}

// TriggerList returns the installed trigger definitions on a root.
func (c *Client) TriggerList(ctx context.Context, watchRoot string) ([]TriggerDefinition, error) {
	resp, err := c.run(ctx, []any{"trigger-list", watchRoot})
	if err != nil {
		return nil, err
	}
	return resp.Triggers, nil
}

// TriggerInstall installs a definition and returns the disposition:
// "created", "replaced" (resets the incremental position), or
// "already_defined" (a true no-op that preserves the position).
func (c *Client) TriggerInstall(ctx context.Context, watchRoot string, def TriggerDefinition) (string, error) {
	argv := []any{"trigger", watchRoot, def}
	resp, err := c.run(ctx, argv)
	if err != nil {
		return "", err
	}
	switch resp.Disposition {
	case "created", "replaced", "already_defined":
		return resp.Disposition, nil
	default:
		return "", &LifecycleError{Command: "trigger", Message: "unexpected disposition " + resp.Disposition}
	}
}

// TriggerDelete removes exactly the named trigger; deleting an absent
// trigger is not an error (deleted=false).
func (c *Client) TriggerDelete(ctx context.Context, watchRoot, name string) (bool, error) {
	resp, err := c.run(ctx, []any{"trigger-del", watchRoot, name})
	if err != nil {
		return false, err
	}
	if resp.Deleted == nil {
		return false, &LifecycleError{Command: "trigger-del", Message: "no deleted member in response"}
	}
	return *resp.Deleted, nil
}

// FindTrigger returns the installed definition with the exact name.
func FindTrigger(defs []TriggerDefinition, name string) (TriggerDefinition, bool) {
	for _, d := range defs {
		if d.Name == name {
			return d, true
		}
	}
	return TriggerDefinition{}, false
}

// SortedNames is a diagnostic helper.
func SortedNames(defs []TriggerDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	sort.Strings(out)
	return out
}
