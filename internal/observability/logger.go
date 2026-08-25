// Package observability implements the structured operational log
// (OPS-001, SEC-007): one JSON object per line on standard error, the
// stable event-name vocabulary from
// docs/architecture/observability-and-operations.md §3, the
// causal correlation fields from §2, the level semantics from §4, and
// the path-privacy policy from retention-and-privacy.md §4. Log lines
// never carry note bodies, resolved secrets, or authorization material;
// paths render according to the configured policy.
package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Level is the operational log level (observability-and-operations §4).
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// ParseLevel maps the CLI --log-level vocabulary.
func ParseLevel(s string) (Level, error) {
	switch s {
	case "debug":
		return LevelDebug, nil
	case "info", "":
		return LevelInfo, nil
	case "warn":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	}
	return LevelInfo, fmt.Errorf("unknown log level %q (want debug, info, warn, or error)", s)
}

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	default:
		return "error"
	}
}

// Event names the stable operational event vocabulary
// (observability-and-operations §3). Every emitted line carries one of
// these names; nothing outside the vocabulary is logged.
const (
	EventSourceReceived          = "source.received"
	EventSourceRejected          = "source.rejected"
	EventObservationPersisted    = "observation.persisted"
	EventBatchPlanned            = "batch.planned"
	EventPolicyDecided           = "policy.decided"
	EventDispatchIntentCreated   = "dispatch.intent_created"
	EventDispatchAttemptStarted  = "dispatch.attempt_started"
	EventDispatchAccepted        = "dispatch.accepted"
	EventDispatchRejected        = "dispatch.rejected"
	EventDispatchUnknown         = "dispatch.unknown"
	EventDispatchRetryScheduled  = "dispatch.retry_scheduled"
	EventDispatchDeadLettered    = "dispatch.dead_lettered"
	EventDispatchMutexSuppressed = "dispatch.mutex_suppressed"
	EventDeliveryReconciled      = "delivery.reconciled"
	EventRouteDirtyMarked        = "route.dirty_marked"
	EventRouteFollowupCreated    = "route.followup_created"
	EventWorkBegun               = "work.begun"
	EventWorkCompleted           = "work.completed"
	EventWorkReceiptInvalid      = "work.receipt_invalid"
	EventFeedbackSuppressedExact = "feedback.suppressed_exact"
	EventFeedbackUnresolved      = "feedback.unresolved"
	EventQuarantineCreated       = "quarantine.created"
	EventQuarantineReleased      = "quarantine.released"
	EventReconciliationRequested = "reconciliation.requested"
	EventMaintenancePruned       = "maintenance.pruned"
	EventMaintenanceVacuumed     = "maintenance.vacuumed"
	EventMaintenanceBackedUp     = "maintenance.backed_up"
	EventDoctorFinding           = "doctor.finding"
)

// PathPolicy is the configured relative-path privacy posture
// (retention-and-privacy §4): relative renders configured-root-relative
// paths, redacted replaces them with a stable digest, full passes them
// through.
type PathPolicy string

const (
	PathsRelative PathPolicy = "relative"
	PathsRedacted PathPolicy = "redacted"
	PathsFull     PathPolicy = "full"
)

// ParsePathPolicy maps the instance.log_paths vocabulary with its
// documented default.
func ParsePathPolicy(s string) (PathPolicy, error) {
	switch s {
	case "relative", "":
		return PathsRelative, nil
	case "redacted":
		return PathsRedacted, nil
	case "full":
		return PathsFull, nil
	}
	return PathsRelative, fmt.Errorf("unknown log_paths policy %q (want relative, redacted, or full)", s)
}

// Correlation carries the causal identity fields available at one event
// (observability-and-operations §2). Correlation fields identify rows;
// they never replace the durable foreign keys in SQLite.
type Correlation struct {
	TraceID       string `json:"trace_id,omitempty"`
	ObservationID string `json:"observation_id,omitempty"`
	BatchID       string `json:"batch_id,omitempty"`
	DecisionID    string `json:"decision_id,omitempty"`
	DispatchID    string `json:"dispatch_id,omitempty"`
	AttemptID     string `json:"attempt_id,omitempty"`
	ReceiptID     string `json:"receipt_id,omitempty"`
	RouteID       string `json:"route_id,omitempty"`
	RouteRevision string `json:"route_revision,omitempty"`
	ResourceID    string `json:"resource_id,omitempty"`
	TargetID      string `json:"target_id,omitempty"`
	ExternalRef   string `json:"external_ref,omitempty"`
	RunID         string `json:"run_id,omitempty"`
}

// redactedKeys are the value keys that never survive into a log line
// regardless of content (SEC-007); their values become [redacted].
var redactedKeys = map[string]bool{
	"secret":           true,
	"secrets":          true,
	"token":            true,
	"authorization":    true,
	"auth_header":      true,
	"password":         true,
	"note_body":        true,
	"note_bodies":      true,
	"credential":       true,
	"secret_ref_value": true,
}

// Logger writes structured JSON log lines to one writer with level
// filtering and the redaction policy. It is safe for concurrent use.
type Logger struct {
	mu    sync.Mutex
	w     io.Writer
	level Level
	paths PathPolicy
	clock func() time.Time
}

// New builds the operational logger. A nil writer disables output
// (tests use this to assert silence).
func New(w io.Writer, level Level, paths PathPolicy) *Logger {
	return &Logger{w: w, level: level, paths: paths, clock: time.Now}
}

// Enabled reports whether the level passes the filter.
// denylisted reports whether a data key is redacted regardless of case
// (SEC-007); the one check both map arms share so they cannot drift
// (epic round-3 F002).
func denylisted(key string) bool { return redactedKeys[strings.ToLower(key)] }

func (l *Logger) Enabled(level Level) bool { return level >= l.level }

// Log emits one event if the level passes. message is human context;
// data carries the bounded event payload with sensitive keys and path
// values redacted by policy.
func (l *Logger) Log(level Level, event string, corr Correlation, message string, data map[string]any) {
	if l == nil || l.w == nil || !l.Enabled(level) {
		return
	}
	// The message path is sanitized like every value: credential-shaped
	// fragments are redacted even in free text (review M-20, E8
	// correction — the field was safe by convention only).
	message = RenderPath(message, PathsRedacted)
	line := map[string]any{
		"time":    l.clock().UTC().Format(time.RFC3339Nano),
		"level":   level.String(),
		"event":   event,
		"message": message,
	}
	for k, v := range sanitize(corr, l.paths) {
		line[k] = v
	}
	if data != nil {
		line["data"] = sanitizeMap(data, l.paths)
	}
	raw, err := json.Marshal(line)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(append(raw, '\n'))
}

// Debug, Info, Warn, and Error emit at their levels.
func (l *Logger) Debug(event string, corr Correlation, message string, data map[string]any) {
	l.Log(LevelDebug, event, corr, message, data)
}

func (l *Logger) Info(event string, corr Correlation, message string, data map[string]any) {
	l.Log(LevelInfo, event, corr, message, data)
}

func (l *Logger) Warn(event string, corr Correlation, message string, data map[string]any) {
	l.Log(LevelWarn, event, corr, message, data)
}

func (l *Logger) Error(event string, corr Correlation, message string, data map[string]any) {
	l.Log(LevelError, event, corr, message, data)
}

// sanitize projects one correlation struct through the redaction
// policy (IDs are already opaque; nothing to redact, but path-bearing
// fields would pass through renderPath if added).
func sanitize(c Correlation, policy PathPolicy) map[string]any {
	out := map[string]any{}
	add := func(k, v string) {
		if v != "" {
			out[k] = v
		}
	}
	add("trace_id", c.TraceID)
	add("observation_id", c.ObservationID)
	add("batch_id", c.BatchID)
	add("decision_id", c.DecisionID)
	add("dispatch_id", c.DispatchID)
	add("attempt_id", c.AttemptID)
	add("receipt_id", c.ReceiptID)
	add("route_id", c.RouteID)
	add("route_revision", c.RouteRevision)
	add("resource_id", c.ResourceID)
	add("target_id", c.TargetID)
	add("external_ref", c.ExternalRef)
	add("run_id", c.RunID)
	return out
}

// sanitizeMap redacts one event payload: sensitive keys lose their
// values, string values containing path separators render through the
// path policy, and note-body-like free text is dropped.
func sanitizeMap(data map[string]any, policy PathPolicy) map[string]any {
	out := make(map[string]any, len(data))
	for k, v := range data {
		if denylisted(k) {
			out[k] = "[redacted]"
			continue
		}
		out[k] = sanitizeValue(v, policy)
	}
	return out
}

func sanitizeValue(v any, policy PathPolicy) any {
	switch t := v.(type) {
	case string:
		return RenderPath(t, policy)
	case []string:
		out := make([]string, len(t))
		for i, s := range t {
			out[i] = RenderPath(s, policy)
		}
		return out
	case map[string]any:
		return sanitizeMap(t, policy)
	case map[string]string:
		// Typed string maps get the same key denylist as map[string]any
		// (SEC-007): a value under a sensitive key is [redacted] before
		// the path policy ever sees it (E9-T3, M-20's remainder; the
		// key check is the round-1 security remediation).
		out := make(map[string]string, len(t))
		for k, v := range t {
			if denylisted(k) {
				out[k] = "[redacted]"
				continue
			}
			out[k] = RenderPath(v, policy)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = sanitizeValue(item, policy)
		}
		return out
	default:
		return v
	}
}

// RenderPath renders one path-bearing string under the privacy policy:
// redacted replaces every path-shaped value with a stable digest so the
// vault structure never leaks; relative and full pass the value through
// (the caller supplies root-relative paths; absolute vault paths never
// reach the log).
func RenderPath(s string, policy PathPolicy) string {
	// Credential-shaped values are redacted regardless of their key or
	// the path policy (SEC-007, E7-T9/M-26).
	if credentialPattern.MatchString(s) {
		return credentialPattern.ReplaceAllString(s, "${1}${2}[redacted]")
	}
	if policy != PathsRedacted || s == "" {
		return s
	}
	if !strings.ContainsAny(s, "/\\") {
		return s // not a path; IDs and codes pass through
	}
	return "path:" + stableDigest(s)
}

// stableDigest derives the stable path digest used by the redacted
// policy: 16 bytes of the SHA-256, hex-encoded.
func stableDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

// credentialPattern matches credential-shaped values regardless of their
// key: bearer/basic/token authorization headers, common credential
// query parameters (hash and fragment forms included), and bare JWT
// blobs are redacted even when the emitter did not label them (SEC-007,
// E7-T9/M-26, widened E8-T4/M-20). The patterns are anchored to the
// credential fragment, never the whole containing path.
var credentialPattern = regexp.MustCompile(`(?i)` +
	// Header forms: Authorization: Bearer x / Basic x / Token x.
	`((?:bearer|basic|token)\s+)[A-Za-z0-9._~+/-]{8,}` +
	// Query and fragment parameter forms, including client_secret,
	// apikey, key, and password.
	`|((?:^|[?&#])(?:token|access_token|api_key|apikey|client_secret|secret|key|password)=)[^&\s]*` +
	// Bare JWTs: three dot-separated base64url segments.
	`|(eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{8,})`)
