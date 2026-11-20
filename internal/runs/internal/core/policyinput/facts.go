package policyinput

import (
	"slices"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func known[T any](value T) policyapi.Fact[T] { return policyapi.Fact[T]{Known: true, Value: &value} }

// Facts includes every persisted run fact with a corresponding policy field.
// An unavailable environment fact uses the snapshot's sealed recorded winners;
// an explicit override must contain real values, never redacted key-only data.
// Changes comes from the opened structural changes artifact.
// Unmaterialized session/review/provider facts stay absent in the owner model.
func Facts(snapshot Snapshot, environment policyapi.Fact[map[string]string], changes policyapi.Fact[[]policyapi.ChangeFacts]) policyapi.RunFacts {
	r := snapshot.Run
	force := r.ForceApply
	if force == "" {
		force = "DISABLED"
	}
	command := policyapi.Fact[string]{Known: true}
	if r.Task != nil {
		command = known(r.Task.Command)
	}
	out := policyapi.RunFacts{
		ID: r.ID, Type: r.Type, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		TriggeredBy: known(r.TriggeredBy), Branch: known(r.Commit.Branch), Commit: known(commit(r.Commit)),
		Flags: known(append([]string{}, snapshot.Flags...)), BasedOnLocalWorkspace: known(r.LocalPreview()),
		DriftDetection: known(r.DriftDetection), ForceApplyMode: known(force), Command: command, Changes: changes,
	}
	if r.ResolvedConfig != nil {
		c := r.ResolvedConfig.Configuration
		if !environment.Known && environment.Value == nil {
			environment = snapshot.Environment
		}
		out.RuntimeConfig = known(policyapi.RuntimeConfigFacts{
			AfterApply: known(slices.Clone(c.AfterApply)), AfterDestroy: known(slices.Clone(c.AfterDestroy)),
			AfterInit: known(slices.Clone(c.AfterInit)), AfterPerform: known(slices.Clone(c.AfterPerform)),
			AfterPlan: known(slices.Clone(c.AfterPlan)), AfterRun: known(slices.Clone(c.AfterRun)),
			BeforeApply: known(slices.Clone(c.BeforeApply)), BeforeDestroy: known(slices.Clone(c.BeforeDestroy)),
			BeforeInit: known(slices.Clone(c.BeforeInit)), BeforePerform: known(slices.Clone(c.BeforePerform)),
			BeforePlan: known(slices.Clone(c.BeforePlan)), GitSparseCheckoutPaths: known(slices.Clone(c.GitSparseCheckoutPaths)),
			Environment: environment, ProjectRoot: c.ProjectRoot, RunnerImage: c.RunnerImage,
			TerraformVersion: known(c.TerraformVersion),
		})
	}
	return out
}

func commit(c stacksapi.Commit) policyapi.CommitFacts {
	author := c.AuthorName
	if c.AuthorLogin != nil && *c.AuthorLogin != "" {
		author = *c.AuthorLogin
	}
	return policyapi.CommitFacts{
		Author: author, Branch: c.Branch, Message: c.Message, At: c.Timestamp,
		Hash: known(c.Hash), Tag: known(c.Tag), URL: known(c.URL),
	}
}
