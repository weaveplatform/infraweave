package push

import (
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func requestFacts(r port.RequestFacts, action string, commented bool, affected int64) policyapi.PullRequestFacts {
	p := r.Snapshot.Request
	if commented {
		action = "commented"
	}
	request := policyapi.PullRequestFacts{
		ID:     int64(p.Number),
		Action: action,
		Author: p.AuthorLogin,
		Title:  p.Title,
		Base: commitFacts(
			r.Base,
		),
		Head:                commitFacts(r.Head),
		Closed:              p.State == "closed" || p.State == "merged" || p.State == "declined" || p.State == "completed" || p.State == "superseded",
		Draft:               p.Draft,
		Diff:                r.Files,
		Labels:              p.Labels,
		AffectedStacksCount: affected,
		HeadOwner:           known(p.HeadOwner),
	}
	if r.Actor != "" {
		request.ActionInitiator = known(r.Actor)
	}
	if r.Comment != nil {
		request.Comment = known(*r.Comment)
	}
	if r.Snapshot.Approved != nil {
		request.Approved = known(*r.Snapshot.Approved)
	}
	if r.Snapshot.Mergeable != nil {
		request.Mergeable = known(*r.Snapshot.Mergeable)
	}
	if r.Snapshot.Undiverged != nil {
		request.Undiverged = known(*r.Snapshot.Undiverged)
	}
	return request
}
