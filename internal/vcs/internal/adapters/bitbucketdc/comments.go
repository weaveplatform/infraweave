package bitbucketdc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// CommentMaxUTF16 follows the Java String size constraint, so supplementary
// Unicode characters consume two units rather than one Unicode code point.
// ref: https://docs.atlassian.com/bitbucket-server/javadoc/8.4.0/api/com/atlassian/bitbucket/comment/Comment.html#getText()
// ref: https://docs.oracle.com/javase/8/docs/api/java/lang/String.html#length--
const CommentMaxUTF16 = 32768

func (*Provider) Presentation() port.CommentPresentation {
	return port.CommentPresentation{MaxUTF16: CommentMaxUTF16}
}

func (p *Provider) Post(ctx context.Context, repo port.Repository, r port.CommentRequest) (port.CommentReference, error) {
	return p.comment(ctx, repo, r, false)
}

func (p *Provider) Upsert(ctx context.Context, repo port.Repository, r port.CommentRequest) (port.CommentReference, error) {
	return p.comment(ctx, repo, r, true)
}

func (*Provider) Minimize(context.Context, port.Repository, port.CommentReference) error { return nil }

type nativeComment struct {
	ID      int64  `json:"id"`
	Version int64  `json:"version"`
	Text    string `json:"text"`
	Created int64  `json:"createdDate"`
}

func (p *Provider) comment(ctx context.Context, repo port.Repository, r port.CommentRequest, edit bool) (port.CommentReference, error) {
	if err := port.ValidateCommentRequest(r, p.Presentation()); err != nil {
		return port.CommentReference{}, port.ErrCommentInput
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return port.CommentReference{}, err
	}
	path := rp + "/pull-requests/" + r.PullRequest
	var found nativeComment
	if r.Existing != nil {
		id, err := strconv.ParseInt(r.Existing.ID, 10, 64)
		if err != nil || id <= 0 {
			return port.CommentReference{}, port.ErrCommentInput
		}
		err = p.decode(ctx, *repo.Integration, path+"/comments/"+r.Existing.ID, nil, "get comment", &found)
		if err != nil && !errors.Is(err, port.ErrNotFound) {
			return port.CommentReference{}, err
		}
		if err == nil && !port.HasCommentMarker(found.Text, r.DedupKey) {
			return port.CommentReference{}, port.ErrCommentInput
		}
	}
	if found.ID == 0 {
		found, err = p.findComment(ctx, *repo.Integration, path, r.DedupKey)
		if err != nil {
			return port.CommentReference{}, err
		}
	}
	if found.ID > 0 && !edit {
		return dcCommentRef(found), nil
	}
	method, rel := http.MethodPost, path+"/comments"
	body := map[string]any{"text": r.Body}
	if found.ID > 0 {
		// An activity can retain an earlier edit version. Read the current native
		// version immediately before PUT; a concurrent edit still returns409 and
		// lets the queued handler retry from a fresh read.
		rel += "/" + strconv.FormatInt(found.ID, 10)
		if err := p.decode(ctx, *repo.Integration, rel, nil, "refresh comment version", &found); err != nil {
			return port.CommentReference{}, err
		}
		if !port.HasCommentMarker(found.Text, r.DedupKey) {
			return port.CommentReference{}, port.ErrCommentInput
		}
		method = http.MethodPut
		body["version"] = found.Version
	}
	resp, err := p.send(ctx, *repo.Integration, method, API+rel, nil, body, "write comment")
	if err != nil {
		return port.CommentReference{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return port.CommentReference{}, classify(resp, "write comment")
	}
	var out nativeComment
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return port.CommentReference{}, fmt.Errorf("bitbucketdc: decode comment: %w", err)
	}
	if out.ID <= 0 || out.Version < 0 {
		return port.CommentReference{}, port.ErrCommentResponse
	}
	return dcCommentRef(out), nil
}

func dcCommentRef(c nativeComment) port.CommentReference {
	return port.CommentReference{ID: strconv.FormatInt(c.ID, 10), Version: c.Version}
}

var _ port.Comments = (*Provider)(nil)

type commentActivity struct {
	Comment nativeComment `json:"comment"`
}

func (p *Provider) findComment(ctx context.Context, in port.Integration, path, key string) (nativeComment, error) {
	var found nativeComment
	// General comments are activity entries. The /comments list is for a
	// required file path; it cannot recover general pull-request comments.
	// ref: https://docs.atlassian.com/bitbucket-server/rest/7.21.0/bitbucket-rest.html
	for count, start := 0, 0; ; count++ {
		if count >= port.CommentSearchPages {
			break
		}
		var page page[commentActivity]
		if err := p.decode(ctx, in, path+"/activities", url.Values{"limit": {"100"}, "start": {strconv.Itoa(start)}}, "find comment", &page); err != nil {
			return nativeComment{}, err
		}
		slices.SortFunc(page.Values, func(a, b commentActivity) int {
			if a.Comment.Created > b.Comment.Created {
				return -1
			}
			if a.Comment.Created < b.Comment.Created {
				return 1
			}
			if a.Comment.ID > b.Comment.ID {
				return -1
			}
			if a.Comment.ID < b.Comment.ID {
				return 1
			}
			return 0
		})
		for _, activity := range page.Values {
			if port.HasCommentMarker(activity.Comment.Text, key) {
				found = activity.Comment
				if found.ID <= 0 {
					return nativeComment{}, port.ErrCommentResponse
				}
				break
			}
		}
		if found.ID > 0 || page.IsLastPage {
			break
		}
		if page.NextPageStart == nil || *page.NextPageStart <= start {
			return nativeComment{}, port.ErrCommentSearchIncomplete
		}
		start = *page.NextPageStart
	}
	return found, nil
}
