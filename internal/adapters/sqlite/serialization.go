package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Serialization-group state (E15-T1, ADR-0021, CON-011 through
// CON-013): one durable slot row per materialized group plus one member
// row per (route, destination) naming the effective group current
// configuration resolved for that lane. Materialization is the topology
// reconciliation step: it is additive and configuration-driven, rewrites
// no historic identity, and recomputes each group's slot state from the
// live destination lanes. A group with several preserved active children
// reports serialization_conflict with NO representative holder — new
// group work is blocked until allowed existing-work exits leave at most
// one active child, at which point the same transaction hands the slot
// to the sole survivor or opens it.

// ErrSerializationConflict is the typed refusal every new group
// acquisition path returns while a group it would join reports a
// preserved serialization conflict (CON-013): no arbitrary holder is
// selected and existing safe exits remain available.
var ErrSerializationConflict = fmt.Errorf("%w: serialization group reports a preserved conflict", ports.ErrStateNotEligible)

// SerializationGroupMemberInput is one (route, destination) lane's
// effective group as current configuration resolved it.
type SerializationGroupMemberInput struct {
	RouteID       string
	DestinationID string
	GroupID       string
}

// SerializationGroupCollision is one preserved active child of a
// conflicted group.
type SerializationGroupCollision struct {
	RouteID       string `json:"route_id"`
	DestinationID string `json:"destination_id"`
	DispatchID    string `json:"dispatch_id"`
}

// SerializationGroupRow is one group's durable slot state.
type SerializationGroupRow struct {
	GroupID             string
	State               string // OPEN | HELD | CONFLICT
	HolderRouteID       string
	HolderDestinationID string
	HolderDispatchID    string
	Collisions          []SerializationGroupCollision
	MaterializedAt      string
	UpdatedAt           string
	Version             int64
}

// SerializationGroupState is the group slot-state vocabulary (the
// durable serialization_conflict of the contract is state CONFLICT).
const (
	GroupStateOpen     = "OPEN"
	GroupStateHeld     = "HELD"
	GroupStateConflict = "CONFLICT"
)

// MaterializeSerializationGroups reconciles the durable group topology
// with current configuration (E15-T1): the member set is replaced from
// the caller's resolved memberships, every referenced group row is
// created on first sight, and each group's slot state is recomputed
// from the live destination lanes — zero active children opens the
// group, exactly one holds it, and two or more preserved actives report
// serialization_conflict with no holder. State changes append typed
// audit rows inside the same transaction (DUR-011). It returns every
// group currently reporting a conflict.
func (s *Store) MaterializeSerializationGroups(ctx context.Context, members []SerializationGroupMemberInput, actor, now string) ([]SerializationGroupRow, error) {
	now = normalizeTimestamp(now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := s.materializeSerializationGroupsTx(tx, members, actor, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rows, nil
}

// materializeSerializationGroupsTx applies the topology reconciliation
// inside the caller's transaction.
func (s *Store) materializeSerializationGroupsTx(tx *sql.Tx, members []SerializationGroupMemberInput, actor, now string) ([]SerializationGroupRow, error) {
	sorted := append([]SerializationGroupMemberInput(nil), members...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].RouteID != sorted[j].RouteID {
			return sorted[i].RouteID < sorted[j].RouteID
		}
		return sorted[i].DestinationID < sorted[j].DestinationID
	})
	seenGroup := map[string]bool{}
	for _, m := range sorted {
		if m.GroupID == "" || m.RouteID == "" || m.DestinationID == "" {
			return nil, fmt.Errorf("%w: serialization group membership needs group, route, and destination (got %+v)", ports.ErrInvalidFanoutRecord, m)
		}
		seenGroup[m.GroupID] = true
	}
	// Membership is derived state: the full set is replaced from current
	// configuration, so a removed destination stops holding its stale
	// group the moment the topology reconciles.
	if _, err := tx.Exec(`DELETE FROM serialization_group_members`); err != nil {
		return nil, err
	}
	groupIDs := make([]string, 0, len(seenGroup))
	for id := range seenGroup {
		groupIDs = append(groupIDs, id)
	}
	sort.Strings(groupIDs)
	for _, id := range groupIDs {
		res, err := tx.Exec(`INSERT OR IGNORE INTO serialization_groups (group_id, state, conflict_json, materialized_at, updated_at) VALUES (?, ?, '[]', ?, ?)`,
			id, GroupStateOpen, now, now)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			if err := s.AppendTransition(tx, serializationTransitionID(id, "created", 1), "serialization_group", id, "", GroupStateOpen, now,
				auditJSON("reason", "materialized", "actor", actor, "note", "group created by topology reconciliation")); err != nil {
				return nil, err
			}
		}
	}
	for _, m := range sorted {
		if _, err := tx.Exec(`INSERT INTO serialization_group_members (route_id, destination_id, group_id) VALUES (?, ?, ?)`,
			m.RouteID, m.DestinationID, m.GroupID); err != nil {
			return nil, err
		}
	}
	// A group current configuration no longer names keeps its row with a
	// frozen state: the durable version must never restart, or a
	// retire/recreate cycle would reuse an audit transition ID and
	// permanently wedge topology reconciliation on the append-only
	// primary key (round-1 F001). Retired groups disappear from the
	// member-backed load view instead; the audit rows keep the group's
	// full lifecycle (DUR-011).
	// Active holders per group: live lanes holding an active dispatch
	// under a member destination. Lanes without a member row (the
	// synthetic legacy lane of pre-cutover work) coordinate outside the
	// group topology exactly as before.
	type holder struct {
		route, destination, dispatch string
	}
	holders := map[string][]holder{}
	rows, err := tx.Query(`SELECT m.group_id, l.route_id, l.destination_id, l.active_dispatch_id
		FROM destination_lane_state l
		JOIN serialization_group_members m ON m.route_id = l.route_id AND m.destination_id = l.destination_id
		WHERE l.lane_state IN ('ACTIVE_CLEAN','ACTIVE_DIRTY') AND l.active_dispatch_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var groupID, routeID, destinationID, dispatchID string
		if err := rows.Scan(&groupID, &routeID, &destinationID, &dispatchID); err != nil {
			rows.Close()
			return nil, err
		}
		holders[groupID] = append(holders[groupID], holder{route: routeID, destination: destinationID, dispatch: dispatchID})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var conflicts []SerializationGroupRow
	for _, id := range groupIDs {
		var (
			state        string
			holderRoute  sql.NullString
			holderDest   sql.NullString
			holderDis    sql.NullString
			conflictJSON string
			materialized string
			version      int64
		)
		if err := tx.QueryRow(`SELECT state, holder_route_id, holder_destination_id, holder_dispatch_id, conflict_json, materialized_at, version
			FROM serialization_groups WHERE group_id = ?`, id).Scan(&state, &holderRoute, &holderDest, &holderDis, &conflictJSON, &materialized, &version); err != nil {
			return nil, err
		}
		active := holders[id]
		sort.Slice(active, func(i, j int) bool {
			if active[i].route != active[j].route {
				return active[i].route < active[j].route
			}
			return active[i].destination < active[j].destination
		})
		next := GroupStateOpen
		nextRoute, nextDest, nextDispatch := "", "", ""
		var collisions []SerializationGroupCollision
		switch {
		case len(active) == 1:
			next = GroupStateHeld
			nextRoute, nextDest, nextDispatch = active[0].route, active[0].destination, active[0].dispatch
		case len(active) >= 2:
			// A preserved migration-time collision (CON-013): every
			// historical identity stays intact and NO representative
			// holder is selected — the group blocks until allowed
			// existing-work exits leave at most one active child.
			next = GroupStateConflict
			for _, h := range active {
				collisions = append(collisions, SerializationGroupCollision{RouteID: h.route, DestinationID: h.destination, DispatchID: h.dispatch})
			}
		}
		raw, err := json.Marshal(collisions)
		if err != nil {
			return nil, err
		}
		row := SerializationGroupRow{
			GroupID: id, State: next, HolderRouteID: nextRoute, HolderDestinationID: nextDest,
			HolderDispatchID: nextDispatch, Collisions: collisions, MaterializedAt: materialized,
			UpdatedAt: now, Version: version + 1,
		}
		if state != next || nullText(holderRoute) != nextRoute || nullText(holderDest) != nextDest ||
			nullText(holderDis) != nextDispatch || conflictJSON != string(raw) {
			if _, err := tx.Exec(`UPDATE serialization_groups
				SET state = ?, holder_route_id = NULLIF(?, ''), holder_destination_id = NULLIF(?, ''), holder_dispatch_id = NULLIF(?, ''),
				    conflict_json = ?, updated_at = ?, version = version + 1
				WHERE group_id = ?`,
				next, nextRoute, nextDest, nextDispatch, string(raw), now, id); err != nil {
				return nil, err
			}
			reason := "topology-unchanged"
			switch {
			case next == GroupStateConflict:
				reason = "serialization_conflict"
			case next == GroupStateHeld && state == GroupStateConflict:
				reason = "conflict-resolved-sole-holder"
			case next == GroupStateHeld:
				reason = "slot-held"
			case next == GroupStateOpen && state == GroupStateConflict:
				reason = "conflict-resolved-open"
			case next == GroupStateOpen && state == GroupStateHeld:
				reason = "slot-released"
			}
			transitionCtx := auditJSON("reason", reason, "actor", actor, "state", next,
				"holder_route_id", nextRoute, "holder_destination_id", nextDest, "holder_dispatch_id", nextDispatch,
				"collisions", len(collisions))
			if err := s.AppendTransition(tx, serializationTransitionID(id, reason, version+1), "serialization_group", id, state, next, now, transitionCtx); err != nil {
				return nil, err
			}
		}
		if next == GroupStateConflict {
			conflicts = append(conflicts, row)
		}
	}
	return conflicts, nil
}

// serializationTransitionID builds the deterministic audit transition ID
// for one group state change: the group, the reason, and the row's new
// version keep the append-only primary key unique across repeated
// same-reason transitions (the same shape route transitions use).
func serializationTransitionID(groupID, reason string, version int64) string {
	return fmt.Sprintf("serialization-group:%s:%s:v%d", groupID, reason, version)
}

// LoadSerializationGroups returns every member-backed group's durable
// slot state sorted by group ID (read-only inspection for status,
// preflight, and doctor surfaces). A group whose membership retired
// keeps its frozen row for version continuity but leaves this view —
// current configuration no longer names it.
func (s *Store) LoadSerializationGroups(ctx context.Context) ([]SerializationGroupRow, error) {
	rows, err := s.QueryContext(ctx, `SELECT g.group_id, g.state, COALESCE(g.holder_route_id, ''), COALESCE(g.holder_destination_id, ''), COALESCE(g.holder_dispatch_id, ''), g.conflict_json, g.materialized_at, g.updated_at, g.version
		FROM serialization_groups g
		WHERE EXISTS (SELECT 1 FROM serialization_group_members m WHERE m.group_id = g.group_id)
		ORDER BY g.group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SerializationGroupRow{}
	for rows.Next() {
		var row SerializationGroupRow
		var conflictJSON string
		if err := rows.Scan(&row.GroupID, &row.State, &row.HolderRouteID, &row.HolderDestinationID, &row.HolderDispatchID, &conflictJSON, &row.MaterializedAt, &row.UpdatedAt, &row.Version); err != nil {
			return nil, err
		}
		if conflictJSON != "" && conflictJSON != "[]" {
			if err := json.Unmarshal([]byte(conflictJSON), &row.Collisions); err != nil {
				return nil, fmt.Errorf("serialization group %s conflict evidence is not valid JSON: %w", row.GroupID, err)
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// SerializationGroupOf reports the effective group one (route,
// destination) lane currently belongs to; ok is false before the first
// topology reconciliation materializes membership.
func (s *Store) SerializationGroupOf(ctx context.Context, routeID, destinationID string) (string, bool, error) {
	var groupID string
	err := s.QueryRowContext(ctx, `SELECT group_id FROM serialization_group_members WHERE route_id = ? AND destination_id = ?`, routeID, destinationID).Scan(&groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return groupID, true, nil
}

// E15-T3 group-slot enforcement (ADR-0021, CON-011 through CON-012):
// the serialization_groups row is the ONE durable slot. Acquisition is
// transactional and fail-closed on preserved conflicts; a held group
// refuses every other lane; release promotes at most the oldest
// first-dirty waiting lane of the same group with destination ID as
// the deterministic tie break; a rerun transfers the slot atomically
// with the lane takeover.

// ErrGroupSlotHeld is defined in ports (ports.ErrGroupSlotHeld): the
// typed refusal an activation receives when another lane's child holds
// the effective group. The caller merges the arrival into its lane's
// dirty generation (CON-012) exactly as a lane-slot race does.

// GroupSlotFree reports whether one lane may activate a child under
// its effective group right now (E15-T3): true when the lane has no
// group membership (pre-materialization or legacy work — no slot
// semantics apply) or the group is OPEN, held by the lane's own
// dispatch, or reserved for this lane by a promotion; false when
// another lane holds it or the group reports a preserved conflict.
// reason names the refusing holder for the operator surface.
func (s *Store) GroupSlotFree(ctx context.Context, routeID, destinationID string) (bool, string, error) {
	var maxVersion sql.NullInt64
	if err := s.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxVersion); err != nil {
		return false, "", err
	}
	if !maxVersion.Valid || maxVersion.Int64 < groupSchemaVersion {
		return true, "", nil
	}
	var (
		state       string
		holderRoute sql.NullString
		holderDest  sql.NullString
		holderDis   sql.NullString
	)
	err := s.QueryRowContext(ctx, `SELECT g.state, g.holder_route_id, g.holder_destination_id, g.holder_dispatch_id
		FROM serialization_groups g
		JOIN serialization_group_members m ON m.group_id = g.group_id
		WHERE m.route_id = ? AND m.destination_id = ?`, routeID, destinationID).
		Scan(&state, &holderRoute, &holderDest, &holderDis)
	if errors.Is(err, sql.ErrNoRows) {
		return true, "", nil
	}
	if err != nil {
		return false, "", err
	}
	switch {
	case state == GroupStateOpen:
		return true, "", nil
	case state == GroupStateConflict:
		return false, "the group reports a preserved serialization conflict", nil
	case nullText(holderRoute) == routeID && nullText(holderDest) == destinationID:
		// Held (or promoted-reserved) by this lane's own lane identity.
		return true, "", nil
	default:
		return false, fmt.Sprintf("held by %s/%s (%s)", nullText(holderRoute), nullText(holderDest), nullText(holderDis)), nil
	}
}

// groupSchemaVersion is the migration that introduced the group
// tables: a database below it has no slot semantics at all (the
// harness's pre-upgrade shapes and any crash-window state before the
// v18 unit applies), so acquisitions and releases there are no-ops
// rather than schema errors.
const groupSchemaVersion = 18

// groupOfLaneTx resolves one lane's group inside a transaction; ok is
// false for lanes without membership (no slot semantics).
func groupOfLaneTx(tx *sql.Tx, routeID, destinationID string) (string, bool, error) {
	var maxVersion sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&maxVersion); err != nil {
		return "", false, err
	}
	if !maxVersion.Valid || maxVersion.Int64 < groupSchemaVersion {
		return "", false, nil
	}
	var groupID string
	err := tx.QueryRow(`SELECT group_id FROM serialization_group_members WHERE route_id = ? AND destination_id = ?`,
		routeID, destinationID).Scan(&groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return groupID, true, nil
}

// acquireGroupSlotTx takes the group slot for one dispatch inside the
// caller's activation transaction (CON-011): a preserved conflict
// refuses, another lane's hold refuses with ErrGroupSlotHeld (the
// caller merges instead), and this lane's own hold or a promotion
// reserved for this lane passes. Idempotent for the holding dispatch.
func (s *Store) acquireGroupSlotTx(tx *sql.Tx, routeID, destinationID, dispatchID, actor, now string) error {
	groupID, ok, err := groupOfLaneTx(tx, routeID, destinationID)
	if err != nil || !ok {
		return err
	}
	var (
		state        string
		holderRoute  sql.NullString
		holderDest   sql.NullString
		holderDis    sql.NullString
		conflictJSON string
		version      int64
	)
	if err := tx.QueryRow(`SELECT state, holder_route_id, holder_destination_id, holder_dispatch_id, conflict_json, version
		FROM serialization_groups WHERE group_id = ?`, groupID).
		Scan(&state, &holderRoute, &holderDest, &holderDis, &conflictJSON, &version); err != nil {
		return err
	}
	switch {
	case state == GroupStateOpen:
	case state == GroupStateConflict:
		return fmt.Errorf("%w (group %s: %s)", ErrSerializationConflict, groupID, boundedCollisions(conflictJSON))
	case nullText(holderDis) == dispatchID:
		return nil
	case nullText(holderRoute) == routeID && nullText(holderDest) == destinationID:
		// A promotion reserved the slot for exactly this lane: the next
		// activation of this lane consumes the reservation.
	default:
		return fmt.Errorf("%w: group %s is held by %s/%s (%s)", ports.ErrGroupSlotHeld, groupID, nullText(holderRoute), nullText(holderDest), nullText(holderDis))
	}
	// The acquisition write is CONDITIONAL on the state this
	// transaction read (optimistic concurrency): a racing activation
	// that committed between the read and this write leaves
	// RowsAffected at zero — the loser then re-reads and reports the
	// typed refusal instead of overwriting the winner's hold.
	// The write succeeds only from the states this transaction's read
	// authorized: OPEN, or HELD by THIS lane's identity (the self-hold
	// idempotence and the consumed promotion reservation). A group held
	// by any other lane — including one acquired between the read and
	// this write — fails the WHERE and reports the typed refusal.
	res, err := tx.Exec(`UPDATE serialization_groups
		SET state = ?, holder_route_id = ?, holder_destination_id = ?, holder_dispatch_id = ?, conflict_json = '[]', updated_at = ?, version = version + 1
		WHERE group_id = ? AND (state = ? OR (state = ? AND holder_route_id = ? AND holder_destination_id = ?))`,
		GroupStateHeld, routeID, destinationID, dispatchID, now, groupID, GroupStateOpen, GroupStateHeld, routeID, destinationID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: group %s was taken concurrently", ports.ErrGroupSlotHeld, groupID)
	}
	return s.AppendTransition(tx, serializationTransitionID(groupID, "slot-acquired", version+1), "serialization_group", groupID,
		state, GroupStateHeld, now, auditJSON("reason", "slot-acquired", "actor", actor,
			"route_id", routeID, "destination_id", destinationID, "dispatch_id", dispatchID))
}

// releaseGroupSlotTx releases the slot of one completing dispatch and
// promotes at most the oldest first-dirty waiting lane of the same
// group (CON-012): the reservation (holder route/destination without a
// dispatch) hands the next activation to the promoted lane
// deterministically — oldest dirty_since first, destination ID then
// route ID as the tie breaks — and a group without a waiting lane
// opens. The completing lane's own follow-up competes for the slot
// like any other lane; promotion never picks the releaser itself.
func (s *Store) releaseGroupSlotTx(tx *sql.Tx, routeID, destinationID, dispatchID, actor, now string) error {
	groupID, ok, err := groupOfLaneTx(tx, routeID, destinationID)
	if err != nil || !ok {
		return err
	}
	var (
		state       string
		holderDis   sql.NullString
		holderRoute sql.NullString
		holderDest  sql.NullString
		version     int64
	)
	if err := tx.QueryRow(`SELECT state, holder_route_id, holder_destination_id, holder_dispatch_id, version
		FROM serialization_groups WHERE group_id = ?`, groupID).
		Scan(&state, &holderRoute, &holderDest, &holderDis, &version); err != nil {
		return err
	}
	if state != GroupStateHeld || (nullText(holderDis) != dispatchID && !(nullText(holderRoute) == routeID && nullText(holderDest) == destinationID)) {
		// The releaser does not hold the slot (a promotion already moved
		// it, or the group is not held): nothing to release.
		return nil
	}
	// The waiting lanes of this group: member lanes other than the
	// releaser that hold dirty work or a ready follow-up, oldest
	// first-dirty first with the deterministic tie breaks.
	var (
		nextRoute string
		nextDest  string
	)
	row := tx.QueryRow(`SELECT l.route_id, l.destination_id
		FROM destination_lane_state l
		JOIN serialization_group_members m ON m.route_id = l.route_id AND m.destination_id = l.destination_id
		WHERE m.group_id = ? AND NOT (l.route_id = ? AND l.destination_id = ?)
		  AND (l.dirty_generation > 0 OR l.lane_state = 'FOLLOWUP_READY')
		ORDER BY l.dirty_since IS NULL, l.dirty_since, l.destination_id, l.route_id
		LIMIT 1`, groupID, routeID, destinationID)
	if err := row.Scan(&nextRoute, &nextDest); errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.Exec(`UPDATE serialization_groups
			SET state = ?, holder_route_id = NULL, holder_destination_id = NULL, holder_dispatch_id = NULL, updated_at = ?, version = version + 1
			WHERE group_id = ? AND state = ? AND holder_dispatch_id = ?`, GroupStateOpen, now, groupID, GroupStateHeld, dispatchID); err != nil {
			return err
		}
		return s.AppendTransition(tx, serializationTransitionID(groupID, "slot-released", version+1), "serialization_group", groupID,
			GroupStateHeld, GroupStateOpen, now, auditJSON("reason", "slot-released", "actor", actor,
				"route_id", routeID, "destination_id", destinationID, "dispatch_id", dispatchID))
	} else if err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE serialization_groups
		SET state = ?, holder_route_id = ?, holder_destination_id = ?, holder_dispatch_id = NULL, updated_at = ?, version = version + 1
		WHERE group_id = ? AND state = ? AND holder_dispatch_id = ?`, GroupStateHeld, nextRoute, nextDest, now, groupID, GroupStateHeld, dispatchID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// The releaser no longer holds the slot (a concurrent transfer
		// or resolution moved it): the promotion is silently skipped —
		// the durable holder already names the winner.
		return nil
	}
	return s.AppendTransition(tx, serializationTransitionID(groupID, "slot-promoted", version+1), "serialization_group", groupID,
		GroupStateHeld, GroupStateHeld, now, auditJSON("reason", "slot-promoted", "actor", actor,
			"route_id", routeID, "destination_id", destinationID, "dispatch_id", dispatchID,
			"promoted_route_id", nextRoute, "promoted_destination_id", nextDest))
}

// transferGroupSlotTx moves the slot of one superseded dispatch to its
// replacement inside the rerun takeover transaction (CON-012: a rerun
// TRANSFERS the slot atomically — never releases-then-races): the
// original's own hold moves to the new dispatch, a conflict refuses
// the rerun, and any other state (open, another lane's hold) leaves
// the new dispatch to acquire through its own activation.
func (s *Store) transferGroupSlotTx(tx *sql.Tx, routeID, destinationID, originalDispatchID, newDispatchID, actor, now string) error {
	groupID, ok, err := groupOfLaneTx(tx, routeID, destinationID)
	if err != nil || !ok {
		return err
	}
	var (
		state       string
		holderDis   sql.NullString
		holderRoute sql.NullString
		holderDest  sql.NullString
		version     int64
	)
	if err := tx.QueryRow(`SELECT state, holder_route_id, holder_destination_id, holder_dispatch_id, version
		FROM serialization_groups WHERE group_id = ?`, groupID).
		Scan(&state, &holderRoute, &holderDest, &holderDis, &version); err != nil {
		return err
	}
	switch {
	case state == GroupStateConflict:
		return fmt.Errorf("%w (group %s)", ErrSerializationConflict, groupID)
	case state == GroupStateHeld && nullText(holderDis) == originalDispatchID:
		res, err := tx.Exec(`UPDATE serialization_groups
			SET holder_dispatch_id = ?, updated_at = ?, version = version + 1
			WHERE group_id = ? AND state = ? AND holder_dispatch_id = ?`, newDispatchID, now, groupID, GroupStateHeld, originalDispatchID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return s.AppendTransition(tx, serializationTransitionID(groupID, "slot-transferred-rerun", version+1), "serialization_group", groupID,
			GroupStateHeld, GroupStateHeld, now, auditJSON("reason", "slot-transferred-rerun", "actor", actor,
				"route_id", routeID, "destination_id", destinationID,
				"from_dispatch_id", originalDispatchID, "dispatch_id", newDispatchID))
	}
	return nil
}

// boundedCollisions renders a conflict row's collision evidence for an
// operator error without echoing unbounded stored JSON.
func boundedCollisions(conflictJSON string) string {
	if len(conflictJSON) > 200 {
		return conflictJSON[:200] + "..."
	}
	if conflictJSON == "" || conflictJSON == "[]" {
		return "preserved active collision"
	}
	return conflictJSON
}
