package http

import (
	"context"
	"fmt"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// A stack has no runs or tasks until the runs domain exists, so its run
// and task reads answer empty pages, and a run by ID is not found. The
// route table has already decided SPACE_READ on the stack's space and
// answered 404 for a stack the caller cannot see, so a client reads a new
// stack's whole page with the shapes the runs domain later fills.
//
// ref: plan P5 task 6

// The lists a stack's run and task cursors belong to. A cursor of one is
// refused by the others.
const (
	runsList         = "stack-runs"
	runsInRangeList  = "stack-runs-in-range"
	proposedRunsList = "stack-proposed-runs"
	tasksList        = "stack-tasks"
)

// maxRange is the widest span runsInRange reads, in seconds: eight days.
const maxRange = 8 * 24 * 60 * 60

// StackRuns lists the stack's tracked runs: none yet.
func (s Server) StackRuns(
	_ context.Context,
	req api.StackRunsRequestObject,
) (api.StackRunsResponseObject, error) {
	if err := cursor(runsList, req.Params.Cursor); err != nil {
		return nil, err
	}
	return api.StackRuns200JSONResponse{Body: api.RunPage{Items: []api.Run{}}}, nil
}

// StackRunsInRange lists the stack's runs between start, inclusive, and
// end, exclusive, both Unix seconds at most eight days apart: none yet.
func (s Server) StackRunsInRange(
	_ context.Context,
	req api.StackRunsInRangeRequestObject,
) (api.StackRunsInRangeResponseObject, error) {
	if err := span(req.Params.Start, req.Params.End); err != nil {
		return nil, err
	}
	if err := cursor(runsInRangeList, req.Params.Cursor); err != nil {
		return nil, err
	}
	return api.StackRunsInRange200JSONResponse{Body: api.RunPage{Items: []api.Run{}}}, nil
}

// StackProposedRuns lists the stack's proposed runs: none yet.
func (s Server) StackProposedRuns(
	_ context.Context,
	req api.StackProposedRunsRequestObject,
) (api.StackProposedRunsResponseObject, error) {
	if err := cursor(proposedRunsList, req.Params.Cursor); err != nil {
		return nil, err
	}
	return api.StackProposedRuns200JSONResponse{Body: api.RunPage{Items: []api.Run{}}}, nil
}

// StackTasks lists the stack's tasks: none yet.
func (s Server) StackTasks(
	_ context.Context,
	req api.StackTasksRequestObject,
) (api.StackTasksResponseObject, error) {
	if err := cursor(tasksList, req.Params.Cursor); err != nil {
		return nil, err
	}
	return api.StackTasks200JSONResponse{Body: api.RunPage{Items: []api.Run{}}}, nil
}

// StackRun answers one of the stack's runs or tasks: none is found, as a
// hidden one is not.
func (s Server) StackRun(
	_ context.Context,
	req api.StackRunRequestObject,
) (api.StackRunResponseObject, error) {
	//nolint:wrapcheck // a problem, answered as it is
	return nil, problem.NotFound.Error(fmt.Sprintf("No run %q on this stack.", req.Params.Id))
}

// cursor refuses a cursor that is not one of list's. No list has yet
// issued one, so any cursor a client sends is refused unless it decodes as
// list's, and a decoded one still starts the empty page.
func cursor(list string, c *api.Cursor) error {
	if c == nil {
		return nil
	}
	_, err := page.Decode[string](list, *c)
	return err //nolint:wrapcheck // a problem, answered as it is
}

// span refuses a start or end that is not Unix seconds, an end before
// start, or a span wider than maxRange, naming the parameter.
func span(start, end string) error {
	from, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		return invalidRange("#/query/start", kernel.ReasonFormat, "start is not Unix seconds.")
	}
	to, err := strconv.ParseInt(end, 10, 64)
	if err != nil {
		return invalidRange("#/query/end", kernel.ReasonFormat, "end is not Unix seconds.")
	}
	if to < from || to-from > maxRange {
		return invalidRange("#/query/end", kernel.ReasonRule,
			"end is not after start by at most eight days (691,200 seconds).")
	}
	return nil
}

func invalidRange(pointer, reason, message string) error {
	return &kernel.Invalid{
		Detail: "The range of runs does not hold.",
		Errors: []kernel.FieldError{{Pointer: pointer, Reason: reason, Message: message}},
	}
}
