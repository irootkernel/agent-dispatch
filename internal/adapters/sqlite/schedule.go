// Managed-schedule timing overrides (E16-T4 round-4 F004, migration
// v20): the durable home of a scheduled-mode schedule's non-default
// `--at HH:MM` choice. The row is keyed by the managed label — the same
// instance/route/configuration-digest identity the plist path derives
// from — and nothing else reads or writes it besides the schedule
// lifecycle.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
)

// ErrNoScheduleAtOverride reports that no override row exists for the
// label (the caller renders the 03:00 default).
var ErrNoScheduleAtOverride = errors.New("no schedule at override")

// ScheduleAtOverride returns the stored `--at HH:MM` override of one
// managed label, or ErrNoScheduleAtOverride when the label runs the
// default timing.
func (s *Store) ScheduleAtOverride(ctx context.Context, label string) (string, error) {
	var at string
	err := s.QueryRowContext(ctx, `SELECT at FROM schedule_at_overrides WHERE label = ?`, label).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoScheduleAtOverride
	}
	if err != nil {
		return "", err
	}
	return at, nil
}

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
