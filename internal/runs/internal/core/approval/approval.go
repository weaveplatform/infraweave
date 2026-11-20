// Package approval evaluates state-scoped approval policies using owner facts.
package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/changes"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/policyinput"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/review"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// Store reads immutable input history and writes the aggregate in its fence.
type Store interface {
	policyinput.Store
	review.Store
	evidence.Store
	evidence.ApprovalStore
	Changes(context.Context, lifecycle.Run) (string, bool, error)
}

// Deps are owner ports; no policy input is taken from transport placeholders.
type Deps struct {
	Store      Store
	Evaluator  policyapi.Evaluator
	Inputs     policyapi.RunInputBuilder
	Stacks     stacksapi.PolicySnapshots
	Pools      workersapi.PoolMetadataReader
	Roles      accessapi.StackRoleReader
	SpaceFacts accessapi.PolicySpaceReader
	Contexts   configapi.PolicyContextReader
	Objects    changes.Objects
	Now        func() time.Time
}

// Service records decisions without driving transitions or confirming a plan.
type Service struct{ d Deps }

// New constructs the evaluator used by scheduling and lifecycle entry.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// Evaluate joins the caller transaction. Input materializes only after policy
// selection; unavailable facts yield safe receipts, storage errors roll back.
func (s *Service) Evaluate(ctx context.Context, run lifecycle.Run) (policyapi.Evaluation, error) {
	if !tx.In(ctx) {
		return policyapi.Evaluation{}, lifecycle.ErrNoTransaction
	}
	current, err := s.d.Store.Lock(ctx, run.Account, run.ID)
	if err != nil {
		return policyapi.Evaluation{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	if current.Version != run.Version || current.State != run.State {
		return policyapi.Evaluation{}, &lifecycle.Conflict{Run: run.ID, State: current.State, Version: current.Version}
	}
	result, err := s.d.Evaluator.Evaluate(ctx, policyapi.EvaluationRequest{
		Account: run.Account, Type: policyapi.Approval, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: run.Stack}, Run: &run.ID,
		BuildInput: func(ctx context.Context) (json.RawMessage, error) { return s.input(ctx, run) },
	})
	if err != nil {
		return policyapi.Evaluation{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	gate := evidence.Approval{State: run.State, StateVersion: run.Version, HasPolicies: result.HasPolicies, Outcome: result.Outcome, At: s.d.Now().UTC()}
	if result.Type != policyapi.Approval || !gate.Valid() {
		return policyapi.Evaluation{}, evidence.ErrReceipt
	}
	if err := s.d.Store.RecordApproval(ctx, run.Account, run.ID, gate); err != nil {
		return policyapi.Evaluation{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	if err := s.d.Store.RecordPolicyEvidence(ctx, run.Account, run.ID, run.Version, result.Receipts); err != nil {
		return policyapi.Evaluation{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	return result, nil
}

func known[T any](v T) policyapi.Fact[T] { return policyapi.Fact[T]{Known: true, Value: &v} }

func (s *Service) input(ctx context.Context, run lifecycle.Run) (json.RawMessage, error) {
	snapshot, err := policyinput.Current(ctx, s.d.Store, run.Account, run.ID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the owner names the failing operation
	}
	stack, err := s.d.Stacks.PolicySnapshot(ctx, run.Account, run.Stack)
	if err != nil {
		return nil, err //nolint:wrapcheck // the owner names the failing operation
	}
	records, err := s.d.Store.Reviews(ctx, run.Account, []kernel.ULID{run.ID})
	if err != nil {
		return nil, err //nolint:wrapcheck // the owner names the failing operation
	}
	reviews, err := review.Effective(records[run.ID], run.State, run.Version)
	if err != nil {
		return nil, fmt.Errorf("runs: invalid approval review history: %w", kernel.ErrUnavailable)
	}
	st, err := s.stackFacts(ctx, run, stack)
	if err != nil {
		return nil, err
	}
	changeFacts, err := s.changeFacts(ctx, run, stack.Settings.Vendor)
	if err != nil {
		return nil, err
	}
	return s.d.Inputs.BuildRunInput(ctx, policyapi.RunInputFacts{ //nolint:wrapcheck // typed builder owns fact validation
		Account: run.Account, Type: policyapi.Approval, At: s.d.Now().UTC(),
		Run: policyinput.Facts(snapshot, snapshot.Environment, changeFacts), Stack: st, Reviews: known(reviews),
	})
}

func (s *Service) stackFacts(ctx context.Context, run lifecycle.Run, snapshot stacksapi.PolicySnapshot) (policyapi.StackFacts, error) {
	st := snapshot.Settings
	roles, err := s.d.Roles.StackRoles(ctx, run.Account, run.Stack)
	if err != nil {
		return policyapi.StackFacts{}, fmt.Errorf("runs: read approval stack roles: %w", err)
	}
	roleFacts := make([]policyapi.RoleFacts, 0, len(roles))
	for _, role := range roles {
		roleFacts = append(roleFacts, policyapi.RoleFacts{ID: role.ID, Slug: role.Slug, Name: role.Name})
	}
	contexts, err := s.d.Contexts.PolicyContextSlugs(ctx, run.Account, run.Stack)
	if err != nil {
		return policyapi.StackFacts{}, fmt.Errorf("runs: read approval contexts: %w", err)
	}
	out := policyapi.StackFacts{
		ID: st.ID, Slug: st.Slug, Name: st.Name, Branch: st.Branch, Namespace: st.Namespace,
		Repository: st.Repository, State: snapshot.State, Autodeploy: st.Autodeploy, Labels: known(snapshot.Labels),
		AdditionalProjectGlobs: known(st.AdditionalProjectGlobs), TerraformVersion: policyapi.Fact[string]{Known: true, Value: st.TerraformVersion},
		Roles: known(roleFacts), Contexts: known(contexts),
	}
	if st.ProjectRoot != nil {
		out.ProjectRoot = *st.ProjectRoot
	}
	if st.Lock != nil {
		out.LockedBy = known(st.Lock.By)
	}
	if st.TrackedCommit != nil {
		c := st.TrackedCommit
		out.TrackedCommit = known(
			policyapi.CommitFacts{Author: c.AuthorName, Branch: c.Branch, Message: c.Message, At: c.Timestamp, Hash: known(c.Hash), Tag: known(c.Tag), URL: known(c.URL)},
		)
	}
	space, err := s.d.SpaceFacts.PolicySpace(ctx, run.Account, st.Space)
	if err != nil {
		return out, err //nolint:wrapcheck // the owner names the failing operation
	}
	out.Space = known(policyapi.SpaceFacts{ID: space.Slug, Name: space.Name, Labels: known(space.Labels)})
	if run.WorkerPool != nil {
		pool, err := s.d.Pools.PoolMetadata(ctx, run.Account, *run.WorkerPool)
		if err != nil && !errors.Is(err, workersapi.ErrNoPool) {
			return out, err //nolint:wrapcheck // the owner names the failing operation
		}
		if err == nil {
			out.WorkerPool = known(policyapi.WorkerPoolFacts{ID: known(pool.ID.String()), Name: known(pool.Name), Labels: known(pool.Labels), Public: pool.Public})
		}
	}
	return out, nil
}

func (s *Service) changeFacts(ctx context.Context, run lifecycle.Run, vendor string) (policyapi.Fact[[]policyapi.ChangeFacts], error) {
	key, found, err := s.d.Store.Changes(ctx, run)
	if err != nil || !found {
		return policyapi.Fact[[]policyapi.ChangeFacts]{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	document, err := s.d.Objects.Open(ctx, objectstore.RunArtifacts, key)
	if errors.Is(err, objectstore.ErrNotFound) {
		return policyapi.Fact[[]policyapi.ChangeFacts]{}, policyapi.ErrInput
	}
	if err != nil {
		return policyapi.Fact[[]policyapi.ChangeFacts]{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	defer document.Close()
	structural, err := plan.Read(document)
	if err != nil {
		return policyapi.Fact[[]policyapi.ChangeFacts]{}, policyapi.ErrInput
	}
	facts, err := policyinput.StructuralChanges(vendor, structural)
	if err != nil {
		return policyapi.Fact[[]policyapi.ChangeFacts]{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	return known(facts), nil
}
