package bitbucketcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type checkStatus struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	State       string `json:"state"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// Set uses the documented commit/key upsert. Repeating a post after a lost
// response updates that key instead of adding a second logical build status.
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
	rel := rp + "/commit/" + url.PathEscape(sha) + "/statuses/build"
	wanted := checkStatus{Key: r.Name, Name: r.Name, State: state, URL: r.URL, Description: port.CheckText(r.Summary, 255)}
	var current checkStatus
	err = p.decode(ctx, *repo.Integration, p.api+rel+"/"+url.PathEscape(r.Name), "recover commit status", &current)
	if err == nil && current == wanted {
		return r.Name, nil
	}
	if err != nil && !errors.Is(err, port.ErrNotFound) {
		return "", err
	}
	if err := p.checkPost(ctx, *repo.Integration, rel, wanted); err != nil {
		return "", err
	}
	return r.Name, nil
}

func (p *Provider) checkPost(ctx context.Context, in port.Integration, rel string, body checkStatus) error {
	if p.host != nil {
		return p.host
	}
	if in.Settings.Email == "" || strings.TrimSpace(string(in.Credential)) == "" {
		return ErrCredential
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("bitbucketcloud: encode commit status: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.api+rel, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("bitbucketcloud: commit status request: %w", err)
	}
	req.SetBasicAuth(in.Settings.Email, strings.TrimSpace(string(in.Credential)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: bitbucketcloud: commit status: %w", kernel.ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classify(resp, "set commit status")
	}
	var result checkStatus
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("bitbucketcloud: decode commit status: %w", err)
	}
	if result.Key != body.Key {
		return port.ErrCheckInput
	}
	return nil
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
