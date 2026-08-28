package config

import (
	"fmt"
	"sort"
)

// ResolvedTarget is one destination target resolved across the two
// declared target maps: a Hermes Kanban target from `hermes_targets` or
// a webhook target from `targets` (FAN-011).
type ResolvedTarget struct {
	ID      string
	Hermes  *HermesTarget
	Webhook *Target
}

// IsHermes reports whether the resolved target is a Hermes Kanban
// target.
func (r ResolvedTarget) IsHermes() bool { return r.Hermes != nil }

// IsWebhook reports whether the resolved target is a webhook target.
func (r ResolvedTarget) IsWebhook() bool { return r.Webhook != nil }

// Type renders the target kind for messages and projections.
func (r ResolvedTarget) Type() string {
	switch {
	case r.Hermes != nil:
		return "hermes-kanban"
	case r.Webhook != nil:
		return r.Webhook.Type
	default:
		return ""
	}
}

// ResolveTarget resolves one target ID across hermes_targets and
// targets. The loader rejects an ID declared in both maps, so at most
// one branch can match a valid configuration.
func (c *Config) ResolveTarget(id string) (ResolvedTarget, bool) {
	if c == nil || id == "" {
		return ResolvedTarget{}, false
	}
	if h, ok := c.HermesTargets[id]; ok {
		h := h
		return ResolvedTarget{ID: id, Hermes: &h}, true
	}
	if t, ok := c.Targets[id]; ok {
		t := t
		return ResolvedTarget{ID: id, Webhook: &t}, true
	}
	return ResolvedTarget{}, false
}

// ErrMultiDestination is the fail-closed refusal for dispatching a route
// that declares more than one destination before E12's per-destination
// lanes exist: the single dispatch pipeline must never silently pick one
// lane.
type ErrMultiDestination struct {
	RouteID string
	Count   int
}

func (e *ErrMultiDestination) Error() string {
	return fmt.Sprintf("route %q declares %d destinations; independent destination lanes arrive with E12 — the v0.1.5 dispatch pipeline executes exactly one destination per route (FAN-011 certified path)", e.RouteID, e.Count)
}

// CertifiedDestination returns the one destination the pre-E12 dispatch
// pipeline executes. Configurations may declare more destinations (they
// parse, validate, and join the revision); executing them fails closed
// here until E12-T2 runs per-destination lanes.
func (r Route) CertifiedDestination(routeID string) (Destination, error) {
	if len(r.Destinations) == 0 {
		return Destination{}, fmt.Errorf("route %q declares no destinations", routeID)
	}
	if len(r.Destinations) > 1 {
		return Destination{}, &ErrMultiDestination{RouteID: routeID, Count: len(r.Destinations)}
	}
	return r.Destinations[0], nil
}

// DestinationByID returns the destination with the given ID.
func (r Route) DestinationByID(id string) (Destination, bool) {
	for _, d := range r.Destinations {
		if d.ID == id {
			return d, true
		}
	}
	return Destination{}, false
}

// SortedDestinations returns the route's destinations ordered by ID so
// every projection, revision, and display is order-independent (FAN-012:
// configuration order is never semantic).
func (r Route) SortedDestinations() []Destination {
	out := append([]Destination(nil), r.Destinations...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
