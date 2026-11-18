package azuredevops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type (
	checkContext struct {
		Name  string `json:"name"`
		Genre string `json:"genre"`
	}
	checkStatus struct {
		ID          int          `json:"id,omitempty"`
		State       string       `json:"state"`
		Description string       `json:"description"`
		Context     checkContext `json:"context"`
		URL         string       `json:"targetUrl"`
		Iteration   int          `json:"iterationId,omitempty"`
	}
)

type checkIteration struct {
	ID     int `json:"id"`
	Source struct {
		SHA string `json:"commitId"`
	} `json:"sourceRefCommit"`
}

// Set updates the native context and the latest recorded PR iteration. The
// commit API exposes create/list rather than a fabricated update-ID route;
// unchanged contexts are recovered before posting another native history row.
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
	body := checkStatus{State: state, Description: port.CheckText(r.Summary, 255), Context: checkContext{Name: r.Name, Genre: "infraweave"}, URL: r.URL}
	id, err := p.commitCheck(ctx, *repo.Integration, rp+"/commits/"+url.PathEscape(sha)+"/statuses", body)
	if err != nil {
		return "", err
	}
	for _, number := range r.PullRequests {
		if number <= 0 {
			return "", port.ErrCheckInput
		}
		if err := p.pullRequestCheck(ctx, *repo.Integration, rp+"/pullRequests/"+strconv.Itoa(number), sha, body); err != nil {
			return "", err
		}
	}
	return strconv.Itoa(id), nil
}

func (p *Provider) commitCheck(ctx context.Context, in port.Integration, rel string, wanted checkStatus) (int, error) {
	var latest *checkStatus
	for skip := 0; ; skip += top {
		var page list[checkStatus]
		_, err := p.decode(ctx, in, rel, url.Values{"top": {strconv.Itoa(top)}, "skip": {strconv.Itoa(skip)}, "latestOnly": {"true"}}, "recover commit status", &page)
		if err != nil {
			return 0, err
		}
		for _, v := range page.Value {
			if v.Context == wanted.Context && (latest == nil || v.ID > latest.ID) {
				latest = new(v)
			}
		}
		if len(page.Value) < top {
			break
		}
	}
	if latest != nil && sameCheck(*latest, wanted) {
		return latest.ID, nil
	}
	posted, err := p.checkPost(ctx, in, rel, wanted)
	return posted.ID, err
}

func (p *Provider) pullRequestCheck(ctx context.Context, in port.Integration, rel, sha string, wanted checkStatus) error {
	var iterations list[checkIteration]
	if _, err := p.decode(ctx, in, rel+"/iterations", url.Values{}, "latest PR iteration", &iterations); err != nil {
		return err
	}
	var latest checkIteration
	for _, v := range iterations.Value {
		if v.ID > latest.ID {
			latest = v
		}
	}
	if latest.ID <= 0 || !port.ValidCommit(latest.Source.SHA) {
		return port.ErrCheckInput
	}
	// A webhook snapshot may lag a later provider push. Never paint that new
	// iteration with a result calculated for the preceding head.
	if latest.Source.SHA != sha {
		return nil
	}
	wanted.Iteration = latest.ID
	path := rel + "/iterations/" + strconv.Itoa(latest.ID) + "/statuses"
	var statuses list[checkStatus]
	if _, err := p.decode(ctx, in, path, url.Values{}, "recover PR status", &statuses); err != nil {
		return err
	}
	var found *checkStatus
	for _, v := range statuses.Value {
		if v.Context == wanted.Context && (found == nil || v.ID > found.ID) {
			found = new(v)
		}
	}
	if found != nil && sameCheck(*found, wanted) {
		return nil
	}
	_, err := p.checkPost(ctx, in, path, wanted)
	return err
}

func sameCheck(a, b checkStatus) bool {
	return a.ID > 0 && a.State == b.State && a.Description == b.Description && a.Context == b.Context && a.URL == b.URL && a.Iteration == b.Iteration
}

func (p *Provider) checkPost(ctx context.Context, in port.Integration, rel string, body checkStatus) (checkStatus, error) {
	var out checkStatus
	o, err := orgOf(in.Settings)
	if err != nil {
		return out, err
	}
	token := strings.TrimSpace(string(in.Credential))
	if token == "" {
		return out, ErrCredential
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return out, fmt.Errorf("azuredevops: encode commit check: %w", err)
	}
	req, err := http.NewRequestWithContext(context.WithValue(ctx, baseKey{}, o.base), http.MethodPost, o.api+rel+"?api-version="+APIVersion, bytes.NewReader(encoded))
	if err != nil {
		return out, fmt.Errorf("azuredevops: commit check request: %w", err)
	}
	req.SetBasicAuth("", token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return out, fmt.Errorf("%w: azuredevops: commit check: %w", kernel.ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, classify(resp, "post commit check")
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("azuredevops: decode commit check: %w", err)
	}
	if out.ID <= 0 || out.Context != body.Context || out.Iteration != body.Iteration {
		return out, port.ErrCheckInput
	}
	return out, nil
}

func nativeCheckState(state port.CheckState) (string, bool) {
	switch state {
	case port.CheckQueued, port.CheckInProgress:
		return "pending", true
	case port.CheckFailure, port.CheckCanceled:
		return "failed", true
	case port.CheckSuccess, port.CheckNeutral:
		return "succeeded", true
	case port.CheckSkipped:
		return "notApplicable", true
	}
	return "", false
}

var _ port.Checks = (*Provider)(nil)
