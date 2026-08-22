package secretresolver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rootkernel/jjukkumi/internal/config"
)

// keychainCommand is the controlled macOS Keychain lookup used for
// keychain: references on darwin builds (provider-specific reference,
// configuration-spec §11). It is a variable so tests can stub the
// subprocess without touching the user's real keychain.
var keychainCommand = func(ctx context.Context, ref string) ([]byte, error) {
	return runSecurity(ctx, ref)
}

// fdFiles caches one *os.File wrapper per open descriptor for the
// process lifetime, and fdCreate serializes wrapper creation: the
// loser of a naive LoadOrStore race would drop its own just-created
// wrapper, whose runtime finalizer then closes the shared descriptor
// on the next GC cycle — the exact defect the cache exists to prevent.
// With creation serialized, exactly one wrapper per descriptor ever
// exists, its finalizer never triggers while cached, and descriptors
// are never closed here because their owning process controls their
// lifetime.
var (
	fdFiles  sync.Map // map[int]*os.File
	fdCreate sync.Mutex
	fdReadMu sync.Mutex
)

// readDeadline bounds every descriptor- and file-backed read so a
// wedged writer cannot hang a submission (SEC-009); the caller's
// context deadline applies when it is sooner.
const readDeadline = 10 * time.Second

// Resolve resolves one parsed secret reference to its value
// (WHK-003, SEC-006). Resolution failures return typed errors that name
// the reference kind, never the value; an unresolved reference is a
// definite pre-use failure because nothing has been transmitted. Every
// kind enforces the maxSecretBytes bound (SEC-009).
func Resolve(ctx context.Context, ref *config.SecretRef) (string, error) {
	if ref == nil {
		return "", errors.New("secret reference is nil")
	}
	var value string
	switch ref.Kind {
	case config.RefEnv:
		v, ok := os.LookupEnv(ref.Name)
		if !ok {
			return "", &UnresolvedError{Ref: ref, Cause: "environment variable is not set"}
		}
		if v == "" {
			return "", &UnresolvedError{Ref: ref, Cause: "environment variable is empty"}
		}
		value = v
	case config.RefFile:
		file, err := os.Open(ref.Path)
		if err != nil {
			return "", &UnresolvedError{Ref: ref, Cause: fmt.Sprintf("reading %s: %v", ref.Path, err)}
		}
		data, err := readBounded(ctx, file)
		closeErr := file.Close()
		if err != nil {
			return "", &UnresolvedError{Ref: ref, Cause: fmt.Sprintf("reading %s: %v", ref.Path, err)}
		}
		if closeErr != nil {
			return "", &UnresolvedError{Ref: ref, Cause: fmt.Sprintf("closing %s: %v", ref.Path, closeErr)}
		}
		value = trimTrailingNewline(string(data))
	case config.RefFD:
		v, err := resolveFD(ctx, ref)
		if err != nil {
			return "", err
		}
		value = v
	case config.RefKeychain:
		out, err := keychainCommand(ctx, ref.Name)
		if err != nil {
			return "", &UnresolvedError{Ref: ref, Cause: err.Error()}
		}
		value = trimTrailingNewline(string(out))
	default:
		return "", &UnresolvedError{Ref: ref, Cause: fmt.Sprintf("unsupported reference kind %q", ref.Kind)}
	}
	if len(value) > maxSecretBytes {
		return "", &UnresolvedError{Ref: ref, Cause: fmt.Sprintf("resolved value exceeds %d bytes; a reference yielding more is a configuration defect, not a credential", maxSecretBytes)}
	}
	if value == "" {
		return "", &UnresolvedError{Ref: ref, Cause: "resolved value is empty"}
	}
	return value, nil
}

// resolveFD reads one open descriptor. The descriptor is owned by the
// launching process and stays open so later submissions through the
// same reference can re-read it: the cached wrapper rewinds to the
// start before every read, while a pipe-backed descriptor is one-shot
// by nature (its retry semantics belong to the writer, not the
// reader). The read loops to EOF under a deadline so a chunked or
// wedged writer can neither silently truncate nor hang the resolution.
func resolveFD(ctx context.Context, ref *config.SecretRef) (string, error) {
	fd, err := strconv.Atoi(ref.Name)
	if err != nil {
		return "", &UnresolvedError{Ref: ref, Cause: fmt.Sprintf("invalid descriptor %q", ref.Name)}
	}
	fdCreate.Lock()
	wrapped, loaded := fdFiles.Load(fd)
	if !loaded {
		file := os.NewFile(uintptr(fd), ref.Text)
		if file == nil {
			fdCreate.Unlock()
			return "", &UnresolvedError{Ref: ref, Cause: fmt.Sprintf("descriptor %d is not open", fd)}
		}
		fdFiles.Store(fd, file)
		wrapped = file
	}
	fdCreate.Unlock()
	file := wrapped.(*os.File)
	// ReadAt is offset-based and leaves the wrapper's seek state
	// untouched, so the cached descriptor is safe for repeated and
	// concurrent resolutions without interleaving seeks. A pipe or
	// socket rejects ReadAt (ESPIPE): that is the documented one-shot
	// reference, consumed through the deadline-bounded sequential read
	// under the dedicated read lock (fdReadMu) so concurrent
	// resolutions cannot interleave on it.
	buf := make([]byte, maxSecretBytes+1)
	n, readErr := file.ReadAt(buf, 0)
	if readErr == nil || readErr == io.EOF {
		return trimTrailingNewline(string(buf[:n])), nil
	}
	if !errors.Is(readErr, os.ErrInvalid) && !strings.Contains(readErr.Error(), "illegal seek") {
		return "", &UnresolvedError{Ref: ref, Cause: fmt.Sprintf("reading fd %d: %v", fd, readErr)}
	}
	fdReadMu.Lock()
	defer fdReadMu.Unlock()
	if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
		// A pipe cannot rewind; the read proceeds from the current
		// position (one-shot semantics).
		_ = seekErr
	}
	data, seqErr := readBounded(ctx, file)
	if seqErr != nil {
		return "", &UnresolvedError{Ref: ref, Cause: fmt.Sprintf("reading fd %d: %v", fd, seqErr)}
	}
	if len(data) == 0 {
		return "", &UnresolvedError{Ref: ref, Cause: fmt.Sprintf("descriptor %d yielded no data; a pipe-backed reference is consumed by its first read (one-shot)", fd)}
	}
	return trimTrailingNewline(string(data)), nil
}

// maxSecretBytes bounds one resolved secret (SEC-009): a reference that
// yields more than this is a configuration defect, not a credential.
const maxSecretBytes = 64 * 1024

// readBounded reads at most maxSecretBytes+1 bytes, honoring the
// caller's context and the readDeadline. The read runs on a goroutine
// because descriptor reads (unlike HTTP requests) cannot be interrupted
// in place; on deadline expiry the goroutine drains into a discarded
// bounded buffer and the caller sees a timeout error.
func readBounded(ctx context.Context, r io.Reader) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, readDeadline)
		defer cancel()
	}
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(r, maxSecretBytes+1))
		done <- result{data, err}
	}()
	select {
	case res := <-done:
		return res.data, res.err
	case <-ctx.Done():
		go func() {
			_, _ = io.Copy(io.Discard, io.LimitReader(r, maxSecretBytes+1))
			<-done
		}()
		return nil, fmt.Errorf("read exceeded its deadline: %w", ctx.Err())
	}
}

// trimTrailingNewline strips one final LF (and CR) — the conventional
// single trailing newline of file- and fd-supplied credentials.
func trimTrailingNewline(s string) string {
	s = strings.TrimSuffix(s, "\n")
	return strings.TrimSuffix(s, "\r")
}

// UnresolvedError is one definite secret-resolution failure. Its message
// carries the reference identifier and the cause, never a value.
type UnresolvedError struct {
	Ref   *config.SecretRef
	Cause string
}

func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("secret %s: %s", e.Ref.Redacted(), e.Cause)
}

// limitWriter caps subprocess output at limit bytes; writes beyond the
// cap are counted and discarded so oversize output is detectable
// without being buffered.
type limitWriter struct {
	buf     bytes.Buffer
	limit   int
	truncat bool
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		w.truncat = true
		room := w.limit - w.buf.Len()
		if room > 0 {
			w.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return w.buf.Write(p)
}

// runSecurity performs the controlled `security find-generic-password`
// lookup on darwin: argv-only invocation, no inherited environment, no
// stdin, a bounded deadline, stdout bounded to maxSecretBytes as the
// credential, and stderr captured separately for bounded diagnostics —
// keychain notices on stderr never contaminate the resolved value
// (SEC-004, SEC-005, SEC-006, SEC-009).
func runSecurity(ctx context.Context, ref string) ([]byte, error) {
	if !keychainSupported() {
		return nil, fmt.Errorf("keychain references are not supported on this platform")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := execCommand(ctx, "/usr/bin/security", "find-generic-password", "-w", "-s", ref)
	cmd.Env = []string{}
	cmd.Stdin = nil
	stdout := &limitWriter{limit: maxSecretBytes + 1}
	stderr := &limitWriter{limit: 512}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.buf.String())
		if len(detail) > 200 {
			detail = detail[:200]
		}
		if detail != "" {
			return nil, fmt.Errorf("keychain lookup failed: %v: %s", err, detail)
		}
		return nil, fmt.Errorf("keychain lookup failed: %v", err)
	}
	if stdout.truncat {
		return nil, fmt.Errorf("keychain item output exceeds %d bytes", maxSecretBytes)
	}
	return stdout.buf.Bytes(), nil
}
