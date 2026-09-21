package syncmembership

import (
	"context"
	"fmt"
	"reflect"

	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

type Repository interface {
	ReadMembershipCommit(context.Context, string) ([]byte, []byte, error)
	CommitParents(context.Context, string) ([]string, error)
	VerifySSHSignature(context.Context, string, string) error
}

type History struct {
	Head      string
	Order     []string
	Revisions map[string]syncrecords.Membership
}

func LoadHistory(ctx context.Context, repo Repository, head string, binding syncrecords.Binding, maxCommits int) (History, error) {
	if maxCommits < 1 || maxCommits > 10000 {
		return History{}, fmt.Errorf("membership history bound must be 1..10000")
	}
	out := History{Head: head, Revisions: map[string]syncrecords.Membership{}}
	plans := map[string]syncrecords.MembershipPlan{}
	currentOID := head
	for currentOID != "" {
		if len(out.Order) >= maxCommits {
			return History{}, fmt.Errorf("membership history exceeds configured bound %d", maxCommits)
		}
		documentRaw, planRaw, err := repo.ReadMembershipCommit(ctx, currentOID)
		if err != nil {
			return History{}, fmt.Errorf("read membership revision %s: %w", currentOID, err)
		}
		document, err := syncrecords.DecodeMembership(documentRaw)
		if err != nil {
			return History{}, fmt.Errorf("membership revision %s: %w", currentOID, err)
		}
		plan, err := syncrecords.DecodePlan(planRaw)
		if err != nil {
			return History{}, fmt.Errorf("membership plan at %s: %w", currentOID, err)
		}
		if !reflect.DeepEqual(document, plan.ProposedMembership) {
			return History{}, fmt.Errorf("membership revision %s does not match its reviewed plan", currentOID)
		}
		if document.GroupID != binding.GroupID || document.ContentRef != binding.ContentRef || document.ContentBinding != binding.ContentBinding || document.ContractDigest != binding.ContractDigest || document.AdministratorKey != binding.AdministratorKey {
			return History{}, fmt.Errorf("membership revision %s is outside the configured trust binding", currentOID)
		}
		if err := repo.VerifySSHSignature(ctx, currentOID, binding.AdministratorKey); err != nil {
			return History{}, fmt.Errorf("membership revision %s administrator signature: %w", currentOID, err)
		}
		parents, err := repo.CommitParents(ctx, currentOID)
		if err != nil {
			return History{}, err
		}
		if document.Predecessor == nil {
			if len(parents) != 0 || plan.ChangeKind != "bootstrap" {
				return History{}, fmt.Errorf("membership root %s is self-authorizing or non-linear", currentOID)
			}
			rebuilt, err := syncrecords.NewPlan("bootstrap", "", nil, nil, document.ActiveMembers, binding)
			if err != nil || !reflect.DeepEqual(rebuilt, plan) {
				return History{}, fmt.Errorf("membership root %s is not the unique configured bootstrap", currentOID)
			}
		} else {
			if len(parents) != 1 || parents[0] != *document.Predecessor || plan.ExpectedPredecessor == nil || *plan.ExpectedPredecessor != *document.Predecessor {
				return History{}, fmt.Errorf("membership revision %s has a stale or non-linear predecessor", currentOID)
			}
		}
		if _, duplicate := out.Revisions[currentOID]; duplicate {
			return History{}, fmt.Errorf("membership history contains a cycle")
		}
		out.Revisions[currentOID] = document
		plans[currentOID] = plan
		out.Order = append(out.Order, currentOID)
		if document.Predecessor == nil {
			break
		}
		currentOID = *document.Predecessor
	}
	for i := 0; i+1 < len(out.Order); i++ {
		childOID, parentOID := out.Order[i], out.Order[i+1]
		plan := plans[childOID]
		if err := syncrecords.ValidateTransition(out.Revisions[parentOID], plan); err != nil {
			return History{}, fmt.Errorf("unauthorized membership transition %s: %w", childOID, err)
		}
	}
	return out, nil
}

func (h History) Current() (syncrecords.Membership, bool) {
	m, ok := h.Revisions[h.Head]
	return m, ok
}

func (h History) AuthorizePublisher(revision, key string, previouslyVerified, administratorCheckpoint bool) error {
	at, ok := h.Revisions[revision]
	if !ok {
		return fmt.Errorf("publication membership revision is not a verified ancestor")
	}
	current, ok := h.Current()
	if !ok {
		return fmt.Errorf("membership history has no current revision")
	}
	return syncrecords.AuthorizePublisher(at, current, key, previouslyVerified, administratorCheckpoint)
}
