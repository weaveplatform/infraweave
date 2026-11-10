package holder

import (
	"bytes"
	"context"
	"slices"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

// List is a grant view: what it lists of the actors that hold a space.
type List string

const (
	// Users are the account's managed users.
	Users List = "users"
	// Groups are the account's managed user groups.
	Groups List = "groups"
	// Keys are the account's API keys but personal ones, which act as
	// their owner, whom Users lists.
	Keys List = "keys"
	// UserSessions are the managed users with live sessions.
	UserSessions List = "user-sessions"
	// KeySessions are the API keys with live sessions.
	KeySessions List = "key-sessions"
)

// Fields are what each list's search filters and orders by.
var Fields = map[List][]search.Field{
	Users:  {{Name: "username", Type: search.String, Orderable: true}},
	Groups: {{Name: "groupName", Type: search.String, Orderable: true}},
	Keys:   {{Name: "name", Type: search.String, Orderable: true}},
	UserSessions: {
		{Name: "login", Type: search.String, Orderable: true},
		{Name: "name", Type: search.String},
	},
	KeySessions: {{Name: "name", Type: search.String, Orderable: true}},
}

// Candidate is a principal that may hold a space, as a list's row: ID is
// the managed user or API key the list names, and the rest is the
// principal the evaluator decides for. A user's Teams are the groups its
// latest sign-in reported, a key's its own, and a session's those it
// signed in with. At is a session's start.
type Candidate struct {
	ID     kernel.ULID
	Kind   kernel.PrincipalKind
	Actor  kernel.ULID
	Member kernel.MemberID
	Teams  []string
	At     time.Time
}

// Row is a list's row: a managed user by login, name and email, a group
// by name and description, or a key by name.
type Row struct {
	ID          kernel.ULID
	Login       string
	Name        string
	Email       string
	Description string
}

// Store reads the candidates of a list and searches its rows.
type Store interface {
	// Candidates are the principals of acct that list may name: each
	// managed user with its latest groups, each API key but personal ones
	// and deleted ones with its teams, or each live session of a user or
	// a key. Groups has none, as the snapshot holds them.
	Candidates(ctx context.Context, acct kernel.AccountID, list List) ([]Candidate, error)
	// SearchHolders reads a page of list's rows of acct among ids that in keeps.
	SearchHolders(
		ctx context.Context,
		acct kernel.AccountID,
		list List,
		in search.Input,
		ids []kernel.ULID,
	) (search.Result[Row], error)
	// SuggestHolders lists the values list's rows of acct among ids that in keeps
	// hold.
	SuggestHolders(
		ctx context.Context,
		acct kernel.AccountID,
		list List,
		in search.SuggestionsInput,
		ids []kernel.ULID,
	) (search.Suggestions, error)
	// HolderGroups are acct's groups among ids, as rows.
	HolderGroups(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) ([]Row, error)
}

// Evaluator answers grants from the account's authorization snapshot, as
// authz.Service does.
type Evaluator interface {
	Holders(
		ctx context.Context,
		acct kernel.AccountID,
		space kernel.ULID,
		kinds ...binding.ActorKind,
	) ([]authz.Holder, error)
	ExplainEach(
		ctx context.Context,
		acct kernel.AccountID,
		space kernel.ULID,
		whos []authz.Who,
	) ([][]authz.Grant, error)
}

// Held is a row of a grant view with the grants it holds on the space,
// each binding once. A session list's row counts its live sessions that
// reach the space, Last being the latest one's start. Groups are the
// groups a user's grants come through.
type Held struct {
	Row
	Grants   []authz.Grant
	Sessions int
	Last     time.Time
	Groups   map[kernel.ULID]Row
}

// Service answers the grant views from the evaluator that decides
// requests, so a view and a decision never disagree.
type Service struct {
	store Store
	authz Evaluator
}

// NewService returns the service on store and the evaluator.
func NewService(store Store, ev Evaluator) *Service {
	return &Service{store: store, authz: ev}
}

// Search reads a page of list's holders of acct's space that in keeps,
// with their grants there. Only holders are searched, so a page is full
// and its cursor exact.
func (s *Service) Search(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
	list List,
	in search.Input,
) (search.Result[Held], error) {
	var out search.Result[Held]
	held, ids, err := s.holders(ctx, acct, space, list)
	if err != nil {
		return out, err
	}
	page, err := s.store.SearchHolders(ctx, acct, list, in, ids)
	if err != nil {
		return out, err //nolint:wrapcheck // the store names the read; a refusal is answered as it is
	}
	out = search.Result[Held]{
		Start: page.Start, End: page.End, HasNext: page.HasNext, HasPrevious: page.HasPrevious,
	}
	var via []kernel.ULID
	for _, e := range page.Edges {
		h := held[e.Node.ID]
		h.Row = e.Node
		for _, g := range h.Grants {
			if g.Actor == binding.ActorGroup && list == Users {
				via = append(via, g.ActorID)
			}
		}
		out.Edges = append(out.Edges, search.Edge[Held]{Cursor: e.Cursor, Node: *h})
	}
	if len(via) == 0 {
		return out, nil
	}
	groups, err := s.store.HolderGroups(ctx, acct, via)
	if err != nil {
		return out, err //nolint:wrapcheck // the store names the read
	}
	byID := make(map[kernel.ULID]Row, len(groups))
	for _, g := range groups {
		byID[g.ID] = g
	}
	for i := range out.Edges {
		out.Edges[i].Node.Groups = byID
	}
	return out, nil
}

// Suggest lists the values list's holders of acct's space that in keeps
// hold, counting holders only.
func (s *Service) Suggest(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
	list List,
	in search.SuggestionsInput,
) (search.Suggestions, error) {
	_, ids, err := s.holders(ctx, acct, space, list)
	if err != nil {
		return search.Suggestions{}, err
	}
	//nolint:wrapcheck // the store names the read; a refusal is answered as it is
	return s.store.SuggestHolders(ctx, acct, list, in, ids)
}

// holders is list's holders of space by ID, and their IDs in order.
func (s *Service) holders(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
	list List,
) (map[kernel.ULID]*Held, []kernel.ULID, error) {
	held := map[kernel.ULID]*Held{}
	if list == Groups {
		hs, err := s.authz.Holders(ctx, acct, space, binding.ActorGroup)
		if err != nil {
			return nil, nil, err //nolint:wrapcheck // the snapshot's read names itself
		}
		for _, h := range hs {
			held[h.ID] = &Held{Grants: h.Grants}
		}
		return held, ids(held), nil
	}
	cands, err := s.store.Candidates(ctx, acct, list)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the store names the read
	}
	whos := make([]authz.Who, len(cands))
	for i, c := range cands {
		whos[i] = authz.Who{Kind: c.Kind, Actor: c.Actor, Member: c.Member, Teams: c.Teams}
	}
	grants, err := s.authz.ExplainEach(ctx, acct, space, whos)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the snapshot's read names itself
	}
	for i, c := range cands {
		if len(grants[i]) == 0 {
			continue
		}
		h := held[c.ID]
		if h == nil {
			h = &Held{}
			held[c.ID] = h
		}
		for _, g := range grants[i] {
			if !slices.ContainsFunc(h.Grants, func(x authz.Grant) bool {
				return x.Binding == g.Binding && x.RoleID == g.RoleID
			}) {
				h.Grants = append(h.Grants, g)
			}
		}
		if list == UserSessions || list == KeySessions {
			h.Sessions++
			if c.At.After(h.Last) {
				h.Last = c.At
			}
		}
	}
	return held, ids(held), nil
}

// ids are held's keys in byte order, so a query's parameters do not
// depend on map order.
func ids(held map[kernel.ULID]*Held) []kernel.ULID {
	out := make([]kernel.ULID, 0, len(held))
	for id := range held {
		out = append(out, id)
	}
	slices.SortFunc(out, func(a, b kernel.ULID) int { return bytes.Compare(a[:], b[:]) })
	return out
}
