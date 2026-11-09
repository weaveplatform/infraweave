package http_test

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/http"
)

// encoded is a cursor of list.
func encoded(t *testing.T, list string) *string {
	t.Helper()
	c, err := page.Encode(list, kernel.Cursor[string]{Key: "k", ID: "id"})
	if err != nil {
		t.Fatal(err)
	}
	return &c
}

// is reports whether err is a problem of type t.
func is(err error, t problem.Type) bool {
	pe, ok := errors.AsType[*problem.Error](err)
	return ok && pe.Problem.Title == t.Title && pe.Problem.Status == t.Status
}

// TestUnitRuns answers each run and task list of a stack with an empty
// page, refusing a cursor that is not the list's, and no run by ID.
func TestUnitRuns(t *testing.T) {
	ctx := t.Context()
	s := http.New(nil, nil, nil)
	lists := map[string]func(*string) (any, error){
		"stack-runs": func(c *string) (any, error) {
			return s.StackRuns(ctx, api.StackRunsRequestObject{
				StackId: "infra", Params: api.StackRunsParams{Cursor: c},
			})
		},
		"stack-runs-in-range": func(c *string) (any, error) {
			return s.StackRunsInRange(ctx, api.StackRunsInRangeRequestObject{
				StackId: "infra",
				Params:  api.StackRunsInRangeParams{Start: "0", End: "691200", Cursor: c},
			})
		},
		"stack-proposed-runs": func(c *string) (any, error) {
			return s.StackProposedRuns(ctx, api.StackProposedRunsRequestObject{
				StackId: "infra", Params: api.StackProposedRunsParams{Cursor: c},
			})
		},
		"stack-tasks": func(c *string) (any, error) {
			return s.StackTasks(ctx, api.StackTasksRequestObject{
				StackId: "infra", Params: api.StackTasksParams{Cursor: c},
			})
		},
	}
	altered := "nope"
	for list, read := range lists {
		for _, c := range []*string{nil, encoded(t, list)} {
			got, err := read(c)
			if err != nil {
				t.Fatalf("%s: %v", list, err)
			}
			if items := runItems(got); items == nil || len(items) != 0 {
				t.Errorf("%s = %+v, want an empty page", list, got)
			}
		}
		for _, c := range []*string{&altered, encoded(t, "stacks")} {
			if _, err := read(c); !is(err, problem.InvalidCursor) {
				t.Errorf("%s with cursor %s = %v, want invalid-cursor", list, *c, err)
			}
		}
	}

	_, err := s.StackRun(ctx, api.StackRunRequestObject{
		StackId: "infra", Params: api.StackRunParams{Id: "01J"},
	})
	if !is(err, problem.NotFound) {
		t.Errorf("a run by ID = %v, want not-found", err)
	}
}

// runItems are the items of a run page response, or nil for another.
func runItems(resp any) []api.Run {
	switch r := resp.(type) {
	case api.StackRuns200JSONResponse:
		return r.Body.Items
	case api.StackRunsInRange200JSONResponse:
		return r.Body.Items
	case api.StackProposedRuns200JSONResponse:
		return r.Body.Items
	case api.StackTasks200JSONResponse:
		return r.Body.Items
	}
	return nil
}

// TestUnitRunsInRange refuses a start or end that is not Unix seconds,
// and an end before start or more than eight days after it, naming the
// parameter.
func TestUnitRunsInRange(t *testing.T) {
	s := http.New(nil, nil, nil)
	for _, c := range []struct {
		start, end, pointer, reason string
	}{
		{"yesterday", "1700000000", "#/query/start", kernel.ReasonFormat},
		{"1700000000", "1.5", "#/query/end", kernel.ReasonFormat},
		{"1700000000", "1699999999", "#/query/end", kernel.ReasonRule},
		{"1700000000", "1700691201", "#/query/end", kernel.ReasonRule},
	} {
		_, err := s.StackRunsInRange(t.Context(), api.StackRunsInRangeRequestObject{
			StackId: "infra", Params: api.StackRunsInRangeParams{Start: c.start, End: c.end},
		})
		inv, ok := errors.AsType[*kernel.Invalid](err)
		if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != c.pointer ||
			inv.Errors[0].Reason != c.reason {
			t.Errorf("%s to %s = %v, want %s at %s", c.start, c.end, err, c.reason, c.pointer)
		}
	}
}
