package github

import (
	"net/url"
	"strings"

	"github.com/google/go-github/v93/github"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

func requestMetadata(pr *github.PullRequest, out *vcsapi.PullRequest) {
	out.AuthorLogin = pr.GetUser().GetLogin()
	out.Title, out.URL = pr.GetTitle(), pr.GetHTMLURL()
	out.CreatedAt, out.UpdatedAt = pr.GetCreatedAt().Time, pr.GetUpdatedAt().Time
	out.State = pr.GetState()
	if pr.GetMerged() {
		out.State = "merged"
	}
	if base := pr.GetHead().GetRepo().GetHTMLURL(); base != "" {
		out.HeadBranchURL = strings.TrimSuffix(base, "/") + "/tree/" + url.PathEscape(out.HeadBranch)
	}
}
