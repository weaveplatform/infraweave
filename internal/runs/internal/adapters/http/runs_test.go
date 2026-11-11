package http

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// TestUnitRunsInRange refuses a start or end that is not Unix seconds,
// and an end before start or more than eight days after it, naming the
// parameter.
func TestUnitRunsInRange(t *testing.T) {
	s := New(nil, nil, nil)
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

// TestUnitRunsRefusals answers each operation with no principal 401, and
// refuses a list's cursor or before that names no run, and a search's
// offset below zero or beside a cursor, before any read.
func TestUnitRunsRefusals(t *testing.T) {
	s := New(nil, nil, nil)
	bg := context.Background()
	for name, err := range map[string]error{
		"Run":       second(s.Run(bg, api.RunRequestObject{})),
		"StackRun":  second(s.StackRun(bg, api.StackRunRequestObject{})),
		"StackRuns": second(s.StackRuns(bg, api.StackRunsRequestObject{})),
		"StackProposedRuns": second(s.StackProposedRuns(bg,
			api.StackProposedRunsRequestObject{})),
		"StackTasks": second(s.StackTasks(bg, api.StackTasksRequestObject{})),
		"SearchRuns": second(s.SearchRuns(bg, api.SearchRunsRequestObject{
			Body: &api.SearchRunsRequest{},
		})),
		"SearchRunsSuggestions": second(s.SearchRunsSuggestions(bg,
			api.SearchRunsSuggestionsRequestObject{Body: &api.SearchSuggestionsInput{}})),
		"RunCancel":      second(s.RunCancel(bg, api.RunCancelRequestObject{})),
		"RunPrioritySet": second(s.RunPrioritySet(bg, api.RunPrioritySetRequestObject{})),
	} {
		if p, ok := errors.AsType[*problem.Error](err); !ok || p.Problem.Status != 401 {
			t.Errorf("%s with no principal = %v", name, err)
		}
	}
	ctx := kernel.WithPrincipal(bg, kernel.Principal{})
	nope := "nope"
	cursor := func(list string) *string {
		c, _ := page.Encode(list, kernel.Cursor[string]{ID: "nope"})
		return &c
	}
	for name, err := range map[string]error{
		"a cursor that does not decode": second(s.StackRuns(ctx, api.StackRunsRequestObject{
			Params: api.StackRunsParams{Cursor: &nope},
		})),
		"a cursor naming no run": second(s.StackTasks(ctx, api.StackTasksRequestObject{
			Params: api.StackTasksParams{Cursor: cursor("stack-tasks")},
		})),
	} {
		if p, ok := errors.AsType[*problem.Error](err); !ok || p.Problem.Status != 400 {
			t.Errorf("%s = %v", name, err)
		}
	}
	_, err := s.StackProposedRuns(ctx, api.StackProposedRunsRequestObject{
		Params: api.StackProposedRunsParams{Before: &nope},
	})
	if inv, ok := errors.AsType[*kernel.Invalid](err); !ok ||
		inv.Errors[0].Pointer != "#/query/before" {
		t.Errorf("a before naming no run = %v", err)
	}
	for _, c := range []struct {
		offset int32
		in     api.SearchInput
		reason string
	}{
		{-1, api.SearchInput{}, kernel.ReasonMinimum},
		{2, api.SearchInput{After: &nope}, kernel.ReasonRule},
	} {
		_, err := s.SearchRuns(ctx, api.SearchRunsRequestObject{Body: &api.SearchRunsRequest{
			Input: c.in, Offset: &c.offset,
		}})
		if inv, ok := errors.AsType[*kernel.Invalid](
			err,
		); !ok ||
			inv.Errors[0].Pointer != "/offset" ||
			inv.Errors[0].Reason != c.reason {
			t.Errorf("offset %d = %v", c.offset, err)
		}
	}
}

// TestUnitUnder puts each pointer of a refused search under the input
// that holds it, and passes any other error through.
func TestUnitUnder(t *testing.T) {
	err := under("/input", &search.Invalid{Detail: "d", Errors: []search.FieldError{
		{Pointer: "/after", Reason: kernel.ReasonRequired},
	}})
	if inv, ok := errors.AsType[*kernel.Invalid](
		err,
	); !ok ||
		inv.Errors[0].Pointer != "/input/after" {
		t.Errorf("under = %v", err)
	}
	other := errors.New("other")
	if got := under("/input", other); !errors.Is(got, other) {
		t.Errorf("another error = %v", got)
	}
}

func second[T any](_ T, err error) error { return err }
