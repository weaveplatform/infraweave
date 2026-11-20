package policyinput

import (
	"context"
	"errors"
	"fmt"
	"io"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// ChangeStore locates immutable structural artifacts without opening secrets.
type ChangeStore interface {
	Changes(context.Context, lifecycle.Run) (string, bool, error)
}

// Objects opens the sealed worker artifact through its storage owner.
type Objects interface {
	Open(context.Context, objectstore.Name, string) (io.ReadCloser, error)
}

// Metadata shares owner-backed stack and change facts between workflow policies.
type Metadata struct {
	Store      ChangeStore
	Objects    Objects
	Pools      workersapi.PoolMetadataReader
	Roles      accessapi.StackRoleReader
	SpaceFacts accessapi.PolicySpaceReader
	Contexts   configapi.PolicyContextReader
}

// Stack projects only metadata supplied by the owning domains.
func (m Metadata) Stack(ctx context.Context, run lifecycle.Run, snapshot stacksapi.PolicySnapshot) (policyapi.StackFacts, error) {
	st := snapshot.Settings
	roles, err := m.Roles.StackRoles(ctx, run.Account, run.Stack)
	if err != nil {
		return policyapi.StackFacts{}, fmt.Errorf("runs: read policy stack roles: %w", err)
	}
	roleFacts := make([]policyapi.RoleFacts, 0, len(roles))
	for _, role := range roles {
		roleFacts = append(roleFacts, policyapi.RoleFacts{ID: role.ID, Slug: role.Slug, Name: role.Name})
	}
	contexts, err := m.Contexts.PolicyContextSlugs(ctx, run.Account, run.Stack)
	if err != nil {
		return policyapi.StackFacts{}, fmt.Errorf("runs: read policy contexts: %w", err)
	}
	tool, err := workflowTool(st)
	if err != nil {
		return policyapi.StackFacts{}, err
	}
	out := policyapi.StackFacts{
		ID: st.ID, Slug: st.Slug, Name: st.Name, Branch: st.Branch, Namespace: st.Namespace,
		Repository: st.Repository, State: snapshot.State, Autodeploy: st.Autodeploy, Labels: known(snapshot.Labels),
		AdditionalProjectGlobs: known(st.AdditionalProjectGlobs), TerraformVersion: policyapi.Fact[string]{Known: true, Value: st.TerraformVersion},
		Roles: known(roleFacts), Contexts: known(contexts),
		WorkflowTool: tool, LockedBy: policyapi.Fact[string]{Known: true}, TrackedCommit: policyapi.Fact[policyapi.CommitFacts]{Known: true},
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
			policyapi.CommitFacts{Author: commitAuthor(*c), Branch: st.Branch, Message: c.Message, At: c.Timestamp, Hash: known(c.Hash), Tag: known(c.Tag), URL: known(c.URL)},
		)
	}
	space, err := m.SpaceFacts.PolicySpace(ctx, run.Account, st.Space)
	if err != nil {
		return out, err //nolint:wrapcheck // the owner names the failing operation
	}
	out.Space = known(policyapi.SpaceFacts{ID: space.Slug, Name: space.Name, Labels: known(space.Labels)})
	if run.WorkerPool != nil {
		pool, err := m.Pools.PoolMetadata(ctx, run.Account, *run.WorkerPool)
		if err != nil && !errors.Is(err, workersapi.ErrNoPool) {
			return out, err //nolint:wrapcheck // the owner names the failing operation
		}
		if err == nil {
			out.WorkerPool = known(policyapi.WorkerPoolFacts{ID: known(pool.ID.String()), Name: known(pool.Name), Labels: known(pool.Labels), Public: pool.Public})
		}
	}
	return out, nil
}

func commitAuthor(c stacksapi.Commit) string {
	if c.AuthorLogin != nil && *c.AuthorLogin != "" {
		return *c.AuthorLogin
	}
	return c.AuthorName
}

// Changes reads the latest recorded structural changes for a waiting run.
func (m Metadata) Changes(ctx context.Context, run lifecycle.Run, vendor string) (policyapi.Fact[[]policyapi.ChangeFacts], error) {
	key, found, err := m.Store.Changes(ctx, run)
	if err != nil || !found {
		return policyapi.Fact[[]policyapi.ChangeFacts]{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	document, err := m.Objects.Open(ctx, objectstore.RunArtifacts, key)
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
	facts, err := StructuralChanges(vendor, structural)
	if err != nil {
		return policyapi.Fact[[]policyapi.ChangeFacts]{}, err //nolint:wrapcheck // the owner names the failing operation
	}
	return known(facts), nil
}
