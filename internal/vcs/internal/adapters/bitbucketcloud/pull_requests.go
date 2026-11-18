package bitbucketcloud

import (
	"net/url"
	"strings"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

func cloudMetadata(r *webhookRequest, out *vcsapi.PullRequest) {
	out.AuthorLogin = r.Author.Nickname
	out.Title, out.URL = r.Title, r.Links.HTML.Href
	out.CreatedAt, out.UpdatedAt = r.CreatedOn, r.UpdatedOn
	switch r.State {
	case "OPEN":
		out.State = "open"
	case "MERGED":
		out.State = "merged"
	case "DECLINED":
		out.State = "closed"
	}
	if r.Source.Repository != nil {
		if base := r.Source.Repository.Links.HTML.Href; base != "" {
			out.HeadBranchURL = strings.TrimSuffix(base, "/") + "/src/" + url.PathEscape(out.HeadBranch)
		}
	}
}
