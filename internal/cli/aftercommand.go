// Post-commit after-command draining of E16-T3 (ADR-0022, NTF-010
// through NTF-016, OPS-001): after a registered notification-producing
// command's core transaction has committed and the command succeeded,
// one bounded pass drains the existing due work of every affected
// after-command route. The pass is silent on success, writes bounded
// diagnostics to stderr only, and never changes the core command's
// stdout, JSON, or exit status.
//
// The registered command set (v0.1.6 §4) is dispatch, work
// completion/failure, applicable dispatch retry and rerun, quarantine
// resolution, and reconciliation — enforced by the call sites that
// invoke maybeAfterCommandDrain on their successful commits. Setup,
// baseline-only and read-only commands, and the explicit notification
// drain itself are excluded — the last to forbid recursion.
package cli

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/app/notifications"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// afterCommandBudget is the non-configurable ten-second wall budget one
// CLI invocation grants to automatic delivery (v0.1.6 §4): it bounds the
// entire invocation's drain, not per route.
const afterCommandBudget = 10 * time.Second

// afterCommandStderrBound caps the diagnostic lines one automatic pass
// may write: problems are visible, never noisy.
const afterCommandStderrBound = 4

// invocationMu guards the invocation clock and the stderr note counter
// shared by Run and the after-command pass (tests invoke Run repeatedly
// in-process).
var invocationMu sync.Mutex

// invocationStartedAt is the wall clock at this Run invocation's start:
// the ten-second budget spans the whole invocation, so the drain
// consumes only what remains of it.
var invocationStartedAt time.Time

// afterCommandNotes counts the diagnostics this invocation wrote.
var afterCommandNotes int

// markInvocationStart records the budget's anchor and resets the note
// counter (E16-T3).
func markInvocationStart() {
	invocationMu.Lock()
	invocationStartedAt = time.Now()
	afterCommandNotes = 0
	invocationMu.Unlock()
}

// afterCommandDrainContext derives the drain's context from the
// invocation's remaining budget. An exhausted budget drains nothing.
func afterCommandDrainContext() (context.Context, context.CancelFunc) {
	invocationMu.Lock()
	started := invocationStartedAt
	invocationMu.Unlock()
	if started.IsZero() {
		started = time.Now()
	}
	remaining := time.Until(started.Add(afterCommandBudget))
	if remaining <= 0 {
		remaining = 0
	}
	return context.WithTimeout(context.Background(), remaining)
}

// maybeAfterCommandDrain runs the post-commit pass for one successful
// registered command (E16-T3): every affected after-command route —
// including routes whose due work predates this invocation — receives
// one bounded, lease-safe drain. A successful pass is silent; problems
// write at most afterCommandStderrBound bounded stderr lines. The
// function never fails the caller: an automatic delivery failure must
// leave the core command successful.
func maybeAfterCommandDrain(command string, configPath string, store afterCommandStore, stderr io.Writer, affectedRoutes ...string) {
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		boundedAfterCommandNote(stderr, "after-command drain skipped: configuration unreadable")
		return
	}
	maybeAfterCommandDrainCfg(command, cfg, store, stderr, affectedRoutes...)
}

// afterCommandStore is the minimal drain surface a registered command's
// open store must expose (E16-T3).
type afterCommandStore interface {
	notifications.NotificationDrainStore
	StartDrainRun(ctx context.Context, in ports.DrainRunInput) error
	FinishDrainRun(ctx context.Context, drainID string, counts ports.DrainRunCounts, completedAt string) error
}

// maybeAfterCommandDrainCfg is the already-loaded-configuration core.
func maybeAfterCommandDrainCfg(command string, cfg *config.Config, store afterCommandStore, stderr io.Writer, affectedRoutes ...string) {
	routes := afterCommandRoutes(cfg, affectedRoutes)
	if len(routes) == 0 {
		return
	}
	ctx, cancel := afterCommandDrainContext()
	defer cancel()
	if deadline, ok := ctx.Deadline(); !ok || !deadline.After(time.Now()) {
		return // the invocation's budget is exhausted
	}
	resolver := notificationSinkResolver(cfg, stderr)
	// F004: the pass interleaves one notification per route per round in
	// route-ID order — no single route can consume the invocation's
	// budget before a later route is visited.
	perRoute := make([]notifications.DrainClaimedReport, len(routes))
	svcs := make([]*notifications.DrainService, len(routes))
	limits := make([]int, len(routes))
	for i, routeID := range routes {
		policy, err := config.EffectiveNotificationDrain(cfg.Routes[routeID].Notifications)
		if err != nil {
			boundedAfterCommandNote(stderr, fmt.Sprintf("route %s: drain policy unreadable; automatic delivery skipped", routeID))
			continue
		}
		drainID := fmt.Sprintf("drain-ac-%s-%d", routeID, time.Now().UnixNano())
		// Evidence writes never ride the drain budget's context: at
		// budget expiry that context is cancelled and the evidence row
		// would stay open forever (round-1 F001).
		if err := store.StartDrainRun(evidenceCtx(), ports.DrainRunInput{
			DrainID: drainID, RouteID: routeID, Trigger: config.DrainModeAfterCommand,
			Mode: policy.Mode, StartedAt: time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			boundedAfterCommandNote(stderr, fmt.Sprintf("route %s: drain evidence unavailable; automatic delivery skipped", routeID))
			continue
		}
		svcs[i] = &notifications.DrainService{
			Store: store, Resolver: resolver, Now: time.Now, Limit: 1,
			Retry: ports.NotificationBackoff{
				Initial: policy.InitialBackoff, Max: policy.MaxBackoff,
				Multiplier: policy.Multiplier, JitterFraction: policy.JitterFraction,
			},
		}
		limits[i] = policy.Limit
		perRoute[i] = notifications.DrainClaimedReport{}
		defer finishAfterCommandRun(store, drainID, &perRoute[i], stderr)
	}
	// Each route stops at its own configured limit (round-1 F003): the
	// rounds interleave, but no route exceeds its per-route bound.
	roundsUsed := make([]int, len(routes))
	for round := 0; round < maxRouteLimit(limits); round++ {
		progressed := false
		for i, routeID := range routes {
			if svcs[i] == nil || ctx.Err() != nil || roundsUsed[i] >= limits[i] {
				continue
			}
			roundsUsed[i]++
			report, err := svcs[i].DrainDue(ctx, routeID)
			if err != nil {
				boundedAfterCommandNote(stderr, fmt.Sprintf("route %s: automatic delivery stopped early: %v", routeID, err))
				svcs[i] = nil // stop visiting this route, keep the others
				continue
			}
			if report.Claimed > 0 {
				progressed = true
			}
			if report.BudgetExpired {
				perRoute[i].BudgetExpired = true
			}
			perRoute[i].Claimed += report.Claimed
			perRoute[i].Delivered += report.Delivered
			perRoute[i].Refused += report.Refused
			perRoute[i].Ambiguous += report.Ambiguous
			perRoute[i].Retryable += report.Retryable
		}
		if ctx.Err() != nil {
			for i := range perRoute {
				perRoute[i].BudgetExpired = true
			}
		}
		if !progressed {
			break
		}
	}
}

// evidenceCtx is the bounded context drain-run evidence writes use: it
// must survive the drain budget's expiry (round-1 F001).
func evidenceCtx() (c context.Context) {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = cancel // the evidence write's own short bound; the process exits soon after
	return c
}

// maxRouteLimit returns the largest configured per-route pass bound.
func maxRouteLimit(limits []int) int {
	max := 0
	for _, l := range limits {
		if l > max {
			max = l
		}
	}
	return max
}

// finishAfterCommandRun completes one route's evidence row with a fresh
// bounded context and reports the pending remainder once.
func finishAfterCommandRun(store afterCommandStore, drainID string, report *notifications.DrainClaimedReport, stderr io.Writer) {
	if err := store.FinishDrainRun(evidenceCtx(), drainID, ports.DrainRunCounts{
		Claimed: report.Claimed, Delivered: report.Delivered, Refused: report.Refused,
		RetryScheduled: report.Ambiguous + report.Retryable, BudgetExpired: report.BudgetExpired,
	}, time.Now().UTC().Format(time.RFC3339)); err != nil {
		boundedAfterCommandNote(stderr, fmt.Sprintf("drain evidence incomplete for %s", drainID))
		return
	}
	if report.Refused > 0 || report.Ambiguous > 0 || report.Retryable > 0 || report.BudgetExpired {
		boundedAfterCommandNote(stderr, fmt.Sprintf(
			"automatic notification drain left work pending (delivered %d, refused %d, retry-scheduled %d, budget_expired %t); the recovery schedule owns the remainder",
			report.Delivered, report.Refused, report.Ambiguous+report.Retryable, report.BudgetExpired))
	}
}

// afterCommandRoutes resolves the affected after-command routes in
// route-ID order: the effective drain mode must be after-command and,
// when the caller names affected routes, the route must be among them.
// The deterministic order is the round-robin's visiting order (v0.1.6
// §4).
func afterCommandRoutes(cfg *config.Config, affected []string) []string {
	affectedSet := map[string]bool{}
	for _, r := range affected {
		affectedSet[r] = true
	}
	out := []string{}
	for _, routeID := range cfg.SortedRouteIDs() {
		policy, err := config.EffectiveNotificationDrain(cfg.Routes[routeID].Notifications)
		if err != nil || policy.Mode != config.DrainModeAfterCommand {
			continue
		}
		if len(affectedSet) > 0 && !affectedSet[routeID] {
			continue
		}
		out = append(out, routeID)
	}
	return out
}

// boundedAfterCommandNote writes one bounded diagnostic line; the pass
// never exceeds afterCommandStderrBound lines per invocation.
func boundedAfterCommandNote(stderr io.Writer, msg string) {
	invocationMu.Lock()
	defer invocationMu.Unlock()
	if afterCommandNotes >= afterCommandStderrBound {
		return
	}
	afterCommandNotes++
	fmt.Fprintf(stderr, "%s\n", msg)
}
