package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.RequestReader = (*Provider)(nil)

// ReadRequest reads the native request and exact diff references; it never
// resolves a mutable branch in place of an absent diff reference.
// ref: https://docs.gitlab.com/api/merge_requests/#retrieve-a-merge-request
func (p *Provider) ReadRequest(ctx context.Context, repo port.Repository, number int) (port.RequestSnapshot, error) {
	if number < 1 {
		return port.RequestSnapshot{}, port.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, pid, err := p.repo(repo)
	if err != nil {
		return port.RequestSnapshot{}, err
	}
	r, response, err := c.MergeRequests.GetMergeRequest(pid, int64(number), &gitlab.GetMergeRequestsOptions{IncludeDivergedCommitsCount: new(true)}, gitlab.WithContext(ctx))
	if err != nil {
		return port.RequestSnapshot{}, classify(response, err, "native merge request")
	}
	target, response, err := c.Projects.GetProject(pid, nil, gitlab.WithContext(ctx))
	if err != nil {
		return port.RequestSnapshot{}, classify(response, err, "native request target")
	}
	if r.IID != int64(number) || r.TargetProjectID != target.ID || r.SourceProjectID <= 0 {
		return port.RequestSnapshot{}, fmt.Errorf("%w: GitLab native request ownership", kernel.ErrUnavailable)
	}
	source := target
	if r.SourceProjectID != target.ID {
		source, response, err = c.Projects.GetProject(r.SourceProjectID, nil, gitlab.WithContext(ctx))
		if err != nil {
			return port.RequestSnapshot{}, classify(response, err, "native request source")
		}
	}
	out := port.RequestSnapshot{
		Base: r.DiffRefs.StartSha, Head: r.DiffRefs.HeadSha,
		BaseRepository: requestRepository(target), HeadRepository: requestRepository(source), Undiverged: new(r.DivergedCommitsCount == 0),
	}
	out.Request = vcsapi.PullRequest{
		Number: number, Title: r.Title, BaseBranch: r.TargetBranch, HeadBranch: r.SourceBranch, State: r.State, Draft: r.Draft,
		URL: r.WebURL, Labels: slices.Clone([]string(r.Labels)), HeadOwner: out.HeadRepository.Namespace, Fork: r.SourceProjectID != r.TargetProjectID,
	}
	if r.Author != nil {
		out.Request.AuthorLogin = r.Author.Username
		out.Request.AuthorID = strconv.FormatInt(r.Author.ID, 10)
	}
	if r.CreatedAt != nil {
		out.Request.CreatedAt = *r.CreatedAt
	}
	if r.UpdatedAt != nil {
		out.Request.UpdatedAt = *r.UpdatedAt
	}
	if source.WebURL != "" {
		out.Request.HeadBranchURL = strings.TrimSuffix(source.WebURL, "/") + "/-/tree/" + url.PathEscape(r.SourceBranch)
	}
	out.Request.HeadRepository = &out.HeadRepository
	switch r.DetailedMergeStatus {
	case "mergeable":
		out.Mergeable = new(true)
	case "conflict", "broken_status", "not_approved", "discussions_not_resolved", "ci_must_pass", "ci_still_running", "draft_status", "blocked_status", "external_status_checks":
		out.Mergeable = new(false)
	}
	approval, response, err := c.MergeRequestApprovals.GetConfiguration(pid, int64(number), gitlab.WithContext(ctx))
	if err == nil {
		out.Approved = new(approval.Approved)
	} else if response == nil || (response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusNotFound) {
		return port.RequestSnapshot{}, classify(response, err, "native request approval state")
	}
	return out, nil
}

func requestRepository(project *gitlab.Project) vcsapi.Repository {
	namespace, name := "", project.Path
	if index := strings.LastIndex(project.PathWithNamespace, "/"); index >= 0 {
		namespace, name = project.PathWithNamespace[:index], project.PathWithNamespace[index+1:]
	}
	return vcsapi.Repository{ID: strconv.FormatInt(project.ID, 10), Namespace: namespace, Name: name}
}
