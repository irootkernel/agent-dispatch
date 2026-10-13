package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/irootkernel/agent-dispatch/internal/adapters/resourceguard"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
	"github.com/irootkernel/agent-dispatch/internal/ports"
	driver "modernc.org/sqlite"
)

const managementContractDigest = "sha256:77dba7fac82cf1e627ad29ab89057c18d4d5c2c380251112579e551d1122cf39"

// Inspect the raw family before legacy global stripping/help can admit flags
// excluded by the closed management grammar. Legacy command parsing is unchanged.
func managementRawArgs(args []string) ([]string, bool, bool) {
	if len(args) < 2 || args[0] != "sync" {
		return nil, false, false
	}
	i := 1
	for i < len(args) {
		flag, _, equals := strings.Cut(args[i], "=")
		if !slices.Contains([]string{"--state-dir", "--timeout", "--log-level", "--trace-id"}, flag) {
			break
		}
		i++
		if !equals {
			i++
		}
	}
	if i < len(args) && args[i] == "request" {
		return args[i+1:], true, i != 1
	}
	return nil, false, false
}

type managementFlags struct{ phase, group, action, target, request, configPath string }

func parseManagementFlags(args []string) (managementFlags, error) {
	var f managementFlags
	if len(args) == 0 || !slices.Contains([]string{"capabilities", "prepare", "submit", "show"}, args[0]) {
		return f, fmt.Errorf("unknown request phase")
	}
	f.phase = args[0]
	seen := map[string]bool{}
	for i := 1; i < len(args); i++ {
		flag, value, equals := strings.Cut(args[i], "=")
		if !equals {
			if i+1 == len(args) {
				return f, fmt.Errorf("missing flag value")
			}
			i++
			value = args[i]
		}
		if seen[flag] || value == "" || strings.HasPrefix(value, "--") || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return f, fmt.Errorf("invalid or duplicate flag value")
		}
		seen[flag] = true
		switch flag {
		case "--group":
			f.group = value
		case "--config":
			f.configPath = value
		case "--output":
			if value != "json" {
				return f, fmt.Errorf("only JSON output is supported")
			}
		case "--action":
			if f.phase != "prepare" {
				return f, fmt.Errorf("action is only valid for prepare")
			}
			f.action = value
		case "--target-request-id":
			if f.phase != "prepare" {
				return f, fmt.Errorf("target is only valid for prepare")
			}
			f.target = value
		case "--request-id":
			if f.phase != "submit" && f.phase != "show" {
				return f, fmt.Errorf("request ID is only valid for submit or show")
			}
			f.request = value
		default:
			return f, fmt.Errorf("unknown request flag")
		}
	}
	if !syncmanagement.ValidGroup(f.group) {
		return f, fmt.Errorf("invalid or missing group")
	}
	if f.phase == "prepare" && (!syncmanagement.ValidAction(f.action) || (f.action == "retry") != (f.target != "") || (f.target != "" && !syncmanagement.ValidHandle(f.target))) {
		return f, fmt.Errorf("invalid action or retry target")
	}
	if (f.phase == "submit" || f.phase == "show") && !syncmanagement.ValidHandle(f.request) {
		return f, fmt.Errorf("invalid or missing request ID")
	}
	return f, nil
}

func runSyncRequest(parent context.Context, args []string, prefix bool, stdout, stderr io.Writer) int {
	return runSyncRequestQualified(parent, args, prefix, []string{"sync-now", "verify-all", "retry", "pause", "resume"}, stdout, stderr)
}

// qualified is supplied only by the internal composition/test harness; it is
// never a CLI/config switch that can enable an unimplemented execution path.
func runSyncRequestQualified(parent context.Context, args []string, prefix bool, qualified []string, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(parent, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	originalStdout := stdout
	stdout = managementContextWriter{ctx: ctx, writer: originalStdout}
	for _, writer := range []io.Writer{originalStdout, stderr} {
		if setter, ok := writer.(interface{ SetWriteDeadline(time.Time) error }); ok {
			deadline, _ := ctx.Deadline()
			if err := setter.SetWriteDeadline(deadline); err == nil {
				defer setter.SetWriteDeadline(time.Time{})
			}
		}
	}
	f, err := parseManagementFlags(args)
	phase := f.phase
	if phase == "" {
		phase = "capabilities"
	}
	command := "sync request " + phase
	fail := func(code, category, message string, exit int, view any) int {
		return managementError(stderr, command, code, category, message, exit, view)
	}
	if err != nil || prefix {
		return fail("flag_invalid", "usage", "invalid closed request arguments", 2, nil)
	}
	if ctx.Err() != nil {
		return fail("sync_request_deadline", "transient_local", "request deadline elapsed", 10, nil)
	}
	cfg, err := config.LoadContext(ctx, resolveConfigPath(f.configPath))
	if ctx.Err() != nil {
		return fail("sync_request_deadline", "transient_local", "request deadline elapsed", 10, nil)
	}
	if err != nil {
		return fail("config_invalid", "configuration", "trusted configuration is invalid", 3, nil)
	}
	if cfg.Sync == nil || cfg.Sync.GroupID != f.group {
		return fail("sync_group_not_found", "configuration", "group is not configured", 3, nil)
	}
	policy, revision, err := config.ManagementRevisions(cfg)
	if err != nil {
		return fail("config_invalid", "configuration", "cannot compute management bindings", 3, nil)
	}
	enabled := cfg.Sync.Enabled && cfg.Sync.Management != nil && cfg.Sync.Management.Enabled
	configured := []string{}
	if cfg.Sync.Management != nil {
		configured = append(configured, cfg.Sync.Management.Actions...)
		slices.Sort(configured)
	}
	available := []string{}
	if enabled {
		for _, action := range configured {
			if slices.Contains(qualified, action) {
				available = append(available, action)
			}
		}
	}
	current := syncmanagement.Binding{GroupID: f.group, InstanceID: cfg.Instance.ID, StateIncarnationID: localSyncIncarnation(cfg), PolicyRevision: policy, ConfigRevision: revision}
	if f.phase == "capabilities" {
		executor := "unknown"
		path := filepath.Join(platformpaths.ResolveStateDir(cfg.Instance.StateDir), StateDBName)
		store, openErr := sqlite.OpenReadOnly(ctx, path)
		if openErr == nil {
			defer store.Close()
			if err := store.CheckManagementCapabilitiesSchema(ctx); err != nil {
				return managementStoreError(ctx, stderr, command, err, nil)
			}
			executor, err = store.ManagementExecutorAvailability(ctx, current, time.Now())
			if err != nil {
				return managementStoreError(ctx, stderr, command, err, nil)
			}
		} else if !errors.Is(openErr, os.ErrNotExist) {
			return managementStoreError(ctx, stderr, command, openErr, nil)
		}
		return managementSuccess(stdout, stderr, command, map[string]any{"schema_version": syncmanagement.Schema, "kind": "capabilities", "group_id": f.group, "contract_digest": managementContractDigest, "enabled": enabled, "configured_actions": configured, "available_actions": available, "executor": executor, "limits": managementLimits(), "data_capacity": cfg.Sync.Bounds.Queue, "side_effects": []string{}})
	}
	stateDir := platformpaths.ResolveStateDir(cfg.Instance.StateDir)
	path := filepath.Join(stateDir, StateDBName)
	if f.phase == "prepare" {
		if !enabled || !slices.Contains(configured, f.action) {
			return fail("sync_management_disabled", "configuration", "action is not authorized", 3, nil)
		}
		if !slices.Contains(available, f.action) {
			return fail("sync_management_unavailable", "configuration", "action execution is not qualified", 3, nil)
		}
	}
	// show and accepted duplicates first inspect existing state without creating
	// files, running migrations, or consulting service/remote health.
	if f.phase == "show" || f.phase == "submit" {
		store, openErr := sqlite.OpenReadOnly(ctx, path)
		if errors.Is(openErr, os.ErrNotExist) {
			return fail("sync_request_not_found", "input_rejected", "request handle was not found", 4, nil)
		}
		if openErr != nil {
			return managementStoreError(ctx, stderr, command, openErr, nil)
		}
		err = store.CheckManagementSchema(ctx)
		var request sqlite.ManagementRequest
		if err == nil {
			request, err = store.LoadManagementRequest(ctx, f.group, f.request, time.Now())
		}
		executor := "unknown"
		if err == nil {
			executor, err = store.ManagementExecutorAvailability(ctx, current, time.Now())
		}
		store.Close()
		if err != nil {
			return managementStoreError(ctx, stderr, command, err, nil)
		}
		view := managementView(request, f.phase, current, []string{})
		view["executor"] = executor
		if f.phase == "show" || request.AcceptedAt != nil {
			return managementSuccess(stdout, stderr, command, view)
		}
		if request.InstanceID != current.InstanceID || request.StateIncarnationID != current.StateIncarnationID {
			return fail("sync_request_obsolete", "input_rejected", "request incarnation is obsolete", 4, view)
		}
		if request.State == "expired" {
			return fail("sync_request_expired", "input_rejected", "prepared request has expired", 4, view)
		}
		if !enabled || !slices.Contains(configured, request.Action) {
			return fail("sync_management_disabled", "configuration", "action is not authorized", 3, view)
		}
		if request.PolicyRevision != policy || request.ConfigRevision != revision {
			return fail("sync_request_binding_changed", "conflict", "prepared request bindings have changed", 14, view)
		}
		if !slices.Contains(available, request.Action) {
			return fail("sync_management_unavailable", "configuration", "action execution is not qualified", 3, view)
		}
	}
	store, err := sqlite.OpenContext(ctx, path)
	if err != nil {
		return managementStoreError(ctx, stderr, command, err, nil)
	}
	defer store.Close()
	if err := store.MigrateContext(ctx, filepath.Join(stateDir, "backups")); err != nil {
		return managementStoreError(ctx, stderr, command, err, nil)
	}
	var request sqlite.ManagementRequest
	var duplicate bool
	if f.phase == "prepare" {
		current.Action = f.action
		if f.target != "" {
			current.TargetRequestID = &f.target
		}
		if syncmanagement.IsControl(f.action) {
			syncRevision, _ := config.SyncRevision(cfg)
			control, loadErr := store.LoadSyncControl(ctx, f.group)
			if errors.Is(loadErr, sql.ErrNoRows) {
				control = defaultSyncControlRow(f.group, syncRevision)
			} else if loadErr != nil {
				return managementStoreError(ctx, stderr, command, loadErr, nil)
			}
			current.ExpectedControlRevision = &control.Revision
		}
		id, newErr := syncmanagement.NewHandle()
		if newErr != nil {
			return fail("internal_unclassified", "internal", "cannot generate a request handle", 40, nil)
		}
		request, _, err = store.PrepareManagementRequest(ctx, id, current, time.Now())
	} else {
		// Admission repeats the CAS inside its transaction. Resume inspection
		// holds the same nonblocking resource guard as the operator command.
		pending, loadErr := store.LoadManagementRequest(ctx, f.group, f.request, time.Now())
		if loadErr != nil {
			return managementStoreError(ctx, stderr, command, loadErr, nil)
		}
		if pending.AcceptedAt == nil && pending.Action == "resume" {
			syncRevision, _ := config.SyncRevision(cfg)
			control, loadErr := store.LoadSyncControl(ctx, f.group)
			if errors.Is(loadErr, sql.ErrNoRows) {
				control = defaultSyncControlRow(f.group, syncRevision)
			} else if loadErr != nil {
				return managementStoreError(ctx, stderr, command, loadErr, nil)
			}
			if control.Revision != *pending.ExpectedControlRevision || control.State == "blocked" || control.MembershipMode != "normal" {
				return managementStoreError(ctx, stderr, command, sqlite.ErrSyncControlHeld, nil)
			}
			guard, guardErr := resourceguard.Acquire(stateDirOf(cfg), cfg.Sync.Resource)
			if guardErr != nil {
				return fail("sync_request_conflict", "conflict", "resource is busy; retain the same handle", 14, nil)
			}
			defer guard.Close()
			var validation bytes.Buffer
			if code := validateSyncResumeContext(ctx, cfg, store, &validation); code != 0 {
				if ctx.Err() != nil {
					return managementStoreError(ctx, stderr, command, ctx.Err(), nil)
				}
				return fail("sync_request_conflict", "conflict", "local resume safety checks refused the request", 14, nil)
			}
		}
		syncRevision, _ := config.SyncRevision(cfg)
		request, duplicate, err = store.AcceptManagementRequest(ctx, sqlite.ManagementAdmission{GroupID: f.group, RequestID: f.request, CurrentBinding: current, Authorized: true, QueueLimit: cfg.Sync.Bounds.Queue, ControlSyncRevision: syncRevision})
	}
	if err != nil {
		if f.phase == "submit" && request.AcceptedAt != nil {
			return fail("sync_effect_unknown", "acceptance_unknown", "acceptance response is unknown; retain the same handle", 13, nil)
		}
		return managementStoreError(ctx, stderr, command, err, nil)
	}
	effects := []string{}
	if f.phase == "prepare" {
		effects = append(effects, "request_draft_write")
	} else if !duplicate {
		effects = append(effects, "request_acceptance_write")
		if syncmanagement.IsControl(request.Action) {
			effects = append(effects, "local_control_write")
		}
	}
	view := managementView(request, f.phase, current, effects)
	executor, lookupErr := store.ManagementExecutorAvailability(ctx, current, time.Now())
	if lookupErr == nil {
		view["executor"] = executor
	}
	return managementSuccess(stdout, stderr, command, view)
}

func managementView(r sqlite.ManagementRequest, phase string, current syncmanagement.Binding, effects []string) map[string]any {
	jobs := make([]map[string]any, 0, len(r.Jobs))
	for _, j := range r.Jobs {
		jobs = append(jobs, map[string]any{"job_id": j.JobID, "kind": j.Kind, "association_reason": j.AssociationReason, "target_commit": j.TargetCommit, "attempts": j.Attempts, "state": j.State})
	}
	return map[string]any{"schema_version": syncmanagement.Schema, "kind": "request", "phase": phase, "request_id": r.RequestID, "group_id": r.GroupID, "action": r.Action, "target_request_id": r.TargetRequestID, "bindings": map[string]any{"instance_id": r.InstanceID, "state_incarnation_id": r.StateIncarnationID, "policy_revision": r.PolicyRevision, "config_revision": r.ConfigRevision, "expected_control_revision": r.ExpectedControlRevision}, "fingerprint": r.Fingerprint, "created_at": r.CreatedAt, "expires_at": r.ExpiresAt, "state": r.State, "accepted_at": r.AcceptedAt, "attempts": r.Attempts, "claim_fence": r.Fence, "jobs": jobs, "outcome": r.Outcome, "binding_current": r.InstanceID == current.InstanceID && r.StateIncarnationID == current.StateIncarnationID && r.PolicyRevision == current.PolicyRevision && r.ConfigRevision == current.ConfigRevision, "executor": "unknown", "side_effects": effects}
}

func managementLimits() map[string]int {
	return map[string]int{"preparation_ttl_seconds": 600, "expired_draft_retention_seconds": 86400, "prepared_data_limit": 256, "prepared_control_limit": 16, "unresolved_data_min": 2, "unresolved_data_max": 1000, "unresolved_control_limit": 16, "terminal_data_limit": 10000, "terminal_control_limit": 1000, "association_limit": 1000, "result_bytes": syncmanagement.ResultBytes, "reason_bytes": 256, "call_deadline_seconds": 5, "execution_deadline_seconds": 240, "claim_lease_seconds": 30, "claim_renewal_seconds": 10, "executor_freshness_seconds": 60, "shutdown_seconds": 30, "cumulative_attempts": 20, "minimum_terminal_retention_days": 180, "management_per_recovery_pass": 1}
}

func managementMessage(message string) string {
	var out strings.Builder
	for _, c := range strings.ToValidUTF8(message, "?") {
		if unicode.IsControl(c) {
			c = ' '
		}
		if out.Len()+utf8.RuneLen(c) > syncmanagement.ReasonBytes {
			break
		}
		out.WriteRune(c)
	}
	return out.String()
}
func managementError(w io.Writer, command, code, category, message string, exit int, result any) int {
	value := map[string]any{"api_version": APIVersion, "command": command, "ok": false, "trace_id": "", "error": map[string]any{"code": code, "category": category, "message": managementMessage(message), "retryable": false}}
	if result != nil {
		value["result"] = result
	}
	if err := managementWrite(w, value); err != nil {
		return 40
	}
	return exit
}
func managementSuccess(stdout, stderr io.Writer, command string, result any) int {
	value := map[string]any{"api_version": APIVersion, "command": command, "ok": true, "result": result, "warnings": []string{}, "trace_id": ""}
	if err := managementWrite(stdout, value); err != nil {
		return managementError(stderr, command, "internal_unclassified", "internal", "response delivery failed; retain any previously known request handle", 40, result)
	}
	return 0
}

type managementContextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w managementContextWriter) Write(raw []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.writer.Write(raw)
}

func managementWrite(w io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > syncmanagement.ResultBytes {
		return fmt.Errorf("bounded envelope is too large")
	}
	n, err := w.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	return err
}
func managementStoreError(ctx context.Context, stderr io.Writer, command string, err error, result any) int {
	code, category, exit := "sqlite_write_failed", "storage", 20
	var busy *ports.StoreError
	var sqliteErr *driver.Error
	switch {
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		code, category, exit = "sync_request_deadline", "transient_local", 10
	case errors.Is(err, sqlite.ErrManagementNotFound):
		code, category, exit = "sync_request_not_found", "input_rejected", 4
	case errors.Is(err, sqlite.ErrManagementExpired):
		code, category, exit = "sync_request_expired", "input_rejected", 4
	case errors.Is(err, sqlite.ErrManagementObsolete):
		code, category, exit = "sync_request_obsolete", "input_rejected", 4
	case errors.Is(err, sqlite.ErrManagementUnauthorized):
		code, category, exit = "sync_request_binding_changed", "conflict", 14
	case errors.Is(err, sqlite.ErrManagementNotRetryable):
		code, category, exit = "sync_request_not_retryable", "conflict", 14
	case errors.Is(err, sqlite.ErrSyncAdmissionConflict), errors.Is(err, sqlite.ErrSyncPrecondition), errors.Is(err, sqlite.ErrSyncControlHeld):
		code, category, exit = "sync_request_conflict", "conflict", 14
	case errors.Is(err, sqlite.ErrSyncQueueFull):
		code, category, exit = "sync_request_capacity", "transient_local", 10
	case errors.As(err, &busy) || (errors.As(err, &sqliteErr) && sqliteErr.Code()&255 == 5):
		code, category, exit = "sync_request_busy", "transient_local", 10
	}
	return managementError(stderr, command, code, category, "request operation refused; inspect the same handle before resubmission", exit, result)
}
