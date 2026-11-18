package bitbucketdc

import (
	"net/url"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

type webhookLinks struct {
	Self []struct {
		Href string `json:"href"`
	} `json:"self"`
}

func dcMetadata(r *webhookRequest, out *vcsapi.PullRequest) {
	out.AuthorLogin = r.Author.User.Name
	out.Title = r.Title
	if len(r.Links.Self) > 0 {
		out.URL = r.Links.Self[0].Href
	}
	if r.CreatedDate > 0 {
		out.CreatedAt = time.UnixMilli(r.CreatedDate)
	}
	if r.UpdatedDate > 0 {
		out.UpdatedAt = time.UnixMilli(r.UpdatedDate)
	}
	switch r.State {
	case "OPEN":
		out.State = "open"
	case "MERGED":
		out.State = "merged"
	case "DECLINED":
		out.State = "closed"
	}
	if repo := r.FromRef.Repository; repo != nil && len(repo.Links.Self) > 0 {
		if u, err := url.Parse(repo.Links.Self[0].Href); err == nil {
			q := u.Query()
			q.Set("at", "refs/heads/"+out.HeadBranch)
			u.RawQuery = q.Encode()
			out.HeadBranchURL = u.String()
		}
	}
}
