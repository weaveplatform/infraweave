package bitbucketdc

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// ErrWebhookPayload refuses malformed consumed events without quoting payloads.
var ErrWebhookPayload = errors.New("bitbucketdc: invalid webhook payload")

var _ port.Webhooks = (*Provider)(nil)

// DeliveryID reads the stable Data Center request identity.
func (*Provider) DeliveryID(h http.Header, _ []byte) string { return h.Get("X-Request-Id") }

// Consumed excludes ping and every unsupported event before persistence.
func (*Provider) Consumed(event string) bool {
	switch event {
	case "repo:refs_changed", "pr:opened", "pr:from_ref_updated", "pr:modified", "pr:reviewer:approved", "pr:reviewer:unapproved", "pr:merged", "pr:declined", "pr:comment:added":
		return true
	}
	return false
}

// Event filters valid non-Git ref changes while retaining malformed consumed
// deliveries for the durable processor's parse-failure path.
func (p *Provider) Event(h http.Header, body []byte) string {
	event := h.Get("X-Event-Key")
	if event == "repo:refs_changed" {
		events, err := p.Parse(h, body)
		if err == nil && len(events) == 0 {
			return ""
		}
	}
	return event
}

// Repository reads the target identity; documented PR/comment payloads place
// it under toRef rather than providing a top-level repository.
func (*Provider) Repository(body []byte) string {
	var d struct {
		Repository *struct {
			ID int64 `json:"id"`
		} `json:"repository"`
		PullRequest struct {
			ToRef struct {
				Repository struct {
					ID int64 `json:"id"`
				} `json:"repository"`
			} `json:"toRef"`
		} `json:"pullRequest"`
	}
	if json.Unmarshal(body, &d) != nil {
		return ""
	}
	id := d.PullRequest.ToRef.Repository.ID
	if d.Repository != nil {
		id = d.Repository.ID
	}
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// Parse normalizes retained payloads without calling the provider.
func (p *Provider) Parse(h http.Header, body []byte) ([]vcsapi.Event, error) {
	event := h.Get("X-Event-Key")
	if !p.Consumed(event) {
		return nil, nil
	}
	var d webhookPayload
	if json.Unmarshal(body, &d) != nil || d.EventKey != event {
		return nil, ErrWebhookPayload
	}
	if event == "repo:refs_changed" {
		return dcPush(d)
	}
	e, err := dcRequest(d, event)
	if err != nil {
		return nil, err
	}
	return []vcsapi.Event{e}, nil
}

type webhookPayload struct {
	EventKey         string              `json:"eventKey"`
	Actor            webhookUser         `json:"actor"`
	Repository       *webhookRepository  `json:"repository"`
	Changes          []webhookChange     `json:"changes"`
	PullRequest      *webhookRequest     `json:"pullRequest"`
	Participant      *webhookParticipant `json:"participant"`
	PreviousFromHash string              `json:"previousFromHash"`
	Comment          *struct {
		Text   string      `json:"text"`
		Author webhookUser `json:"author"`
	} `json:"comment"`
}

type webhookRepository struct {
	Links   webhookLinks `json:"links"`
	ID      int64        `json:"id"`
	Slug    string       `json:"slug"`
	Project struct {
		Key string `json:"key"`
	} `json:"project"`
}

type webhookUser struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type webhookParticipant struct {
	User     webhookUser `json:"user"`
	Approved *bool       `json:"approved"`
	Status   string      `json:"status"`
}

type webhookRequest struct {
	Title        string               `json:"title"`
	State        string               `json:"state"`
	CreatedDate  int64                `json:"createdDate"`
	UpdatedDate  int64                `json:"updatedDate"`
	Links        webhookLinks         `json:"links"`
	ID           int                  `json:"id"`
	Draft        bool                 `json:"draft"`
	FromRef      webhookRef           `json:"fromRef"`
	ToRef        webhookRef           `json:"toRef"`
	Author       webhookParticipant   `json:"author"`
	Reviewers    []webhookParticipant `json:"reviewers"`
	Participants []webhookParticipant `json:"participants"`
}

type webhookRef struct {
	ID           string             `json:"id"`
	DisplayID    string             `json:"displayId"`
	Type         string             `json:"type"`
	LatestCommit string             `json:"latestCommit"`
	Repository   *webhookRepository `json:"repository"`
}

type webhookChange struct {
	Ref      webhookRef `json:"ref"`
	RefID    string     `json:"refId"`
	FromHash string     `json:"fromHash"`
	ToHash   string     `json:"toHash"`
}

func dcRepository(r *webhookRepository) (vcsapi.Repository, bool) {
	if r == nil {
		return vcsapi.Repository{}, false
	}
	return vcsapi.Repository{ID: strconv.FormatInt(r.ID, 10), Namespace: r.Project.Key, Name: r.Slug}, r.ID > 0 && r.Project.Key != "" && r.Slug != ""
}

func dcPush(d webhookPayload) ([]vcsapi.Event, error) {
	repo, ok := dcRepository(d.Repository)
	if !ok || len(d.Changes) == 0 {
		return nil, ErrWebhookPayload
	}
	out := make([]vcsapi.Event, 0, len(d.Changes))
	for _, c := range d.Changes {
		e, err := dcChange(c, repo, d.Actor.Name)
		if err != nil {
			return nil, err
		}
		if e.Type != "" {
			out = append(out, e)
		}
	}
	return out, nil
}

func dcChange(c webhookChange, repo vcsapi.Repository, author string) (vcsapi.Event, error) {
	id := c.Ref.ID
	if id == "" {
		id = c.RefID
	}
	kind := c.Ref.Type
	if kind == "" {
		switch {
		case strings.HasPrefix(id, "refs/heads/"):
			kind = "BRANCH"
		case strings.HasPrefix(id, "refs/tags/"):
			kind = "TAG"
		case id == "":
			return vcsapi.Event{}, ErrWebhookPayload
		default:
			return vcsapi.Event{}, nil
		}
	}
	if kind != "BRANCH" && kind != "TAG" {
		return vcsapi.Event{}, nil
	}
	name := c.Ref.DisplayID
	if name == "" {
		if kind == "BRANCH" {
			name = strings.TrimPrefix(id, "refs/heads/")
		} else {
			name = strings.TrimPrefix(id, "refs/tags/")
		}
	}
	if name == "" || c.FromHash == "" || c.ToHash == "" {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	e := vcsapi.Event{Type: "push", Repository: repo, Before: c.FromHash, Head: c.ToHash, Author: author}
	if kind == "TAG" {
		e.Type, e.Tag = "tag", name
	} else {
		e.Branch = name
	}
	return e, nil
}

func dcRequest(d webhookPayload, event string) (vcsapi.Event, error) {
	r := d.PullRequest
	if r == nil || r.ID <= 0 {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	if !port.ValidCommit(r.FromRef.LatestCommit) && event != "pr:merged" && event != "pr:declined" {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	source, sourceOK := dcRepository(r.FromRef.Repository)
	target, targetOK := dcRepository(r.ToRef.Repository)
	head, base := dcBranch(r.FromRef), dcBranch(r.ToRef)
	if !sourceOK || !targetOK || head == "" || base == "" {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	if d.Repository != nil && d.Repository.ID != r.ToRef.Repository.ID {
		return vcsapi.Event{}, ErrWebhookPayload
	}
	fork := source.ID != target.ID
	e := vcsapi.Event{Type: "pull_request", Repository: target, Head: r.FromRef.LatestCommit, Branch: head, Author: d.Actor.Name}
	if event == "pr:comment:added" {
		if d.Comment == nil {
			return vcsapi.Event{}, ErrWebhookPayload
		}
		e.Type, e.Action = "comment", "created"
		e.Comment = &vcsapi.Comment{Body: d.Comment.Text, Author: d.Comment.Author.Name, PullRequest: r.ID, Head: e.Head, BaseBranch: base, Fork: fork}
		return e, nil
	}
	e.Action = "edited"
	switch event {
	case "pr:opened":
		e.Action = "opened"
	case "pr:from_ref_updated":
		e.Action, e.Before = "synchronize", d.PreviousFromHash
	case "pr:merged":
		e.Action = "merged"
	case "pr:declined":
		e.Action = "closed"
	}
	// An OPEN state says nothing about conflicts, branch protection or
	// target ancestry. These facts remain false without payload proof.
	e.PullRequest = &vcsapi.PullRequest{
		Number:     r.ID,
		HeadBranch: head,
		BaseBranch: base,
		HeadOwner:  source.Namespace,
		Draft:      r.Draft,
		Fork:       fork,
		Approved:   dcApproved(d),
		Labels:     []string{},
	}
	dcMetadata(r, e.PullRequest)
	return e, nil
}

func dcBranch(ref webhookRef) string {
	if ref.DisplayID != "" {
		return ref.DisplayID
	}
	return strings.TrimPrefix(ref.ID, "refs/heads/")
}

func dcApproved(d webhookPayload) bool {
	r := d.PullRequest
	eligible := func(p webhookParticipant) bool {
		if r.Author.User.ID <= 0 || p.User.ID <= 0 || r.Author.User.ID == p.User.ID {
			return false
		}
		if p.Approved != nil {
			return *p.Approved
		}
		return p.Status == "APPROVED"
	}
	for _, group := range [][]webhookParticipant{r.Reviewers, r.Participants} {
		for _, p := range group {
			if d.Participant != nil && p.User.ID == d.Participant.User.ID {
				p = *d.Participant
			}
			if eligible(p) {
				return true
			}
		}
	}
	return d.Participant != nil && eligible(*d.Participant)
}
