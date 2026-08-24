package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
)

// intentStates maps the durable intent-state vocabulary for the
// operational counters (status, doctor).
func intentStates() []string {
	return []string{"ready", "submitting", "accepted", "rejected", "unknown", "retry_wait", "reconciling", "dead_lettered", "superseded", "completed", "failed", "canceled"}
}

// CountIntentsByState returns the dispatch-intent counts per state
// (observability-and-operations §5 queue counters).
func (s *Store) CountIntentsByState(ctx context.Context) (map[string]int64, error) {
	out := make(map[string]int64, len(intentStates()))
	for _, state := range intentStates() {
		var n int64
		if err := s.QueryRowContext(ctx, `SELECT COUNT(*) FROM dispatch_intents WHERE state = ?`, state).Scan(&n); err != nil {
			return nil, fmt.Errorf("counting %s intents: %w", state, err)
		}
		out[state] = n
	}
	return out, nil
}

// CountQuarantineByState returns the quarantine counts per state.
func (s *Store) CountQuarantineByState(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{"held": 0, "released": 0, "discarded": 0, "superseded": 0}
	for _, state := range []string{"held", "released", "discarded", "superseded"} {
		var n int64
		if err := s.QueryRowContext(ctx, `SELECT COUNT(*) FROM quarantine_items WHERE state = ?`, state).Scan(&n); err != nil {
			return nil, fmt.Errorf("counting %s quarantine: %w", state, err)
		}
		out[state] = n
	}
	return out, nil
}

// OldestUnresolved returns the created_at of the oldest dispatch intent
// in an unresolved state (unknown, retry_wait, dead_lettered,
// submitting, reconciling), or "" when none exist — the retention
// visibility counter from observability-and-operations §5.
func (s *Store) OldestUnresolved(ctx context.Context) (string, error) {
	var oldest sql.NullString
	err := s.QueryRowContext(ctx, `SELECT MIN(created_at) FROM dispatch_intents WHERE state IN ('unknown','retry_wait','dead_lettered','submitting','reconciling')`).Scan(&oldest)
	if err != nil {
		return "", fmt.Errorf("finding oldest unresolved intent: %w", err)
	}
	if !oldest.Valid {
		return "", nil
	}
	return oldest.String, nil
}

// StaleLeases lists dispatch intents that hold an expired attempt lease
// (doctor's stale-lease finding, OPS-005 posture).
func (s *Store) StaleLeases(ctx context.Context, now string) ([]string, error) {
	rows, err := s.QueryContext(ctx, `SELECT dispatch_id FROM dispatch_intents WHERE state = 'submitting' AND lease_expires_at IS NOT NULL AND lease_expires_at < ? ORDER BY dispatch_id LIMIT 100`, now)
	if err != nil {
		return nil, fmt.Errorf("listing stale leases: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// notActiveSlotSQL is the shared active-slot guard for every prune
// lineage delete (plan and execution, E9-T2/T4-F005): the route's live
// dispatch keeps its lineage whatever its age.
const notActiveSlotSQL = ` AND dispatch_id NOT IN (SELECT active_dispatch_id FROM route_runtime_state WHERE active_dispatch_id IS NOT NULL)`

// resolvedTerminalStates are the intent states whose lineage is fully
// resolved for retention (retention-and-privacy §2: everything else is
// retained until resolution).
func resolvedTerminalStates() string {
	// A closed dead letter is superseded (already in the set); an open
	// dead letter stays retained until the operator closes it
	// (retention-and-privacy §2, E7-T7 round-1 remediation). An accepted
	// dispatch is NOT resolved: it holds live, unreceipted work on the
	// target — its lineage is retained until the dispatch completes or
	// is explicitly superseded (E8-T4, H-3/AC-503).
	return "('rejected','superseded','completed','failed','canceled')"
}

// PruneCutoffs carries the per-class retention cutoffs as canonical
// timestamps.
type PruneCutoffs struct {
	Observations       string `json:"observations"`
	Attempts           string `json:"attempts"`
	CompletedReceipts  string `json:"completed_receipts"`
	ResolvedQuarantine string `json:"resolved_quarantine"`
}

// PruneCounts reports the per-class prune counts of one plan or
// execution.
type PruneCounts struct {
	Attempts     int64 `json:"attempts"`
	Receipts     int64 `json:"receipts"`
	WorkReceipts int64 `json:"work_receipts"`
	Intents      int64 `json:"intents"`
	Decisions    int64 `json:"decisions"`
	Batches      int64 `json:"batches"`
	Observations int64 `json:"observations"`
	PathFacts    int64 `json:"path_facts"`
	Quarantine   int64 `json:"quarantine"`
}

// PrunePlan is the dry-run result: per-class counts plus the bounded
// dispatch-id sample.
type PrunePlan struct {
	Cutoffs   PruneCutoffs `json:"cutoffs"`
	Counts    PruneCounts  `json:"counts"`
	SampleIDs []string     `json:"sample_dispatch_ids,omitempty"`
}

// PlanPrune computes what the retention policy would delete now
// (retention-and-privacy §3): children before parents, never orphaning
// a dispatch, receipt, quarantine, or the evidence an unresolved
// lineage still needs; state-transition audit rows are never pruned.
// The per-class counts mirror the execution conditions exactly — an
// intent counts as prunable only when its attempts and receipts are
// outside retention under the same cutoffs.
func (s *Store) PlanPrune(ctx context.Context, cutoffs PruneCutoffs) (PrunePlan, error) {
	plan := PrunePlan{Cutoffs: cutoffs}
	terminal := resolvedTerminalStates()
	c := cutoffs
	count := func(label, query string, args ...any) (int64, error) {
		var n int64
		if err := s.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			return 0, fmt.Errorf("planning %s prune: %w", label, err)
		}
		return n, nil
	}
	var err error
	// The plan mirrors the execution guards exactly (the active slot and
	// unresolved dispatches never count; E8-T4, H-3, with begun receipts
	// prunable on terminal dispatches — E9 epic validation round-1 F001).
	// The JOIN'd plan queries alias the guard's dispatch_id to the
	// joined table (the shared constant's bare name is ambiguous here).
	attemptSlot := ` AND a.dispatch_id NOT IN (SELECT active_dispatch_id FROM route_runtime_state WHERE active_dispatch_id IS NOT NULL)`
	receiptSlot := ` AND r.dispatch_id NOT IN (SELECT active_dispatch_id FROM route_runtime_state WHERE active_dispatch_id IS NOT NULL)`
	if plan.Counts.Attempts, err = count("attempt",
		`SELECT COUNT(*) FROM dispatch_attempts a JOIN dispatch_intents i ON i.dispatch_id = a.dispatch_id WHERE a.completed_at IS NOT NULL AND a.completed_at < ? AND i.state IN `+terminal+attemptSlot, c.Attempts); err != nil {
		return plan, err
	}
	if plan.Counts.Receipts, err = count("receipt",
		`SELECT COUNT(*) FROM dispatch_receipts r JOIN dispatch_intents i ON i.dispatch_id = r.dispatch_id WHERE r.received_at < ? AND i.state IN `+terminal+receiptSlot, c.CompletedReceipts); err != nil {
		return plan, err
	}
	if plan.Counts.WorkReceipts, err = count("work receipt",
		`SELECT COUNT(*) FROM work_receipts WHERE submitted_at < ? AND dispatch_id IN (SELECT dispatch_id FROM dispatch_intents WHERE state IN `+terminal+`)`+notActiveSlotSQL, c.CompletedReceipts); err != nil {
		return plan, err
	}
	if plan.Counts.Intents, err = count("intent",
		`SELECT COUNT(*) FROM dispatch_intents i WHERE i.state IN `+terminal+` AND i.updated_at < ?
		 AND NOT EXISTS (SELECT 1 FROM dispatch_attempts a WHERE a.dispatch_id = i.dispatch_id AND (a.completed_at IS NULL OR a.completed_at >= ?))
		 AND NOT EXISTS (SELECT 1 FROM dispatch_receipts r WHERE r.dispatch_id = i.dispatch_id AND r.received_at >= ?)
		 AND NOT EXISTS (SELECT 1 FROM work_receipts w WHERE w.dispatch_id = i.dispatch_id AND w.submitted_at >= ?)
		 AND i.dispatch_id NOT IN (SELECT active_dispatch_id FROM route_runtime_state WHERE active_dispatch_id IS NOT NULL)`,
		c.Attempts, c.Attempts, c.CompletedReceipts, c.CompletedReceipts); err != nil {
		return plan, err
	}
	if err := s.planRemaining(ctx, &plan, terminal); err != nil {
		return plan, err
	}
	rows, err := s.QueryContext(ctx, `SELECT dispatch_id FROM dispatch_intents WHERE state IN `+terminal+` AND updated_at < ? ORDER BY updated_at LIMIT 20`, c.Attempts)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				plan.SampleIDs = append(plan.SampleIDs, id)
			}
		}
	}
	return plan, nil
}

// planRemaining counts decisions, batches, observations, path facts,
// and resolved quarantine for the plan.
func (s *Store) planRemaining(ctx context.Context, plan *PrunePlan, terminal string) error {
	c := plan.Cutoffs
	// The plan mirrors the execution's cascade through one shared CTE
	// chain: the intents the execution would delete free their
	// decisions, whose deletion frees their batches, whose deletion
	// frees the observations only those batches referenced. Each class
	// therefore counts exactly what the ordered execution deletes.
	prunable := `WITH prunable_intents AS (
			SELECT dispatch_id FROM dispatch_intents WHERE state IN ` + terminal + ` AND updated_at < ?
			 AND NOT EXISTS (SELECT 1 FROM dispatch_attempts a WHERE a.dispatch_id = dispatch_intents.dispatch_id AND (a.completed_at IS NULL OR a.completed_at >= ?))
			 AND NOT EXISTS (SELECT 1 FROM dispatch_receipts r WHERE r.dispatch_id = dispatch_intents.dispatch_id AND r.received_at >= ?)
			 AND NOT EXISTS (SELECT 1 FROM work_receipts w WHERE w.dispatch_id = dispatch_intents.dispatch_id AND w.submitted_at >= ?)
			 AND dispatch_id NOT IN (SELECT active_dispatch_id FROM route_runtime_state WHERE active_dispatch_id IS NOT NULL)),
		prunable_decisions AS (
			SELECT d.decision_id, d.batch_id FROM policy_decisions d WHERE d.created_at < ?
			 AND NOT EXISTS (SELECT 1 FROM dispatch_intents i WHERE i.decision_id = d.decision_id AND i.dispatch_id NOT IN (SELECT dispatch_id FROM prunable_intents))
			 AND NOT EXISTS (SELECT 1 FROM policy_decisions newer WHERE newer.supersedes_decision_id = d.decision_id)
			 AND NOT EXISTS (SELECT 1 FROM quarantine_items q WHERE q.decision_id = d.decision_id)),
		prunable_batches AS (
			SELECT b.batch_id FROM change_batches b WHERE b.created_at < ?
			 AND NOT EXISTS (SELECT 1 FROM policy_decisions d WHERE d.batch_id = b.batch_id AND d.decision_id NOT IN (SELECT decision_id FROM prunable_decisions))
			 AND NOT EXISTS (SELECT 1 FROM quarantine_items q WHERE q.batch_id = b.batch_id))
		`
	args := []any{c.Attempts, c.Attempts, c.CompletedReceipts, c.CompletedReceipts, c.Attempts, c.Observations}
	if err := s.QueryRowContext(ctx, prunable+`SELECT COUNT(*) FROM prunable_decisions`, args...).Scan(&plan.Counts.Decisions); err != nil {
		return fmt.Errorf("planning decision prune: %w", err)
	}
	if err := s.QueryRowContext(ctx, prunable+`SELECT COUNT(*) FROM prunable_batches`, args...).Scan(&plan.Counts.Batches); err != nil {
		return fmt.Errorf("planning batch prune: %w", err)
	}
	if err := s.QueryRowContext(ctx, prunable+`SELECT COUNT(*) FROM source_observations o WHERE o.observed_at < ?
		 AND NOT EXISTS (SELECT 1 FROM batch_observations bo WHERE bo.observation_id = o.observation_id
		                  AND bo.batch_id NOT IN (SELECT batch_id FROM prunable_batches))`,
		append(append([]any{}, args...), c.Observations)...).Scan(&plan.Counts.Observations); err != nil {
		return fmt.Errorf("planning observation prune: %w", err)
	}
	if err := s.QueryRowContext(ctx, `SELECT COUNT(*) FROM path_facts WHERE observed_at < ?`, c.Observations).Scan(&plan.Counts.PathFacts); err != nil {
		return fmt.Errorf("planning path-fact prune: %w", err)
	}
	if err := s.QueryRowContext(ctx, `SELECT COUNT(*) FROM quarantine_items WHERE state IN ('released','discarded','superseded') AND resolved_at IS NOT NULL AND resolved_at < ?`, c.ResolvedQuarantine).Scan(&plan.Counts.Quarantine); err != nil {
		return fmt.Errorf("planning quarantine prune: %w", err)
	}
	return nil
}

// ExecutePrune deletes exactly the planned rows in one transaction,
// children before parents, with foreign keys enforced, and records the
// maintenance audit row (actor, reason, counts, cutoffs, time). It
// returns the executed counts.
func (s *Store) ExecutePrune(ctx context.Context, cutoffs PruneCutoffs, actor, reason, now string) (PruneCounts, error) {
	var counts PruneCounts
	terminal := resolvedTerminalStates()
	c := cutoffs
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return counts, err
	}
	defer tx.Rollback()
	exec := func(label string, query string, args ...any) (int64, error) {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, fmt.Errorf("pruning %s: %w", label, err)
		}
		n, err := res.RowsAffected()
		return n, err
	}
	// The terminal-lineage guard every record delete shares (the E9-T2
	// convention of one shared predicate; the epic round-3 F001 remediation
	// removed the last inline duplication).
	terminalLineage := `dispatch_id IN (SELECT dispatch_id FROM dispatch_intents WHERE state IN ` + terminal + `)`
	// The active-slot guard applies to every lineage delete via the
	// shared predicate (E8-T4, H-3/AC-503; E9-T2/T4-F005 shares it
	// between plan and execution).
	if counts.Attempts, err = exec("attempts",
		`DELETE FROM dispatch_attempts WHERE completed_at IS NOT NULL AND completed_at < ? AND `+terminalLineage+notActiveSlotSQL, c.Attempts); err != nil {
		return counts, err
	}
	if counts.Receipts, err = exec("receipts",
		`DELETE FROM dispatch_receipts WHERE received_at < ? AND `+terminalLineage+notActiveSlotSQL, c.CompletedReceipts); err != nil {
		return counts, err
	}
	// A begun receipt is the attribution anchor of an in-flight run and
	// never prunes while its dispatch is unresolved or holds the active
	// slot (FBK-002/FBK-003, E7-T9/M-20): a running dispatch is never in
	// the terminal set, and the active-slot guard covers the rest. On a
	// TERMINAL dispatch past retention the begun receipt prunes with
	// everything else — otherwise its un-cascaded foreign key blocks the
	// intent delete and wedges the whole prune (E9 epic validation
	// round-1 F001).
	if counts.WorkReceipts, err = exec("work receipts",
		`DELETE FROM work_receipts WHERE submitted_at < ? AND `+terminalLineage+notActiveSlotSQL, c.CompletedReceipts); err != nil {
		return counts, err
	}
	// Intents only when nothing remains to orphan: no surviving
	// attempts, receipts, or work receipts, and never the route's
	// active slot.
	if counts.Intents, err = exec("intents",
		`DELETE FROM dispatch_intents WHERE state IN `+terminal+` AND updated_at < ?
		 AND NOT EXISTS (SELECT 1 FROM dispatch_attempts a WHERE a.dispatch_id = dispatch_intents.dispatch_id)
		 AND NOT EXISTS (SELECT 1 FROM dispatch_receipts r WHERE r.dispatch_id = dispatch_intents.dispatch_id)
		 AND NOT EXISTS (SELECT 1 FROM work_receipts w WHERE w.dispatch_id = dispatch_intents.dispatch_id AND w.submitted_at >= ?)
		 AND dispatch_id NOT IN (SELECT active_dispatch_id FROM route_runtime_state WHERE active_dispatch_id IS NOT NULL)`,
		c.Attempts, c.CompletedReceipts); err != nil {
		return counts, err
	}
	if counts.Decisions, err = exec("decisions",
		`DELETE FROM policy_decisions WHERE created_at < ?
		 AND NOT EXISTS (SELECT 1 FROM dispatch_intents i WHERE i.decision_id = policy_decisions.decision_id)
		 AND NOT EXISTS (SELECT 1 FROM policy_decisions newer WHERE newer.supersedes_decision_id = policy_decisions.decision_id)
		 AND NOT EXISTS (SELECT 1 FROM quarantine_items q WHERE q.decision_id = policy_decisions.decision_id)`, c.Attempts); err != nil {
		return counts, err
	}
	if counts.Batches, err = exec("batches",
		`DELETE FROM change_batches WHERE created_at < ?
		 AND NOT EXISTS (SELECT 1 FROM policy_decisions d WHERE d.batch_id = change_batches.batch_id)
		 AND NOT EXISTS (SELECT 1 FROM quarantine_items q WHERE q.batch_id = change_batches.batch_id)`, c.Observations); err != nil {
		return counts, err
	}
	if counts.Observations, err = exec("observations",
		`DELETE FROM source_observations WHERE observed_at < ? AND NOT EXISTS (SELECT 1 FROM batch_observations bo WHERE bo.observation_id = source_observations.observation_id)`, c.Observations); err != nil {
		return counts, err
	}
	if counts.PathFacts, err = exec("path facts",
		`DELETE FROM path_facts WHERE observed_at < ?`, c.Observations); err != nil {
		return counts, err
	}
	if counts.Quarantine, err = exec("quarantine",
		`DELETE FROM quarantine_items WHERE state IN ('released','discarded','superseded') AND resolved_at IS NOT NULL AND resolved_at < ?`, c.ResolvedQuarantine); err != nil {
		return counts, err
	}
	detail, _ := json.Marshal(map[string]any{"actor": actor, "reason": reason, "cutoffs": cutoffs, "counts": counts})
	if _, err := tx.ExecContext(ctx, `INSERT INTO state_transitions (entity_type, entity_id, from_state, to_state, recorded_at, context_json)
		VALUES ('maintenance', 'prune', '', 'pruned', ?, ?)`, now, string(detail)); err != nil {
		return counts, fmt.Errorf("recording prune audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return counts, err
	}
	return counts, nil
}

// HasActiveWork reports whether any dispatch intent is submitting or
// holds an unexpired attempt lease — the vacuum refusal precondition
// (cli-spec §11).
func (s *Store) HasActiveWork(ctx context.Context, now string) (bool, error) {
	var n int
	err := s.QueryRowContext(ctx, `SELECT COUNT(*) FROM dispatch_intents WHERE state IN ('submitting','reconciling') OR (lease_expires_at IS NOT NULL AND lease_expires_at > ?)`, now).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("checking active work: %w", err)
	}
	return n > 0, nil
}

// Vacuum reclaims free database pages (maintenance vacuum; OPS-008
// posture). The caller must first refuse while active work exists.
func (s *Store) Vacuum(ctx context.Context) error {
	if _, err := s.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	return nil
}

// DatabaseBytes returns the current database file size — the status
// size counter (observability-and-operations §5).
func (s *Store) DatabaseBytes() (int64, error) {
	fi, err := os.Stat(s.path)
	if err != nil {
		return 0, fmt.Errorf("stating database: %w", err)
	}
	return fi.Size(), nil
}

// LatestSchemaVersion returns the newest migration version this build
// knows (doctor's migration-state comparison, OPS-005).
func (s *Store) LatestSchemaVersion() int {
	latest := 0
	for _, m := range Migrations {
		if m.Version > latest {
			latest = m.Version
		}
	}
	return latest
}
