package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/go-github/v93/github"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// ErrWebhookPayload refuses malformed or incomplete consumed deliveries.
// It contains no payload bytes, which may include private comment text.
var ErrWebhookPayload = errors.New("github: malformed webhook payload")

// webhook retains only documented fields consumed by normalization. GitHub's
// SDK owns its nested payload types; changes.login also serves organization
// renames, whose SDK event does not currently retain changes.
//
//nolint:tagliatelle // GitHub owns the webhook JSON field names.
type webhook struct {
	Action              string                      `json:"action"`
	Ref                 string                      `json:"ref"`
	Before              string                      `json:"before"`
	After               string                      `json:"after"`
	Forced              bool                        `json:"forced"`
	Repository          *github.Repository          `json:"repository"`
	Sender              *github.User                `json:"sender"`
	PullRequest         *github.PullRequest         `json:"pull_request"`
	Issue               *github.Issue               `json:"issue"`
	Comment             *github.IssueComment        `json:"comment"`
	CheckRun            *github.CheckRun            `json:"check_run"`
	RequestedAction     *github.RequestedAction     `json:"requested_action"`
	Installation        *github.Installation        `json:"installation"`
	Repositories        []*github.Repository        `json:"repositories"`
	RepositoriesAdded   []*github.Repository        `json:"repositories_added"`
	RepositoriesRemoved []*github.Repository        `json:"repositories_removed"`
	Account             *github.User                `json:"account"`
	Organization        *github.Organization        `json:"organization"`
	Changes             *github.InstallationChanges `json:"changes"`
}

// Parse normalizes one delivery without provider calls or account mutations.
// Unsupported families/actions are empty. Payloads alone do not establish a
// repository's aggregate approval threshold, so Approved remains false.
func (p *Provider) Parse(h http.Header, body []byte) ([]vcsapi.Event, error) {
	kind := h.Get("X-GitHub-Event")
	if !p.Consumed(kind) {
		return nil, nil
	}
	var d *webhook
	if json.Unmarshal(body, &d) != nil || d == nil {
		return nil, ErrWebhookPayload
	}
	if !d.routingValid(kind) {
		return nil, ErrWebhookPayload
	}
	if !d.consumed(kind) {
		return nil, nil
	}
	event, err := d.normalize(kind)
	if err != nil {
		return nil, err
	}
	return []vcsapi.Event{event}, nil
}

func (d *webhook) normalize(kind string) (vcsapi.Event, error) {
	e := vcsapi.Event{Type: kind, Action: d.Action, Author: d.Sender.GetLogin()}
	switch kind {
	case "installation", "installation_repositories":
		return d.installation(e)
	case "installation_target", "organization":
		return d.owner(e, kind)
	}
	e.Repository = repository(d.Repository)
	if !validRepository(e.Repository) {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	switch kind {
	case "push":
		if d.Before == "" || d.After == "" {
			return vcsapi.Event{}, ErrWebhookPayload
		}
		e.Before, e.Head, e.Forced = d.Before, d.After, d.Forced
		if branch, ok := strings.CutPrefix(d.Ref, "refs/heads/"); ok {
			e.Branch = branch
		} else {
			e.Type, e.Tag = "tag", strings.TrimPrefix(d.Ref, "refs/tags/")
		}
	case "pull_request", "pull_request_review":
		return d.pullRequest(e)
	case "issue_comment":
		if d.Comment == nil || d.Issue.GetNumber() <= 0 {
			return vcsapi.Event{}, ErrWebhookPayload
		}
		e.Type = "comment"
		e.Comment = &vcsapi.Comment{Body: d.Comment.GetBody(), Author: d.Comment.GetUser().GetLogin(), PullRequest: d.Issue.GetNumber()}
	case "check_run":
		if d.RequestedAction == nil || d.RequestedAction.Identifier == "" || d.CheckRun == nil {
			return vcsapi.Event{}, ErrWebhookPayload
		}
		e.Type, e.Head = "check_action", d.CheckRun.GetHeadSHA()
		e.CheckAction = &vcsapi.CheckAction{Action: d.RequestedAction.Identifier, ExternalID: d.CheckRun.GetExternalID()}
	case "repository":
		e.Type = "repository_renamed"
	}
	return e, nil
}

func (d *webhook) pullRequest(e vcsapi.Event) (vcsapi.Event, error) {
	pr := d.PullRequest
	if pr.GetNumber() <= 0 || pr.GetHead() == nil || pr.GetBase() == nil {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	e.Type, e.Head, e.Branch, e.Before = "pull_request", pr.GetHead().GetSHA(), pr.GetHead().GetRef(), d.Before
	if e.Action == "closed" && pr.GetMerged() {
		e.Action = "merged"
	}
	labels := make([]string, 0, len(pr.Labels))
	for _, label := range pr.Labels {
		labels = append(labels, label.GetName())
	}
	// mergeable alone says only that conflicts are absent. "clean" also
	// excludes branch-protection blockers; null and other states prove neither.
	clean := pr.GetMergeable() && pr.GetMergeableState() == "clean"
	e.PullRequest = &vcsapi.PullRequest{
		Number: pr.GetNumber(), HeadBranch: pr.GetHead().GetRef(), BaseBranch: pr.GetBase().GetRef(),
		HeadOwner: pr.GetHead().GetRepo().GetOwner().GetLogin(), Draft: pr.GetDraft(),
		Fork:      pr.GetHead().GetRepo().GetID() != pr.GetBase().GetRepo().GetID(),
		Mergeable: clean, Undiverged: clean, Labels: labels,
	}
	return e, nil
}

func repository(r *github.Repository) vcsapi.Repository {
	if r.GetID() <= 0 {
		return vcsapi.Repository{}
	}
	namespace := r.GetOwner().GetLogin()
	if namespace == "" {
		if owner, _, ok := strings.Cut(r.GetFullName(), "/"); ok {
			namespace = owner
		}
	}
	return vcsapi.Repository{ID: strconv.FormatInt(r.GetID(), 10), Namespace: namespace, Name: r.GetName()}
}

func validRepository(r vcsapi.Repository) bool {
	return r.ID != "" && r.Namespace != "" && r.Name != ""
}

func repositories(rs []*github.Repository) []vcsapi.Repository {
	out := make([]vcsapi.Repository, 0, len(rs))
	for _, r := range rs {
		out = append(out, repository(r))
	}
	return out
}

func (d *webhook) installation(e vcsapi.Event) (vcsapi.Event, error) {
	in := d.Installation
	if in.GetID() <= 0 || in.GetAccount().GetID() <= 0 || in.GetAccount().GetLogin() == "" {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	e.Type = "installation"
	e.Installation = &vcsapi.Installation{
		ID: strconv.FormatInt(in.GetID(), 10), Action: d.Action,
		Owner:        vcsapi.Owner{ID: strconv.FormatInt(in.GetAccount().GetID(), 10), Login: in.GetAccount().GetLogin()},
		Repositories: repositories(d.Repositories), Added: repositories(d.RepositoriesAdded), Removed: repositories(d.RepositoriesRemoved),
	}
	for _, group := range [][]vcsapi.Repository{e.Installation.Repositories, e.Installation.Added, e.Installation.Removed} {
		for _, r := range group {
			if !validRepository(r) {
				return vcsapi.Event{}, ErrWebhookPayload
			}
		}
	}
	return e, nil
}

func (d *webhook) owner(e vcsapi.Event, kind string) (vcsapi.Event, error) {
	id, login := d.Account.GetID(), d.Account.GetLogin()
	if kind == "organization" {
		id, login = d.Organization.GetID(), d.Organization.GetLogin()
	}
	previous := d.Changes.GetLogin().GetFrom()
	if id <= 0 || login == "" || previous == "" {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	e.Type = "owner_renamed"
	e.OwnerRename = &vcsapi.OwnerRename{ID: strconv.FormatInt(id, 10), PreviousLogin: previous, Login: login}
	return e, nil
}
