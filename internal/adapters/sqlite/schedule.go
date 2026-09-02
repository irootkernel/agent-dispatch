// Managed-schedule timing overrides (E16-T4 round-4 F004, migration
// v20): the durable home of a scheduled-mode schedule's non-default
// `--at HH:MM` choice. The row is keyed by the managed label — the same
// instance/route/configuration-digest identity the plist path derives
// from. The CLI layer owns the read (a bounded unmigrated query shared
// by render/inspect/disable and the posture); this file owns the write.
package sqlite

import (
	"context"
)

// SetScheduleAtOverride upserts the label's timing override; an empty
// `at` clears the row (the schedule returns to the default timing).
func (s *Store) SetScheduleAtOverride(ctx context.Context, label, at string) error {
	if at == "" {
		_, err := s.ExecContext(ctx, `DELETE FROM schedule_at_overrides WHERE label = ?`, label)
		return err
	}
	_, err := s.ExecContext(ctx, `INSERT INTO schedule_at_overrides (label, at, updated_at)
		VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(label) DO UPDATE SET at = excluded.at, updated_at = excluded.updated_at`, label, at)
	return err
}
