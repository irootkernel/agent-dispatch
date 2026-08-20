package dispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/rootkernel/jjukkumi/internal/domain/fingerprint"
	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// RequestContractVersion is the task request contract the runtime builds
// (hermes-task-contract.md §2); the version vocabulary lives once on the
// port so producer and adapters cannot drift.
const RequestContractVersion = ports.TaskRequestContractVersion

// RequestInput carries the plan-derived facts the immutable task request
// is built from (E3-T2 dispatch-intent creation).
type RequestInput struct {
	DispatchID string
	Route      ports.TaskRouteRef
	Resource   ports.TaskResource
	TargetID   string
	Generation int64
	// Fingerprint is the batch content fingerprint (DAT-005).
	Fingerprint records.Digest
	// Changes are the canonical sorted manifest changes.
	Changes []records.ChangeItem
	// Flags are the source flags affecting semantics, in contract order.
	Flags []string
	// AcceptanceCriteria is the non-empty route acceptance list.
	AcceptanceCriteria []string
	Assignment         *ports.TaskAssignment
	ExecutionHints     *ports.TaskExecutionHints
}

// workspaceBindingForm is the contract workspace binding surface.
var workspaceBindingForm = regexp.MustCompile(`^(scratch|worktree|worktree:.+|dir:.+)$`)

// BuildRequest constructs the immutable logical task request and its
// idempotency key. The key is derived from the target, route revision,
// generation, contract version, and content fingerprint, so the same
// planned work submitted twice maps to one durable intent
// (DUR-012 uniqueness).
func BuildRequest(in RequestInput) (ports.TaskRequest, string, error) {
	if in.DispatchID == "" || in.Route.ID == "" || in.Route.Revision == "" {
		return ports.TaskRequest{}, "", fmt.Errorf("request needs dispatch and route identity")
	}
	if in.Resource.ID == "" || in.Resource.Workspace == "" {
		return ports.TaskRequest{}, "", fmt.Errorf("request needs resource identity and workspace")
	}
	// The contract workspace binding form (hermes-task-contract §2):
	// scratch | worktree | worktree:<path> | dir:<path>. Failing here
	// prevents persisting an intent no target adapter can render.
	if !workspaceBindingForm.MatchString(in.Resource.Workspace) {
		return ports.TaskRequest{}, "", fmt.Errorf("workspace %q is not the contract binding form (scratch, worktree, worktree:<path>, or dir:<path>)", in.Resource.Workspace)
	}
	if in.TargetID == "" {
		return ports.TaskRequest{}, "", fmt.Errorf("request needs the target ID")
	}
	if in.Generation < 1 {
		return ports.TaskRequest{}, "", fmt.Errorf("generation must be >= 1")
	}
	if _, err := records.ParseDigest(string(in.Fingerprint)); err != nil {
		return ports.TaskRequest{}, "", fmt.Errorf("content fingerprint: %v", err)
	}
	if len(in.AcceptanceCriteria) == 0 {
		return ports.TaskRequest{}, "", fmt.Errorf("acceptance criteria must not be empty")
	}
	flags := in.Flags
	if flags == nil {
		flags = []string{} // the contract requires an array, never null
	}
	key, err := fingerprint.Idempotency(records.IdempotencyKeyInput{
		ContentFingerprint: string(in.Fingerprint),
		Generation:         in.Generation,
		RequestVersion:     RequestContractVersion,
		RouteID:            in.Route.ID,
		RouteRevision:      in.Route.Revision,
		TargetID:           in.TargetID,
	})
	if err != nil {
		return ports.TaskRequest{}, "", fmt.Errorf("idempotency key: %v", err)
	}
	req := ports.TaskRequest{
		ContractVersion: RequestContractVersion,
		DispatchID:      in.DispatchID,
		IdempotencyKey:  key,
		Route:           in.Route,
		Resource:        in.Resource,
		Assignment:      in.Assignment,
		ExecutionHints:  in.ExecutionHints,
		Activation: ports.TaskActivation{
			Mode:               "latest_state",
			Generation:         in.Generation,
			ContentFingerprint: string(in.Fingerprint),
			Manifest:           manifest(in.Changes),
			Flags:              flags,
		},
		AcceptanceCriteria: in.AcceptanceCriteria,
	}
	return req, key, nil
}

// manifest projects the canonical changes into the relative-path
// activation manifest (ADR-0008: activation evidence, not snapshots).
func manifest(changes []records.ChangeItem) []ports.TaskManifestItem {
	items := make([]ports.TaskManifestItem, 0, len(changes))
	for _, c := range changes {
		items = append(items, ports.TaskManifestItem{
			Path:         c.Path,
			Operation:    string(c.Operation),
			BeforeDigest: string(c.BeforeDigest),
			AfterDigest:  string(c.AfterDigest),
		})
	}
	return items
}

// MarshalRequest encodes the request in field order as the durable
// immutable request JSON.
func MarshalRequest(req ports.TaskRequest) (string, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// WikiAcceptanceCriteria is the fixed v1 acceptance list from
// hermes-task-contract.md §2: the durable request carries it verbatim.
var WikiAcceptanceCriteria = []string{
	"Evaluate the current vault state using the configured LLM Wiki skill.",
	"Re-evaluate indexing, referencing, and grouping affected by the latest state.",
	"Respect Hermes permissions, approvals, and protected-path policy.",
	"Do not assume that a manifest path still exists at execution time.",
	"Report a bounded JJUKKUMI work receipt when the companion CLI is available.",
}

// ManifestDigest derives the manifest digest recorded on the intent: the
// SHA-256 of the canonical JSON projection of the sorted change
// manifest, the same bytes the request's activation manifest carries.
func ManifestDigest(changes []records.ChangeItem) string {
	items := manifest(changes)
	raw, err := json.Marshal(items)
	if err != nil {
		raw = []byte("[]")
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
