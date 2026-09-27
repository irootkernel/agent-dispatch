package cli

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/secretresolver"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/syncmembership"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

const syncPeerSocketName = "http.sock"

type peerRequest struct {
	group, sender, receiver, membership, contentRef string
}

type peerService struct {
	cfg        *config.Config
	configPath string
	store      *sqlite.Store
	members    func(context.Context) (string, [2]syncrecords.ActiveMember, error)
	reconcile  func(context.Context) (bool, string)
	wake       chan struct{}
	slots      chan struct{}
	rateMu     sync.Mutex
	rate       map[string]peerRate
	stderr     io.Writer
	managed    bool
	warnMu     sync.Mutex
	warnedAt   map[string]time.Time
	memberMu   sync.Mutex
	memberHead string
	memberPair [2]syncrecords.ActiveMember
}

type peerRate struct {
	window time.Time
	count  int
}

func runSyncServe(args []string, _ io.Writer, stderr io.Writer) int {
	const command = "sync serve"
	var group, explicitConfig string
	managed := false
	seen := map[string]bool{}
	for i := 0; i < len(args); {
		if args[i] == "--managed" && !seen["--managed"] {
			managed, seen["--managed"] = true, true
			i++
			continue
		}
		if i+1 >= len(args) || seen[args[i]] || args[i+1] == "" {
			return usageError(stderr, command, "serve requires --group GROUP and accepts --config PATH")
		}
		seen[args[i]] = true
		switch args[i] {
		case "--group":
			group = args[i+1]
		case "--config":
			explicitConfig = args[i+1]
		default:
			return usageError(stderr, command, "serve requires --group GROUP and accepts --config PATH")
		}
		i += 2
	}
	if group == "" {
		return usageError(stderr, command, "serve requires --group GROUP and accepts --config PATH")
	}
	if managed {
		capManagedSyncLog(stderr)
	}
	configPath := resolveConfigPath(explicitConfig)
	cfg, err := config.Load(configPath)
	if err != nil {
		if managed {
			fmt.Fprintln(stderr, "sync serve: configuration unavailable; managed service stopped")
			return 0
		}
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	if cfg.Sync == nil || cfg.Sync.GroupID != group || !cfg.Sync.Enabled {
		if managed {
			fmt.Fprintln(stderr, "sync serve: configured group is disabled; managed service stopped")
			return 0
		}
		return planErr(stderr, command, "sync_precondition_failed", "conflict", "configured sync group must be enabled", 14)
	}
	if err := verifyPeerSignerIsolation(cfg.Sync); err != nil {
		return planErr(stderr, command, "sync_trust_failed", "security", err.Error(), 30)
	}
	_, store, code := openOperatorStore(command, configPath, stderr)
	if code != 0 {
		return code
	}
	defer store.Close()
	revision, _ := config.SyncRevision(cfg)
	control, err := store.EnsureSyncControl(context.Background(), cfg.Sync.GroupID, revision, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	svc := &peerService{cfg: cfg, configPath: configPath, store: store, slots: make(chan struct{}, 2), wake: make(chan struct{}, 1), rate: map[string]peerRate{}, stderr: stderr, warnedAt: map[string]time.Time{}, managed: managed}
	if control.ConfigRevision != revision {
		svc.warn("sync control configuration binding is stale; run sync reconcile")
	}
	svc.members = svc.currentMembers
	if _, _, err := svc.members(context.Background()); err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	listener, socketPath, err := listenPeerSocket(stateDirOf(cfg))
	if err != nil {
		return planErr(stderr, command, "sync_precondition_failed", "conflict", "local peer socket is unavailable: "+err.Error(), 14)
	}
	fmt.Fprintf(stderr, "sync serve: listening on unix:%s for group %s\n", socketPath, cfg.Sync.GroupID)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := servePeer(ctx, listener, svc); err != nil {
		return planErr(stderr, command, "sync_retryable", "transient_local", err.Error(), 10)
	}
	fmt.Fprintln(stderr, "sync serve: stopped; committed peer work remains durable")
	return 0
}

// An owner-only Unix socket prevents another local UID from taking over the
// final Serve hop while this process is down. Same-UID compromise is outside
// the OS credential boundary; the directional peer secret still gates requests.
func listenPeerSocket(stateDir string) (net.Listener, string, error) {
	dir := filepath.Join(stateDir, "peer-service")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return nil, "", fmt.Errorf("peer socket directory must be an owner-only real directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, "", fmt.Errorf("peer socket directory owner is not the service user")
	}
	path := filepath.Join(dir, syncPeerSocketName)
	if existing, err := os.Lstat(path); err == nil {
		if existing.Mode()&os.ModeSocket == 0 {
			return nil, "", fmt.Errorf("peer socket path is occupied by a non-socket")
		}
		if peer, err := net.DialTimeout("unix", path, 100*time.Millisecond); err == nil {
			peer.Close()
			return nil, "", fmt.Errorf("peer socket is already serving")
		} else if !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, "", fmt.Errorf("peer socket liveness could not be checked")
		}
		if err := os.Remove(path); err != nil {
			return nil, "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		return nil, "", err
	}
	return listener, path, nil
}

// Worker diagnostics are fixed strings so neither peer input nor subprocess
// output reaches the service log. Repeated failures are reported once a minute.
func (s *peerService) warn(reason string) {
	if s.stderr == nil {
		return
	}
	s.warnMu.Lock()
	defer s.warnMu.Unlock()
	if s.warnedAt == nil {
		s.warnedAt = map[string]time.Time{}
	}
	now := time.Now()
	if now.Sub(s.warnedAt[reason]) < time.Minute {
		return
	}
	s.warnedAt[reason] = now
	if s.managed {
		capManagedSyncLog(s.stderr)
	}
	fmt.Fprintf(s.stderr, "sync serve: %s; inspect sync status and restart after correcting the cause\n", reason)
}

// launchd keeps its log descriptor open for the lifetime of a service. Trim
// that regular file in place before writing so a persistent warning cannot
// grow it without bound; pipes and systemd's journal are left to their owner.
func capManagedSyncLog(w io.Writer) {
	f, ok := w.(*os.File)
	if !ok {
		return
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < scheduleLogMaxBytes {
		return
	}
	if f.Truncate(0) == nil {
		_, _ = f.Seek(0, io.SeekStart)
	}
}

func (s *peerService) configurationProblem() string {
	current, err := config.Load(s.configPath)
	if err != nil || current.Sync == nil || !current.Sync.Enabled {
		return "configuration unavailable"
	}
	loadedRevision, _ := config.SyncRevision(s.cfg)
	currentRevision, _ := config.SyncRevision(current)
	if loadedRevision != currentRevision {
		return "configuration changed"
	}
	return ""
}

func servePeer(ctx context.Context, listener net.Listener, svc *peerService) error {
	server := &http.Server{
		Handler: svc, MaxHeaderBytes: 8 << 10,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second,
	}
	serviceCtx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() { defer workers.Done(); svc.deliveryLoop(serviceCtx) }()
	go func() { defer workers.Done(); svc.inboxLoop(serviceCtx) }()
	shutdownDone := make(chan struct{})
	go func() {
		<-serviceCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		workerDone := make(chan struct{})
		go func() { workers.Wait(); close(workerDone) }()
		select {
		case <-workerDone:
		case <-shutdownCtx.Done():
		}
		close(shutdownDone)
	}()
	err := server.Serve(listener)
	cancel()
	<-shutdownDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// A managed user service shares the operator's UID. Only absent environment
// variables or descriptors can be command-only signer references for it.
func verifyPeerSignerIsolation(s *config.Sync) error {
	for _, value := range []string{s.PublisherSigningKeyRef, s.AdministratorSigningKeyRef} {
		if value == "" {
			continue
		}
		ref, err := config.ParseSecretRef(value)
		if err != nil {
			return fmt.Errorf("invalid signing-key reference")
		}
		switch ref.Kind {
		case config.RefEnv:
			if _, found := os.LookupEnv(ref.Name); found {
				return fmt.Errorf("signing-key environment reference is present in the service process")
			}
		case config.RefFD:
			n, _ := strconv.Atoi(ref.Name)
			var stat syscall.Stat_t
			err := syscall.Fstat(n, &stat)
			if err == nil {
				return fmt.Errorf("signing-key descriptor is open in the service process")
			}
			if !errors.Is(err, syscall.EBADF) {
				return fmt.Errorf("signing-key descriptor isolation could not be checked")
			}
		default:
			return fmt.Errorf("managed peer service requires command-only env: or fd: signing references")
		}
	}
	return nil
}

func (s *peerService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.Fragment != "" ||
		(r.URL.Path != "/v1/sync/nudges" && r.URL.Path != "/v1/sync/status") {
		http.NotFound(w, r)
		return
	}
	if len(r.Header.Values("Content-Type")) != 1 || r.Header.Get("Content-Type") != "application/json" ||
		len(r.Header.Values("Content-Encoding")) != 0 ||
		len(r.TransferEncoding) != 0 || len(r.Trailer) != 0 || len(r.Header.Values("Authorization")) != 1 {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	limit := int64(256 << 10)
	if r.URL.Path == "/v1/sync/status" {
		limit = syncrecords.MaxPeerStatusBytes
	}
	if r.ContentLength > limit {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusRequestEntityTooLarge)
		return
	}
	if r.URL.Path == "/v1/sync/nudges" {
		s.nudge(w, r, raw)
		return
	}
	s.status(w, r, raw)
}

func (s *peerService) nudge(w http.ResponseWriter, r *http.Request, raw []byte) {
	nudge, err := syncrecords.DecodeNudge(raw)
	if err != nil {
		http.Error(w, "invalid nudge", http.StatusBadRequest)
		return
	}
	if !s.authorize(w, r, peerRequest{nudge.GroupID, nudge.Sender, nudge.Receiver, nudge.MembershipRevision, nudge.ContentRef}) {
		return
	}
	canonical, _ := syncrecords.CanonicalNudge(nudge)
	// The canonical bytes make whitespace and field order irrelevant to a
	// retry while preserving an exact identity conflict for changed fields.
	fingerprint := sha256.Sum256(canonical)
	_, err = s.store.AdmitPeerNudge(r.Context(), sqlite.PeerNudgeInput{
		GroupID: nudge.GroupID, PublicationID: nudge.PublicationID,
		Fingerprint: "sha256:" + hex.EncodeToString(fingerprint[:]), PayloadJSON: string(canonical),
		QueueLimit: s.cfg.Sync.Bounds.Queue, ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		if errors.Is(err, sqlite.ErrSyncAdmissionConflict) {
			http.Error(w, "conflicting nudge identity", http.StatusConflict)
			return
		}
		http.Error(w, "nudge not committed", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	if s.wake != nil {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

func (s *peerService) status(w http.ResponseWriter, r *http.Request, raw []byte) {
	request, err := syncrecords.DecodeStatusRequest(raw)
	if err != nil {
		http.Error(w, "invalid status request", http.StatusBadRequest)
		return
	}
	if !s.authorize(w, r, peerRequest{request.GroupID, request.Sender, request.Receiver, request.MembershipRevision, request.ContentRef}) {
		return
	}
	if request.ScopeDigest != config.SyncScopeDigest(s.cfg, s.cfg.Sync.Resource) || request.ContractDigest != config.SyncContractDigest() {
		http.Error(w, "status binding mismatch", http.StatusConflict)
		return
	}
	client, err := membershipGitClient(s.cfg, s.cfg.Sync)
	if err != nil {
		http.Error(w, "status unavailable", http.StatusServiceUnavailable)
		return
	}
	response := observeSyncPeer(r.Context(), s.cfg, s.store, client, request)
	if err := response.Validate(); err != nil {
		http.Error(w, "status unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (s *peerService) authorize(w http.ResponseWriter, r *http.Request, request peerRequest) bool {
	if request.group != s.cfg.Sync.GroupID || request.receiver != s.cfg.Sync.LocalInstanceID ||
		request.sender == request.receiver || request.contentRef != s.cfg.Sync.ContentRef {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	var sender *config.SyncNode
	for i := range s.cfg.Sync.Nodes {
		if s.cfg.Sync.Nodes[i].InstanceID == request.sender {
			sender = &s.cfg.Sync.Nodes[i]
		}
	}
	if sender == nil || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if !s.allowRate(request.sender) {
		http.Error(w, "rate limit", http.StatusTooManyRequests)
		return false
	}
	if reason := s.configurationProblem(); reason != "" {
		s.warn(reason)
		http.Error(w, "service "+reason, http.StatusServiceUnavailable)
		return false
	}
	configuredRevision, _ := config.SyncRevision(s.cfg)
	ref, err := config.ParseSecretRef(sender.CredentialRef)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	secret, err := secretresolver.Resolve(r.Context(), ref)
	if err != nil {
		http.Error(w, "peer credential unavailable", http.StatusServiceUnavailable)
		return false
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if provided == "" || strings.ContainsAny(provided, " \t\r\n") || len(provided) > 4096 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	wantHash, gotHash := sha256.Sum256([]byte(secret)), sha256.Sum256([]byte(provided))
	if subtle.ConstantTimeCompare(wantHash[:], gotHash[:]) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	head, members, err := s.members(r.Context())
	if err != nil || head != request.membership || members[0].InstanceID != request.sender || members[1].InstanceID != request.receiver {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	control, err := s.store.LoadSyncControl(r.Context(), request.group)
	if err != nil || control.ConfigRevision != configuredRevision || control.MembershipMode != "normal" || control.State == "blocked" {
		if err == nil && control.ConfigRevision != configuredRevision {
			s.warn("sync control configuration binding is stale; run sync reconcile")
		}
		http.Error(w, "peer group unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (s *peerService) allowRate(sender string) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	now := time.Now()
	rate := s.rate[sender]
	if now.Sub(rate.window) >= time.Minute {
		rate = peerRate{window: now}
	}
	if rate.count >= 120 {
		return false
	}
	rate.count++
	s.rate[sender] = rate
	return true
}

func (s *peerService) currentMembers(ctx context.Context) (string, [2]syncrecords.ActiveMember, error) {
	var members [2]syncrecords.ActiveMember
	client, err := membershipGitClient(s.cfg, s.cfg.Sync)
	if err != nil {
		return "", members, err
	}
	head, err := client.ResolveRef(ctx, s.cfg.Sync.MembershipRef)
	if err != nil {
		return "", members, err
	}
	s.memberMu.Lock()
	defer s.memberMu.Unlock()
	if head == s.memberHead {
		return head, s.memberPair, nil
	}
	if s.memberHead != "" {
		relation, err := client.Compare(ctx, s.memberHead, head)
		if err != nil || relation != gitlocal.RelationBehind {
			return "", members, fmt.Errorf("local peer membership did not advance linearly")
		}
	}
	members, err = loadConfiguredMembers(ctx, s.cfg, client, head)
	if err != nil {
		return "", members, err
	}
	s.memberHead, s.memberPair = head, members
	return head, members, nil
}

// loadConfiguredMembers reads membership using only explicit configuration and
// Git inputs. Status and doctor use it without constructing a serving process.
func loadConfiguredMembers(ctx context.Context, cfg *config.Config, client *gitlocal.Client, head string) ([2]syncrecords.ActiveMember, error) {
	var members [2]syncrecords.ActiveMember
	history, err := syncmembership.LoadHistory(ctx, client, head, membershipBinding(cfg, cfg.Sync), cfg.Sync.Bounds.HistoryCommits)
	if err != nil {
		return members, err
	}
	current, ok := history.Current()
	if !ok || current.Mode != "normal" || len(current.ActiveMembers) != 2 {
		return members, fmt.Errorf("peer membership is not normal and current")
	}
	for _, member := range current.ActiveMembers {
		var configured *config.SyncNode
		for i := range cfg.Sync.Nodes {
			if cfg.Sync.Nodes[i].InstanceID == member.InstanceID {
				configured = &cfg.Sync.Nodes[i]
			}
		}
		if configured == nil || configured.StateIncarnationID != member.StateIncarnationID || configured.Endpoint != member.Endpoint || configured.PublisherKey != member.PublisherKey {
			return members, fmt.Errorf("peer membership differs from configured identities")
		}
		if member.InstanceID == cfg.Sync.LocalInstanceID {
			members[1] = member
		} else {
			members[0] = member
		}
	}
	if members[0].InstanceID == "" || members[1].InstanceID == "" {
		return members, fmt.Errorf("configured local peer is not active")
	}
	return members, nil
}
