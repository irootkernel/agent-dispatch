package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/config"
)

// Execute the real reconciliation path in a separate CLI-shaped process.
// Its Git stand-in has its own process group, just like gitlocal commands.
func init() {
	if len(os.Args) == 3 && os.Args[1] == "sync-shutdown-git-helper" {
		if os.WriteFile(os.Args[2], []byte(strconv.Itoa(os.Getpid())), 0o600) != nil {
			os.Exit(2)
		}
		time.Sleep(15 * time.Second)
		os.Exit(0)
	}
	if os.Getenv("AGENT_DISPATCH_SYNC_SHUTDOWN_HELPER") == "ignore" && len(os.Args) > 2 && os.Args[1] == "sync" && os.Args[2] == "reconcile" {
		signal.Ignore(syscall.SIGTERM)
		if os.WriteFile(os.Getenv("AGENT_DISPATCH_SYNC_SHUTDOWN_READY"), []byte("ready"), 0o600) != nil {
			os.Exit(2)
		}
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	if os.Getenv("AGENT_DISPATCH_SYNC_SHUTDOWN_HELPER") == "1" && len(os.Args) > 2 && os.Args[1] == "sync" && os.Args[2] == "reconcile" {
		os.Exit(Run(os.Args[1:], io.Discard, io.Discard))
	}
}

func TestSyncReconcileShutdownCancelsIndependentGitGroup(t *testing.T) {
	testSyncShutdownGitGroup(t, false)
}

func TestSyncServiceShutdownWaitsForIndependentGitGroup(t *testing.T) {
	testSyncShutdownGitGroup(t, true)
}

func TestSyncServiceShutdownRefusesSuccessAfterForcedChildExit(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	marker := filepath.Join(t.TempDir(), "child-ready")
	t.Setenv("AGENT_DISPATCH_SYNC_SHUTDOWN_HELPER", "ignore")
	t.Setenv("AGENT_DISPATCH_SYNC_SHUTDOWN_READY", marker)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- servePeer(ctx, listener, svc) }()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("unresponsive child did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("service reported successful cleanup after forced child exit")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("forced child shutdown exceeded its bound")
	}
}

func testSyncShutdownGitGroup(t *testing.T, service bool) {
	t.Helper()
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	root := t.TempDir()
	resource := svc.cfg.Resources[svc.cfg.Sync.Resource]
	resource.Root = root
	svc.cfg.Resources[svc.cfg.Sync.Resource] = resource
	raw, err := json.Marshal(svc.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	revision, _ := config.SyncRevision(svc.cfg)
	if _, err := svc.store.SetSyncControl(context.Background(), svc.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "git.pid")
	bin := t.TempDir()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := fmt.Sprintf("#!/bin/sh\nexec %s sync-shutdown-git-helper %s\n", quote(executable), quote(marker))
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AGENT_DISPATCH_SYNC_SHUTDOWN_HELPER", "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	if service {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		go func() { done <- servePeer(ctx, listener, svc) }()
	} else {
		go func() {
			settled, _ := svc.runReconcile(ctx)
			if settled {
				done <- errors.New("cancelled reconciliation claimed settlement")
			} else {
				done <- nil
			}
		}()
	}
	var pid int
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if raw, err := os.ReadFile(marker); err == nil {
			pid, _ = strconv.Atoi(string(raw))
			if pid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("Git stand-in did not start")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Release()
	identity := func() string {
		out, _ := exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=,command=").Output()
		return string(out)
	}
	startedIdentity := identity()
	if !strings.Contains(startedIdentity, executable+" sync-shutdown-git-helper "+marker) {
		t.Fatalf("fixture process identity mismatch: %q", startedIdentity)
	}
	cleanup := true
	defer func() {
		if cleanup && identity() == startedIdentity {
			_ = process.Kill()
		}
	}()
	if group, err := syscall.Getpgid(pid); err != nil || group != pid {
		t.Fatalf("Git group=%d pid=%d err=%v", group, pid, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("reconciliation did not finish bounded shutdown")
	}
	if err := process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("Git process still alive after reconciliation stopped: %v", err)
	}
	cleanup = false
	if svc.reconcileShutdownErr != nil {
		t.Fatal(svc.reconcileShutdownErr)
	}
}
