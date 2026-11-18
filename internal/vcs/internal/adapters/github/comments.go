package github

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// CommentClientMaxBytes is a conservative client budget: the issue-comment REST
// schema does not promise a numeric native maximum. Keep UTF-8 bytes bounded.
// ref: https://docs.github.com/en/rest/issues/comments#create-an-issue-comment
const CommentClientMaxBytes = 60_000

// ref: https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/organizing-information-with-collapsed-sections
func (*Provider) Presentation() port.CommentPresentation {
	return port.CommentPresentation{MaxBytes: CommentClientMaxBytes, Details: true}
}

func (p *Provider) Post(ctx context.Context, repo port.Repository, r port.CommentRequest) (port.CommentReference, error) {
	return p.comment(ctx, repo, r, false)
}

func (p *Provider) Upsert(ctx context.Context, repo port.Repository, r port.CommentRequest) (port.CommentReference, error) {
	return p.comment(ctx, repo, r, true)
}

func (p *Provider) comment(ctx context.Context, repo port.Repository, r port.CommentRequest, edit bool) (port.CommentReference, error) {
	if err := port.ValidateCommentRequest(r, p.Presentation()); err != nil {
		return port.CommentReference{}, port.ErrCommentInput
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	a, id, err := p.installationOf(ctx, repo)
	if err != nil {
		return port.CommentReference{}, err
	}
	c := p.installationFor(a, id, CommentWrite)
	pr, _ := strconv.Atoi(r.PullRequest) //nolint:errcheck // validated above
	var found *github.IssueComment
	if r.Existing != nil {
		id, err := strconv.ParseInt(r.Existing.ID, 10, 64)
		if err != nil || id <= 0 {
			return port.CommentReference{}, port.ErrCommentInput
		}
		note, resp, err := c.Issues.GetComment(ctx, repo.Namespace, repo.Name, id)
		switch {
		case err != nil:
			if resp == nil || resp.StatusCode != http.StatusNotFound {
				return port.CommentReference{}, classify(err, "get comment")
			}
		case note == nil || !strings.HasSuffix(note.GetIssueURL(), "/issues/"+r.PullRequest) || !port.HasCommentMarker(note.GetBody(), r.DedupKey):
			return port.CommentReference{}, port.ErrCommentInput
		default:
			found = note
		}
	}
	if found == nil {
		found, err = findComment(ctx, c, repo, pr, r.DedupKey)
		if err != nil {
			return port.CommentReference{}, err
		}
	}
	if found != nil && !edit {
		return githubCommentRef(found)
	}
	var note *github.IssueComment
	if found == nil {
		note, _, err = c.Issues.CreateComment(ctx, repo.Namespace, repo.Name, pr, github.IssueCommentRequest{Body: r.Body})
	} else {
		note, _, err = c.Issues.UpdateComment(ctx, repo.Namespace, repo.Name, found.GetID(), github.IssueCommentRequest{Body: r.Body})
	}
	if err != nil {
		return port.CommentReference{}, classify(err, "write comment")
	}
	return githubCommentRef(note)
}

func githubCommentRef(note *github.IssueComment) (port.CommentReference, error) {
	if note == nil || note.GetID() <= 0 || note.GetNodeID() == "" {
		return port.CommentReference{}, port.ErrCommentResponse
	}
	return port.CommentReference{ID: strconv.FormatInt(note.GetID(), 10), NodeID: note.GetNodeID()}, nil
}

// Minimize verifies repository ownership and reads the native minimization
// state before writing OUTDATED. An uncertain completed mutation is recovered
// by its state, without depending on repeated-mutation behavior.
// ref: https://docs.github.com/en/graphql/reference/issues#minimizecomment
func (p *Provider) Minimize(ctx context.Context, repo port.Repository, ref port.CommentReference) error {
	commentID, parseErr := strconv.ParseInt(ref.ID, 10, 64)
	if ref.NodeID == "" || parseErr != nil || commentID <= 0 {
		return port.ErrCommentInput
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	a, id, err := p.installationOf(ctx, repo)
	if err != nil {
		return err
	}
	c := p.installationFor(a, id, CommentWrite)
	// GraphQL identities are global. Verify this repository's REST identity
	// before minimizing so a reference from another reachable repository
	// cannot cross the caller's repository boundary.
	native, _, err := c.Issues.GetComment(ctx, repo.Namespace, repo.Name, commentID)
	if err != nil {
		return classify(err, "verify comment repository")
	}
	if native == nil || native.GetNodeID() != ref.NodeID {
		return port.ErrCommentInput
	}

	endpoint, _ := url.Parse(a.host.api) //nolint:errcheck // hostOf validates it
	endpoint.Path = commentGraphQLPath(endpoint.Path)
	minimized, err := commentAlreadyOutdated(ctx, c, endpoint.String(), ref.NodeID)
	if err != nil || minimized {
		return err
	}

	body := struct {
		Query     string            `json:"query"`
		Variables map[string]string `json:"variables"`
	}{
		Query: `mutation($id:ID!){minimizeComment(input:{subjectId:$id,classifier:OUTDATED}){minimizedComment{isMinimized}}}`, Variables: map[string]string{"id": ref.NodeID},
	}
	req, err := c.NewRequest(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return classify(err, "minimize comment")
	}
	var out struct {
		Data struct {
			Minimize struct {
				Comment struct {
					Minimized bool `json:"isMinimized"`
				} `json:"minimizedComment"`
			} `json:"minimizeComment"`
		} `json:"data"`
		Errors []commentGraphQLError `json:"errors"`
	}
	response, err := c.Do(req, &out)
	if err != nil {
		return classify(err, "minimize comment")
	}
	if err := commentGraphQLErrors(response, out.Errors); err != nil {
		return err
	}
	if !out.Data.Minimize.Comment.Minimized {
		return port.ErrCommentResponse
	}
	return nil
}

func commentGraphQLPath(api string) string {
	if strings.HasSuffix(api, "/api/v3/") {
		return strings.TrimSuffix(api, "/v3/") + "/graphql"
	}
	return strings.TrimSuffix(api, "/") + "/graphql"
}

var _ port.Comments = (*Provider)(nil)

func findComment(ctx context.Context, c *github.Client, repo port.Repository, pr int, key string) (*github.IssueComment, error) {
	var found *github.IssueComment
	// Issue-scoped REST lists are ascending and expose no documented sort.
	// Discover the last page, then traverse backwards and reverse each page.
	// ref: https://docs.github.com/en/rest/issues/comments#list-issue-comments
	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100, Page: 1}}
	list, resp, err := c.Issues.ListComments(ctx, repo.Namespace, repo.Name, pr, opts)
	if err != nil {
		return nil, classify(err, "find comment")
	}
	first := list
	last := max(resp.LastPage, 1)
	if resp.NextPage > 0 && resp.LastPage == 0 {
		return nil, port.ErrCommentSearchIncomplete
	}
	for count, page := 0, last; page >= 1; page-- {
		if count >= port.CommentSearchPages {
			break
		}
		if page == 1 {
			list = first
		}
		if page != 1 {
			opts.Page = page
			list, _, err = c.Issues.ListComments(ctx, repo.Namespace, repo.Name, pr, opts)
			if err != nil {
				return nil, classify(err, "find comment")
			}
		}
		slices.SortFunc(list, func(a, b *github.IssueComment) int {
			if a.GetID() > b.GetID() {
				return -1
			}
			if a.GetID() < b.GetID() {
				return 1
			}
			return 0
		})
		for _, note := range list {
			if note != nil && port.HasCommentMarker(note.GetBody(), key) {
				found = note
				break
			}
		}
		if found != nil {
			break
		}
		count++
	}
	return found, nil
}

// GraphQL exposes a lowercase minimizedReason, unlike the mutation's enum.
// ref: https://docs.github.com/en/graphql/reference/issues#issuecomment
func commentAlreadyOutdated(ctx context.Context, c *github.Client, endpoint, node string) (bool, error) {
	body := struct {
		Query     string            `json:"query"`
		Variables map[string]string `json:"variables"`
	}{
		Query: `query($id:ID!){node(id:$id){... on IssueComment{id isMinimized minimizedReason}}}`, Variables: map[string]string{"id": node},
	}
	req, err := c.NewRequest(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return false, classify(err, "read comment minimization")
	}
	var out struct {
		Data struct {
			Node *struct {
				ID        string `json:"id"`
				Minimized *bool  `json:"isMinimized"`
				Reason    string `json:"minimizedReason"`
			} `json:"node"`
		} `json:"data"`
		Errors []commentGraphQLError `json:"errors"`
	}
	response, err := c.Do(req, &out)
	if err != nil {
		return false, classify(err, "read comment minimization")
	}
	if err := commentGraphQLErrors(response, out.Errors); err != nil {
		return false, err
	}
	if out.Data.Node == nil || out.Data.Node.ID != node || out.Data.Node.Minimized == nil {
		return false, port.ErrCommentResponse
	}
	return *out.Data.Node.Minimized && out.Data.Node.Reason == "outdated", nil
}

type commentGraphQLError struct {
	Type string `json:"type"`
}

// A GraphQL limit can be HTTP200. Retain the documented error classification
// and reset header; when no reset is supplied, use a bounded client backoff.
// ref: https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api
func commentGraphQLErrors(response *github.Response, failures []commentGraphQLError) error {
	for _, failure := range failures {
		switch failure.Type {
		case "RATE_LIMITED":
			reset := time.Now().Add(time.Minute)
			if response != nil && !response.Rate.Reset.IsZero() {
				reset = response.Rate.Reset.Time
			}
			return &port.RateLimited{Reset: reset}
		case "FORBIDDEN":
			return ErrCredential
		}
	}
	if len(failures) > 0 {
		return port.ErrCommentResponse
	}
	return nil
}
