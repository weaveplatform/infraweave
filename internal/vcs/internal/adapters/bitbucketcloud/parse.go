package bitbucketcloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// ErrWebhookPayload is a consumed delivery that cannot be normalized. Its
// message deliberately carries none of the retained payload's values.
var ErrWebhookPayload = errors.New("bitbucketcloud: invalid webhook payload")

var _ port.Webhooks = (*Provider)(nil)

// DeliveryID is stable across Bitbucket's automatic and explicit redeliveries.
func (*Provider) DeliveryID(h http.Header, _ []byte) string { return h.Get("X-Request-UUID") }

// Consumed names the documented Cloud events consumed by push policies.
func (*Provider) Consumed(event string) bool {
	switch event {
	case "repo:push", "pullrequest:created", "pullrequest:updated", "pullrequest:approved",
		"pullrequest:unapproved", "pullrequest:fulfilled", "pullrequest:rejected", "pullrequest:comment_created":
		return true
	}
	return false
}

// Event filters valid non-Git ref changes. A malformed consumed body retains
// its event name so intake stores it and the processor records a parse failure.
func (p *Provider) Event(h http.Header, body []byte) string {
	event := h.Get("X-Event-Key")
	if event == "repo:push" {
		events, err := p.Parse(h, body)
		if err == nil && len(events) == 0 {
			return ""
		}
	}
	return event
}

// Repository retains the UUID rather than a path that a rename can change.
func (*Provider) Repository(body []byte) string {
	var d struct {
		Repository struct {
			UUID string `json:"uuid"`
		} `json:"repository"`
	}
	if json.Unmarshal(body, &d) != nil {
		return ""
	}
	return d.Repository.UUID
}

// Parse normalizes only the retained event header and body, without API calls.
func (p *Provider) Parse(h http.Header, body []byte) ([]vcsapi.Event, error) {
	event := h.Get("X-Event-Key")
	if !p.Consumed(event) {
		return nil, nil
	}
	var d webhookPayload
	if json.Unmarshal(body, &d) != nil {
		return nil, ErrWebhookPayload
	}
	repo, ok := cloudRepository(d.Repository)
	if !ok {
		return nil, ErrWebhookPayload
	}
	if event == "repo:push" {
		return cloudPush(d, repo)
	}
	e, err := cloudRequest(d, event, repo)
	if err != nil {
		return nil, err
	}
	return []vcsapi.Event{e}, nil
}

// Cloud owns the snake_case JSON names in these transport-only structures.
//
//nolint:tagliatelle
type webhookPayload struct {
	Actor      webhookUser        `json:"actor"`
	Repository *webhookRepository `json:"repository"`
	Push       *struct {
		Changes []webhookChange `json:"changes"`
	} `json:"push"`
	PullRequest *webhookRequest `json:"pullrequest"`
	Approval    *struct {
		User webhookUser `json:"user"`
	} `json:"approval"`
	Comment *struct {
		User    webhookUser `json:"user"`
		Content struct {
			Raw string `json:"raw"`
		} `json:"content"`
	} `json:"comment"`
}

//nolint:tagliatelle // provider-owned transport names
type webhookRepository struct {
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
	UUID      string `json:"uuid"`
	Slug      string `json:"slug"`
	FullName  string `json:"full_name"`
	Workspace struct {
		Slug string `json:"slug"`
	} `json:"workspace"`
}

//nolint:tagliatelle // provider-owned transport names
type webhookUser struct {
	UUID      string `json:"uuid"`
	AccountID string `json:"account_id"`
	Nickname  string `json:"nickname"`
}

type webhookRequest struct {
	Title     string    `json:"title"`
	State     string    `json:"state"`
	CreatedOn time.Time `json:"created_on"` //nolint:tagliatelle // Bitbucket Cloud's native field.
	UpdatedOn time.Time `json:"updated_on"` //nolint:tagliatelle // Bitbucket Cloud's native field.
	Links     struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
	ID           int         `json:"id"`
	Author       webhookUser `json:"author"`
	Draft        bool        `json:"draft"`
	Source       webhookSide `json:"source"`
	Destination  webhookSide `json:"destination"`
	Participants []struct {
		User     webhookUser `json:"user"`
		Approved bool        `json:"approved"`
	} `json:"participants"`
}

type webhookSide struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
	Commit struct {
		Hash string `json:"hash"`
	} `json:"commit"`
	Repository *webhookRepository `json:"repository"`
}

type webhookChange struct {
	Old    json.RawMessage `json:"old"`
	New    json.RawMessage `json:"new"`
	Forced bool            `json:"forced"`
}

type webhookRef struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Target struct {
		Hash string `json:"hash"`
	} `json:"target"`
}

func cloudRepository(r *webhookRepository) (vcsapi.Repository, bool) {
	if r == nil {
		return vcsapi.Repository{}, false
	}
	ns, name := r.Workspace.Slug, r.Slug
	if before, after, ok := strings.Cut(r.FullName, "/"); ok {
		if ns == "" {
			ns = before
		}
		if name == "" {
			name = after
		}
	}
	return vcsapi.Repository{ID: r.UUID, Namespace: ns, Name: name}, r.UUID != "" && ns != "" && name != ""
}

func cloudPush(d webhookPayload, repo vcsapi.Repository) ([]vcsapi.Event, error) {
	if d.Push == nil || len(d.Push.Changes) == 0 {
		return nil, ErrWebhookPayload
	}
	out := make([]vcsapi.Event, 0, len(d.Push.Changes))
	for _, c := range d.Push.Changes {
		e, err := cloudChange(c, repo, d.Actor.Nickname)
		if err != nil {
			return nil, err
		}
		if e.Type != "" {
			out = append(out, e)
		}
	}
	return out, nil
}

const zeroHash = "0000000000000000000000000000000000000000"

func cloudChange(c webhookChange, repo vcsapi.Repository, author string) (vcsapi.Event, error) {
	var oldRef, newRef *webhookRef
	if len(c.Old) == 0 || len(c.New) == 0 || json.Unmarshal(c.Old, &oldRef) != nil || json.Unmarshal(c.New, &newRef) != nil {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	r := newRef
	if r == nil {
		r = oldRef
	}
	if r == nil || r.Type == "" {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	kind := ""
	switch r.Type {
	case "branch":
		kind = "push"
	case "tag", "annotated_tag":
		kind = "tag"
	default:
		return vcsapi.Event{}, nil
	}
	for _, ref := range []*webhookRef{oldRef, newRef} {
		if ref != nil && (ref.Name == "" || ref.Target.Hash == "" || ref.Type != r.Type) {
			return vcsapi.Event{}, ErrWebhookPayload
		}
	}
	e := vcsapi.Event{Type: kind, Repository: repo, Before: zeroHash, Head: zeroHash, Forced: c.Forced, Author: author}
	if oldRef != nil {
		e.Before = oldRef.Target.Hash
	}
	if newRef != nil {
		e.Head = newRef.Target.Hash
	}
	if kind == "tag" {
		e.Tag = r.Name
	} else {
		e.Branch = r.Name
	}
	return e, nil
}

func cloudRequest(d webhookPayload, event string, repo vcsapi.Repository) (vcsapi.Event, error) {
	r := d.PullRequest
	if r == nil || r.ID <= 0 || r.Source.Branch.Name == "" || r.Destination.Branch.Name == "" {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	if !port.ValidCommit(r.Source.Commit.Hash) && event != "pullrequest:fulfilled" && event != "pullrequest:rejected" {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	source, sourceOK := cloudRepository(r.Source.Repository)
	target, targetOK := cloudRepository(r.Destination.Repository)
	if !sourceOK || !targetOK || !strings.EqualFold(repo.ID, target.ID) {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	fork := !strings.EqualFold(source.ID, target.ID)
	e := vcsapi.Event{Type: "pull_request", Repository: repo, Head: r.Source.Commit.Hash, Branch: r.Source.Branch.Name, Author: d.Actor.Nickname}
	if event == "pullrequest:comment_created" {
		if d.Comment == nil {
			return vcsapi.Event{}, ErrWebhookPayload
		}
		e.Type, e.Action = "comment", "created"
		e.Comment = &vcsapi.Comment{
			Body:        d.Comment.Content.Raw,
			Author:      d.Comment.User.Nickname,
			PullRequest: r.ID,
			Head:        e.Head,
			BaseBranch:  r.Destination.Branch.Name,
			Fork:        fork,
		}
		return e, nil
	}
	e.Action = "edited"
	switch event {
	case "pullrequest:created":
		e.Action = "opened"
	case "pullrequest:fulfilled":
		e.Action = "merged"
	case "pullrequest:rejected":
		e.Action = "closed"
	}
	// OPEN and an individual approval prove neither branch ancestry nor
	// conflict/branch-protection requirements. No provider fetch runs here.
	e.PullRequest = &vcsapi.PullRequest{
		Number:     r.ID,
		HeadBranch: r.Source.Branch.Name,
		BaseBranch: r.Destination.Branch.Name,
		HeadOwner:  source.Namespace,
		Draft:      r.Draft,
		Fork:       fork,
		Approved:   cloudApproved(d, event),
		Labels:     []string{},
	}
	cloudMetadata(r, e.PullRequest)
	return e, nil
}

func cloudApproved(d webhookPayload, event string) bool {
	r := d.PullRequest
	for _, participant := range r.Participants {
		if !participant.Approved || !cloudNonAuthor(r.Author, participant.User) {
			continue
		}
		if event == "pullrequest:unapproved" && d.Approval != nil && cloudSameUser(participant.User, d.Approval.User) {
			continue
		}
		return true
	}
	return event == "pullrequest:approved" && d.Approval != nil && cloudNonAuthor(r.Author, d.Approval.User)
}

func cloudSameUser(a, b webhookUser) bool {
	if a.UUID != "" && b.UUID != "" {
		return strings.EqualFold(a.UUID, b.UUID)
	}
	return a.AccountID != "" && b.AccountID != "" && a.AccountID == b.AccountID
}

func cloudNonAuthor(author, reviewer webhookUser) bool {
	if author.UUID != "" && reviewer.UUID != "" {
		return !strings.EqualFold(author.UUID, reviewer.UUID)
	}
	return author.AccountID != "" && reviewer.AccountID != "" && author.AccountID != reviewer.AccountID
}
