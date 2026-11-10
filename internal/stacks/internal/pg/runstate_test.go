package pg_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// TestAccServiceRunState writes the runs domain's projection of a stack:
// a new stack is NONE and unblocked, a written state and blocked read
// back and filter and sort a search, the write leaves the version and the
// events as they are, and a state outside kernel.StackStates or a missing
// stack is refused.
func TestAccServiceRunState(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	created := map[string]core.Stack{}
	for _, name := range []string{"Infra", "Web", "Idle"} {
		st, err := f.svc.Create(ctx, f.admin, "", false, input(name))
		if err != nil {
			t.Fatal(err)
		}
		if st.State != kernel.StackStateNone || st.Blocked {
			t.Errorf("a new stack is %s, blocked %v; want NONE, unblocked", st.State, st.Blocked)
		}
		created[st.Slug] = st
	}
	for slug, state := range map[string]kernel.StackState{
		"infra": kernel.StackState(kernel.RunStateApplying),
		"web":   kernel.StackState(kernel.RunStateFailed),
	} {
		if err := f.svc.SetRunState(
			ctx,
			f.acct,
			created[slug].ID,
			state,
			slug == "infra",
		); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.svc.Stack(ctx, f.acct, "infra")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != kernel.StackState(kernel.RunStateApplying) || !got.Blocked ||
		got.Version != created["infra"].Version {
		t.Errorf(
			"after SetRunState: %s, blocked %v, version %d",
			got.State,
			got.Blocked,
			got.Version,
		)
	}
	if c := f.changes(t, got.ID); !reflect.DeepEqual(c, []string{stacksapi.ChangeCreated}) {
		t.Errorf("events %v, want only the create's", c)
	}

	slugs := func(in search.Input) []string {
		t.Helper()
		res, err := f.svc.Search(ctx, f.admin, in)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(res.Edges))
		for i, e := range res.Edges {
			out[i] = e.Node.Slug
		}
		return out
	}
	if got := slugs(search.Input{Predicates: []search.Predicate{{
		Field: "state", Constraint: search.Constraint{EnumEquals: []string{"FAILED", "NONE"}},
	}}}); !slices.Equal(got, []string{"idle", "web"}) {
		t.Errorf("stacks FAILED or NONE = %v", got)
	}
	if got := slugs(search.Input{OrderBy: []search.Order{{Field: "state"}}}); !slices.Equal(
		got, []string{"infra", "web", "idle"},
	) {
		t.Errorf("stacks by state = %v", got)
	}

	if err := f.svc.SetRunState(
		ctx, f.acct, got.ID, kernel.StackState(kernel.RunStateQueued), false,
	); !errors.Is(err, core.ErrStackState) {
		t.Errorf("SetRunState(QUEUED) = %v, want ErrStackState", err)
	}
	if err := f.svc.SetRunState(
		ctx, f.acct, kernel.NewULID(), kernel.StackStateNone, false,
	); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("SetRunState of a missing stack = %v, want ErrNotFound", err)
	}
}
