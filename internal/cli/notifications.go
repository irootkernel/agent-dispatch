package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/notificationsink"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	appnotifications "github.com/irootkernel/agent-dispatch/internal/app/notifications"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Notification commands (E13-T2, CLI-013): `notifications test`, `list`,
// `retry`, and `drain` over the durable outbox of E13-T1. Delivery
// outcomes are data: a refused, ambiguous, or retryable delivery is a
// visible, inspectable result, never a mutation of dispatch or work
// state (NTF-005).

// runNotifications dispatches the notification subcommands.
func runNotifications(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "notifications", "notifications requires a subcommand: test, list, retry, or drain")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "test":
		return runNotificationsTest("notifications test", rest, stdout, stderr)
	case "list":
		return runNotificationsList("notifications list", rest, stdout, stderr)
	case "retry":
		return runNotificationsRetry("notifications retry", rest, stdout, stderr)
	case "drain":
		return runNotificationsDrain("notifications drain", rest, stdout, stderr)
	default:
		return usageError(stderr, "notifications", fmt.Sprintf("unknown notifications subcommand %q", sub))
	}
}

// notificationSinkResolver builds one sink adapter from the route's
// declared notifications block (SEC-012: endpoints and credentials are
// trusted configuration; event and worker data never select a sink).
// The log sink emits its structured payload lines on stderr — stdout
// is the one-envelope contract (CLI-001/002) and stderr is the
// structured log stream (OPS-001). The webhook transport uses the
// shared strict-client factory so the CLI tests can substitute their
// loopback certificate authority without weakening the production
// transport (SEC-013).
func notificationSinkResolver(cfg *config.Config, stderr io.Writer) appnotifications.SinkResolver {
	return func(routeID string, sink ports.NotificationSinkRef) (ports.NotificationSink, error) {
		route, ok := cfg.Routes[routeID]
		if !ok {
			return nil, fmt.Errorf("route %q is not defined", routeID)
		}
		if route.Notifications == nil {
			return nil, fmt.Errorf("route %q declares no notifications block", routeID)
		}
		for _, declared := range route.Notifications.Sinks {
			if declared.ID != sink.ID {
				continue
			}
			switch declared.Type {
			case "log":
				return &notificationsink.LogSink{ID: declared.ID, Out: stderr}, nil
			case "webhook":
				timeout := notificationsink.DefaultDeliveryTimeout
				opts := notificationsink.WebhookOptions{
					SinkID: declared.ID, Endpoint: declared.Endpoint, Timeout: timeout,
					Client: webhookClientFactory(timeout),
				}
				if declared.Auth != nil {
					opts.AuthType = declared.Auth.Type
					opts.SecretRef = declared.Auth.SecretRef
					opts.AuthHeaderName = declared.Auth.HeaderName
				}
				adapter, err := notificationsink.NewWebhookSink(opts)
				if err != nil {
					return nil, fmt.Errorf("sink %q: %w", declared.ID, err)
				}
				return adapter, nil
			default:
				return nil, fmt.Errorf("sink %q has unknown type %q", declared.ID, declared.Type)
			}
		}
		return nil, fmt.Errorf("route %q declares no notification sink %q", routeID, sink.ID)
	}
}

// notificationSinkOf resolves one declared sink by id for the test
// command and reports the sink's type.
func notificationSinkOf(cfg *config.Config, routeID, sinkID string, stderr io.Writer) (ports.NotificationSink, string, error) {
	route, ok := cfg.Routes[routeID]
	if !ok {
		return nil, "", fmt.Errorf("route %q is not defined", routeID)
	}
	if route.Notifications == nil {
		return nil, "", fmt.Errorf("route %q declares no notifications block", routeID)
	}
	for _, declared := range route.Notifications.Sinks {
		if declared.ID == sinkID {
			sink, err := notificationSinkResolver(cfg, stderr)(routeID, ports.NotificationSinkRef{ID: declared.ID, Type: declared.Type})
			return sink, declared.Type, err
		}
	}
	return nil, "", fmt.Errorf("route %q declares no notification sink %q", routeID, sinkID)
}

// runNotificationsTest probes one declared sink (NTF-008): one
// transport-level delivery of a probe payload in the
// notification-event/v1 envelope — never stored, never a source event,
// never a Hermes task, and never confusable with a real transition: the
// probe's event value is "test", outside the stored vocabulary by
// design, and its stable idempotency identity derives from the
// (route, sink) pair so repeated probes deduplicate at the endpoint.
func runNotificationsTest(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, map[string]bool{"--config": true, "--route": true, "--sink": true})
	if code != 0 {
		return code
	}
	routeID := flags.val("--route")
	sinkID := flags.val("--sink")
	if routeID == "" || sinkID == "" {
		return usageError(stderr, command, "notifications test requires --route <id> and --sink <id>")
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	sink, sinkType, err := notificationSinkOf(cfg, routeID, sinkID, stderr)
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	now := time.Now().UTC()
	probeID := notificationProbeID(routeID, sinkID)
	delivery := ports.NotificationDelivery{
		NotificationID: probeID,
		RouteID:        routeID,
		SinkID:         sinkID,
		IdempotencyKey: records.NotificationIdempotencyKey(probeID),
		PayloadJSON:    notificationProbePayload(routeID, sinkID, cfg, now),
	}
	attempt := sink.Deliver(requestCtx(), delivery)
	return writeEnvelope(stdout, command, map[string]any{
		"route_id":        routeID,
		"sink_id":         sinkID,
		"sink_type":       sinkType,
		"notification_id": probeID,
		"outcome":         string(attempt.Outcome),
		"error_code":      nilIfEmpty(attempt.ErrorCode),
		"created_nothing": true,
		"note":            "the probe is transport-level only: no notification intent, source event, or Hermes task was created (NTF-008)",
	})
}

// notificationProbeID derives the deterministic probe identity of one
// (route, sink) pair: pattern-compatible with the stored records so
// endpoint-side validation sees a well-formed identifier, and stable so
// repeated probes present the same idempotency key.
func notificationProbeID(routeID, sinkID string) string {
	sum := sha256.Sum256([]byte("agent-dispatch/notification-test-probe/v1\x00" + routeID + "\x00" + sinkID))
	return "ntf-" + hex.EncodeToString(sum[:])
}

// notificationProbePayload renders the probe payload: the
// notification-event/v1 envelope with the dedicated "test" event value
// and a source block that identifies the probe explicitly.
func notificationProbePayload(routeID, sinkID string, cfg *config.Config, now time.Time) string {
	revision := ""
	if route, ok := cfg.Routes[routeID]; ok {
		revision = config.NotificationPolicyRevision(route)
	}
	doc := map[string]any{
		"schema_version":               "agent-dispatch.notification-event/v1",
		"notification_id":              notificationProbeID(routeID, sinkID),
		"route_id":                     routeID,
		"event":                        "test",
		"transition":                   "route:" + routeID + ":sink-test",
		"sink_id":                      sinkID,
		"notification_policy_revision": revision,
		"created_at":                   now.Format(time.RFC3339),
		"source": map[string]string{
			"origin": "notifications_test", "probe": "true",
		},
	}
	raw, _ := json.Marshal(doc)
	return string(raw)
}

// runNotificationsList renders the durable notification intents with
// their attempt projection (NTF-004). The limit mirrors the store's
// listing bound: 1 through 500, a wider value is a usage error rather
// than a silent clamp.
func runNotificationsList(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, map[string]bool{"--config": true, "--route": true, "--state": true, "--sink": true, "--limit": true})
	if code != 0 {
		return code
	}
	_, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	filter := ports.NotificationFilter{
		RouteID: flags.val("--route"),
		State:   flags.val("--state"),
		SinkID:  flags.val("--sink"),
	}
	if filter.State != "" {
		if _, err := records.ParseNotificationState(filter.State); err != nil {
			return usageError(stderr, command, "--state must be one of pending, delivered, or refused")
		}
	}
	if raw := flags.val("--limit"); raw != "" {
		n, ok := parseBoundedLimit(raw)
		if !ok {
			return usageError(stderr, command, "--limit must be an integer between 1 and 500")
		}
		filter.Limit = n
	}
	rows, err := closer.ListNotifications(requestCtx(), filter)
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, rec := range rows {
		entry := map[string]any{
			"notification_id": rec.NotificationID,
			"route_id":        rec.RouteID,
			"event":           string(rec.Event),
			"transition":      rec.Transition,
			"sink_id":         rec.SinkID,
			"sink_type":       rec.SinkType,
			"state":           string(rec.State),
			"attempt_count":   rec.AttemptCount,
			"last_outcome":    nilIfEmpty(string(rec.LastOutcome)),
			"created_at":      rec.CreatedAt,
		}
		if rec.DestinationID != "" {
			entry["destination_id"] = rec.DestinationID
		}
		if rec.ResolvedAt != "" {
			entry["resolved_at"] = rec.ResolvedAt
		}
		out = append(out, entry)
	}
	return writeEnvelope(stdout, command, map[string]any{
		"notifications": out,
		"count":         len(out),
	})
}

// runNotificationsRetry forces one delivery attempt of one
// notification (CLI-013): a refused notification re-arms first — the
// operator's explicit decision to retry after fixing the sink — and the
// attempt presents the notification's stable idempotency identity, so
// the endpoint deduplicates it against every earlier attempt (NTF-007).
// A delivered notification never re-sends.
func runNotificationsRetry(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, map[string]bool{"--config": true})
	if code != 0 {
		return code
	}
	notificationID := flags.positional
	if notificationID == "" {
		return usageError(stderr, command, "notifications retry requires a notification ID")
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	_, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	ctx := requestCtx()
	rec, err := closer.LoadNotification(ctx, notificationID)
	if err != nil {
		if errors.Is(err, ports.ErrNotificationNotFound) {
			return planErr(stderr, command, "notification_not_found", "input_rejected", err.Error(), 4)
		}
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	// The sink constructs BEFORE any state changes: a defective
	// declaration is a pure configuration failure that re-arms nothing.
	delivery := appnotifications.Delivery{
		Store:    notificationDeliveryStore{store: closer},
		Resolver: notificationSinkResolver(cfg, stderr),
		Now:      time.Now,
	}
	if _, err := delivery.Resolver(rec.RouteID, ports.NotificationSinkRef{ID: rec.SinkID, Type: rec.SinkType}); err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	if err := closer.RetryNotification(ctx, notificationID); err != nil {
		if errors.Is(err, ports.ErrStateNotEligible) {
			return planErr(stderr, command, "notification_already_delivered", "input_rejected", err.Error(), 4)
		}
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	outcome, err := delivery.DeliverOne(ctx, rec)
	if err != nil {
		var construction *appnotifications.SinkConstructionError
		if errors.As(err, &construction) {
			return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
		}
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	after, err := closer.LoadNotification(ctx, notificationID)
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	return writeEnvelope(stdout, command, map[string]any{
		"notification_id": notificationID,
		"outcome":         string(outcome),
		"state":           string(after.State),
		"note":            "the retry presented the notification's stable idempotency identity; no dispatch, receipt, or work state changed (NTF-005/NTF-007)",
	})
}

// notificationDeliveryStore adapts the concrete store to the delivery
// service's bounded surface.
type notificationDeliveryStore struct{ store *sqlite.Store }

func (s notificationDeliveryStore) PendingNotifications(ctx context.Context, limit int) ([]ports.NotificationEventRecord, error) {
	return s.store.PendingNotifications(ctx, limit)
}

func (s notificationDeliveryStore) ListNotifications(ctx context.Context, filter ports.NotificationFilter) ([]ports.NotificationEventRecord, error) {
	return s.store.ListNotifications(ctx, filter)
}

func (s notificationDeliveryStore) LoadNotification(ctx context.Context, id string) (ports.NotificationEventRecord, error) {
	return s.store.LoadNotification(ctx, id)
}

func (s notificationDeliveryStore) ListNotificationAttempts(ctx context.Context, id string) ([]ports.NotificationAttemptRecord, error) {
	return s.store.ListNotificationAttempts(ctx, id)
}

func (s notificationDeliveryStore) CountNotificationsByState(ctx context.Context) (map[string]int64, error) {
	return s.store.CountNotificationsByState(ctx)
}

func (s notificationDeliveryStore) EnqueueRouteNotification(ctx context.Context, routeID string, event records.NotificationEventKind, transition, destinationID string, source map[string]string, now string) (int, error) {
	return s.store.EnqueueRouteNotification(ctx, routeID, event, transition, destinationID, source, now)
}

func (s notificationDeliveryStore) RetryNotification(ctx context.Context, id string) error {
	return s.store.RetryNotification(ctx, id)
}

func (s notificationDeliveryStore) RecordNotificationAttempt(ctx context.Context, in ports.NotificationAttemptInput) (ports.NotificationAttemptRecord, error) {
	return s.store.RecordNotificationAttempt(ctx, in)
}

func (s notificationDeliveryStore) ClaimDueNotifications(ctx context.Context, filter ports.NotificationClaimFilter) ([]ports.NotificationClaim, error) {
	return s.store.ClaimDueNotifications(ctx, filter)
}

func (s notificationDeliveryStore) RecordNotificationAttemptFenced(ctx context.Context, in ports.NotificationAttemptInput, claim ports.NotificationClaim, backoff ports.NotificationBackoff) (ports.NotificationAttemptRecord, error) {
	return s.store.RecordNotificationAttemptFenced(ctx, in, claim, backoff)
}

func (s notificationDeliveryStore) ReleaseNotificationClaims(ctx context.Context, owner string, claims []ports.NotificationClaim) error {
	return s.store.ReleaseNotificationClaims(ctx, owner, claims)
}

// runNotificationsDrain delivers the pending notifications, oldest
// first, bounded (CLI-013, observability-and-operations §8): the pass
// performs one bounded attempt per pending notification. The OPS-013
// drift evaluation rides the scheduled runner — its only automatic
// surface (v0.1.6 §4, E16-T3 evidence) — and never the explicit drain
// (recursion exclusion). Delivery outcomes are reported, never
// exit-coded: an ambiguous or retryable remainder is visible work for
// the next pass (NTF-007). The pass bound never hides a backlog:
// `pending` and `pending_remaining` report the store's post-pass
// pending truth, not just this pass's outcomes.
func runNotificationsDrain(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, map[string]bool{"--config": true, "--limit": true})
	if code != 0 {
		return code
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	_, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	// The manual drain shares the E16-T2 lease-safe service: it selects
	// due work only, claims atomically, records fenced outcomes, and
	// persists the retry backoff (NTF-011 through NTF-014).
	limit := 0
	if raw := flags.val("--limit"); raw != "" {
		n, ok := parseBoundedLimit(raw)
		if !ok {
			return usageError(stderr, command, "--limit must be an integer between 1 and 500")
		}
		limit = n
	}
	drainer := &appnotifications.DrainService{
		Store:    notificationDeliveryStore{store: closer},
		Resolver: notificationSinkResolver(cfg, stderr),
		Now:      time.Now,
		Limit:    limit,
	}
	ctx := requestCtx()
	report, err := drainer.DrainDue(ctx, "")
	if err != nil {
		var construction *appnotifications.SinkConstructionError
		if errors.As(err, &construction) {
			return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
		}
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	byState, err := closer.CountNotificationsByState(ctx)
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	pendingRemaining := int(byState[string(records.NotificationPending)])
	return writeEnvelope(stdout, command, map[string]any{
		"drain":             report,
		"pending":           report.Pending() || pendingRemaining > 0,
		"pending_remaining": pendingRemaining,
		"note":              "one bounded attempt per pending notification; ambiguous and retryable outcomes stay pending under their stable idempotency identity",
	})
}

// driftEnqueue binds the drift evaluation to the store's enqueue
// surface; tests swap it to prove the scheduled runner's
// storage-failure posture.
var driftEnqueue = func(ctx context.Context, store *sqlite.Store, routeID string, event records.NotificationEventKind, transition string, source map[string]string, now string) (int, error) {
	return store.EnqueueRouteNotification(ctx, routeID, event, transition, "", source, now)
}

// evaluateDriftNotifications runs the OPS-013 drift evaluation for the
// notification-enabled routes (E13-T2): the watchman class enqueues
// watchman_drift and the capability/profile/skill classes enqueue
// integration_drift, each exactly once per drift appearance — the
// occurrence discriminator is the finding's digest, so a persisting
// drift never re-notifies and a changed or newly appearing drift does
// (AC-901). The reconciliation class is excluded: its appearances
// already notify through the pending-reconcile transitions of E13-T1.
// A drift-enqueue storage failure aborts the drain as the storage
// class — it is a durable-store condition, never a delivery outcome
// that belongs in the envelope as data.
func evaluateDriftNotifications(ctx context.Context, cfg *config.Config, store *sqlite.Store, now string) ([]map[string]any, error) {
	out := []map[string]any{}
	for _, routeID := range cfg.SortedRouteIDs() {
		route := cfg.Routes[routeID]
		if route.Notifications == nil || len(route.Notifications.Sinks) == 0 {
			continue
		}
		entry := map[string]any{"route_id": routeID, "enqueue": map[string]any{}}
		enqueued := entry["enqueue"].(map[string]any)
		for _, finding := range routeDriftFindings(ctx, cfg, store, routeID, route) {
			var event records.NotificationEventKind
			switch finding.Class {
			case "watchman":
				event = records.EventWatchmanDrift
			case "capability", "profile", "skill":
				event = records.EventIntegrationDrift
			default:
				continue
			}
			transition := "drift:" + finding.Class + ":" + driftOccurrence(routeID, finding)
			created, err := driftEnqueue(ctx, store, routeID, event, transition,
				map[string]string{"class": finding.Class, "origin": "drift_evaluation"}, now)
			if err != nil {
				return nil, err
			}
			if created == 0 {
				// Zero created is either the policy filter or a
				// dedup-collapsed replay of the same drift appearance —
				// distinguished through the store surface, never raw SQL
				// from the CLI layer.
				existing, qerr := store.CountNotificationsByTransition(ctx, routeID, transition)
				if qerr != nil {
					return nil, qerr
				}
				if existing > 0 {
					enqueued[finding.Class] = map[string]any{"already_notified": true, "detail": finding.Detail}
				} else {
					enqueued[finding.Class] = map[string]any{"filtered": true, "detail": finding.Detail}
				}
				continue
			}
			enqueued[finding.Class] = map[string]any{"sinks": created, "detail": finding.Detail}
		}
		out = append(out, entry)
	}
	return out, nil
}

// driftOccurrence digests one drift finding's STABLE identity — the
// machine key, never the presentation text: the same persisting drift
// keeps its occurrence (deduplicated), a wording change in the detail
// does not re-notify, and a genuinely changed drift identity does.
func driftOccurrence(routeID string, finding driftFinding) string {
	sum := sha256.Sum256([]byte(routeID + "\x00" + finding.Class + "\x00" + finding.Key))
	return hex.EncodeToString(sum[:16])
}

// parseBoundedLimit parses the listing bound 1..500 — the store's
// listing ceiling, surfaced as a usage error instead of a silent clamp.
func parseBoundedLimit(raw string) (int, bool) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 500 {
		return 0, false
	}
	return n, true
}

func (s notificationDeliveryStore) StartDrainRun(ctx context.Context, in ports.DrainRunInput) error {
	return s.store.StartDrainRun(ctx, in)
}

func (s notificationDeliveryStore) FinishDrainRun(ctx context.Context, drainID string, counts ports.DrainRunCounts, completedAt string) error {
	return s.store.FinishDrainRun(ctx, drainID, counts, completedAt)
}
