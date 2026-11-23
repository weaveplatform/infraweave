package read

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// QueueFields are the fields a search of a pool's queue filters, and the
// orderable ones order, named as the reference's schedulable-run search
// names them: the commit SHA, when the run was created, whether it detects
// drift, whether its priority is high, its position in the queue, its
// priority, its stack's name, its state and its type. createdAt and
// position order alone. A search that names no order reads by position.
var QueueFields = []search.Field{
	{Name: "commit", Type: search.String, Orderable: true},
	{Name: "createdAt", Type: search.Ordering, Orderable: true},
	{Name: "isDriftDetection", Type: search.Boolean},
	{Name: "isPrioritized", Type: search.Boolean, Orderable: true},
	{Name: "position", Type: search.Ordering, Orderable: true},
	{Name: "priorityPreset", Type: search.Enum, Values: names(kernel.RunPriorities()), Orderable: true},
	{Name: "stackName", Type: search.String, Orderable: true},
	{Name: "state", Type: search.Enum, Values: names(machine.Claimable()), Orderable: true},
	{Name: "type", Type: search.Enum, Values: names(kernel.RunTypes()), Orderable: true},
}

// Queue names a pool's queue: a private pool's ID, or nil for the public
// pool, whose queue holds only the runs a public worker may claim, at
// normal priority and detecting no drift.
type Queue struct {
	Pool *kernel.ULID
}

// QueueStore reads the runs waiting for a worker. Each method joins the
// transaction its context carries, and reads only acct's runs on the
// scope's stacks in one of states.
type QueueStore interface {
	// SearchQueue reads a page of q's runs that in keeps, by
	// QueueFields, each with its position in the queue among the scope's
	// runs; an input they cannot answer is a *search.Invalid.
	SearchQueue(ctx context.Context, acct kernel.AccountID, q Queue, states []kernel.RunState, in search.Input, scope Scope) (search.Result[Queued], error)
	// SuggestQueue lists the values q's runs that in keeps hold.
	SuggestQueue(ctx context.Context, acct kernel.AccountID, q Queue, states []kernel.RunState, in search.SuggestionsInput, scope Scope) (search.Suggestions, error)
	// CountQueues counts the runs of each private pool of pools, a pool
	// none waits for absent.
	CountQueues(ctx context.Context, acct kernel.AccountID, pools []kernel.ULID, states []kernel.RunState, scope Scope) (map[kernel.ULID]int, error)
	// CountPublic counts the public pool's runs in states, and the runs a
	// public worker holds, in one of held.
	CountPublic(ctx context.Context, acct kernel.AccountID, states, held []kernel.RunState, scope Scope) (waiting, busy int, err error)
}

// Queued is a run of a pool's queue and its position there, from 1.
type Queued struct {
	Run      lifecycle.Run
	Position int64
}

// QueuedView is a run of a queue as the API answers it: the run, its
// position, and its stack with the stack's provider and its space's slug.
type QueuedView struct {
	Run      lifecycle.Run
	Position int64
	Stack    stacksapi.Stack
	Provider string
	Space    string
}

// SearchQueue reads a page of q's runs on the stacks in the spaces p may
// read. The positions count those runs alone, in the claim's order, as the
// read finds them; a position reserves nothing. The public pool's queue
// is empty while public execution is switched off, since no worker claims
// from it then.
func (s *Service) SearchQueue(ctx context.Context, p kernel.Principal, q Queue, in search.Input) (search.Result[QueuedView], error) {
	return snapshot(ctx, s.d.DB, func(ctx context.Context) (search.Result[QueuedView], error) {
		scope, err := s.queueScope(ctx, p, q)
		if err != nil {
			return search.Result[QueuedView]{}, err
		}
		res, err := s.d.Queue.SearchQueue(ctx, p.AccountID, q, machine.Claimable(), in, scope)
		if err != nil {
			return search.Result[QueuedView]{}, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
		}
		slugs, err := s.d.Spaces.Slugs(ctx, p.AccountID)
		if err != nil {
			return search.Result[QueuedView]{}, err //nolint:wrapcheck // the access domain names the read
		}
		stacks := make(map[kernel.ULID]stacksapi.Stack, len(scope.Stacks))
		for _, st := range scope.Stacks {
			stacks[st.ID] = st
		}
		providers := map[kernel.ULID]string{}
		out := search.Result[QueuedView]{
			Edges: make([]search.Edge[QueuedView], len(res.Edges)),
			Start: res.Start, End: res.End, HasNext: res.HasNext, HasPrevious: res.HasPrevious,
		}
		for i, e := range res.Edges {
			id := e.Node.Run.Stack
			if _, ok := providers[id]; !ok {
				st, err := s.d.Stacks.Settings(ctx, p.AccountID, id)
				if err != nil {
					return search.Result[QueuedView]{}, err //nolint:wrapcheck // the stacks domain names the read
				}
				providers[id] = st.Provider
			}
			out.Edges[i] = search.Edge[QueuedView]{Cursor: e.Cursor, Node: QueuedView{
				Run: e.Node.Run, Position: e.Node.Position, Stack: stacks[id], Provider: providers[id],
				Space: slugs[stacks[id].Space],
			}}
		}
		return out, nil
	})
}

// SuggestQueue lists the values q's runs on the stacks in the spaces p may
// read hold, under in's predicates, as SearchQueue reads them.
func (s *Service) SuggestQueue(ctx context.Context, p kernel.Principal, q Queue, in search.SuggestionsInput) (search.Suggestions, error) {
	scope, err := s.queueScope(ctx, p, q)
	if err != nil {
		return search.Suggestions{}, err
	}
	//nolint:wrapcheck // the store names the read; a refusal is answered as it is
	return s.d.Queue.SuggestQueue(ctx, p.AccountID, q, machine.Claimable(), in, scope)
}

// queueScope is p's scope for q: no stack at all for the public pool's
// queue while public execution is switched off, so it lists and counts no
// run.
func (s *Service) queueScope(ctx context.Context, p kernel.Principal, q Queue) (Scope, error) {
	scope, err := s.scope(ctx, p)
	if err == nil && q.Pool == nil && !s.d.PublicPoolEnabled {
		scope.Stacks = nil
	}
	return scope, err
}

// PoolQueues counts the runs waiting for each of pools on the stacks in
// the spaces p may read, as workersapi.Queues says.
func (s *Service) PoolQueues(ctx context.Context, p kernel.Principal, pools []kernel.ULID) (map[kernel.ULID]workersapi.QueueCounts, error) {
	out := make(map[kernel.ULID]workersapi.QueueCounts, len(pools))
	if len(pools) == 0 {
		return out, nil
	}
	scope, err := s.scope(ctx, p)
	if err != nil {
		return nil, err
	}
	got, err := s.d.Queue.CountQueues(ctx, p.AccountID, pools, machine.Claimable(), scope)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	for pool, n := range got {
		out[pool] = workersapi.QueueCounts{Pending: n, Schedulable: n}
	}
	return out, nil
}

// PublicQueue counts the public pool's runs on the stacks in the spaces p
// may read, as workersapi.Queues says.
func (s *Service) PublicQueue(ctx context.Context, p kernel.Principal) (workersapi.QueueCounts, error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return workersapi.QueueCounts{}, err
	}
	waiting, busy, err := s.d.Queue.CountPublic(ctx, p.AccountID, machine.Claimable(), onWorker(), scope)
	if err != nil {
		return workersapi.QueueCounts{}, err //nolint:wrapcheck // the store names the read
	}
	out := workersapi.QueueCounts{Pending: waiting, Busy: busy}
	if s.d.PublicPoolEnabled {
		out.Schedulable = waiting
	}
	return out, nil
}

// onWorker are the states in which a worker holds a run.
func onWorker() []kernel.RunState {
	var out []kernel.RunState
	for _, st := range kernel.RunStates() {
		if st.OnWorker() {
			out = append(out, st)
		}
	}
	return out
}

var _ workersapi.Queues = (*Service)(nil)
