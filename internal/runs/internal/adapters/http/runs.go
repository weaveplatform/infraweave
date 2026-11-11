package http

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

// maxRange is the widest span runsInRange reads, in seconds: eight days.
const maxRange = 8 * 24 * 60 * 60

// Run answers one run of the caller's account. The route has decided
// SPACE_READ on its stack's space.
func (s Server) Run(ctx context.Context, req api.RunRequestObject) (api.RunResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.read.Run(ctx, p, req.RunId)
	if err != nil {
		return nil, runError(err, "", req.RunId)
	}
	return api.Run200JSONResponse{Body: runOf(v)}, nil
}

// StackRun answers one of the stack's runs or tasks.
func (s Server) StackRun(
	ctx context.Context,
	req api.StackRunRequestObject,
) (api.StackRunResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.read.StackRun(ctx, p, req.StackId, req.Params.Id)
	if err != nil {
		return nil, runError(err, req.StackId, req.Params.Id)
	}
	return api.StackRun200JSONResponse{Body: runOf(v)}, nil
}

// StackRuns lists the stack's tracked and destroy runs, newest first.
func (s Server) StackRuns(
	ctx context.Context,
	req api.StackRunsRequestObject,
) (api.StackRunsResponseObject, error) {
	out, err := s.list(ctx, req.StackId, read.ListRuns, req.Params.Cursor, req.Params.Before,
		req.Params.Limit, read.Query{})
	if err != nil {
		return nil, err
	}
	return api.StackRuns200JSONResponse{Body: out}, nil
}

// StackProposedRuns lists the stack's proposed runs, newest first.
func (s Server) StackProposedRuns(
	ctx context.Context,
	req api.StackProposedRunsRequestObject,
) (api.StackProposedRunsResponseObject, error) {
	out, err := s.list(ctx, req.StackId, read.ListProposed, req.Params.Cursor,
		req.Params.Before, req.Params.Limit, read.Query{})
	if err != nil {
		return nil, err
	}
	return api.StackProposedRuns200JSONResponse{Body: out}, nil
}

// StackTasks lists the stack's tasks, newest first.
func (s Server) StackTasks(
	ctx context.Context,
	req api.StackTasksRequestObject,
) (api.StackTasksResponseObject, error) {
	out, err := s.list(ctx, req.StackId, read.ListTasks, req.Params.Cursor, req.Params.Before,
		req.Params.Limit, read.Query{})
	if err != nil {
		return nil, err
	}
	return api.StackTasks200JSONResponse{Body: out}, nil
}

// StackRunsInRange lists the stack's runs created from start, inclusive,
// until end, exclusive, both Unix seconds at most eight days apart,
// newest first.
func (s Server) StackRunsInRange(
	ctx context.Context,
	req api.StackRunsInRangeRequestObject,
) (api.StackRunsInRangeResponseObject, error) {
	from, to, err := span(req.Params.Start, req.Params.End)
	if err != nil {
		return nil, err
	}
	out, err := s.list(ctx, req.StackId, read.ListInRange, req.Params.Cursor, nil,
		req.Params.Limit, read.Query{From: from, To: to})
	if err != nil {
		return nil, err
	}
	return api.StackRunsInRange200JSONResponse{Body: out}, nil
}

// list reads a page of the stack's list l: q's range, older than the run
// before names, after the run cursor ends a page at. The route has
// decided SPACE_READ on the stack's space.
func (s Server) list(
	ctx context.Context,
	slug string,
	l read.List,
	cursor, before *string,
	limit *int32,
	q read.Query,
) (api.RunPage, error) {
	p, err := principal(ctx)
	if err != nil {
		return api.RunPage{}, err
	}
	q.Limit = kernel.PageLimit(limit)
	if cursor != nil {
		c, err := page.Decode[string](string(l), *cursor)
		if err != nil {
			return api.RunPage{}, err //nolint:wrapcheck // a problem, answered as it is
		}
		id, err := kernel.ParseULID(c.ID)
		if err != nil {
			//nolint:wrapcheck // as above
			return api.RunPage{}, problem.InvalidCursor.Error("The cursor names no run.")
		}
		q.After = &id
	}
	if before != nil {
		id, err := kernel.ParseULID(*before)
		if err != nil {
			return api.RunPage{}, &kernel.Invalid{
				Detail: "The page of runs does not hold.",
				Errors: []kernel.FieldError{{
					Pointer: "#/query/before", Reason: kernel.ReasonFormat,
					Message: "before is not a run ID.",
				}},
			}
		}
		q.Before = &id
	}
	pg, err := s.read.StackRuns(ctx, p, slug, l, q)
	if err != nil {
		return api.RunPage{}, runError(err, slug, "")
	}
	out := api.RunPage{Items: make([]api.Run, len(pg.Views))}
	for i, v := range pg.Views {
		out.Items[i] = runOf(v)
	}
	if pg.More {
		// Encoding a string cannot fail.
		next, _ := page.Encode(string(l), kernel.Cursor[string]{
			ID: pg.Views[len(pg.Views)-1].Run.ID.String(),
		})
		out.NextCursor = &next
	}
	return out, nil
}

// span is the range start and end name, refusing a start or end that is
// not Unix seconds, an end before start, or a span wider than maxRange,
// naming the parameter.
func span(start, end string) (time.Time, time.Time, error) {
	from, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		return time.Time{}, time.Time{}, invalidRange("#/query/start", kernel.ReasonFormat,
			"start is not Unix seconds.")
	}
	to, err := strconv.ParseInt(end, 10, 64)
	if err != nil {
		return time.Time{}, time.Time{}, invalidRange("#/query/end", kernel.ReasonFormat,
			"end is not Unix seconds.")
	}
	if to < from || to-from > maxRange {
		return time.Time{}, time.Time{}, invalidRange("#/query/end", kernel.ReasonRule,
			"end is not after start by at most eight days (691,200 seconds).")
	}
	return time.Unix(from, 0).UTC(), time.Unix(to, 0).UTC(), nil
}

func invalidRange(pointer, reason, message string) error {
	return &kernel.Invalid{
		Detail: "The range of runs does not hold.",
		Errors: []kernel.FieldError{{Pointer: pointer, Reason: reason, Message: message}},
	}
}

// SearchRuns searches the runs of the caller's account's stacks in the
// spaces it may read, newest first, a page at a time from a cursor or
// from an offset.
func (s Server) SearchRuns(
	ctx context.Context,
	req api.SearchRunsRequestObject,
) (api.SearchRunsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := apisearch.Input(req.Body.Input)
	if o := req.Body.Offset; o != nil {
		if err := offset(*o, in); err != nil {
			return nil, err
		}
		in.Offset = int(*o)
	}
	found, err := s.read.Search(ctx, p, in)
	if err != nil {
		return nil, under("/input", err)
	}
	res := found.Result
	out := api.SearchRunsOutput{
		Edges:    make([]api.RunConnection, len(res.Edges)),
		PageInfo: apisearch.PageInfo(res),
		Total:    int32(min(found.Total, math.MaxInt32)), //nolint:gosec // clamped
	}
	for i, e := range res.Edges {
		out.Edges[i] = api.RunConnection{Cursor: e.Cursor, Node: api.RunWithStack{
			Run: api.RunRef{Id: e.Node.Run.String()}, Stack: api.StackRef{Id: e.Node.Stack},
		}}
	}
	return api.SearchRuns200JSONResponse{Body: out}, nil
}

// offset refuses a negative offset, and one beside a cursor or a page
// other than the first.
func offset(o int32, in search.Input) error {
	switch {
	case o < 0:
		return invalidOffset(kernel.ReasonMinimum, "offset is at least 0.")
	case o > 0 && (in.After != "" || in.Page != ""):
		return invalidOffset(kernel.ReasonRule,
			"offset pages from the first row; leave out input.after and input.requestedPage.")
	}
	return nil
}

func invalidOffset(reason, message string) error {
	return &kernel.Invalid{
		Detail: "The search's paging does not hold.",
		Errors: []kernel.FieldError{{Pointer: "/offset", Reason: reason, Message: message}},
	}
}

// under is err with each of its pointers under at, when it is a
// *kernel.Invalid of the input at holds.
func under(at string, err error) error {
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok {
		return err
	}
	out := &kernel.Invalid{Detail: inv.Detail, Errors: make([]kernel.FieldError, len(inv.Errors))}
	for i, e := range inv.Errors {
		e.Pointer = at + e.Pointer
		out.Errors[i] = e
	}
	return out
}

// SearchRunsSuggestions lists the values the runs of the caller's
// account's stacks in the spaces it may read hold.
func (s Server) SearchRunsSuggestions(
	ctx context.Context,
	req api.SearchRunsSuggestionsRequestObject,
) (api.SearchRunsSuggestionsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	got, err := s.read.Suggest(ctx, p, apisearch.SuggestionsInput(*req.Body))
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	return api.SearchRunsSuggestions200JSONResponse{Body: apisearch.Suggestions(got)}, nil
}

// RunCancel cancels a run of the stack that has not started. The route
// has decided RUN_CANCEL on the stack's space.
func (s Server) RunCancel(
	ctx context.Context,
	req api.RunCancelRequestObject,
) (api.RunCancelResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	note := ""
	if req.Body != nil && req.Body.Note != nil {
		note = *req.Body.Note
	}
	v, err := s.control.Cancel(ctx, p, req.StackId, req.RunId, note)
	if err != nil {
		return nil, runError(err, req.StackId, req.RunId)
	}
	return api.RunCancel200JSONResponse{Body: runOf(v)}, nil
}

// RunPrioritySet sets the priority of a run of the stack that has not
// ended. The route has decided RUN_PRIORITIZE_SET on the stack's space.
func (s Server) RunPrioritySet(
	ctx context.Context,
	req api.RunPrioritySetRequestObject,
) (api.RunPrioritySetResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.control.SetPriority(ctx, p, req.StackId, req.RunId,
		kernel.RunPriority(req.Body.Preset))
	if err != nil {
		return nil, runError(err, req.StackId, req.RunId)
	}
	return api.RunPrioritySet200JSONResponse{Body: runOf(v)}, nil
}
