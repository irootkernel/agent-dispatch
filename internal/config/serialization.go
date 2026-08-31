package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Serialization-group resolution and topology (E15-T1, ADR-0021,
// CON-011 through CON-014): every destination resolves one effective
// Agent Dispatch serialization group from an explicit
// `serialization_group`, the deprecated `mutex_key` alias, or the exact
// resource-derived default. Explicit values use one bounded ASCII
// grammar with no case folding or Unicode normalization; the group is
// global within one state database and at most one active child may
// hold it.

// SerializationGroupDefaultPrefix heads the resource-derived default
// group: exactly `resource:<resource_id>` where the resource ID is the
// unchanged `resources` map key (CON-011).
const SerializationGroupDefaultPrefix = "resource:"

// MaxSerializationGroupBytes bounds one explicit group value: 1 through
// 255 ASCII bytes (ADR-0021).
const MaxSerializationGroupBytes = 255

// serializationGroupPattern is the explicit-group grammar: an ASCII
// alphanumeric first byte, then ASCII alphanumerics, dots, colons,
// slashes, and hyphens. The class is ASCII-only, so a match can never
// contain a multi-byte character; no case folding or Unicode
// normalization is applied anywhere in resolution.
var serializationGroupPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)

// ValidateSerializationGroupValue enforces the explicit-group grammar
// and byte bound. The value is used verbatim — no trimming, folding, or
// normalization — so an empty, oversized, or non-grammar value is an
// error, never a repair.
func ValidateSerializationGroupValue(value string) error {
	if value == "" {
		return fmt.Errorf("serialization group must not be empty")
	}
	if len(value) > MaxSerializationGroupBytes {
		return fmt.Errorf("serialization group %q exceeds %d ASCII bytes (got %d bytes)", value, MaxSerializationGroupBytes, len(value))
	}
	if !serializationGroupPattern.MatchString(value) {
		return fmt.Errorf("serialization group %q must match ^[A-Za-z0-9][A-Za-z0-9._:/-]*$ (1-%d ASCII bytes, no case folding or Unicode normalization)", value, MaxSerializationGroupBytes)
	}
	return nil
}

// EffectiveSerializationGroup resolves one destination's effective
// serialization group (CON-011): the explicit `serialization_group`,
// else the deprecated `mutex_key` alias, else exactly
// `resource:<resource_id>`. An explicit value in the default form
// intentionally joins the resource-derived group — the resolved identity
// is the same string either way.
func EffectiveSerializationGroup(resourceID string, dest Destination) string {
	switch {
	case dest.SerializationGroup != "":
		return dest.SerializationGroup
	case dest.MutexKey != "":
		return dest.MutexKey
	default:
		return SerializationGroupDefaultPrefix + resourceID
	}
}

// SerializationGroupSource names how one destination's effective group
// was resolved, for operator surfaces that must not present an
// explicit policy and the silent default as the same fact.
type SerializationGroupSource string

const (
	// SerializationGroupExplicit is the explicit serialization_group
	// field.
	SerializationGroupExplicit SerializationGroupSource = "explicit"
	// SerializationGroupDeprecatedAlias is the mutex_key alias.
	SerializationGroupDeprecatedAlias SerializationGroupSource = "deprecated_alias"
	// SerializationGroupResourceDefault is the resource-derived default.
	SerializationGroupResourceDefault SerializationGroupSource = "resource_default"
)

// SerializationGroupResolution is one destination's resolved group with
// its provenance.
type SerializationGroupResolution struct {
	RouteID        string
	DestinationID  string
	ResourceID     string
	Group          string
	Source         SerializationGroupSource
	UsesAliasValue bool
}

// ResolveSerializationGroup resolves one (route, destination) pair.
func ResolveSerializationGroup(routeID string, route Route, dest Destination) SerializationGroupResolution {
	resourceID := route.Source.Resource
	res := SerializationGroupResolution{
		RouteID:       routeID,
		DestinationID: dest.ID,
		ResourceID:    resourceID,
	}
	switch {
	case dest.SerializationGroup != "":
		res.Group = dest.SerializationGroup
		res.Source = SerializationGroupExplicit
		// An explicit default-form value joins the resource group by
		// identity; the operator declared it, so the source stays
		// explicit while the group equals the default form.
		res.UsesAliasValue = false
	case dest.MutexKey != "":
		res.Group = dest.MutexKey
		res.Source = SerializationGroupDeprecatedAlias
		res.UsesAliasValue = true
	default:
		res.Group = SerializationGroupDefaultPrefix + resourceID
		res.Source = SerializationGroupResourceDefault
	}
	return res
}

// SerializationGroupMemberships resolves every configured destination's
// effective group across all routes (CON-011), sorted by route and
// destination for deterministic consumption. A webhook destination
// carries no explicit group fields (they are refused at validation), so
// it always joins its resource's default group.
func SerializationGroupMemberships(cfg *Config) []SerializationGroupResolution {
	out := make([]SerializationGroupResolution, 0, len(cfg.Routes))
	for _, routeID := range cfg.SortedRouteIDs() {
		route := cfg.Routes[routeID]
		for _, dest := range route.SortedDestinations() {
			out = append(out, ResolveSerializationGroup(routeID, route, dest))
		}
	}
	return out
}

// CrossGroupConflict is one unacknowledged same-resource cross-group
// topology violation (CON-013): destinations governing one resource
// under different serialization groups without every involved route
// acknowledging the concurrency.
type CrossGroupConflict struct {
	// ResourceID is the shared governed resource.
	ResourceID string
	// Groups are the distinct effective groups sorted lexicographically.
	Groups []string
	// UnacknowledgedRoutes are the involved routes missing
	// allow_cross_group_concurrency: true, sorted.
	UnacknowledgedRoutes []string
	// InvolvedRoutes is every route governing the resource, sorted.
	InvolvedRoutes []string
}

// CrossGroupConflicts computes every unacknowledged cross-group topology
// violation in the configuration (CON-013): one conflict per resource
// whose destinations resolve to more than one effective group while at
// least one involved route does not set allow_cross_group_concurrency.
// A resource whose involved routes all acknowledge the topology produces
// no conflict — the concurrency is explicit, never silently "safe".
func CrossGroupConflicts(cfg *Config) []CrossGroupConflict {
	type resourceGroups struct {
		groups map[string]bool
		routes map[string]bool
		acked  map[string]bool
	}
	byResource := map[string]*resourceGroups{}
	for _, m := range SerializationGroupMemberships(cfg) {
		rg := byResource[m.ResourceID]
		if rg == nil {
			rg = &resourceGroups{groups: map[string]bool{}, routes: map[string]bool{}, acked: map[string]bool{}}
			byResource[m.ResourceID] = rg
		}
		rg.groups[m.Group] = true
		rg.routes[m.RouteID] = true
		rg.acked[m.RouteID] = cfg.Routes[m.RouteID].AllowCrossGroupConcurrency
	}
	ids := make([]string, 0, len(byResource))
	for id := range byResource {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var conflicts []CrossGroupConflict
	for _, id := range ids {
		rg := byResource[id]
		if len(rg.groups) < 2 {
			continue
		}
		groups := sortedSet(rg.groups)
		involved := sortedSet(rg.routes)
		var unacked []string
		for _, routeID := range involved {
			if !rg.acked[routeID] {
				unacked = append(unacked, routeID)
			}
		}
		if len(unacked) == 0 {
			continue
		}
		conflicts = append(conflicts, CrossGroupConflict{
			ResourceID: id, Groups: groups,
			UnacknowledgedRoutes: unacked, InvolvedRoutes: involved,
		})
	}
	return conflicts
}

// Describe renders one conflict as bounded operator text.
func (c CrossGroupConflict) Describe() string {
	return fmt.Sprintf("resource %q is governed by destinations in %d different serialization groups (%s) across routes %s; every involved route must set allow_cross_group_concurrency: true or the groups must align (unacknowledged: %s)",
		c.ResourceID, len(c.Groups), strings.Join(c.Groups, ", "), strings.Join(c.InvolvedRoutes, ", "), strings.Join(c.UnacknowledgedRoutes, ", "))
}

func sortedSet(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for v := range in {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// validateSerializationFields applies the destination serialization
// contract (CON-011) beyond the schema: explicit values use the bounded
// grammar (the deprecated alias included, because a set alias IS the
// effective group), and simultaneous alias fields must agree —
// identical values pass with a deprecation warning, differing values
// fail validation. New configuration never emits mutex_key; existing
// documents keep loading through the alias.
func validateSerializationFields(cfg *Config) (errs []error, warnings []string) {
	for _, routeID := range cfg.SortedRouteIDs() {
		route := cfg.Routes[routeID]
		for _, dest := range route.SortedDestinations() {
			where := fmt.Sprintf("route %q destinations.%s", routeID, dest.ID)
			if dest.SerializationGroup != "" {
				if err := ValidateSerializationGroupValue(dest.SerializationGroup); err != nil {
					errs = append(errs, fmt.Errorf("%s serialization_group: %v", where, err))
				}
			}
			if dest.MutexKey != "" {
				if err := ValidateSerializationGroupValue(dest.MutexKey); err != nil {
					errs = append(errs, fmt.Errorf("%s mutex_key: %v (the deprecated alias is the effective serialization group and must satisfy the group grammar)", where, err))
				}
			}
			switch {
			case dest.SerializationGroup != "" && dest.MutexKey != "":
				if dest.SerializationGroup != dest.MutexKey {
					errs = append(errs, fmt.Errorf("%s declares serialization_group %q and the deprecated mutex_key %q with different values; the alias may coexist only when the values are identical (CON-011)", where, dest.SerializationGroup, dest.MutexKey))
				} else {
					warnings = append(warnings, fmt.Sprintf("%s declares mutex_key %q beside an identical serialization_group; mutex_key is a deprecated alias — remove it", where, dest.MutexKey))
				}
			case dest.MutexKey != "":
				warnings = append(warnings, fmt.Sprintf("%s resolves its serialization group through the deprecated mutex_key alias %q; replace it with serialization_group", where, dest.MutexKey))
			}
		}
	}
	return errs, warnings
}
