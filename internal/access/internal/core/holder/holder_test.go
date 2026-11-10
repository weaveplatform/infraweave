package holder_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/holder"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

var errDown = errors.New("down")

// book is a store of candidates and rows; searched records the IDs each
// search was held to, and fail names the method that fails.
type book struct {
	cands    map[holder.List][]holder.Candidate
	rows     map[kernel.ULID]holder.Row
	searched []kernel.ULID
	fail     string
}

func (b *book) Candidates(
	_ context.Context,
	_ kernel.AccountID,
	list holder.List,
) ([]holder.Candidate, error) {
	if b.fail == "candidates" {
		return nil, errDown
	}
	return b.cands[list], nil
}

func (b *book) page(ids []kernel.ULID) search.Result[holder.Row] {
	b.searched = ids
	var out search.Result[holder.Row]
	for _, id := range ids {
		out.Edges = append(
			out.Edges,
			search.Edge[holder.Row]{Cursor: id.String(), Node: b.rows[id]},
		)
	}
	return out
}

func (b *book) SearchHolders(
	_ context.Context,
	_ kernel.AccountID,
	_ holder.List,
	_ search.Input,
	ids []kernel.ULID,
) (search.Result[holder.Row], error) {
	if b.fail == "search" {
		return search.Result[holder.Row]{}, errDown
	}
	return b.page(ids), nil
}

func (b *book) SuggestHolders(
	_ context.Context,
	_ kernel.AccountID,
	_ holder.List,
	_ search.SuggestionsInput,
	ids []kernel.ULID,
) (search.Suggestions, error) {
	b.searched = ids
	return search.Suggestions{Count: len(ids)}, nil
}

func (b *book) HolderGroups(
	_ context.Context,
	_ kernel.AccountID,
	ids []kernel.ULID,
) ([]holder.Row, error) {
	if b.fail == "groups" {
		return nil, errDown
	}
	out := make([]holder.Row, 0, len(ids))
	for _, id := range ids {
		out = append(out, b.rows[id])
	}
	return out, nil
}

// judge grants each principal what its actor and teams are bound to.
type judge struct {
	grants map[string][]authz.Grant
	groups []authz.Holder
	fail   bool
}

func (j *judge) Holders(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
	...binding.ActorKind,
) ([]authz.Holder, error) {
	if j.fail {
		return nil, errDown
	}
	return j.groups, nil
}

func (j *judge) ExplainEach(
	_ context.Context,
	_ kernel.AccountID,
	_ kernel.ULID,
	whos []authz.Who,
) ([][]authz.Grant, error) {
	if j.fail {
		return nil, errDown
	}
	out := make([][]authz.Grant, len(whos))
	for i, w := range whos {
		out[i] = append(out[i], j.grants[w.Actor.String()]...)
		for _, t := range w.Teams {
			out[i] = append(out[i], j.grants[t]...)
		}
	}
	return out, nil
}

func TestUnitHolders(t *testing.T) {
	ctx := t.Context()
	acct, space := kernel.NewAccountID(), kernel.NewULID()
	alice, bob, key, team := kernel.NewULID(), kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	own := authz.Grant{
		Binding: kernel.NewULID(),
		Actor:   binding.ActorUser,
		ActorID: alice,
		Role:    "space-reader",
	}
	via := authz.Grant{
		Binding: kernel.NewULID(),
		Actor:   binding.ActorGroup,
		ActorID: team,
		Role:    "space-writer",
	}
	keyed := authz.Grant{
		Binding: kernel.NewULID(), Actor: binding.ActorAPIKey, ActorID: key, Role: "space-admin",
		Origin: binding.OriginAccessRule,
	}
	early, late := time.Unix(100, 0), time.Unix(200, 0)
	b := &book{
		cands: map[holder.List][]holder.Candidate{
			holder.Users: {
				{ID: alice, Kind: kernel.PrincipalUser, Actor: alice, Teams: []string{"ops"}},
				{ID: bob, Kind: kernel.PrincipalUser, Actor: bob},
			},
			holder.KeySessions: {
				{ID: key, Kind: kernel.PrincipalAPIKey, Actor: key, At: early},
				{ID: key, Kind: kernel.PrincipalAPIKey, Actor: key, At: late},
				{ID: key, Kind: kernel.PrincipalAPIKey, Actor: key, At: early},
			},
		},
		rows: map[kernel.ULID]holder.Row{
			alice: {ID: alice, Login: "alice"},
			team:  {ID: team, Name: "ops", Description: "on call"},
			key:   {ID: key, Name: "deploy"},
		},
	}
	j := &judge{
		grants: map[string][]authz.Grant{
			alice.String(): {own},
			"ops":          {via},
			key.String():   {keyed},
		},
		groups: []authz.Holder{
			{Actor: binding.ActorGroup, ID: team, Name: "ops", Grants: []authz.Grant{via}},
		},
	}
	svc := holder.NewService(b, j)

	t.Run(
		"a user holds its own grants and its groups', and no one else is searched",
		func(t *testing.T) {
			got, err := svc.Search(ctx, acct, space, holder.Users, search.Input{})
			if err != nil || len(got.Edges) != 1 ||
				!slices.Equal(b.searched, []kernel.ULID{alice}) {
				t.Fatalf("Search = %+v, %v; searched %v", got, err, b.searched)
			}
			n := got.Edges[0].Node
			if n.Login != "alice" || len(n.Grants) != 2 || n.Groups[team].Description != "on call" {
				t.Errorf("alice's row = %+v", n)
			}
		},
	)
	t.Run("a group holds what the snapshot says", func(t *testing.T) {
		got, err := svc.Search(ctx, acct, space, holder.Groups, search.Input{})
		if err != nil || len(got.Edges) != 1 || got.Edges[0].Node.Name != "ops" ||
			got.Edges[0].Node.Groups != nil {
			t.Fatalf("Search = %+v, %v", got, err)
		}
	})
	t.Run("a key's sessions count once each, its grants once", func(t *testing.T) {
		got, err := svc.Search(ctx, acct, space, holder.KeySessions, search.Input{})
		if err != nil || len(got.Edges) != 1 {
			t.Fatalf("Search = %+v, %v", got, err)
		}
		n := got.Edges[0].Node
		if n.Sessions != 3 || !n.Last.Equal(late) || len(n.Grants) != 1 {
			t.Errorf("the key's row = %+v", n)
		}
	})
	t.Run("suggestions count holders", func(t *testing.T) {
		got, err := svc.Suggest(ctx, acct, space, holder.Users, search.SuggestionsInput{})
		if err != nil || got.Count != 1 {
			t.Fatalf("Suggest = %+v, %v", got, err)
		}
	})
	t.Run("no holder is no row", func(t *testing.T) {
		got, err := svc.Search(ctx, acct, space, holder.Keys, search.Input{})
		if err != nil || len(got.Edges) != 0 || len(b.searched) != 0 {
			t.Fatalf("Search = %+v, %v; searched %v", got, err, b.searched)
		}
	})
	t.Run("failures", func(t *testing.T) {
		for _, fail := range []string{"candidates", "search", "groups"} {
			b.fail = fail
			if _, err := svc.Search(
				ctx,
				acct,
				space,
				holder.Users,
				search.Input{},
			); !errors.Is(
				err,
				errDown,
			) {
				t.Errorf("%s fails: Search = %v", fail, err)
			}
		}
		b.fail = "candidates"
		if _, err := svc.Suggest(
			ctx,
			acct,
			space,
			holder.Users,
			search.SuggestionsInput{},
		); !errors.Is(
			err,
			errDown,
		) {
			t.Errorf("Suggest = %v", err)
		}
		b.fail, j.fail = "", true
		for _, list := range []holder.List{holder.Users, holder.Groups} {
			if _, err := svc.Search(
				ctx,
				acct,
				space,
				list,
				search.Input{},
			); !errors.Is(
				err,
				errDown,
			) {
				t.Errorf("the evaluator fails: Search %s = %v", list, err)
			}
		}
	})
}
