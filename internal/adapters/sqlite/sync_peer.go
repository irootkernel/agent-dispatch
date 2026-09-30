package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PeerNudgeInput is an already authenticated, bounded request. Fingerprint
// identifies the exact request bytes; the store retains the original payload.
type PeerNudgeInput struct {
	GroupID, PublicationID, Fingerprint, PayloadJSON, ReceivedAt string
	QueueLimit                                                   int
}

// PeerNudgeRow is one durable inbox obligation. Sequence orders recovery even
// when peers supply the same timestamp or requests arrive out of order.
type PeerNudgeRow struct {
	Sequence                                                     int64
	GroupID, PublicationID, Fingerprint, PayloadJSON, ReceivedAt string
}

type PeerNudgeBacklog struct {
	Pending         int
	Failed          int
	Retained        int
	OldestPendingAt string
	OldestReason    string
}

// Processed identities remain durable for exact replay detection. Once this
// bound is reached, new publications are refused and the sender retains its
// delivery obligation rather than silently deleting idempotency evidence.
const PeerNudgeLedgerLimit = 100_000

// AdmitPeerNudge returns duplicate only for the same logical identity and
// exact fingerprint/payload. A new admission is acknowledged after commit.
func (s *Store) AdmitPeerNudge(ctx context.Context, in PeerNudgeInput) (duplicate bool, err error) {
	return s.admitPeerNudge(ctx, in, PeerNudgeLedgerLimit)
}

func (s *Store) admitPeerNudge(ctx context.Context, in PeerNudgeInput, ledgerLimit int) (duplicate bool, err error) {
	if err := validatePeerNudge(in); err != nil {
		return false, err
	}
	if ledgerLimit < 1 || ledgerLimit > PeerNudgeLedgerLimit {
		return false, fmt.Errorf("invalid peer nudge ledger limit")
	}
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		duplicate, err = s.admitPeerNudgeOnce(ctx, in, ledgerLimit)
		if err == nil || !isBusy(err) {
			return duplicate, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return false, &syncBusyError{err: lastErr}
}

func validatePeerNudge(in PeerNudgeInput) error {
	if in.GroupID == "" || len(in.GroupID) > 256 || in.PublicationID == "" || len(in.PublicationID) > 256 ||
		in.Fingerprint == "" || len(in.Fingerprint) > 128 {
		return fmt.Errorf("invalid peer nudge identity or fingerprint")
	}
	if in.QueueLimit < 2 || in.QueueLimit > 1000 {
		return fmt.Errorf("peer nudge queue limit must be 2..1000")
	}
	if len(in.PayloadJSON) == 0 || len(in.PayloadJSON) > 16384 || !json.Valid([]byte(in.PayloadJSON)) {
		return fmt.Errorf("peer nudge payload must be valid JSON of at most 16384 bytes")
	}
	if len(in.ReceivedAt) == 0 || len(in.ReceivedAt) > 64 {
		return fmt.Errorf("invalid peer nudge received timestamp")
	}
	if _, err := time.Parse(time.RFC3339Nano, in.ReceivedAt); err != nil {
		return fmt.Errorf("invalid peer nudge received timestamp: %w", err)
	}
	return nil
}

func (s *Store) admitPeerNudgeOnce(ctx context.Context, in PeerNudgeInput, ledgerLimit int) (bool, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var fingerprint, payload string
	err = tx.QueryRowContext(ctx, `SELECT request_fingerprint,payload_json FROM sync_peer_nudges
		WHERE group_id=? AND publication_id=?`, in.GroupID, in.PublicationID).Scan(&fingerprint, &payload)
	if err == nil {
		if fingerprint != in.Fingerprint || payload != in.PayloadJSON {
			return false, fmt.Errorf("peer publication %q already has different request bytes: %w", in.PublicationID, ErrSyncAdmissionConflict)
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	pending, err := countUnresolvedSyncObligations(ctx, tx, in.GroupID)
	if err != nil {
		return false, err
	}
	if pending >= in.QueueLimit {
		return false, fmt.Errorf("peer group %q has %d unresolved obligations (limit %d): %w", in.GroupID, pending, in.QueueLimit, ErrSyncQueueFull)
	}
	// Keep a pure nudge inbox from consuming the slot that reconciliation
	// needs to admit its import. Existing jobs still share the aggregate bound.
	var pendingNudges int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_peer_nudges
		WHERE group_id=? AND processed_at IS NULL`, in.GroupID).Scan(&pendingNudges); err != nil {
		return false, err
	}
	if pendingNudges >= in.QueueLimit-1 {
		return false, fmt.Errorf("peer group %q has %d pending nudges; one work slot is reserved (limit %d): %w", in.GroupID, pendingNudges, in.QueueLimit, ErrSyncQueueFull)
	}
	var retained int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_peer_nudges WHERE group_id=?`, in.GroupID).Scan(&retained); err != nil {
		return false, err
	}
	if retained >= ledgerLimit {
		return false, fmt.Errorf("peer group %q has %d retained nudges (limit %d): %w", in.GroupID, retained, ledgerLimit, ErrSyncQueueFull)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_peer_nudges
		(group_id,publication_id,request_fingerprint,payload_json,received_at)
		VALUES (?,?,?,?,?)`, in.GroupID, in.PublicationID, in.Fingerprint, in.PayloadJSON, in.ReceivedAt); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

// LoadPendingPeerNudges returns at most limit unprocessed rows in admission
// order. A worker must durably record its downstream obligation before marking
// the corresponding row processed.
func (s *Store) LoadPendingPeerNudges(ctx context.Context, groupID string, limit int) ([]PeerNudgeRow, error) {
	if groupID == "" || len(groupID) > 256 || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid peer nudge query group or limit")
	}
	rows, err := s.QueryContext(ctx, `SELECT sequence,group_id,publication_id,request_fingerprint,payload_json,received_at
		FROM sync_peer_nudges WHERE group_id=? AND processed_at IS NULL ORDER BY sequence LIMIT ?`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pending []PeerNudgeRow
	for rows.Next() {
		var row PeerNudgeRow
		if err := rows.Scan(&row.Sequence, &row.GroupID, &row.PublicationID, &row.Fingerprint, &row.PayloadJSON, &row.ReceivedAt); err != nil {
			return nil, err
		}
		pending = append(pending, row)
	}
	return pending, rows.Err()
}

// MarkPeerNudgeProcessed conditionally closes one exact obligation. A stale
// fingerprint, missing row, or repeat completion is a failed precondition.
func (s *Store) MarkPeerNudgeProcessed(ctx context.Context, groupID, publicationID, fingerprint, processedAt string) error {
	return s.ResolvePeerNudge(ctx, groupID, publicationID, fingerprint, "covered", processedAt)
}

// ResolvePeerNudge records whether a verified reconcile covered the peer's
// target or only the current approved remote head. Neither outcome asserts
// that HTTP admission itself completed an import.
func (s *Store) ResolvePeerNudge(ctx context.Context, groupID, publicationID, fingerprint, resolution, processedAt string) error {
	if groupID == "" || publicationID == "" || fingerprint == "" || len(processedAt) == 0 || len(processedAt) > 64 {
		return fmt.Errorf("invalid peer nudge completion identity or timestamp")
	}
	if resolution != "covered" && resolution != "superseded" && resolution != "invalid_payload" && resolution != "obsolete_binding" {
		return fmt.Errorf("invalid peer nudge resolution")
	}
	if _, err := time.Parse(time.RFC3339Nano, processedAt); err != nil {
		return fmt.Errorf("invalid peer nudge completion timestamp: %w", err)
	}
	result, err := s.ExecContext(ctx, `UPDATE sync_peer_nudges SET processed_at=?,resolution=?,last_reason=''
		WHERE group_id=? AND publication_id=? AND request_fingerprint=? AND processed_at IS NULL`,
		processedAt, resolution, groupID, publicationID, fingerprint)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrSyncPrecondition
	}
	return nil
}

func (s *Store) RecordPeerNudgeFailure(ctx context.Context, groupID, publicationID, fingerprint, reason string) error {
	switch reason {
	case "reconcile_failed", "local_ref_unavailable", "coverage_unavailable", "store_payload_invalid":
	default:
		return fmt.Errorf("invalid peer nudge failure reason")
	}
	result, err := s.ExecContext(ctx, `UPDATE sync_peer_nudges SET last_reason=?
		WHERE group_id=? AND publication_id=? AND request_fingerprint=? AND processed_at IS NULL`,
		reason, groupID, publicationID, fingerprint)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrSyncPrecondition
	}
	return nil
}

func (s *Store) LoadPeerNudgeBacklog(ctx context.Context, groupID string) (PeerNudgeBacklog, error) {
	var result PeerNudgeBacklog
	if groupID == "" || len(groupID) > 256 {
		return result, fmt.Errorf("invalid peer nudge group")
	}
	var oldest, reason sql.NullString
	err := s.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN last_reason!='' THEN 1 ELSE 0 END),0),
		(SELECT COUNT(*) FROM sync_peer_nudges WHERE group_id=?),
		(SELECT received_at FROM sync_peer_nudges WHERE group_id=? AND processed_at IS NULL ORDER BY sequence LIMIT 1),
		(SELECT last_reason FROM sync_peer_nudges WHERE group_id=? AND processed_at IS NULL ORDER BY sequence LIMIT 1)
		FROM sync_peer_nudges WHERE group_id=? AND processed_at IS NULL`, groupID, groupID, groupID, groupID).Scan(&result.Pending, &result.Failed, &result.Retained, &oldest, &reason)
	if err != nil {
		return result, err
	}
	if oldest.Valid {
		result.OldestPendingAt = oldest.String
	}
	if reason.Valid {
		result.OldestReason = reason.String
	}
	return result, nil
}
