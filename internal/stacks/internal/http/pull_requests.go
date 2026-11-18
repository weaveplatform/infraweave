package http

import (
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func requestViews(requests []stacksapi.PullRequest) []api.PullRequest {
	out := make([]api.PullRequest, 0, len(requests))
	for _, r := range requests {
		runs := make([]api.RunRef, 0, len(r.Runs))
		for _, id := range r.Runs {
			runs = append(runs, api.RunRef{Id: id.String()})
		}
		out = append(out, api.PullRequest{
			Id: r.ID, AuthorLogin: r.AuthorLogin, Title: r.Title, Url: r.URL,
			HeadBranch: r.HeadBranch, HeadBranchURL: r.HeadBranchURL, HeadSHA: r.HeadSHA, Draft: r.Draft,
			CreatedAt: unix(r.CreatedAt.Unix()), UpdatedAt: unix(r.UpdatedAt.Unix()), Runs: runs,
		})
	}
	return out
}
