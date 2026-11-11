package read

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// ErrNotFound reports a run the caller's account lacks, or one of another
// stack than the request names.
var ErrNotFound = errors.New("runs: no such run")

// Fields are the fields a run search filters, and the orderable ones
// order: its ID, its stack's slug and space, its type, state, commit SHA,
// trigger, priority and drift detection, and when it was created.
var Fields = []search.Field{
	{Name: "id", Type: search.String},
	{Name: "stack", Type: search.String, Orderable: true},
	{Name: "space", Type: search.Hierarchy},
	{Name: "type", Type: search.Enum, Values: names(kernel.RunTypes())},
	{Name: "state", Type: search.Enum, Values: names(kernel.RunStates()), Orderable: true},
	{Name: "commit", Type: search.String},
	{Name: "triggeredBy", Type: search.String, Orderable: true},
	{Name: "priority", Type: search.Enum, Values: names(kernel.RunPriorities())},
	{Name: "driftDetection", Type: search.Boolean},
	{Name: "createdAt", Type: search.Time, Orderable: true},
}

func names[T ~string](all []T) []string {
	out := make([]string, len(all))
	for i, v := range all {
		out[i] = string(v)
	}
	return out
}

// Scope is what a run search sees: the spaces the caller may read, the
// account's tree, and the stacks in those spaces, whose runs it reads.
type Scope struct {
	Reachable []kernel.ULID
	Spaces    []accessapi.SpaceNode
	Stacks    []stacksapi.Stack
}

// Query is a page of a stack's list: at most Limit runs, newest first,
// those older than After, the last run of the page before, and than
// Before; and, for ListInRange, those created from From until To.
type Query struct {
	Limit    int32
	After    *kernel.ULID
	Before   *kernel.ULID
	From, To time.Time
}

// List is one of a stack's lists of runs.
type List string

// The stack's lists: its tracked and destroy runs, its proposed runs, its
// tasks, and all its runs created in a range.
const (
	ListRuns     List = "stack-runs"
	ListProposed List = "stack-proposed-runs"
	ListTasks    List = "stack-tasks"
	ListInRange  List = "stack-runs-in-range"
)

// Types are the run types l holds, every type for ListInRange.
func (l List) Types() []kernel.RunType {
	switch l {
	case ListRuns:
		return []kernel.RunType{kernel.RunTypeTracked, kernel.RunTypeDestroy}
	case ListProposed:
		return []kernel.RunType{kernel.RunTypeProposed}
	case ListTasks:
		return []kernel.RunType{kernel.RunTypeTask}
	}
	return kernel.RunTypes()
}

// Store reads runs. Each method joins the transaction its context carries.
type Store interface {
	// Get reads acct's run id; ErrNotFound when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (lifecycle.Run, error)
	// Histories reads the transitions of acct's runs, oldest first.
	Histories(
		ctx context.Context,
		acct kernel.AccountID,
		runs []kernel.ULID,
	) (map[kernel.ULID][]lifecycle.Step, error)
	// Blockers reads the run that holds each of acct's stacks' run lock,
	// with a stack no run holds absent.
	Blockers(
		ctx context.Context,
		acct kernel.AccountID,
		stacks []kernel.ULID,
	) (map[kernel.ULID]kernel.ULID, error)
	// List reads up to q.Limit+1 of acct's stack's runs of types, as q
	// pages them.
	List(
		ctx context.Context,
		acct kernel.AccountID,
		stack kernel.ULID,
		types []kernel.RunType,
		q Query,
	) ([]lifecycle.Run, error)
	// Search reads a page of acct's runs in scope that in keeps, by
	// Fields; an input they cannot answer is a *search.Invalid.
	Search(
		ctx context.Context,
		acct kernel.AccountID,
		in search.Input,
		scope Scope,
	) (search.Result[lifecycle.Run], error)
	// Count counts acct's runs in scope that in keeps.
	Count(ctx context.Context, acct kernel.AccountID, in search.Input, scope Scope) (int, error)
	// Suggest lists the values acct's runs in scope that in keeps hold.
	Suggest(
		ctx context.Context,
		acct kernel.AccountID,
		in search.SuggestionsInput,
		scope Scope,
	) (search.Suggestions, error)
}

// Deps are what the reads read through.
type Deps struct {
	Store  Store
	Stacks stacksapi.Stacks
	Spaces accessapi.Spaces
	Authz  accessapi.Authorizer
}

// Service reads runs as the API answers them.
type Service struct{ d Deps }

// New returns the service over d.
func New(d Deps) *Service { return &Service{d: d} }

// View is a run as the API answers it: the run, its stack and the
// stack's branch, its transitions, oldest first, and the actions the
// caller may take on it.
type View struct {
	Run     lifecycle.Run
	Stack   stacksapi.Stack
	Branch  string
	History []lifecycle.Step
	Actions []accessapi.Action
}

// Page is a page of views and whether a later page has runs.
type Page struct {
	Views []View
	More  bool
}

// Found is a page of a run search: each run with its stack, the cursors
// of the page, and how many runs the search keeps in all.
type Found struct {
	Result search.Result[Hit]
	Total  int
}

// Hit is a run a search found and its stack.
type Hit struct {
	Run   kernel.ULID
	Stack string
}

// Run reads p's run raw. The route has decided SPACE_READ on its stack's
// space.
func (s *Service) Run(ctx context.Context, p kernel.Principal, raw string) (View, error) {
	r, err := s.get(ctx, p.AccountID, raw)
	if err != nil {
		return View{}, err
	}
	return s.One(ctx, p, r)
}

// StackRun reads p's run raw of stack slug; ErrNotFound when it is
// another stack's. The route has decided SPACE_READ on the stack's
// space.
func (s *Service) StackRun(
	ctx context.Context,
	p kernel.Principal,
	slug, raw string,
) (View, error) {
	st, err := s.d.Stacks.BySlug(ctx, p.AccountID, slug)
	if err != nil {
		return View{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	r, err := s.get(ctx, p.AccountID, raw)
	if err != nil {
		return View{}, err
	}
	if r.Stack != st.ID {
		return View{}, fmt.Errorf("%w: %s on %s", ErrNotFound, raw, slug)
	}
	return s.One(ctx, p, r)
}

// get reads acct's run raw; ErrNotFound when raw is no run ID.
func (s *Service) get(
	ctx context.Context,
	acct kernel.AccountID,
	raw string,
) (lifecycle.Run, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return lifecycle.Run{}, fmt.Errorf("%w: %q", ErrNotFound, raw)
	}
	return s.d.Store.Get(ctx, acct, id) //nolint:wrapcheck // the store names the read
}

// SpaceOf is the space of acct's run raw's stack, for the route to decide
// an action on; accessapi.ErrNoSubject when acct lacks the run.
func (s *Service) SpaceOf(
	ctx context.Context,
	acct kernel.AccountID,
	raw string,
) (kernel.ULID, error) {
	r, err := s.get(ctx, acct, raw)
	if errors.Is(err, ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	if err != nil {
		return kernel.ULID{}, err
	}
	st, err := s.d.Stacks.Get(ctx, acct, r.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return st.Space, err //nolint:wrapcheck // the stacks domain names the read
}

// StackOf is the stack of acct's run id; ErrNotFound when acct lacks it.
func (s *Service) StackOf(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (kernel.ULID, error) {
	r, err := s.d.Store.Get(ctx, acct, id)
	return r.Stack, err //nolint:wrapcheck // the store names the read
}

// StackRuns reads a page of p's stack slug's list l. The route has
// decided SPACE_READ on the stack's space.
func (s *Service) StackRuns(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	l List,
	q Query,
) (Page, error) {
	st, err := s.d.Stacks.BySlug(ctx, p.AccountID, slug)
	if err != nil {
		return Page{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	runs, err := s.d.Store.List(ctx, p.AccountID, st.ID, l.Types(), q)
	if err != nil {
		return Page{}, err //nolint:wrapcheck // the store names the read
	}
	more := len(runs) > int(q.Limit)
	views, err := s.Views(ctx, p, runs[:min(len(runs), int(q.Limit))]...)
	return Page{Views: views, More: more}, err
}

// Search reads a page of the runs of p's account's stacks in the spaces
// it may read, which the query leaves the others out of, and how many it
// keeps in all.
func (s *Service) Search(ctx context.Context, p kernel.Principal, in search.Input) (Found, error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return Found{}, err
	}
	res, err := s.d.Store.Search(ctx, p.AccountID, in, scope)
	if err != nil {
		return Found{}, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	total, err := s.d.Store.Count(ctx, p.AccountID, in, scope)
	if err != nil {
		return Found{}, err //nolint:wrapcheck // as above
	}
	slugs := make(map[kernel.ULID]string, len(scope.Stacks))
	for _, st := range scope.Stacks {
		slugs[st.ID] = st.Slug
	}
	out := Found{Total: total, Result: search.Result[Hit]{
		Edges: make([]search.Edge[Hit], len(res.Edges)),
		Start: res.Start, End: res.End, HasNext: res.HasNext, HasPrevious: res.HasPrevious,
	}}
	for i, e := range res.Edges {
		out.Result.Edges[i] = search.Edge[Hit]{
			Cursor: e.Cursor, Node: Hit{Run: e.Node.ID, Stack: slugs[e.Node.Stack]},
		}
	}
	return out, nil
}

// Suggest lists the values the runs of p's account's stacks in the spaces
// it may read hold, under in's predicates, so no value or count names a
// run p cannot read.
func (s *Service) Suggest(
	ctx context.Context,
	p kernel.Principal,
	in search.SuggestionsInput,
) (search.Suggestions, error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return search.Suggestions{}, err
	}
	return s.d.Store.Suggest(ctx, p.AccountID, in, scope) //nolint:wrapcheck // as the store's
}

// scope is what p's searches see: the spaces it may read, its account's
// tree and the stacks in the spaces it may read.
func (s *Service) scope(ctx context.Context, p kernel.Principal) (Scope, error) {
	reach, err := s.d.Authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return Scope{}, err //nolint:wrapcheck // the snapshot's read names itself
	}
	tree, err := s.d.Spaces.Tree(ctx, p.AccountID)
	if err != nil {
		return Scope{}, err //nolint:wrapcheck // the access domain names the read
	}
	stacks, err := s.d.Stacks.InSpaces(ctx, p.AccountID, reach)
	if err != nil {
		return Scope{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	return Scope{Reachable: reach, Spaces: tree, Stacks: stacks}, nil
}

// One is r as p reads it.
func (s *Service) One(ctx context.Context, p kernel.Principal, r lifecycle.Run) (View, error) {
	vs, err := s.Views(ctx, p, r)
	if err != nil {
		return View{}, err
	}
	return vs[0], nil
}

// Views are runs as p reads them, in their order: each with its stack,
// the stack's branch, its transitions and the actions p may take on it.
func (s *Service) Views(
	ctx context.Context,
	p kernel.Principal,
	runs ...lifecycle.Run,
) ([]View, error) {
	acct := p.AccountID
	ids := make([]kernel.ULID, len(runs))
	var stackIDs []kernel.ULID
	for i, r := range runs {
		ids[i] = r.ID
		if !slices.Contains(stackIDs, r.Stack) {
			stackIDs = append(stackIDs, r.Stack)
		}
	}
	histories, err := s.d.Store.Histories(ctx, acct, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	blockers, err := s.d.Store.Blockers(ctx, acct, stackIDs)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	settings := make(map[kernel.ULID]stacksapi.Settings, len(stackIDs))
	held := map[kernel.ULID][]accessapi.Action{}
	for _, id := range stackIDs {
		st, err := s.d.Stacks.Settings(ctx, acct, id)
		if err != nil {
			return nil, err //nolint:wrapcheck // the stacks domain names the read
		}
		settings[id] = st
		if _, ok := held[st.Space]; !ok {
			if held[st.Space], err = s.d.Authz.Actions(ctx, p, st.Space); err != nil {
				return nil, err //nolint:wrapcheck // the snapshot's read names itself
			}
		}
	}
	out := make([]View, len(runs))
	for i, r := range runs {
		st := settings[r.Stack]
		blocker, ok := blockers[r.Stack]
		out[i] = View{
			Run: r, Stack: st.Stack, Branch: st.Branch, History: histories[r.ID],
			Actions: Allowed(r, ok && blocker == r.ID, held[st.Space]),
		}
	}
	return out, nil
}

// Allowed are the actions a caller holding held may take on r: one for
// each edge its state allows next that a caller drives, cancel needing
// RUN_CANCEL_BLOCKING when r holds its stack's run lock, and
// RUN_PRIORITIZE_SET until it ends. A run that has ended allows none.
func Allowed(r lifecycle.Run, blocking bool, held []accessapi.Action) []accessapi.Action {
	out := []accessapi.Action{}
	add := func(a accessapi.Action) {
		if slices.Contains(held, a) && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	for _, e := range machine.Next(r.Type, r.State) {
		if !slices.Contains(e.Drivers, machine.DriverAPI) {
			continue
		}
		switch e.To {
		case kernel.RunStateCanceled:
			if blocking {
				add(accessapi.ActionRunCancelBlocking)
			} else {
				add(accessapi.ActionRunCancel)
			}
		case kernel.RunStateDiscarded:
			add(accessapi.ActionRunDiscard)
		case kernel.RunStateStopped:
			add(accessapi.ActionRunStop)
		case kernel.RunStateConfirmed:
			add(accessapi.ActionRunConfirm)
		case kernel.RunStateReplanRequested:
			add(accessapi.ActionRunTargetedReplan)
		}
	}
	if !r.State.Terminal() {
		add(accessapi.ActionRunPrioritizeSet)
	}
	return out
}
