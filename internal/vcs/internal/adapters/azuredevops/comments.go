package azuredevops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// CommentClientMaxBytes is a conservative client budget. The native thread
// comment schema documents content as a string without a numeric maximum.
// ref: https://learn.microsoft.com/en-us/rest/api/azure/devops/git/pull-request-thread-comments/create?view=azure-devops-rest-7.1
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
	ID      int64  `json:"id"`
	Content string `json:"content"`
	Deleted bool   `json:"isDeleted"`
}
type nativeThread struct {
	ID        int64           `json:"id"`
	Published time.Time       `json:"publishedDate"`
	Comments  []nativeComment `json:"comments"`
	Deleted   bool            `json:"isDeleted"`
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
	path := rp + "/pullRequests/" + r.PullRequest + "/threads"
	var found nativeComment
	var thread int64
	if r.Existing != nil {
		var err error
		thread, err = strconv.ParseInt(r.Existing.ThreadID, 10, 64)
		if err != nil || thread <= 0 {
			return port.CommentReference{}, port.ErrCommentInput
		}
		id, err := strconv.ParseInt(r.Existing.ID, 10, 64)
		if err != nil || id <= 0 {
			return port.CommentReference{}, port.ErrCommentInput
		}
		err = p.commentSend(ctx, *repo.Integration, http.MethodGet, path+"/"+r.Existing.ThreadID+"/comments/"+r.Existing.ID, nil, &found)
		if err != nil && !errors.Is(err, port.ErrNotFound) {
			return port.CommentReference{}, err
		}
		if err == nil && (found.Deleted || !port.HasCommentMarker(found.Content, r.DedupKey)) {
			return port.CommentReference{}, port.ErrCommentInput
		}
	}
	if found.ID == 0 || found.Deleted {
		found, thread, err = p.findComment(ctx, *repo.Integration, path, r.DedupKey)
		if err != nil {
			return port.CommentReference{}, err
		}
	}
	if found.ID > 0 && !edit {
		return azureCommentRef(thread, found.ID), nil
	}
	if found.ID > 0 {
		var out nativeComment
		if err := p.commentSend(
			ctx,
			*repo.Integration,
			http.MethodPatch,
			path+"/"+strconv.FormatInt(thread, 10)+"/comments/"+strconv.FormatInt(found.ID, 10),
			map[string]string{"content": r.Body},
			&out,
		); err != nil {
			return port.CommentReference{}, err
		}
		if out.ID <= 0 {
			return port.CommentReference{}, port.ErrCommentResponse
		}
		return azureCommentRef(thread, out.ID), nil
	}
	var out nativeThread
	body := map[string]any{"comments": []map[string]any{{"parentCommentId": 0, "content": r.Body, "commentType": 1}}, "status": 1}
	if err := p.commentSend(ctx, *repo.Integration, http.MethodPost, path, body, &out); err != nil {
		return port.CommentReference{}, err
	}
	if out.ID <= 0 || len(out.Comments) != 1 || out.Comments[0].ID <= 0 {
		return port.CommentReference{}, port.ErrCommentResponse
	}
	return azureCommentRef(out.ID, out.Comments[0].ID), nil
}

func azureCommentRef(thread, id int64) port.CommentReference {
	return port.CommentReference{ID: strconv.FormatInt(id, 10), ThreadID: strconv.FormatInt(thread, 10)}
}

func (p *Provider) commentSend(ctx context.Context, in port.Integration, method, path string, body, out any) error {
	o, err := orgOf(in.Settings)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(in.Credential))
	if token == "" {
		return ErrCredential
	}
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			return fmt.Errorf("azuredevops: encode comment: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(context.WithValue(ctx, baseKey{}, o.base), method, o.api+path+"?"+url.Values{"api-version": {APIVersion}}.Encode(), &payload)
	if err != nil {
		return fmt.Errorf("azuredevops: comment request: %w", err)
	}
	req.SetBasicAuth("", token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: azuredevops: comment: %w", kernel.ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classify(resp, "comment")
	}
	if resp.StatusCode == http.StatusNonAuthoritativeInfo && !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return ErrCredential
	}
	if resp.Header.Get("X-Ms-Continuationtoken") != "" {
		return port.ErrCommentSearchIncomplete
	}
	// A non-paginated API still needs a finite decode budget. Do not interpret a
	// truncated response as absence and create a duplicate comment.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out); err != nil {
		return fmt.Errorf("azuredevops: decode comment: %w", err)
	}
	return nil
}

var _ port.Comments = (*Provider)(nil)

func (p *Provider) findComment(ctx context.Context, in port.Integration, path, key string) (nativeComment, int64, error) {
	var found nativeComment
	var thread int64
	// Native thread listing returns all threads and documents no pagination.
	// Bound the response bytes and inspected records, then sort newest first.
	// ref: https://learn.microsoft.com/en-us/rest/api/azure/devops/git/pull-request-threads/list?view=azure-devops-rest-7.1
	var list struct {
		Value []nativeThread `json:"value"`
	}
	if err := p.commentSend(ctx, in, http.MethodGet, path, nil, &list); err != nil {
		return nativeComment{}, 0, err
	}

	slices.SortFunc(list.Value, func(a, b nativeThread) int {
		if order := b.Published.Compare(a.Published); order != 0 {
			return order
		}
		if a.ID > b.ID {
			return -1
		}
		if a.ID < b.ID {
			return 1
		}
		return 0
	})
	total := 0
	for _, t := range list.Value {
		if t.Deleted {
			continue
		}
		if total >= port.CommentSearchPages*100 {
			break
		}
		slices.SortFunc(t.Comments, func(a, b nativeComment) int {
			if a.ID > b.ID {
				return -1
			}
			if a.ID < b.ID {
				return 1
			}
			return 0
		})
		for _, c := range t.Comments {
			if total >= port.CommentSearchPages*100 {
				break
			}
			total++
			if !c.Deleted && port.HasCommentMarker(c.Content, key) {
				if t.ID <= 0 || c.ID <= 0 {
					return nativeComment{}, 0, port.ErrCommentResponse
				}
				found, thread = c, t.ID
				break
			}
		}
		if found.ID > 0 && !found.Deleted {
			break
		}
	}
	return found, thread, nil
}
