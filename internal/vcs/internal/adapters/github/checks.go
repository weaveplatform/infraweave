package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// checkBody deliberately includes an empty actions array. Omitting it on an
// update leaves previously offered actions visible after promotion is disabled.
//
//nolint:tagliatelle // GitHub owns the native JSON names.
type checkBody struct {
	Name        string                 `json:"name"`
	HeadSHA     string                 `json:"head_sha,omitempty"`
	DetailsURL  string                 `json:"details_url"`
	ExternalID  string                 `json:"external_id"`
	Status      string                 `json:"status"`
	Conclusion  *string                `json:"conclusion,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Output      *github.CheckRunOutput `json:"output"`
	Actions     []port.CheckAction     `json:"actions"`
}

// Set recovers by immutable run marker as well as name/stack identity. Native
// status history on the same commit cannot make one run adopt another's check.
func (p *Provider) Set(ctx context.Context, repo port.Repository, sha string, report port.CheckReport) (string, error) {
	status, conclusion, ok := nativeCheckState(report.State)
	if !ok || !port.ValidCommit(sha) || report.Name == "" || report.RecoveryID == "" || report.URL == "" {
		return "", port.ErrCheckInput
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	a, installation, err := p.installationOf(ctx, repo)
	if err != nil {
		return "", err
	}
	c := p.installationFor(a, installation, CheckWrite)
	found, err := findCheck(ctx, c, repo, sha, report)
	if err != nil {
		return "", err
	}
	body := checkBody{
		Name: report.Name, DetailsURL: report.URL, ExternalID: report.ExternalID, Status: status, Actions: report.Actions,
		Output: &github.CheckRunOutput{Title: new(port.CheckText(report.Title, 255)), Summary: new(port.CheckText(report.Summary, 65000)), Text: new(checkMarker(report))},
	}
	if body.Actions == nil {
		body.Actions = []port.CheckAction{}
	}
	if conclusion != "" {
		body.Conclusion = &conclusion
		body.CompletedAt = new(time.Now().UTC())
	}
	method, path := http.MethodPost, fmt.Sprintf("repos/%s/%s/check-runs", repo.Namespace, repo.Name)
	if found != nil {
		method = http.MethodPatch
		path += "/" + strconv.FormatInt(found.GetID(), 10)
	} else {
		body.HeadSHA = sha
	}
	kept, err := writeCheck(ctx, c, method, path, body)
	// Some GitHub installations refuse reopening a concluded check. A new
	// native generation retains the same visible name and recovery marker.
	if err != nil && found != nil && found.GetStatus() == "completed" && status != "completed" {
		e := &github.ErrorResponse{}
		if errors.As(err, &e) && e.Response != nil && e.Response.StatusCode == http.StatusUnprocessableEntity {
			body.HeadSHA = sha
			kept, err = writeCheck(ctx, c, http.MethodPost, fmt.Sprintf("repos/%s/%s/check-runs", repo.Namespace, repo.Name), body)
		}
	}
	if err != nil {
		return "", classify(err, "set commit check")
	}
	if kept == nil || kept.GetID() <= 0 || kept.GetHeadSHA() != sha || kept.GetName() != report.Name {
		return "", port.ErrCheckInput
	}
	return strconv.FormatInt(kept.GetID(), 10), nil
}

func checkMarker(r port.CheckReport) string { return "<!-- " + r.RecoveryID + " -->" }

func findCheck(ctx context.Context, c *github.Client, repo port.Repository, sha string, r port.CheckReport) (*github.CheckRun, error) {
	opts := &github.ListCheckRunsOptions{CheckName: new(r.Name), Filter: new("all"), ListOptions: github.ListOptions{PerPage: 100}}
	var found *github.CheckRun
	for {
		list, resp, err := c.Checks.ListCheckRunsForRef(ctx, repo.Namespace, repo.Name, sha, opts)
		if err != nil {
			return nil, classify(err, "recover commit check")
		}
		if list == nil {
			return nil, port.ErrCheckInput
		}
		for _, v := range list.CheckRuns {
			if v != nil && v.GetID() > 0 && v.GetHeadSHA() == sha && v.GetName() == r.Name && v.GetExternalID() == r.ExternalID &&
				strings.Contains(v.GetOutput().GetText(), checkMarker(r)) &&
				(found == nil || v.GetID() > found.GetID()) {
				found = v
			}
		}
		if resp.NextPage == 0 {
			return found, nil
		}
		opts.Page = resp.NextPage
	}
}

func writeCheck(ctx context.Context, c *github.Client, method, path string, body checkBody) (*github.CheckRun, error) {
	req, err := c.NewRequest(ctx, method, path, body)
	if err != nil {
		return nil, fmt.Errorf("github: encode check: %w", err)
	}
	var result *github.CheckRun
	_, err = c.Do(req, &result)
	return result, err //nolint:wrapcheck // Set classifies the native error, including reopen refusal
}

func nativeCheckState(state port.CheckState) (status, conclusion string, ok bool) {
	switch state {
	case port.CheckQueued:
		return "queued", "", true
	case port.CheckInProgress:
		return "in_progress", "", true
	case port.CheckSuccess, port.CheckNeutral, port.CheckFailure, port.CheckSkipped:
		return "completed", string(state), true
	case port.CheckCanceled:
		return "completed", "cancelled", true
	}
	return "", "", false
}

var _ port.Checks = (*Provider)(nil)
