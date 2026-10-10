package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
)

// PruneManagementRequests removes at most 256 eligible leaves in one bounded
// transaction. Accepted unresolved requests, live retry references, unresolved
// jobs and young terminal evidence survive regardless of age. Subsequent passes
// may remove newly unreferenced ancestors; underlying jobs remain for ordinary
// retention to prune after their management associations are gone.
func (s *Store) PruneManagementRequests(ctx context.Context, now time.Time, completedRetention time.Duration) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	if now.IsZero() || completedRetention < 0 {
		return 0, fmt.Errorf("invalid management retention time")
	}
	if completedRetention < syncmanagement.MinimumRetentionDays*24*time.Hour {
		completedRetention = syncmanagement.MinimumRetentionDays * 24 * time.Hour
	}
	tx, err := s.beginManagementTx(ctx, "")
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT r.group_id,r.request_id FROM sync_management_requests r WHERE
	 ((r.accepted_at IS NULL AND r.expires_at < ?) OR (r.state='finished' AND r.resolved_at < ?))
	 AND NOT EXISTS (SELECT 1 FROM sync_management_requests child WHERE child.target_request_id=r.request_id)
	 AND NOT EXISTS (SELECT 1 FROM sync_management_jobs a JOIN sync_jobs j ON j.job_id=a.job_id
	  WHERE a.request_id=r.request_id AND (j.retain_until_resolved=1 OR j.resolved_at IS NULL))
	 ORDER BY r.created_at,r.request_id LIMIT 256`, managementTime(now.Add(-syncmanagement.ExpiredRetentionSeconds*time.Second)), managementTime(now.Add(-completedRetention)))
	if err != nil {
		return 0, err
	}
	type candidate struct{ group, id string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.group, &c.id); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	// Validate the bounded selected heads before deleting any evidence. A corrupt
	// timestamp that sorts before the cutoff must not make a request pruneable.
	for _, c := range candidates {
		if _, err := loadManagement(ctx, tx, c.group, c.id); err != nil {
			return 0, err
		}
	}
	for _, c := range candidates {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sync_management_requests WHERE request_id=?`, c.id); err != nil {
			return 0, err
		}
	}
	return int64(len(candidates)), tx.Commit()
}
