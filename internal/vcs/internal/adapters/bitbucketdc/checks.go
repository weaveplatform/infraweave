package bitbucketdc

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type checkStatus struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	State       string `json:"state"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// Set writes the repository-scoped build key. Posting the same commit/key
// updates the native status, including recovery after a successful lost reply.
func (p *Provider) Set(ctx context.Context, repo port.Repository, sha string, r port.CheckReport) (string, error) {
	state, ok := nativeCheckState(r.State)
	if !ok || !port.ValidCommit(sha) || r.Name == "" || r.URL == "" {
		return "", port.ErrCheckInput
	}
	rp, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rel := rp + "/commits/" + url.PathEscape(sha) + "/builds"
	wanted := checkStatus{Key: r.Name, Name: r.Name, State: state, URL: r.URL, Description: port.CheckText(r.Summary, 255)}
	var current checkStatus
	err = p.decode(ctx, *repo.Integration, rel, url.Values{"key": {r.Name}}, "recover commit build", &current)
	if err == nil && current == wanted {
		return r.Name, nil
	}
	if err != nil && !errors.Is(err, port.ErrNotFound) {
		return "", err
	}
	resp, err := p.send(ctx, *repo.Integration, http.MethodPost, API+rel, nil, wanted, "set commit build")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", classify(resp, "set commit build")
	}
	return r.Name, nil
}

func nativeCheckState(state port.CheckState) (string, bool) {
	switch state {
	case port.CheckQueued, port.CheckInProgress:
		return "INPROGRESS", true
	case port.CheckFailure, port.CheckCanceled:
		return "FAILED", true
	case port.CheckSuccess, port.CheckNeutral, port.CheckSkipped:
		return "SUCCESSFUL", true
	}
	return "", false
}

var _ port.Checks = (*Provider)(nil)
