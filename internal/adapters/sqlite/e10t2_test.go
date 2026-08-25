package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
)

// TestE10T2WatchBindingPersistence pins SRC-009's durable record: the
// managed binding upserts per route, loads back with its four distinct
// values, and an absent binding reports the typed not-found sentinel.
func TestE10T2WatchBindingPersistence(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.LoadWatchBinding(ctx, "wiki-maintenance"); !errors.Is(err, ErrWatchBindingNotFound) {
		t.Fatalf("an absent binding must report the typed sentinel, got %v", err)
	}
	first := watchman.Binding{
		RouteID: "wiki-maintenance", ResourceID: "vault-main",
		ConfiguredRoot: "/srv/vault/workspace/vault", ActualRoot: "/srv/vault",
		RelativeRoot: "workspace/vault", TriggerName: "agent-dispatch.wiki",
		UpdatedAt: "2026-08-26T00:00:00Z",
	}
	if err := s.SaveWatchBinding(ctx, first); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadWatchBinding(ctx, "wiki-maintenance")
	if err != nil || loaded != first {
		t.Fatalf("the binding must round-trip: %+v %v", loaded, err)
	}
	// A reinstall over a changed topology replaces the record whole.
	second := first
	second.ActualRoot = "/srv/vault/workspace/vault"
	second.RelativeRoot = "."
	second.UpdatedAt = "2026-08-26T00:01:00Z"
	if err := s.SaveWatchBinding(ctx, second); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.LoadWatchBinding(ctx, "wiki-maintenance")
	if err != nil || loaded != second {
		t.Fatalf("the binding must replace atomically per route: %+v %v", loaded, err)
	}
	var count int
	if err := s.QueryRow(`SELECT COUNT(*) FROM watch_bindings`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("one route holds exactly one binding: %d %v", count, err)
	}
}
