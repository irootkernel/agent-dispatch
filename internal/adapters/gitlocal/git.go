// Package gitlocal exposes the closed Git vocabulary used by Wiki sync.
// It deliberately has no generic argv or environment entrypoint.
package gitlocal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

const (
	maxRuntime = 120 * time.Second
	maxOutput  = 1 << 20
)

var (
	ErrMissingRef       = errors.New("git ref is missing")
	ErrInvalidSignature = errors.New("git SSH signature is invalid")
	ErrTimeout          = errors.New("git subprocess timed out")
	ErrOutputBound      = errors.New("git subprocess output exceeded its bound")
	ErrPushRejected     = errors.New("git fast-forward push was rejected")
	ErrPushAmbiguous    = errors.New("git push outcome is ambiguous")
	ErrHistoryBound     = errors.New("git history exceeds configured bound")
	ErrFetchRewrite     = errors.New("git fetch would rewrite approved history")
	ErrRemoteBinding    = errors.New("git remote binding is not approved")
)

type Limits struct {
	Timeout   time.Duration
	MaxOutput int
}

type Client struct {
	root      string
	git       string
	ssh       string
	sshKeygen string
	timeout   time.Duration
	maxOutput int
}

type WorktreeState struct {
	State WorktreeCondition
	Head  string
}

type WorktreeCondition string

const (
	WorktreeClean WorktreeCondition = "clean"
	WorktreeDirty WorktreeCondition = "dirty"
)

type Relation string

const (
	RelationEqual    Relation = "equal"
	RelationAhead    Relation = "ahead"
	RelationBehind   Relation = "behind"
	RelationDiverged Relation = "diverged"
)

type PushResult struct {
	State      PushState
	RemoteOID  string
	Candidate  string
	Underlying error
}

type PushState string

const (
	PushConfirmed  PushState = "confirmed"
	PushRejected   PushState = "rejected"
	PushNotStarted PushState = "not_started"
	PushAmbiguous  PushState = "ambiguous"
)

// ImportState is the exact Git/index/worktree fence captured before a live
// import. DirtyPaths excludes ignored files because Git proves they cannot be
// overwritten by a tracked target path without reporting an untracked
// collision first.
type ImportState struct {
	Head            string
	ContentRef      string
	IndexTree       string
	Digest          string
	DirtyPaths      []string
	UntrackedPaths  []string
	ActiveOperation string
}

func New(root string, limits Limits) (*Client, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("git root must be absolute")
	}
	clean := filepath.Clean(root)
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("git root is not a directory: %w", err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("locating git: %w", err)
	}
	git, err = filepath.Abs(git)
	if err != nil {
		return nil, fmt.Errorf("resolving git executable: %w", err)
	}
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		return nil, fmt.Errorf("locating ssh: %w", err)
	}
	ssh, err = filepath.Abs(ssh)
	if err != nil {
		return nil, fmt.Errorf("resolving ssh executable: %w", err)
	}
	sshKeygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		return nil, fmt.Errorf("locating ssh-keygen: %w", err)
	}
	sshKeygen, err = filepath.Abs(sshKeygen)
	if err != nil {
		return nil, fmt.Errorf("resolving ssh-keygen executable: %w", err)
	}
	if limits.Timeout <= 0 || limits.Timeout > maxRuntime {
		return nil, fmt.Errorf("git timeout must be between 1ns and %s", maxRuntime)
	}
	if limits.MaxOutput < 1024 || limits.MaxOutput > maxOutput {
		return nil, fmt.Errorf("git output bound must be 1024..%d bytes per stream", maxOutput)
	}
	return &Client{root: clean, git: git, ssh: ssh, sshKeygen: sshKeygen, timeout: limits.Timeout, maxOutput: limits.MaxOutput}, nil
}

func (c *Client) Inspect(ctx context.Context) (WorktreeState, error) {
	out, _, err := c.run(ctx, nil, nil, "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all")
	if err != nil {
		return WorktreeState{}, err
	}
	state := WorktreeState{State: WorktreeClean}
	for _, record := range bytes.Split(out, []byte{0}) {
		line := string(record)
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			state.Head = strings.TrimPrefix(line, "# branch.oid ")
		case line != "" && !strings.HasPrefix(line, "# "):
			state.State = WorktreeDirty
		}
	}
	return state, nil
}

// InspectImport captures a closed, digestible view of the checked-out content
// ref, index, and non-ignored local changes. It has no mutating Git operation.
func (c *Client) InspectImport(ctx context.Context, contentRef string) (ImportState, error) {
	if !validRef(contentRef) {
		return ImportState{}, fmt.Errorf("unsafe content ref")
	}
	headRaw, _, err := c.run(ctx, nil, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return ImportState{}, err
	}
	head := strings.TrimSpace(string(headRaw))
	if !validOID(head) {
		return ImportState{}, fmt.Errorf("git returned an invalid HEAD")
	}
	content, err := c.ResolveRef(ctx, contentRef)
	if err != nil {
		return ImportState{}, err
	}
	symbolic, _, err := c.run(ctx, nil, nil, "symbolic-ref", "-q", "HEAD")
	if err != nil || strings.TrimSpace(string(symbolic)) != contentRef || head != content {
		return ImportState{}, fmt.Errorf("checked-out HEAD is not the configured content ref")
	}
	indexRaw, _, err := c.run(ctx, nil, nil, "write-tree")
	if err != nil {
		return ImportState{}, err
	}
	indexTree := strings.TrimSpace(string(indexRaw))
	if !validOID(indexTree) {
		return ImportState{}, fmt.Errorf("git returned an invalid index tree")
	}
	status, _, err := c.run(ctx, nil, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=no")
	if err != nil {
		return ImportState{}, err
	}
	dirty, untracked, err := parsePorcelainPaths(status)
	if err != nil {
		return ImportState{}, err
	}
	ignoredRaw, _, ignoredErr := c.run(ctx, nil, nil, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if ignoredErr != nil {
		return ImportState{}, ignoredErr
	}
	for _, rawPath := range bytes.Split(ignoredRaw, []byte{0}) {
		if len(rawPath) == 0 {
			continue
		}
		path := string(rawPath)
		if !validTreePath(path) {
			return ImportState{}, fmt.Errorf("git ignored-file listing contains an unsafe path")
		}
		untracked = append(untracked, path)
	}
	sort.Strings(untracked)
	operation, err := c.activeOperation(ctx)
	if err != nil {
		return ImportState{}, err
	}
	for _, path := range dirty {
		if operation == "" && strings.HasPrefix(path, ".agent-dispatch-sync/") {
			operation = "controller_dirty"
			break
		}
	}
	h := sha256.New()
	for _, value := range []string{"agent-dispatch.git-import-state/v1", head, content, indexTree, operation} {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	_, _ = h.Write(status)
	// Ignored occupants participate in the closed overwrite fence even though
	// porcelain intentionally omits them.
	_, _ = h.Write(ignoredRaw)
	return ImportState{Head: head, ContentRef: content, IndexTree: indexTree,
		Digest: "sha256:" + fmt.Sprintf("%x", h.Sum(nil)), DirtyPaths: dirty,
		UntrackedPaths: untracked, ActiveOperation: operation}, nil
}

func parsePorcelainPaths(raw []byte) ([]string, []string, error) {
	records := bytes.Split(raw, []byte{0})
	dirtySet, untrackedSet := map[string]bool{}, map[string]bool{}
	for i := 0; i < len(records); i++ {
		record := records[i]
		if len(record) == 0 {
			continue
		}
		if len(record) < 4 || record[2] != ' ' {
			return nil, nil, fmt.Errorf("invalid Git porcelain record")
		}
		status, path := string(record[:2]), string(record[3:])
		if !validTreePath(path) {
			return nil, nil, fmt.Errorf("git status contains an unsafe path")
		}
		dirtySet[path] = true
		if status == "??" {
			untrackedSet[path] = true
		}
		if status[0] == 'R' || status[0] == 'C' || status[1] == 'R' || status[1] == 'C' {
			i++
			if i >= len(records) || len(records[i]) == 0 || !validTreePath(string(records[i])) {
				return nil, nil, fmt.Errorf("invalid Git rename status")
			}
			dirtySet[string(records[i])] = true
		}
	}
	dirty, untracked := make([]string, 0, len(dirtySet)), make([]string, 0, len(untrackedSet))
	for path := range dirtySet {
		dirty = append(dirty, path)
	}
	for path := range untrackedSet {
		untracked = append(untracked, path)
	}
	sort.Strings(dirty)
	sort.Strings(untracked)
	return dirty, untracked, nil
}

func (c *Client) activeOperation(ctx context.Context) (string, error) {
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "BISECT_LOG"} {
		raw, _, err := c.run(ctx, nil, nil, "rev-parse", "--git-path", marker)
		if err != nil {
			return "", err
		}
		path := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.root, path)
		}
		if _, err := os.Lstat(path); err == nil {
			return marker, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}

// ImportOverlap returns exact or Unicode/case aliases between target effects
// and dirty paths. Case folding is intentionally conservative on every host:
// a plan safe under this rule is also safe if the vault later moves to a
// case-insensitive filesystem.
func ImportOverlap(targets, dirty, untracked []string) (overlap, collisions []string) {
	target := map[string]string{}
	for _, path := range targets {
		target[records.PortablePathIdentity(path)] = path
	}
	for _, path := range dirty {
		if _, ok := target[records.PortablePathIdentity(path)]; ok {
			overlap = append(overlap, path)
		}
	}
	for _, path := range untracked {
		if _, ok := target[records.PortablePathIdentity(path)]; ok {
			collisions = append(collisions, path)
		}
	}
	sort.Strings(overlap)
	sort.Strings(collisions)
	return overlap, collisions
}

// FirstParentRange returns commits after from through target in application
// order. Merge commits, an uncovered base, and bound exhaustion fail closed.
func (c *Client) FirstParentRange(ctx context.Context, from, target string, limit int) ([]string, error) {
	if !validOID(from) || !validOID(target) || limit < 1 || limit > 100000 {
		return nil, fmt.Errorf("invalid bounded history request")
	}
	if from == target {
		return []string{}, nil
	}
	var reverse []string
	current := target
	for len(reverse) < limit {
		parents, err := c.CommitParents(ctx, current)
		if err != nil {
			return nil, err
		}
		if len(parents) != 1 {
			return nil, fmt.Errorf("content history is not linear")
		}
		reverse = append(reverse, current)
		if parents[0] == from {
			for i, j := 0, len(reverse)-1; i < j; i, j = i+1, j-1 {
				reverse[i], reverse[j] = reverse[j], reverse[i]
			}
			return reverse, nil
		}
		current = parents[0]
	}
	return nil, ErrHistoryBound
}

// ApplyImportIndex stages only the target paths and advances the checked-out
// content ref with expected-old semantics. Disjoint staged entries remain
// byte-for-byte represented by the existing index.
func (c *Client) ApplyImportIndex(ctx context.Context, contentRef, from, target string, files map[string][]byte, deletions []string) error {
	if !validRef(contentRef) || !validOID(from) || !validOID(target) || from == target {
		return fmt.Errorf("invalid import index request")
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		if !validTreePath(path) {
			return fmt.Errorf("unsafe import path %q", path)
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		blob, _, err := c.run(ctx, nil, files[path], "hash-object", "-w", "--stdin")
		if err != nil {
			return err
		}
		oid := strings.TrimSpace(string(blob))
		if !validOID(oid) {
			return fmt.Errorf("git returned an invalid import blob")
		}
		if _, _, err := c.run(ctx, nil, nil, "update-index", "--add", "--cacheinfo", "100644,"+oid+","+path); err != nil {
			return err
		}
	}
	for _, path := range deletions {
		if !validTreePath(path) {
			return fmt.Errorf("unsafe import deletion %q", path)
		}
		if _, _, err := c.run(ctx, nil, nil, "update-index", "--force-remove", "--", path); err != nil {
			return err
		}
	}
	// Controller records are verified append-only evidence, not Watchman
	// attribution effects. They still need to follow the checked-out commit so
	// the index/worktree does not become dirty merely because the ref advanced.
	controllers := map[string][]byte{}
	for _, prefix := range []string{".agent-dispatch-sync/publications/", ".agent-dispatch-sync/checkpoints/", ".agent-dispatch-sync/checkpoint-plans/"} {
		entries, err := c.ReadTreePrefix(ctx, target, prefix)
		if err != nil {
			return err
		}
		for path, raw := range entries {
			controllers[path] = raw
		}
	}
	controllerPaths := make([]string, 0, len(controllers))
	for path := range controllers {
		controllerPaths = append(controllerPaths, path)
	}
	sort.Strings(controllerPaths)
	for _, path := range controllerPaths {
		raw := controllers[path]
		if err := c.writeControllerFile(path, raw); err != nil {
			return err
		}
		blob, _, err := c.run(ctx, nil, raw, "hash-object", "-w", "--stdin")
		if err != nil {
			return err
		}
		oid := strings.TrimSpace(string(blob))
		if !validOID(oid) {
			return fmt.Errorf("git returned an invalid controller blob")
		}
		if _, _, err := c.run(ctx, nil, nil, "update-index", "--add", "--cacheinfo", "100644,"+oid+","+path); err != nil {
			return err
		}
	}
	current, err := c.ResolveRef(ctx, contentRef)
	if err != nil {
		return err
	}
	if current == target {
		return nil
	}
	if current != from {
		return fmt.Errorf("content ref changed during import")
	}
	return c.UpdateRefExpected(ctx, contentRef, target, from)
}

func (c *Client) writeControllerFile(path string, raw []byte) error {
	if !validContentControllerPath(path) {
		return fmt.Errorf("unsafe controller path")
	}
	dir := filepath.Dir(filepath.Join(c.root, filepath.FromSlash(path)))
	if err := c.checkControllerAncestors(path); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Recheck after creating missing directories. This matches the Markdown
	// writer's containment boundary and catches an ancestor replaced while the
	// path was being prepared.
	if err := c.checkControllerAncestors(path); err != nil {
		return err
	}
	target := filepath.Join(c.root, filepath.FromSlash(path))
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("controller target is a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".agent-dispatch-controller-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	err = tmp.Chmod(0o600)
	if err == nil {
		var written int
		written, err = tmp.Write(raw)
		if err == nil && written != len(raw) {
			err = fmt.Errorf("short controller write")
		}
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, target)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

func (c *Client) checkControllerAncestors(path string) error {
	cur := c.root
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(path)), "/") {
		cur = filepath.Join(cur, filepath.FromSlash(part))
		if info, err := os.Lstat(cur); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("controller path ancestor is not a regular directory")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (c *Client) ResolveRef(ctx context.Context, ref string) (string, error) {
	if !validRef(ref) {
		return "", fmt.Errorf("unsafe Git ref %q", ref)
	}
	out, _, err := c.run(ctx, nil, nil, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		if exitCode(err) == 128 {
			return "", ErrMissingRef
		}
		return "", err
	}
	oid := strings.TrimSpace(string(out))
	if !validOID(oid) {
		return "", fmt.Errorf("git returned an invalid object ID")
	}
	return oid, nil
}

func (c *Client) Compare(ctx context.Context, left, right string) (Relation, error) {
	if !validOID(left) || !validOID(right) {
		return "", fmt.Errorf("full Git object IDs are required")
	}
	if left == right {
		return RelationEqual, nil
	}
	leftAncestor, err := c.isAncestor(ctx, left, right)
	if err != nil {
		return "", err
	}
	rightAncestor, err := c.isAncestor(ctx, right, left)
	if err != nil {
		return "", err
	}
	switch {
	case leftAncestor:
		return RelationBehind, nil
	case rightAncestor:
		return RelationAhead, nil
	default:
		return RelationDiverged, nil
	}
}

func (c *Client) isAncestor(ctx context.Context, older, newer string) (bool, error) {
	_, _, err := c.run(ctx, nil, nil, "merge-base", "--is-ancestor", older, newer)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

func (c *Client) Fetch(ctx context.Context, remote, sourceRef, destinationRef, repositoryDigest string) error {
	remoteURL, err := c.verifyRemote(ctx, remote, repositoryDigest)
	if err != nil {
		return err
	}
	if !validRef(sourceRef) || !validRef(destinationRef) {
		return fmt.Errorf("unsafe fetch ref")
	}
	_, stderr, err := c.run(ctx, nil, nil, "fetch", "--upload-pack=git-upload-pack", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", remoteURL, sourceRef+":"+destinationRef)
	if err != nil && fetchRejectedRewrite(stderr) {
		return fmt.Errorf("%w: %v", ErrFetchRewrite, err)
	}
	return err
}

func fetchRejectedRewrite(stderr []byte) bool {
	message := strings.ToLower(string(stderr))
	return strings.Contains(message, "non-fast-forward") || strings.Contains(message, "would clobber existing tag")
}

func (c *Client) ReadFile(ctx context.Context, oid, path string) ([]byte, error) {
	if !validOID(oid) || !validTreePath(path) {
		return nil, fmt.Errorf("invalid object or tree path")
	}
	out, _, err := c.run(ctx, nil, nil, "cat-file", "blob", oid+":"+path)
	return out, err
}

func (c *Client) CommitParents(ctx context.Context, oid string) ([]string, error) {
	if !validOID(oid) {
		return nil, fmt.Errorf("full Git object ID is required")
	}
	out, _, err := c.run(ctx, nil, nil, "cat-file", "commit", oid)
	if err != nil {
		return nil, err
	}
	var parents []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "parent ") {
			parent := strings.TrimPrefix(line, "parent ")
			if !validOID(parent) {
				return nil, fmt.Errorf("commit contains an invalid parent")
			}
			parents = append(parents, parent)
		}
	}
	return parents, nil
}

func (c *Client) VerifySSHSignature(ctx context.Context, oid, expectedFingerprint string) error {
	if !validOID(oid) || !validFingerprint(expectedFingerprint) {
		return ErrInvalidSignature
	}
	raw, _, err := c.run(ctx, nil, nil, "cat-file", "commit", oid)
	if err != nil {
		return err
	}
	keyType, keyBlob, fingerprint, err := embeddedSSHKey(raw)
	if err != nil || subtle.ConstantTimeCompare([]byte(fingerprint), []byte(expectedFingerprint)) != 1 {
		return ErrInvalidSignature
	}
	tmp, err := os.MkdirTemp("", "agent-dispatch-signers-")
	if err != nil {
		return fmt.Errorf("creating signer policy: %w", err)
	}
	defer os.RemoveAll(tmp)
	allowed := filepath.Join(tmp, "allowed_signers")
	line := "* " + keyType + " " + base64.StdEncoding.EncodeToString(keyBlob) + "\n"
	if err := os.WriteFile(allowed, []byte(line), 0o600); err != nil {
		return fmt.Errorf("writing signer policy: %w", err)
	}
	_, _, err = c.run(ctx, nil, nil,
		"-c", "gpg.format=ssh", "-c", "gpg.ssh.allowedSignersFile="+allowed,
		"verify-commit", "--raw", oid)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return nil
}

// CreateSignedMembershipCommit writes immutable objects through a private
// index and creates one SSH-signed commit. It does not update a ref.
func (c *Client) CreateSignedMembershipCommit(ctx context.Context, document, plan, privateKey []byte, predecessor string, now time.Time) (string, error) {
	if len(document) == 0 || len(plan) == 0 || len(privateKey) == 0 || (predecessor != "" && !validOID(predecessor)) {
		return "", fmt.Errorf("membership document, plan, signing key, and valid predecessor are required")
	}
	tmp, err := os.MkdirTemp("", "agent-dispatch-membership-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	keyPath := filepath.Join(tmp, "administrator_key")
	if err := os.WriteFile(keyPath, privateKey, 0o600); err != nil {
		return "", fmt.Errorf("writing ephemeral signing key: %w", err)
	}
	env := map[string]string{
		"GIT_INDEX_FILE":      filepath.Join(tmp, "index"),
		"GIT_AUTHOR_DATE":     now.UTC().Format(time.RFC3339),
		"GIT_COMMITTER_DATE":  now.UTC().Format(time.RFC3339),
		"GIT_AUTHOR_NAME":     "Agent Dispatch Membership",
		"GIT_AUTHOR_EMAIL":    "membership@agent-dispatch.invalid",
		"GIT_COMMITTER_NAME":  "Agent Dispatch Membership",
		"GIT_COMMITTER_EMAIL": "membership@agent-dispatch.invalid",
	}
	blob, _, err := c.run(ctx, env, document, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	blobOID := strings.TrimSpace(string(blob))
	if !validOID(blobOID) {
		return "", fmt.Errorf("git returned an invalid blob ID")
	}
	if _, _, err := c.run(ctx, env, nil, "read-tree", "--empty"); err != nil {
		return "", err
	}
	if _, _, err := c.run(ctx, env, nil, "update-index", "--add", "--cacheinfo", "100644,"+blobOID+",.agent-dispatch-sync/membership.json"); err != nil {
		return "", err
	}
	planBlob, _, err := c.run(ctx, env, plan, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	planOID := strings.TrimSpace(string(planBlob))
	if !validOID(planOID) {
		return "", fmt.Errorf("git returned an invalid plan blob ID")
	}
	if _, _, err := c.run(ctx, env, nil, "update-index", "--add", "--cacheinfo", "100644,"+planOID+",.agent-dispatch-sync/membership-plan.json"); err != nil {
		return "", err
	}
	tree, _, err := c.run(ctx, env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	treeOID := strings.TrimSpace(string(tree))
	args := []string{"-c", "gpg.format=ssh", "-c", "user.signingKey=" + keyPath, "commit-tree", "-S" + keyPath, treeOID}
	if predecessor != "" {
		args = append(args, "-p", predecessor)
	}
	args = append(args, "-m", "Update Agent Dispatch membership")
	commit, _, err := c.run(ctx, env, nil, args...)
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(commit))
	if !validOID(oid) {
		return "", fmt.Errorf("git returned an invalid commit ID")
	}
	return oid, nil
}

// ReadMembershipCommit accepts exactly the two regular files owned by the
// membership contract. Extra paths, symlinks, and executable entries fail
// closed before either document is trusted.
func (c *Client) ReadMembershipCommit(ctx context.Context, oid string) ([]byte, []byte, error) {
	if !validOID(oid) {
		return nil, nil, fmt.Errorf("full Git object ID is required")
	}
	out, _, err := c.run(ctx, nil, nil, "ls-tree", "-r", "--full-tree", "-z", oid)
	if err != nil {
		return nil, nil, err
	}
	want := map[string]bool{
		".agent-dispatch-sync/membership.json":      true,
		".agent-dispatch-sync/membership-plan.json": true,
	}
	seen := map[string]bool{}
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		parts := bytes.SplitN(entry, []byte{'\t'}, 2)
		meta := bytes.Fields(parts[0])
		if len(parts) != 2 || len(meta) != 3 || string(meta[0]) != "100644" || string(meta[1]) != "blob" || !want[string(parts[1])] || seen[string(parts[1])] {
			return nil, nil, fmt.Errorf("membership commit has a non-contract tree layout")
		}
		seen[string(parts[1])] = true
	}
	if len(seen) != len(want) {
		return nil, nil, fmt.Errorf("membership commit is missing a contract file")
	}
	document, err := c.ReadFile(ctx, oid, ".agent-dispatch-sync/membership.json")
	if err != nil {
		return nil, nil, err
	}
	plan, err := c.ReadFile(ctx, oid, ".agent-dispatch-sync/membership-plan.json")
	if err != nil {
		return nil, nil, err
	}
	return document, plan, nil
}

func (c *Client) UpdateRefExpected(ctx context.Context, ref, candidate, expected string) error {
	if !validRef(ref) || !validOID(candidate) || (expected != "" && !validOID(expected)) {
		return fmt.Errorf("invalid expected-ref update")
	}
	current, resolveErr := c.ResolveRef(ctx, ref)
	if resolveErr == nil {
		if _, _, err := c.run(ctx, nil, nil, "symbolic-ref", "--quiet", ref); err == nil {
			return fmt.Errorf("expected-old ref update refuses symbolic ref %q", ref)
		} else if exitCode(err) != 1 {
			return err
		}
		if expected == "" || current != expected {
			return fmt.Errorf("expected-old ref does not match: %w", ErrPushRejected)
		}
	} else if !errors.Is(resolveErr, ErrMissingRef) {
		return resolveErr
	} else if expected != "" {
		return fmt.Errorf("expected-old ref is missing: %w", ErrPushRejected)
	}
	old := expected
	if old == "" {
		old = strings.Repeat("0", len(candidate))
	}
	_, _, err := c.run(ctx, nil, nil, "update-ref", ref, candidate, old)
	return err
}

func (c *Client) PushFastForward(ctx context.Context, remote, ref, candidate, expected, repositoryDigest string) PushResult {
	result := PushResult{State: PushAmbiguous, Candidate: candidate}
	if !validRef(ref) || !validOID(candidate) || (expected != "" && !validOID(expected)) {
		result.Underlying = fmt.Errorf("invalid push identity")
		return result
	}
	remoteURL, err := c.verifyRemote(ctx, remote, repositoryDigest)
	if err != nil {
		result.State, result.Underlying = PushRejected, err
		return result
	}
	remoteBefore, err := c.remoteRefURL(ctx, remoteURL, ref)
	if err != nil && !errors.Is(err, ErrMissingRef) {
		result.State, result.Underlying = PushNotStarted, err
		return result
	}
	if remoteBefore == candidate {
		result.State, result.RemoteOID = PushConfirmed, candidate
		return result
	}
	if remoteBefore != expected {
		result.State, result.RemoteOID = PushRejected, remoteBefore
		result.Underlying = ErrPushRejected
		return result
	}
	_, _, pushErr := c.run(ctx, nil, nil, "push", "--receive-pack=git-receive-pack", "--porcelain", "--no-force", "--no-verify", "--recurse-submodules=no", remoteURL, candidate+":"+ref)
	remoteAfter, inspectErr := c.remoteRefURL(ctx, remoteURL, ref)
	if errors.Is(inspectErr, ErrMissingRef) {
		remoteAfter, inspectErr = "", nil
	}
	result.RemoteOID = remoteAfter
	if inspectErr == nil && remoteAfter == candidate {
		result.State = PushConfirmed
		return result
	}
	if pushErr == nil {
		result.Underlying = ErrPushAmbiguous
		return result
	}
	if inspectErr == nil && remoteAfter == expected {
		result.State, result.Underlying = PushRejected, fmt.Errorf("%w: %v", ErrPushRejected, pushErr)
		return result
	}
	result.Underlying = fmt.Errorf("%w: push=%v inspection=%v", ErrPushAmbiguous, pushErr, inspectErr)
	return result
}

func (c *Client) RemoteRef(ctx context.Context, remote, ref, repositoryDigest string) (string, error) {
	remoteURL, err := c.verifyRemote(ctx, remote, repositoryDigest)
	if err != nil {
		return "", err
	}
	return c.remoteRefURL(ctx, remoteURL, ref)
}

func (c *Client) remoteRefURL(ctx context.Context, remoteURL, ref string) (string, error) {
	if !validRef(ref) {
		return "", fmt.Errorf("unsafe remote ref")
	}
	out, _, err := c.run(ctx, nil, nil, "ls-remote", "--upload-pack=git-upload-pack", "--refs", remoteURL, ref)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", ErrMissingRef
	}
	fields := strings.Fields(line)
	if len(fields) != 2 || fields[1] != ref || !validOID(fields[0]) {
		return "", fmt.Errorf("ambiguous remote-ref response")
	}
	return fields[0], nil
}

func (c *Client) verifyRemote(ctx context.Context, remote, expectedDigest string) (string, error) {
	if !validRemoteName(remote) || !strings.HasPrefix(expectedDigest, "sha256:") {
		return "", fmt.Errorf("invalid configured remote binding: %w", ErrRemoteBinding)
	}
	if err := c.rejectURLRewrites(ctx); err != nil {
		return "", fmt.Errorf("configured remote rewrite policy could not be verified: %w: %v", ErrRemoteBinding, err)
	}
	out, _, err := c.run(ctx, nil, nil, "remote", "get-url", "--all", remote)
	if err != nil {
		return "", fmt.Errorf("configured remote fetch URL could not be verified: %w: %v", ErrRemoteBinding, err)
	}
	pushOut, _, err := c.run(ctx, nil, nil, "remote", "get-url", "--push", "--all", remote)
	if err != nil {
		return "", fmt.Errorf("configured remote push URL could not be verified: %w: %v", ErrRemoteBinding, err)
	}
	urls, pushURLs := strings.Fields(string(out)), strings.Fields(string(pushOut))
	if len(urls) != 1 || len(pushURLs) != 1 || urls[0] != pushURLs[0] {
		return "", fmt.Errorf("configured remote must resolve to one identical fetch and push URL: %w", ErrRemoteBinding)
	}
	digest, _, err := config.RemoteRepositoryDigest(urls[0])
	if err != nil || subtle.ConstantTimeCompare([]byte(digest), []byte(expectedDigest)) != 1 {
		return "", fmt.Errorf("configured remote repository identity does not match its approved digest: %w", ErrRemoteBinding)
	}
	return urls[0], nil
}

func (c *Client) rejectURLRewrites(ctx context.Context) error {
	out, _, err := c.run(ctx, nil, nil, "config", "--name-only", "--get-regexp", `^url\..*\.`)
	if err != nil {
		if exitCode(err) == 1 {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".insteadof") || strings.HasSuffix(lower, ".pushinsteadof") {
			return fmt.Errorf("configured remote refuses effective URL rewrite rule %q: %w", name, ErrRemoteBinding)
		}
	}
	return nil
}

func (c *Client) run(parent context.Context, extraEnv map[string]string, stdin []byte, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	base := []string{"-C", c.root,
		"-c", "core.bare=false",
		"-c", "core.worktree=" + c.root,
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "core.pager=cat",
		"-c", "core.attributesFile=/dev/null",
		"-c", "core.alternateRefsCommand=",
		"-c", "credential.helper=",
		"-c", "http.followRedirects=false",
		"-c", "http.proxy=",
		"-c", "https.proxy=",
		"-c", "http.extraHeader=",
		"-c", "diff.external=",
		"-c", "fetch.recurseSubmodules=false",
		"-c", "submodule.recurse=false",
		"-c", "protocol.ext.allow=never",
		"-c", "protocol.file.allow=never",
		"-c", "ssh.variant=ssh",
		"-c", "gpg.ssh.program=" + c.sshKeygen,
		"-c", "core.sshCommand=" + c.ssh + " -F /dev/null -oBatchMode=yes -oClearAllForwardings=yes -oPermitLocalCommand=no -oLocalCommand=none -oProxyCommand=none -oProxyJump=none -oRequestTTY=no -oStrictHostKeyChecking=yes",
	}
	cmd := exec.CommandContext(ctx, c.git, append(base, args...)...)
	configureProcessGroup(cmd)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PROTOCOL_FROM_USER=0",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GCM_INTERACTIVE=Never",
		"GIT_ASKPASS=/bin/false",
		"SSH_ASKPASS=/bin/false",
		"LC_ALL=C",
		"LANG=C",
	}
	if socket := os.Getenv("SSH_AUTH_SOCK"); filepath.IsAbs(socket) {
		cmd.Env = append(cmd.Env, "SSH_AUTH_SOCK="+socket)
	}
	keys := make([]string, 0, len(extraEnv))
	for key := range extraEnv {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+extraEnv[key])
	}
	var stdout, stderr boundedBuffer
	stdout.limit, stderr.limit = c.maxOutput, c.maxOutput
	stdout.cancel, stderr.cancel = cancel, cancel
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return stdout.bytes(), stderr.bytes(), ErrOutputBound
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return stdout.bytes(), stderr.bytes(), ErrTimeout
	}
	if err != nil {
		return stdout.bytes(), stderr.bytes(), &CommandError{Args: append([]string(nil), args...), ExitCode: exitCode(err), Stderr: sanitize(stderr.string()), Err: err}
	}
	return stdout.bytes(), stderr.bytes(), nil
}

type CommandError struct {
	Args     []string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("restricted git command failed (exit %d): %s", e.ExitCode, e.Stderr)
}
func (e *CommandError) Unwrap() error { return e.Err }

type boundedBuffer struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if remaining < len(p) {
		b.exceeded = true
		if b.cancel != nil {
			b.cancel()
		}
	}
	return len(p), nil
}

func (b *boundedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}
func (b *boundedBuffer) string() string { return string(b.bytes()) }

func exitCode(err error) int {
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		return commandErr.ExitCode
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func sanitize(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			return r
		}
		return -1
	}, value)
	if len(value) > 1024 {
		value = value[:1024]
	}
	return strings.TrimSpace(value)
}

var oidPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
var fingerprintPattern = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{42}[AEIMQUYcgkosw048]$`)
var remotePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validOID(value string) bool         { return oidPattern.MatchString(value) }
func validFingerprint(value string) bool { return fingerprintPattern.MatchString(value) }
func validRemoteName(value string) bool  { return remotePattern.MatchString(value) }

func validRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/") || strings.HasSuffix(ref, "/") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.Contains(ref, "//") || strings.ContainsAny(ref, " ~^:?*[\\") {
		return false
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func validTreePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "\\") || strings.Contains(path, "//") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsRune(part, 0) {
			return false
		}
	}
	return true
}

func embeddedSSHKey(commit []byte) (string, []byte, string, error) {
	lines := strings.Split(string(commit), "\n")
	var armor []string
	inside := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "gpgsig -----BEGIN SSH SIGNATURE-----"):
			inside = true
			armor = append(armor, "-----BEGIN SSH SIGNATURE-----")
		case inside && strings.HasPrefix(line, " "):
			trimmed := strings.TrimPrefix(line, " ")
			armor = append(armor, trimmed)
			if trimmed == "-----END SSH SIGNATURE-----" {
				inside = false
				goto decoded
			}
		}
	}

decoded:
	if len(armor) < 3 || armor[len(armor)-1] != "-----END SSH SIGNATURE-----" {
		return "", nil, "", ErrInvalidSignature
	}
	encoded := strings.Join(armor[1:len(armor)-1], "")
	sshsig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(sshsig) < 10 || string(sshsig[:6]) != "SSHSIG" {
		return "", nil, "", ErrInvalidSignature
	}
	if binary.BigEndian.Uint32(sshsig[6:10]) != 1 {
		return "", nil, "", ErrInvalidSignature
	}
	keyBlob, rest, ok := sshString(sshsig[10:])
	if !ok || len(rest) == 0 {
		return "", nil, "", ErrInvalidSignature
	}
	keyTypeBytes, _, ok := sshString(keyBlob)
	if !ok || string(keyTypeBytes) != "ssh-ed25519" {
		return "", nil, "", ErrInvalidSignature
	}
	hash := sha256.Sum256(keyBlob)
	fingerprint := "SHA256:" + base64.RawStdEncoding.EncodeToString(hash[:])
	return string(keyTypeBytes), keyBlob, fingerprint, nil
}

func sshString(data []byte) ([]byte, []byte, bool) {
	if len(data) < 4 {
		return nil, nil, false
	}
	n := int(binary.BigEndian.Uint32(data[:4]))
	if n < 0 || n > len(data)-4 {
		return nil, nil, false
	}
	return data[4 : 4+n], data[4+n:], true
}

// SnapshotTree creates an immutable tree from a base plus a closed set of
// regular-file replacements in a private index. No working-tree path changes.
func (c *Client) SnapshotTree(ctx context.Context, base string, files map[string][]byte) (string, error) {
	return c.SnapshotTreeChanges(ctx, base, files, nil)
}

// SnapshotTreeChanges creates a private-index tree from exact additions and
// deletions. It never changes the caller's index or working tree.
func (c *Client) SnapshotTreeChanges(ctx context.Context, base string, files map[string][]byte, deletions []string) (string, error) {
	if !validOID(base) || len(files) == 0 {
		return "", fmt.Errorf("snapshot requires a base commit and files")
	}
	tmp, err := os.MkdirTemp("", "agent-dispatch-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := map[string]string{"GIT_INDEX_FILE": filepath.Join(tmp, "index")}
	if _, _, err := c.run(ctx, env, nil, "read-tree", base+"^{tree}"); err != nil {
		return "", err
	}
	for _, path := range deletions {
		if !validTreePath(path) {
			return "", fmt.Errorf("unsafe snapshot deletion %q", path)
		}
		if _, _, err := c.run(ctx, env, nil, "update-index", "--force-remove", "--", path); err != nil {
			return "", err
		}
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		if !validTreePath(path) {
			return "", fmt.Errorf("unsafe snapshot path %q", path)
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		blob, _, err := c.run(ctx, env, files[path], "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		oid := strings.TrimSpace(string(blob))
		if !validOID(oid) {
			return "", fmt.Errorf("git returned an invalid blob ID")
		}
		if _, _, err := c.run(ctx, env, nil, "update-index", "--add", "--cacheinfo", "100644,"+oid+","+path); err != nil {
			return "", err
		}
	}
	out, _, err := c.run(ctx, env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(out))
	if !validOID(tree) {
		return "", fmt.Errorf("git returned an invalid tree ID")
	}
	return tree, nil
}

// ReadContentFiles returns the ordinary synchronized Markdown files from a
// content commit. Controller metadata is ignored; every other attachment or
// unsupported Git mode fails closed.
func (c *Client) ReadContentFiles(ctx context.Context, oid string) (map[string][]byte, error) {
	if !validOID(oid) {
		return nil, fmt.Errorf("full Git object ID is required")
	}
	out, _, err := c.run(ctx, nil, nil, "ls-tree", "-r", "--full-tree", "-z", oid)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		parts := bytes.SplitN(entry, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid content tree entry")
		}
		meta := bytes.Fields(parts[0])
		path := string(parts[1])
		if len(meta) != 3 || string(meta[0]) != "100644" || string(meta[1]) != "blob" || !validTreePath(path) {
			return nil, fmt.Errorf("content tree contains an unsupported entry")
		}
		if strings.HasPrefix(path, ".agent-dispatch-sync/") {
			if !validContentControllerPath(path) {
				return nil, fmt.Errorf("content tree contains unsupported controller path %q", path)
			}
			continue
		}
		lower := strings.ToLower(path)
		if !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".markdown") {
			return nil, fmt.Errorf("content tree contains unsupported attachment %q", path)
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	files := make(map[string][]byte, len(paths))
	for _, path := range paths {
		raw, readErr := c.ReadFile(ctx, oid, path)
		if readErr != nil {
			return nil, readErr
		}
		files[path] = raw
	}
	return files, nil
}

func validContentControllerPath(path string) bool {
	for _, prefix := range []string{
		".agent-dispatch-sync/publications/",
		".agent-dispatch-sync/checkpoints/",
		".agent-dispatch-sync/checkpoint-plans/",
	} {
		if strings.HasPrefix(path, prefix) {
			name := strings.TrimPrefix(path, prefix)
			return name != "" && !strings.Contains(name, "/") && strings.HasSuffix(name, ".json")
		}
	}
	return false
}

func (c *Client) TreeHasPrefix(ctx context.Context, oid, prefix string) (bool, error) {
	if !validOID(oid) || !validTreePath(strings.TrimSuffix(prefix, "/")) {
		return false, fmt.Errorf("invalid tree prefix query")
	}
	out, _, err := c.run(ctx, nil, nil, "ls-tree", "-r", "--name-only", "-z", oid)
	if err != nil {
		return false, err
	}
	for _, path := range bytes.Split(out, []byte{0}) {
		if strings.HasPrefix(string(path), prefix) {
			return true, nil
		}
	}
	return false, nil
}

// ReadTreePrefix returns regular non-executable blobs below one fixed
// controller prefix. Unsupported modes fail closed.
func (c *Client) ReadTreePrefix(ctx context.Context, oid, prefix string) (map[string][]byte, error) {
	if !validOID(oid) || !validTreePath(strings.TrimSuffix(prefix, "/")) {
		return nil, fmt.Errorf("invalid tree prefix query")
	}
	out, _, err := c.run(ctx, nil, nil, "ls-tree", "-r", "--full-tree", "-z", oid, "--", prefix)
	if err != nil {
		return nil, err
	}
	result := map[string][]byte{}
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		parts := bytes.SplitN(entry, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid controller tree entry")
		}
		meta := bytes.Fields(parts[0])
		path := string(parts[1])
		if len(meta) != 3 || string(meta[0]) != "100644" || string(meta[1]) != "blob" || !strings.HasPrefix(path, prefix) || !validTreePath(path) {
			return nil, fmt.Errorf("controller tree contains an unsupported entry")
		}
		raw, readErr := c.ReadFile(ctx, oid, path)
		if readErr != nil {
			return nil, readErr
		}
		result[path] = raw
	}
	return result, nil
}

// CreateSignedContentCommit signs an already frozen private-index tree. The
// key exists only in a 0700 temporary directory and no ref is moved here.
func (c *Client) CreateSignedContentCommit(ctx context.Context, tree string, privateKey []byte, predecessor string, now time.Time, role, message string) (string, error) {
	if !validOID(tree) || len(privateKey) == 0 || (predecessor != "" && !validOID(predecessor)) {
		return "", fmt.Errorf("signed content commit requires a tree, key, and valid predecessor")
	}
	if role != "publisher" && role != "administrator" {
		return "", fmt.Errorf("unsupported content signer role")
	}
	if message == "" || strings.ContainsAny(message, "\r\n") {
		return "", fmt.Errorf("invalid content commit message")
	}
	tmp, err := os.MkdirTemp("", "agent-dispatch-content-sign-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	keyPath := filepath.Join(tmp, "signing_key")
	if err := os.WriteFile(keyPath, privateKey, 0o600); err != nil {
		return "", err
	}
	name := "Agent Dispatch Publisher"
	email := "publisher@agent-dispatch.invalid"
	if role == "administrator" {
		name = "Agent Dispatch Administrator"
		email = "administrator@agent-dispatch.invalid"
	}
	env := map[string]string{"GIT_AUTHOR_DATE": now.UTC().Format(time.RFC3339), "GIT_COMMITTER_DATE": now.UTC().Format(time.RFC3339), "GIT_AUTHOR_NAME": name, "GIT_AUTHOR_EMAIL": email, "GIT_COMMITTER_NAME": name, "GIT_COMMITTER_EMAIL": email}
	args := []string{"-c", "gpg.format=ssh", "-c", "user.signingKey=" + keyPath, "commit-tree", "-S" + keyPath, tree}
	if predecessor != "" {
		args = append(args, "-p", predecessor)
	}
	args = append(args, "-m", message)
	out, _, err := c.run(ctx, env, nil, args...)
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(out))
	if !validOID(oid) {
		return "", fmt.Errorf("git returned an invalid commit ID")
	}
	return oid, nil
}

var _ io.Writer = (*boundedBuffer)(nil)
