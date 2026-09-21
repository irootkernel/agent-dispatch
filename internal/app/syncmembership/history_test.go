package syncmembership

import (
	"context"
	"errors"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

type fakeRevision struct {
	document, plan []byte
	parents        []string
	signature      error
}
type fakeRepository map[string]fakeRevision

func (f fakeRepository) ReadMembershipCommit(_ context.Context, oid string) ([]byte, []byte, error) {
	r, ok := f[oid]
	if !ok {
		return nil, nil, errors.New("missing revision")
	}
	return r.document, r.plan, nil
}
func (f fakeRepository) CommitParents(_ context.Context, oid string) ([]string, error) {
	return append([]string(nil), f[oid].parents...), nil
}
func (f fakeRepository) VerifySSHSignature(_ context.Context, oid, _ string) error {
	return f[oid].signature
}

func TestLoadHistoryVerifiesLinearPinnedTransitions(t *testing.T) {
	binding, members := historyBinding(), historyMembers()
	rootPlan, err := syncrecords.NewPlan("bootstrap", "", nil, nil, members, binding)
	if err != nil {
		t.Fatal(err)
	}
	rootOID := "1111111111111111111111111111111111111111"
	desired := append([]syncrecords.ActiveMember(nil), members...)
	desired[0].Endpoint = "https://node-a-next.example.ts.net"
	childPlan, err := syncrecords.NewPlan("endpoint_update", "node-a", &rootPlan.ProposedMembership, &rootOID, desired, binding)
	if err != nil {
		t.Fatal(err)
	}
	childOID := "2222222222222222222222222222222222222222"
	repo := fakeRepository{rootOID: encodedRevision(t, rootPlan, nil), childOID: encodedRevision(t, childPlan, []string{rootOID})}
	history, err := LoadHistory(context.Background(), repo, childOID, binding, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Order) != 2 || history.Order[0] != childOID || history.Order[1] != rootOID {
		t.Fatalf("history order=%v", history.Order)
	}

	bad := repo[childOID]
	bad.parents = []string{rootOID, "3333333333333333333333333333333333333333"}
	repo[childOID] = bad
	if _, err := LoadHistory(context.Background(), repo, childOID, binding, 10); err == nil {
		t.Fatal("merge membership history accepted")
	}
	bad.parents = []string{rootOID}
	bad.signature = gitlocal.ErrInvalidSignature
	repo[childOID] = bad
	if _, err := LoadHistory(context.Background(), repo, childOID, binding, 10); !errors.Is(err, gitlocal.ErrInvalidSignature) {
		t.Fatalf("invalid signature: %v", err)
	}
}

func encodedRevision(t *testing.T, plan syncrecords.MembershipPlan, parents []string) fakeRevision {
	t.Helper()
	document, err := syncrecords.CanonicalMembership(plan.ProposedMembership)
	if err != nil {
		t.Fatal(err)
	}
	planRaw, err := syncrecords.CanonicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	return fakeRevision{document: document, plan: planRaw, parents: parents}
}

func historyBinding() syncrecords.Binding {
	return syncrecords.Binding{GroupID: "wiki-pair", ContentRef: "refs/heads/wiki-sync", ContentBinding: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", ContractDigest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", AdministratorKey: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
}
func historyMembers() []syncrecords.ActiveMember {
	return []syncrecords.ActiveMember{{InstanceID: "node-a", StateIncarnationID: "node-a-0001", PublisherKey: "SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA", Endpoint: "https://node-a.example.ts.net"}, {InstanceID: "node-b", StateIncarnationID: "node-b-0001", PublisherKey: "SHA256:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCA", Endpoint: "https://node-b.example.ts.net"}}
}
