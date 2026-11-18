package pullrequest

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

var _ stacksapi.PullRequestReader = (*Service)(nil)

// PullRequests reads through the current binding after stacks has checked reach.
// Candidate paging continues past ineligible requests before applying the cap.
func (s *Service) PullRequests(ctx context.Context, acct kernel.AccountID, stacks []stacksapi.Settings) (map[kernel.ULID][]stacksapi.PullRequest, error) {
	out := make(map[kernel.ULID][]stacksapi.PullRequest, len(stacks))
	for _, stack := range stacks {
		out[stack.ID] = []stacksapi.PullRequest{}
		if stack.VCSDetached {
			continue
		}
		in, found, err := s.d.Integrations.Binding(ctx, acct, stack.Provider, stack.VCSIntegration)
		if err != nil {
			return nil, fmt.Errorf("vcs: resolve stack request binding: %w", err)
		}
		if !found {
			continue
		}
		var after *Cursor
		for len(out[stack.ID]) < Limit {
			page, err := s.d.Store.Candidates(ctx, acct, in.ID, stack.Namespace, stack.Repository, stack.Branch, after, Batch)
			if err != nil {
				return nil, fmt.Errorf("vcs: read stack request candidates: %w", err)
			}
			if len(page) == 0 {
				break
			}
			eligible, err := s.match(ctx, acct, stack.ID, page)
			if err != nil {
				return nil, err
			}
			out[stack.ID] = append(out[stack.ID], eligible[:min(len(eligible), Limit-len(out[stack.ID]))]...)
			last := page[len(page)-1]
			after = &Cursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
			if len(page) < Batch {
				break
			}
		}
	}
	return out, nil
}

func (s *Service) match(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, page []Request) ([]stacksapi.PullRequest, error) {
	ids := make([]kernel.ULID, 0, len(page))
	for _, request := range page {
		ids = append(ids, request.ID)
	}
	heads, err := s.d.Store.Heads(ctx, acct, ids)
	if err != nil {
		return nil, fmt.Errorf("vcs: read stack request heads: %w", err)
	}
	unique := map[string]bool{}
	for _, hashes := range heads {
		for _, hash := range hashes {
			unique[hash] = true
		}
	}
	hashes := make([]string, 0, len(unique))
	for hash := range unique {
		hashes = append(hashes, hash)
	}
	slices.Sort(hashes)
	if len(hashes) == 0 {
		return nil, nil
	}
	if s.d.Runs == nil {
		return nil, kernel.ErrUnavailable
	}
	runs, err := s.d.Runs.OnCommits(ctx, acct, stack, hashes)
	if err != nil {
		return nil, fmt.Errorf("vcs: read stack request runs: %w", err)
	}
	bySHA := map[string][]runsapi.CommitRun{}
	for _, run := range runs {
		bySHA[run.CommitSHA] = append(bySHA[run.CommitSHA], run)
	}
	out := make([]stacksapi.PullRequest, 0, len(page))
	for _, request := range page {
		var matching []runsapi.CommitRun
		for _, hash := range heads[request.ID] {
			matching = append(matching, bySHA[hash]...)
		}
		if len(matching) == 0 {
			continue
		}
		slices.SortFunc(matching, func(a, b runsapi.CommitRun) int {
			if currentA, currentB := a.CommitSHA == request.HeadSHA, b.CommitSHA == request.HeadSHA; currentA != currentB {
				if currentA {
					return -1
				}
				return 1
			}
			if n := b.CreatedAt.Compare(a.CreatedAt); n != 0 {
				return n
			}
			return bytes.Compare(b.ID[:], a.ID[:])
		})
		view := stacksapi.PullRequest{
			ID: request.Number, AuthorLogin: request.AuthorLogin, Title: request.Title, URL: request.URL,
			HeadBranch: request.HeadBranch, HeadBranchURL: request.HeadBranchURL, HeadSHA: request.HeadSHA, Draft: request.Draft,
			CreatedAt: request.CreatedAt, UpdatedAt: request.UpdatedAt, Runs: make([]kernel.ULID, 0, len(matching)),
		}
		for _, run := range matching {
			view.Runs = append(view.Runs, run.ID)
		}
		out = append(out, view)
	}
	return out, nil
}
