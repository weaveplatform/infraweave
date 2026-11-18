package azuredevops

import (
	"net/url"
	"strconv"
	"strings"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

func azureMetadata(r webhookRequest, out *vcsapi.PullRequest) {
	out.AuthorLogin = r.CreatedBy.login()
	out.Title, out.URL = r.Title, r.Links.Web.Href
	out.CreatedAt, out.UpdatedAt = r.CreationDate, r.UpdatedAt
	if out.UpdatedAt.IsZero() {
		out.UpdatedAt = r.ClosedDate
	}
	if out.UpdatedAt.IsZero() {
		out.UpdatedAt = r.CreationDate
	}
	switch r.Status {
	case "active":
		out.State = "open"
	case "completed":
		out.State = "merged"
	case "abandoned":
		out.State = "closed"
	}
	base := r.Repository.WebURL
	if base == "" {
		base = r.Repository.RemoteURL
	}
	if base != "" {
		if out.URL == "" {
			out.URL = strings.TrimSuffix(base, "/") + "/pullrequest/" + strconv.Itoa(r.PullRequestID)
		}
		if u, err := url.Parse(base); err == nil {
			q := u.Query()
			q.Set("version", "GB"+out.HeadBranch)
			u.RawQuery = q.Encode()
			out.HeadBranchURL = u.String()
		}
	}
}
