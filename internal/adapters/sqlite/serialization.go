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
