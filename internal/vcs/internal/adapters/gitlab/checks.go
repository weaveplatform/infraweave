package gitlab

import (
	"context"
	"strconv"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Set uses GitLab's external status context. It never creates a CI pipeline;
// GitLab itself chooses the native history for repeated terminal statuses.
func (p *Provider) Set(ctx context.Context, repo port.Repository, sha string, r port.CheckReport) (string, error) {
	state, ok := nativeCheckState(r.State)
	if !ok || !port.ValidCommit(sha) || r.Name == "" || r.URL == "" {
		return "", port.ErrCheckInput
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, project, err := p.repo(repo)
	if err != nil {
		return "", err
	}
	desc := port.CheckText(r.Summary, 255)
	opts := &gitlab.GetCommitStatusesOptions{Name: new(r.Name), ListOptions: gitlab.ListOptions{PerPage: 100}}
	for {
		list, resp, err := c.Commits.GetCommitStatuses(project, sha, opts, gitlab.WithContext(ctx))
		if err != nil {
			return "", classify(resp, err, "recover commit status")
		}
		for _, v := range list {
			if v != nil && v.ID > 0 && v.SHA == sha && v.Name == r.Name && v.Status == string(state) && v.TargetURL == r.URL && v.Description == desc {
				return strconv.FormatInt(v.ID, 10), nil
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	v, resp, err := c.Commits.SetCommitStatus(
		project,
		sha,
		&gitlab.SetCommitStatusOptions{State: state, Name: new(r.Name), TargetURL: new(r.URL), Description: new(desc)},
		gitlab.WithContext(ctx),
	)
	if err != nil {
		return "", classify(resp, err, "set commit status")
	}
	if v == nil || v.ID <= 0 || v.SHA != sha || v.Name != r.Name {
		return "", port.ErrCheckInput
	}
	return strconv.FormatInt(v.ID, 10), nil
}

func nativeCheckState(state port.CheckState) (gitlab.BuildStateValue, bool) {
	switch state {
	case port.CheckQueued:
		return gitlab.BuildStateValue("pending"), true
	case port.CheckInProgress:
		return gitlab.BuildStateValue("running"), true
	case port.CheckFailure:
		return gitlab.BuildStateValue("failed"), true
	case port.CheckCanceled:
		return gitlab.BuildStateValue("canceled"), true
	case port.CheckSkipped:
		return gitlab.BuildStateValue("skipped"), true
	case port.CheckSuccess, port.CheckNeutral:
		return gitlab.BuildStateValue("success"), true
	}
	return "", false
}

var _ port.Checks = (*Provider)(nil)
