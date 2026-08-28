package records

import (
	"fmt"
	"sort"
	"unicode/utf8"
)

// AggregateOrigin is the closed vocabulary of aggregate-event creation
// causes (domain-model multi-destination fan-out, E12-T1). The value is
// evidence, never a routing input.
type AggregateOrigin string

const (
	OriginArrival   AggregateOrigin = "arrival"
	OriginFollowup  AggregateOrigin = "followup"
	OriginRerun     AggregateOrigin = "rerun"
	OriginRebuild   AggregateOrigin = "rebuild"
	OriginReconcile AggregateOrigin = "reconcile"
)

// aggregateOrigins is the closed origin set; ParseAggregateOrigin fails
// closed on anything else (DAT-009 posture for stored enum values).
var aggregateOrigins = map[AggregateOrigin]struct{}{
	OriginArrival:   {},
	OriginFollowup:  {},
	OriginRerun:     {},
	OriginRebuild:   {},
	OriginReconcile: {},
}

// ParseAggregateOrigin validates one origin value.
func ParseAggregateOrigin(s string) (AggregateOrigin, error) {
	origin := AggregateOrigin(s)
	if _, ok := aggregateOrigins[origin]; !ok {
		return "", errf(fmt.Sprintf("unknown aggregate origin %q", s))
	}
	return origin, nil
}

// DestinationSelection is the inspectable selection evidence of one
// destination beneath an aggregate event (FAN-010, E12-T1): which
// destination was selected, under which destination revision, for which
// workstream, and the closed machine reason it was selected.
type DestinationSelection struct {
	DestinationID       string `json:"destination_id"`
	DestinationRevision string `json:"destination_revision"`
	Workstream          string `json:"workstream"`
	Reason              string `json:"reason"`
}

// Validate fails closed on incomplete or non-canonical selection rows.
func (s DestinationSelection) Validate() error {
	if s.DestinationID == "" || s.DestinationRevision == "" || s.Workstream == "" || s.Reason == "" {
		return errf("destination selection needs id, revision, workstream, and reason")
	}
	for _, v := range []string{s.DestinationID, s.DestinationRevision, s.Workstream, s.Reason} {
		if !utf8.ValidString(v) {
			return errf("destination selection strings must be valid UTF-8")
		}
	}
	return nil
}

// SortSelections orders selections canonically by destination ID with the
// workstream as the deterministic tiebreaker, so the selection summary and
// every digest derived from it never depend on configuration order
// (FAN-012).
func SortSelections(selections []DestinationSelection) {
	sort.SliceStable(selections, func(i, j int) bool {
		if selections[i].DestinationID != selections[j].DestinationID {
			return selections[i].DestinationID < selections[j].DestinationID
		}
		return selections[i].Workstream < selections[j].Workstream
	})
}

// ChildIdempotencyKeyInput is the dedicated child idempotency-key
// projection (DAT-014, E12-T1) with keys in RFC 8785 lexicographic order.
// The projection binds the child to the route ID and revision, the source
// generation and content fingerprint, the destination ID and revision, the
// workstream, the target scope, and the request contract version, so two
// destinations under one aggregate event can never collide and a
// behavior-affecting destination edit (a new destination revision) always
// changes the key. Attempt numbers, submission times, and other
// retry-varying fields remain deliberately absent (DAT-006): a retry keeps
// the child and its key (CON-009).
type ChildIdempotencyKeyInput struct {
	ContentFingerprint  string `json:"content_fingerprint"`
	DestinationID       string `json:"destination_id"`
	DestinationRevision string `json:"destination_revision"`
	Generation          int64  `json:"generation"`
	RequestVersion      string `json:"request_contract_version"`
	RouteID             string `json:"route_id"`
	RouteRevision       string `json:"route_revision"`
	TargetScope         string `json:"target_scope"`
	Workstream          string `json:"workstream"`
}
