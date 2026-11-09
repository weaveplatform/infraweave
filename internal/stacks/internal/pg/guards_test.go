package pg_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/http"
)

// guard is a StackGuard that records what it is asked, names lost for
// every move and fails with err.
type guard struct {
	lost    []string
	err     error
	moves   []stacksapi.StackMove
	deleted []stacksapi.Stack
}

func (g *guard) LostOnMove(_ context.Context, m stacksapi.StackMove) ([]string, error) {
	g.moves = append(g.moves, m)
	return g.lost, g.err
}

func (g *guard) Deleting(_ context.Context, st stacksapi.Stack) error {
	g.deleted = append(g.deleted, st)
	return g.err
}

// TestAccServiceGuards checks that a stack's move asks every registered
// guard and is refused, as 422 naming the stack and each entity, when one
// would lose an entity; that an update in place asks none; and that a
// delete lets each guard delete its rows first, and is undone when one
// fails.
func TestAccServiceGuards(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	st, err := f.svc.Create(ctx, f.admin, "", false, input("Infra"))
	if err != nil {
		t.Fatal(err)
	}
	quiet, loud := &guard{}, &guard{lost: []string{"context prod", "context shared"}}
	f.svc.Guard(quiet)
	f.svc.Guard(loud)

	if _, err := f.svc.Update(ctx, f.admin, "infra", kernel.Expect{}, input("Infra")); err != nil {
		t.Fatal(err)
	}
	if len(quiet.moves)+len(loud.moves) != 0 {
		t.Errorf("an update in place asked the guards: %v, %v", quiet.moves, loud.moves)
	}

	top := input("Infra")
	top.Space = ""
	_, err = f.svc.Update(ctx, f.admin, "infra", kernel.Expect{}, top)
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok || len(inv.Errors) != 2 || inv.Errors[0].Pointer != "/space" ||
		inv.Errors[0].Reason != kernel.ReasonRule ||
		inv.Errors[0].Message != "Stack infra would no longer reach context prod." ||
		inv.Detail != "The move is refused: stack infra would no longer reach context prod, "+
			"context shared." {
		t.Errorf("a move a guard refuses = %#v", err)
	}
	want := stacksapi.StackMove{Stack: st.Stack, To: f.acct.ULID}
	if len(quiet.moves) != 1 || quiet.moves[0] != want || len(loud.moves) != 1 {
		t.Errorf("the guards were asked %v and %v, want %v", quiet.moves, loud.moves, want)
	}
	if got, err := f.svc.Stack(ctx, f.acct, "infra"); err != nil || got.Space != f.team ||
		got.Version != 2 {
		t.Errorf("a refused move wrote %+v, %v", got, err)
	}

	loud.lost, loud.err = nil, errors.New("guard down")
	if _, err := f.svc.Update(
		ctx, f.admin, "infra", kernel.Expect{}, top,
	); !errors.Is(err, loud.err) {
		t.Errorf("a move whose guard fails = %v", err)
	}
	loud.err = nil
	if got, err := f.svc.Update(ctx, f.admin, "infra", kernel.Expect{}, top); err != nil ||
		got.Space != f.acct.ULID {
		t.Errorf("a move no guard refuses = %+v, %v", got, err)
	}

	loud.err = errors.New("guard down")
	if _, err := f.svc.Delete(ctx, f.admin, "infra", kernel.Expect{}); !errors.Is(err, loud.err) {
		t.Errorf("a delete whose guard fails = %v", err)
	}
	if _, err := f.svc.Stack(ctx, f.acct, "infra"); err != nil {
		t.Errorf("a delete whose guard failed left %v", err)
	}
	loud.err = nil
	quiet.deleted, loud.deleted = nil, nil
	gone, err := f.svc.Delete(ctx, f.admin, "infra", kernel.Expect{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(quiet.deleted, []stacksapi.Stack{gone.Stack}) ||
		!reflect.DeepEqual(loud.deleted, []stacksapi.Stack{gone.Stack}) {
		t.Errorf("Deleting was asked %v and %v, want %v", quiet.deleted, loud.deleted, gone.Stack)
	}
}

// TestAccServiceHoldFind checks that the service holds and finds stacks
// through the store, and finds none without a space or a label; it lists
// a space's stacks and reads stacks' labels.
func TestAccServiceHoldFind(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	in := input("Infra")
	in.Labels = []string{"autoattach:prod"}
	st, err := f.svc.Create(ctx, f.admin, "", false, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Hold(ctx, f.acct, st.ID); err != nil {
		t.Errorf("Hold = %v", err)
	}
	if err := f.svc.Hold(ctx, f.acct, kernel.NewULID()); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("Hold of a missing stack = %v", err)
	}
	labels := []string{"autoattach:prod"}
	if got, err := f.svc.Find(ctx, f.acct, []kernel.ULID{f.team}, labels); err != nil ||
		!reflect.DeepEqual(got, []stacksapi.Stack{st.Stack}) {
		t.Errorf("Find = %v, %v", got, err)
	}
	if got, err := f.svc.InSpaces(ctx, f.acct, []kernel.ULID{f.team}); err != nil ||
		!reflect.DeepEqual(got, []stacksapi.Stack{st.Stack}) {
		t.Errorf("InSpaces = %v, %v", got, err)
	}
	other := kernel.NewULID()
	if got, err := f.svc.Labels(ctx, f.acct, []kernel.ULID{st.ID, other}); err != nil ||
		!reflect.DeepEqual(got, map[kernel.ULID][]string{st.ID: labels, other: {}}) {
		t.Errorf("Labels = %v, %v", got, err)
	}
	f.db.Close()
	if got, err := f.svc.InSpaces(ctx, f.acct, nil); got != nil || err != nil {
		t.Errorf("InSpaces of no space = %v, %v", got, err)
	}
	if got, err := f.svc.Labels(ctx, f.acct, nil); len(got) != 0 || err != nil {
		t.Errorf("Labels of no stack = %v, %v", got, err)
	}
	if _, err := f.svc.Labels(ctx, f.acct, []kernel.ULID{st.ID}); err == nil {
		t.Error("Labels on a closed pool succeeded")
	}
	for name, spaces := range map[string][]kernel.ULID{"no space": nil, "no label": {f.team}} {
		l := labels
		if spaces != nil {
			l = nil
		}
		if got, err := f.svc.Find(ctx, f.acct, spaces, l); got != nil || err != nil {
			t.Errorf("Find with %s = %v, %v", name, got, err)
		}
	}
}

// configuration is a stack's configuration as the configuration domain
// would answer it, failing with err, and recording the stacks each read
// asks for.
type configuration struct {
	contexts map[kernel.ULID][]stacksapi.AttachedContext
	config   map[kernel.ULID][]stacksapi.ConfigElement
	err      error
	// configErr fails Config alone.
	configErr error
	asked     [][]kernel.ULID
}

func (c *configuration) AttachedContexts(
	_ context.Context,
	_ kernel.AccountID,
	stacks []kernel.ULID,
) (map[kernel.ULID][]stacksapi.AttachedContext, error) {
	c.asked = append(c.asked, stacks)
	return c.contexts, c.err
}

func (c *configuration) Config(
	context.Context,
	kernel.AccountID,
	[]kernel.ULID,
) (map[kernel.ULID][]stacksapi.ConfigElement, error) {
	if c.configErr != nil {
		return nil, c.configErr
	}
	return c.config, c.err
}

// TestAccHandlersConfiguration checks that a stack's view shows the
// attached contexts and config elements the bound configuration answers,
// read once for a whole list, with a write-only value left out; that it
// answers empty lists before one is bound; and that it fails when the
// configuration domain does.
func TestAccHandlersConfiguration(t *testing.T) {
	f := newFixture(t)
	s := http.New(f.svc, f.authz, f.spaces)
	// handed is a copy made before the binding, as internal/app hands the
	// server to the router, which still reads what is bound later.
	handed := s
	admin := kernel.WithPrincipal(t.Context(), f.admin)
	infra, err := f.svc.Create(t.Context(), f.admin, "", false, input("Infra"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.svc.Create(t.Context(), f.admin, "", false, input("Other"))
	if err != nil {
		t.Fatal(err)
	}
	read := func() (api.Stack, error) {
		got, err := handed.Stack(admin, api.StackRequestObject{StackId: "infra"})
		if err != nil {
			return api.Stack{}, err
		}
		return got.(api.Stack200JSONResponse).Body, nil
	}
	if v, err := read(); err != nil || v.AttachedContexts == nil || len(v.AttachedContexts) != 0 ||
		v.Config == nil || len(v.Config) != 0 {
		t.Errorf("an unbound stack view: contexts %v, config %v, %v",
			v.AttachedContexts, v.Config, err)
	}

	at := time.Unix(1_800_000_000, 0)
	region := stacksapi.ConfigElement{
		ID: "TF_VAR_region", Value: new("eu-west-1"), Checksum: "c1", CreatedAt: at,
	}
	token := stacksapi.ConfigElement{ID: "TOKEN", Value: new("s3cr3t"), WriteOnly: true}
	file := stacksapi.ConfigElement{
		ID: "creds.json", FileMount: true, Value: new("{}"), FileMode: new("0600"),
	}
	attachment := kernel.NewULID()
	c := &configuration{
		contexts: map[kernel.ULID][]stacksapi.AttachedContext{infra.ID: {
			{
				ID:      attachment,
				Context: "prod",
				Name:    "Prod",
				Labels:  []string{"autoattach:infra"},
				Hooks: map[string][]string{
					stacksapi.BeforeInit: {"make"},
				},
				Config:       []stacksapi.ConfigElement{file},
				Priority:     1,
				Autoattached: true,
			},
		}},
		config: map[kernel.ULID][]stacksapi.ConfigElement{infra.ID: {region, token}},
	}
	s.BindConfiguration(c)
	v, err := read()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.AttachedContexts) != 1 {
		t.Fatalf("contexts %+v", v.AttachedContexts)
	}
	got := v.AttachedContexts[0]
	if got.Id != attachment.String() || got.ContextId != "prod" || got.ContextName != "Prod" ||
		got.ContextDescription != nil || !reflect.DeepEqual(got.ContextLabels, []string{"autoattach:infra"}) ||
		!reflect.DeepEqual(got.ContextHooks.BeforeInit, []string{"make"}) ||
		got.ContextHooks.AfterRun == nil || got.Priority != 1 || !got.IsAutoattached ||
		len(
			got.Config,
		) != 1 || got.Config[0].Type != api.FILEMOUNT || *got.Config[0].Value != "{}" ||
		*got.Config[0].FileMode != "0600" || got.Config[0].CreatedAt != nil {
		t.Errorf("the attached context %+v", got)
	}
	want := []api.ConfigElement{
		{
			Id: "TF_VAR_region", Type: api.ENVIRONMENTVARIABLE, Value: new("eu-west-1"),
			Checksum: "c1", CreatedAt: new(int32(1_800_000_000)),
		},
		{Id: "TOKEN", Type: api.ENVIRONMENTVARIABLE, WriteOnly: true},
	}
	if !reflect.DeepEqual(v.Config, want) {
		t.Errorf("config %+v, want %+v", v.Config, want)
	}

	listed, err := handed.Stacks(admin, api.StacksRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	items := listed.(api.Stacks200JSONResponse).Body.Items
	if len(items) != 2 || len(c.asked) != 2 || len(c.asked[1]) != 2 {
		t.Fatalf("listed %d stacks, asking %v", len(items), c.asked)
	}
	for _, v := range items {
		if v.Id == other.Slug && (v.AttachedContexts == nil || len(v.AttachedContexts) != 0 ||
			v.Config == nil || len(v.Config) != 0) {
			t.Errorf("a stack with no configuration: %v, %v", v.AttachedContexts, v.Config)
		}
	}

	c.err = errors.New("configuration down")
	if _, err := read(); !errors.Is(err, c.err) {
		t.Errorf("a view whose contexts fail = %v", err)
	}
	c.err, c.configErr = nil, errors.New("config down")
	if _, err := read(); !errors.Is(err, c.configErr) {
		t.Errorf("a view whose config elements fail = %v", err)
	}
}
