package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

type policyStub struct {
	decision PolicyDecision
	err      error
}

func (p policyStub) Evaluate(context.Context, domain.Scope, Proposal) (PolicyDecision, error) {
	return p.decision, p.err
}

type lifecycleStub struct{ calls int }

func (r *lifecycleStub) ApplyProposalAuthorized(_ context.Context, proposal Proposal, scope domain.Scope, at time.Time) (domain.Memory, domain.MemoryVersion, domain.Operation, error) {
	r.calls++
	memory := domain.Memory{ID: proposal.TargetID}
	operation := domain.Operation{
		ID: "op-test", Phase: domain.PhaseApply, Kind: string(proposal.Kind), Actor: scope.Actor,
		TargetID: memory.ID, ProposalID: proposal.ID, OccurredAt: at, Result: "applied",
	}
	return memory, domain.MemoryVersion{}, operation, nil
}

func lifecycleProposal() Proposal {
	return Proposal{
		ID: "proposal-lifecycle", Namespace: "ns-test", ObservationIDs: []domain.ID{"obs-test"},
		Kind: ProposalArchive, TargetID: "mem-test", Payload: json.RawMessage(`{}`),
		StrategyID: "test@1.0.0", StrategyHash: strings.Repeat("0", 64),
		CreatedAt: time.Now().UTC(),
	}
}

func lifecycleScope() domain.Scope {
	return domain.Scope{
		Namespace: "ns-test", Owner: "owner-a", Subject: "subject-a", Actor: "actor-a",
		Visibility: domain.VisibilityPrivate,
	}
}

func TestLifecycleServiceDeniesBeforeRepositoryMutation(t *testing.T) {
	repository := &lifecycleStub{}
	service, err := NewLifecycleService(policyStub{decision: PolicyDecision{Allow: false, ReasonCode: "owner_mismatch"}}, repository)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(context.Background(), lifecycleScope(), lifecycleProposal(), time.Now().UTC())
	if !errors.Is(err, ErrPolicyDenied) || repository.calls != 0 || result.Decision.ReasonCode != "owner_mismatch" {
		t.Fatalf("result=%+v calls=%d err=%v", result, repository.calls, err)
	}
}

func TestLifecycleServiceRecordsAuthorizedActor(t *testing.T) {
	repository := &lifecycleStub{}
	service, err := NewLifecycleService(policyStub{decision: PolicyDecision{Allow: true, ReasonCode: "owner_allowed"}}, repository)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(context.Background(), lifecycleScope(), lifecycleProposal(), time.Now().UTC())
	if err != nil || repository.calls != 1 || result.Operation.Actor != "actor-a" || result.Decision.ReasonCode != "owner_allowed" {
		t.Fatalf("result=%+v calls=%d err=%v", result, repository.calls, err)
	}
}

func TestLifecycleServiceRejectsPolicyWithoutReason(t *testing.T) {
	service, err := NewLifecycleService(policyStub{decision: PolicyDecision{Allow: true}}, &lifecycleStub{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(context.Background(), lifecycleScope(), lifecycleProposal(), time.Now().UTC()); err == nil {
		t.Fatal("reasonless policy decision succeeded")
	}
}
