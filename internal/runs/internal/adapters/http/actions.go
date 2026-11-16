package http

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	api "github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

// RunRetry queues a run of a proposed or tracked run's type on its
// commit, once it has ended. The route has decided RUN_RETRY on the
// stack's space.
func (s Server) RunRetry(ctx context.Context, req api.RunRetryRequestObject) (api.RunRetryResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.control.Retry(ctx, p, req.StackId, req.RunId)
	if err != nil {
		return nil, runError(err, req.StackId, req.RunId)
	}
	return api.RunRetry200JSONResponse{Body: runOf(v)}, nil
}

// RunPromote queues a tracked run on a proposed run's commit, which
// becomes the stack's tracked commit. The route has decided RUN_PROMOTE
// on the stack's space.
func (s Server) RunPromote(ctx context.Context, req api.RunPromoteRequestObject) (api.RunPromoteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.control.Promote(ctx, p, req.StackId, req.RunId)
	if err != nil {
		return nil, runError(err, req.StackId, req.RunId)
	}
	return api.RunPromote200JSONResponse{Body: runOf(v)}, nil
}

// RunDiscardAll discards each of the stack's runs that waits. The route
// has decided RUN_DISCARD on the stack's space.
func (s Server) RunDiscardAll(ctx context.Context, req api.RunDiscardAllRequestObject) (api.RunDiscardAllResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var note string
	if req.Body != nil {
		note = deref(req.Body.Note)
	}
	d, err := s.control.DiscardAll(ctx, p, req.StackId, note)
	if err != nil {
		return nil, runError(err, req.StackId, "")
	}
	body := api.RunDiscardAll{
		DiscardedRuns: make(
			[]api.RunRef,
			len(d.Runs),
		),
		FailedDiscarding: make([]string, len(d.Failed)),
	}
	for i, id := range d.Runs {
		body.DiscardedRuns[i] = api.RunRef{Id: id.String()}
	}
	for i, id := range d.Failed {
		body.FailedDiscarding[i] = id.String()
	}
	return api.RunDiscardAll200JSONResponse{Body: body}, nil
}

// RunComment comments on a run of the stack. The route has decided
// RUN_COMMENT on the stack's space.
func (s Server) RunComment(ctx context.Context, req api.RunCommentRequestObject) (api.RunCommentResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.control.Comment(ctx, p, req.StackId, req.RunId, req.Body.Body)
	if err != nil {
		return nil, runError(err, req.StackId, req.RunId)
	}
	return api.RunComment200JSONResponse{Body: commentOf(c)}, nil
}

// RunLogsDelete deletes a run's logs ahead of their retention, once it
// has ended. The route has decided SPACE_ADMIN on the account's top
// space.
func (s Server) RunLogsDelete(ctx context.Context, req api.RunLogsDeleteRequestObject) (api.RunLogsDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.logs.Delete(ctx, p, req.StackId, req.RunId)
	if err != nil {
		return nil, runError(err, req.StackId, req.RunId)
	}
	v, err := s.read.One(ctx, p, r)
	if err != nil {
		return nil, err //nolint:wrapcheck // the read names itself
	}
	return api.RunLogsDelete200JSONResponse{Body: runOf(v)}, nil
}

// commentOf is c as the API answers a comment.
func commentOf(c read.Comment) api.RunComment {
	return api.RunComment{Body: c.Body, CreatedAt: unix(c.At.Unix()), Username: c.Username}
}

// sentence is a guard's blocker as the API answers it, a sentence; nil
// for none.
func sentence(blocker string) *string {
	if blocker == "" {
		return nil
	}
	r, n := utf8.DecodeRuneInString(blocker)
	s := string(unicode.ToUpper(r)) + blocker[n:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return &s
}
