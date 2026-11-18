package gitlab

import (
	"context"
	"errors"
	"strconv"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// CommentMaxRunes is the native note-body limit, in characters.
// ref: https://docs.gitlab.com/api/notes/#create-a-merge-request-note
const CommentMaxRunes = 1_000_000

// ref: https://docs.gitlab.com/user/markdown/#collapsible-section
func (*Provider) Presentation() port.CommentPresentation {
	return port.CommentPresentation{MaxRunes: CommentMaxRunes, Details: true}
}

func (p *Provider) Post(ctx context.Context, repo port.Repository, r port.CommentRequest) (port.CommentReference, error) {
	return p.comment(ctx, repo, r, false)
}

func (p *Provider) Upsert(ctx context.Context, repo port.Repository, r port.CommentRequest) (port.CommentReference, error) {
	return p.comment(ctx, repo, r, true)
}

func (*Provider) Minimize(context.Context, port.Repository, port.CommentReference) error { return nil }

func (p *Provider) comment(ctx context.Context, repo port.Repository, r port.CommentRequest, edit bool) (port.CommentReference, error) {
	if err := port.ValidateCommentRequest(r, p.Presentation()); err != nil {
		return port.CommentReference{}, port.ErrCommentInput
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, _, project, err := p.repo(repo)
	if err != nil {
		return port.CommentReference{}, err
	}
	pr, _ := strconv.ParseInt(r.PullRequest, 10, 64) //nolint:errcheck // validated above
	var found *gitlab.Note
	if r.Existing != nil {
		id, err := strconv.ParseInt(r.Existing.ID, 10, 64)
		if err != nil || id <= 0 {
			return port.CommentReference{}, port.ErrCommentInput
		}
		note, resp, err := c.Notes.GetMergeRequestNote(project, pr, id, gitlab.WithContext(ctx))
		switch {
		case err != nil:
			if !errors.Is(classify(resp, err, "get comment"), port.ErrNotFound) {
				return port.CommentReference{}, classify(resp, err, "get comment")
			}
		case note == nil || !port.HasCommentMarker(note.Body, r.DedupKey):
			return port.CommentReference{}, port.ErrCommentInput
		default:
			found = note
		}
	}
	if found == nil {
		opts := &gitlab.ListMergeRequestNotesOptions{OrderBy: new("created_at"), Sort: new("desc"), ListOptions: gitlab.ListOptions{PerPage: 100, Page: 1}}
		for count := 0; count < port.CommentSearchPages; count++ {
			list, resp, err := c.Notes.ListMergeRequestNotes(project, pr, opts, gitlab.WithContext(ctx))
			if err != nil {
				return port.CommentReference{}, classify(resp, err, "find comment")
			}
			for _, note := range list {
				if note != nil && !note.System && port.HasCommentMarker(note.Body, r.DedupKey) {
					found = note
					break
				}
			}
			if found != nil || resp.NextPage == 0 {
				break
			}
			if resp.NextPage <= opts.Page {
				return port.CommentReference{}, port.ErrCommentSearchIncomplete
			}
			opts.Page = resp.NextPage
		}
	}
	if found != nil && !edit {
		return gitlabCommentRef(found)
	}
	var note *gitlab.Note
	if found == nil {
		created, resp, err := c.Notes.CreateMergeRequestNote(project, pr, &gitlab.CreateMergeRequestNoteOptions{Body: new(r.Body)}, gitlab.WithContext(ctx))
		if err != nil {
			return port.CommentReference{}, classify(resp, err, "create comment")
		}
		note = created
	} else {
		updated, resp, err := c.Notes.UpdateMergeRequestNote(project, pr, found.ID, &gitlab.UpdateMergeRequestNoteOptions{Body: new(r.Body)}, gitlab.WithContext(ctx))
		if err != nil {
			return port.CommentReference{}, classify(resp, err, "update comment")
		}
		note = updated
	}
	return gitlabCommentRef(note)
}

func gitlabCommentRef(note *gitlab.Note) (port.CommentReference, error) {
	if note == nil || note.ID <= 0 {
		return port.CommentReference{}, port.ErrCommentResponse
	}
	return port.CommentReference{ID: strconv.FormatInt(note.ID, 10)}, nil
}

var _ port.Comments = (*Provider)(nil)
