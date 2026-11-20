package core

import (
	"context"
	"encoding/json"
	"fmt"

	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// BuildPushInput uses the contract's typed projection without reading another
// domain's persistence or substituting unknown provider facts.
func (*Service) BuildPushInput(ctx context.Context, facts policyapi.PushInputFacts) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("build push policy input: %w", err)
	}
	if facts.Account.IsZero() || facts.Stack.ID.IsZero() || facts.Stack.Slug == "" || !facts.Stack.State.Valid() ||
		facts.Push.Value == nil && facts.PullRequest.Value == nil {
		return nil, policyapi.ErrInput
	}
	b := &inputBuilder{}
	document := inputs.PushInput{
		Stack: inputs.Value(b.stack(facts.Stack)),
		Push: convertFact(b, facts.Push, func(p policyapi.PushFacts) inputs.Push {
			if p.At.IsZero() || p.Hash == "" || p.AffectedStacksCount < 0 {
				b.invalid = true
			}
			return inputs.Push{
				Author: inputs.Value(p.Author), Branch: inputs.Value(p.Branch), Hash: inputs.Value(p.Hash), Message: inputs.Value(p.Message),
				CreatedAt: inputs.Value(p.At.UnixNano()), AffectedFiles: inputs.Value(append([]string{}, p.AffectedFiles...)),
				AffectedStacksCount: inputs.Value(p.AffectedStacksCount), Tag: fact(b, p.Tag),
				Commits: convertFact(b, p.Commits, func(commits []policyapi.PushCommitFacts) []inputs.PushCommit {
					out := make([]inputs.PushCommit, 0, len(commits))
					for _, c := range commits {
						commit := b.commit(c.Commit)
						out = append(out, inputs.PushCommit{
							Author: commit.Author, Branch: commit.Branch, Message: commit.Message, CreatedAt: commit.CreatedAt,
							Hash: commit.Hash, Tag: commit.Tag, URL: commit.URL, PullRequestID: commit.PullRequestID,
							ExistOnTrackedBranch: commit.ExistOnTrackedBranch, AffectedFiles: inputs.Value(append([]string{}, c.AffectedFiles...)),
						})
					}
					return out
				}),
			}
		}),
		PullRequest: convertFact(b, facts.PullRequest, func(p policyapi.PullRequestFacts) inputs.PullRequest {
			if p.ID < 1 || p.AffectedStacksCount < 0 {
				b.invalid = true
			}
			return inputs.PullRequest{
				ID: inputs.Value(p.ID), Action: inputs.Value(p.Action), Author: inputs.Value(p.Author), Title: inputs.Value(p.Title),
				Base: inputs.Value(b.commit(p.Base)), Head: inputs.Value(b.commit(p.Head)), Closed: inputs.Value(p.Closed), Draft: inputs.Value(p.Draft),
				Diff: inputs.Value(append([]string{}, p.Diff...)), Labels: inputs.Value(append([]string{}, p.Labels...)), AffectedStacksCount: inputs.Value(p.AffectedStacksCount),
				ActionInitiator: fact(b, p.ActionInitiator), HeadOwner: fact(b, p.HeadOwner), Approved: fact(b, p.Approved), Mergeable: fact(b, p.Mergeable),
				Undiverged: fact(b, p.Undiverged), Comment: fact(b, p.Comment),
			}
		}),
		VCSIntegration: convertFact(b, facts.VCSIntegration, func(in policyapi.VCSIntegrationFacts) inputs.VCSIntegration {
			if in.ID.IsZero() || in.CreatedAt.IsZero() || in.UpdatedAt.IsZero() {
				b.invalid = true
			}
			return inputs.VCSIntegration{
				ID: inputs.Value(in.ID.String()), Description: inputs.Value(in.Description), IsDefault: inputs.Value(in.IsDefault),
				Labels: inputs.Value(append([]string{}, in.Labels...)), Name: inputs.Value(in.Name), Provider: inputs.Value(in.Provider),
				CreatedAt: inputs.Value(in.CreatedAt.UnixNano()), UpdatedAt: inputs.Value(in.UpdatedAt.UnixNano()),
				Space: convertFact(b, in.Space, func(s policyapi.SpaceFacts) inputs.Space {
					return inputs.Space{ID: inputs.Value(s.ID), Name: inputs.Value(s.Name), Labels: fact(b, s.Labels)}
				}),
			}
		}),
		InProgress: convertFact(b, facts.InProgress, func(runs []policyapi.RunFacts) inputs.InProgress {
			out := make([]inputs.InProgressRun, 0, len(runs))
			for _, facts := range runs {
				r := b.run(facts)
				out = append(out, inputs.InProgressRun{
					ID: r.ID, Type: r.Type, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, TriggeredBy: r.TriggeredBy,
					Branch: r.Branch, Commit: r.Commit, BasedOnLocalWorkspace: r.BasedOnLocalWorkspace, DriftDetection: r.DriftDetection,
					ForceApplyMode: r.ForceApplyMode, Command: r.Command, UserProvidedMetadata: r.UserProvidedMetadata,
				})
			}
			raw, err := json.Marshal(out)
			var value inputs.InProgress
			if err != nil || json.Unmarshal(raw, &value) != nil {
				b.invalid = true
			}
			return value
		}),
	}
	if b.invalid {
		return nil, policyapi.ErrInput
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, policyapi.ErrInput
	}
	if err := inputs.Validate(string(policyapi.Push), raw); err != nil {
		return nil, policyapi.ErrInput
	}
	return raw, nil
}
