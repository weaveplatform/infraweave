package bitbucketcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// CommentClientMaxBytes is a conservative UTF-8 client budget; the native
// comment schema specifies no numeric body bound and no collapsible HTML.
// ref: https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/#api-repositories-workspace-repo-slug-pullrequests-pull-request-id-comments-post
const CommentClientMaxBytes = 30_000

func (*Provider) Presentation() port.CommentPresentation {
	return port.CommentPresentation{MaxBytes: CommentClientMaxBytes}
}

func (p *Provider) Post(ctx context.Context, repo port.Repository, r port.CommentRequest) (port.CommentReference, error) {
	return p.comment(ctx, repo, r, false)
}

func (p *Provider) Upsert(ctx context.Context, repo port.Repository, r port.CommentRequest) (port.CommentReference, error) {
	return p.comment(ctx, repo, r, true)
}

func (*Provider) Minimize(context.Context, port.Repository, port.CommentReference) error { return nil }

type nativeComment struct {
	ID      int64 `json:"id"`
	Deleted bool  `json:"deleted"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
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
	path := rp + "/pullrequests/" + r.PullRequest + "/comments"
	var found nativeComment
	if r.Existing != nil {
		id, err := strconv.ParseInt(r.Existing.ID, 10, 64)
		if err != nil || id <= 0 {
			return port.CommentReference{}, port.ErrCommentInput
		}
		err = p.commentSend(ctx, *repo.Integration, http.MethodGet, path+"/"+r.Existing.ID, nil, &found)
		if err != nil && !errors.Is(err, port.ErrNotFound) {
			return port.CommentReference{}, err
		}
		if err == nil && (found.Deleted || !port.HasCommentMarker(found.Content.Raw, r.DedupKey)) {
			return port.CommentReference{}, port.ErrCommentInput
		}
	}
	if found.ID == 0 || found.Deleted {
		found, err = p.findComment(ctx, *repo.Integration, path, r.DedupKey)
		if err != nil {
			return port.CommentReference{}, err
		}
	}
	if found.ID > 0 && !edit {
		return port.CommentReference{ID: strconv.FormatInt(found.ID, 10)}, nil
	}
	method := http.MethodPost
	if found.ID > 0 {
		method = http.MethodPut
		path += "/" + strconv.FormatInt(found.ID, 10)
	}
	var out nativeComment
	body := map[string]any{"content": map[string]string{"raw": r.Body}}
	if err := p.commentSend(ctx, *repo.Integration, method, path, body, &out); err != nil {
		return port.CommentReference{}, err
	}
	if out.ID <= 0 {
		return port.CommentReference{}, port.ErrCommentResponse
	}
	return port.CommentReference{ID: strconv.FormatInt(out.ID, 10)}, nil
}

func (p *Provider) commentSend(ctx context.Context, in port.Integration, method, path string, body any, out any) error {
	if p.host != nil {
		return p.host
	}
	token := strings.TrimSpace(string(in.Credential))
	if token == "" || in.Settings.Email == "" {
		return ErrCredential
	}
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			return fmt.Errorf("bitbucketcloud: encode comment: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, p.api+path, &payload)
	if err != nil {
		return fmt.Errorf("bitbucketcloud: comment request: %w", err)
	}
	req.SetBasicAuth(in.Settings.Email, token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: bitbucketcloud: comment: %w", kernel.ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classify(resp, "comment")
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("bitbucketcloud: decode comment: %w", err)
	}
	return nil
}

var _ port.Comments = (*Provider)(nil)

func (p *Provider) findComment(ctx context.Context, in port.Integration, path, key string) (nativeComment, error) {
	var found nativeComment
	next := p.api + path + "?" + url.Values{"sort": {"-created_on"}, "pagelen": {"100"}}.Encode()
	seen := map[string]bool{}
	for count := 0; next != ""; count++ {
		if count >= port.CommentSearchPages {
			break
		}
		if seen[next] {
			return nativeComment{}, port.ErrCommentSearchIncomplete
		}
		seen[next] = true
		// Native pagination links cannot move the credential to another origin,
		// repository, pull request, or route.
		u, err := url.Parse(next)
		base, _ := url.Parse(p.api + path) //nolint:errcheck // hostOf and repoPath validate the base
		if err != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.EscapedPath() != base.EscapedPath() || u.User != nil || u.Fragment != "" {
			return nativeComment{}, port.ErrCommentSearchIncomplete
		}
		var page page[nativeComment]
		if err := p.decode(ctx, in, next, "find comment", &page); err != nil {
			return nativeComment{}, err
		}
		for _, c := range page.Values {
			if !c.Deleted && port.HasCommentMarker(c.Content.Raw, key) {
				found = c
				if found.ID <= 0 {
					return nativeComment{}, port.ErrCommentResponse
				}
				break
			}
		}
		if found.ID > 0 {
			break
		}
		next = page.Next
	}
	return found, nil
}
