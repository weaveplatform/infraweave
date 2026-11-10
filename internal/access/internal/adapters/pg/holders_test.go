package pg_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/holder"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

// TestAccHolders reads each grant view's candidates and searches its rows
// among the IDs it is given alone.
func TestAccHolders(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), "hooli")
	admit(t, x, x.member, "ops")
	soon := time.Now().Add(time.Hour)
	// A member's second web session revokes its first, so the ended one
	// comes first.
	ended := x.webSession(soon)
	if err := x.s.CreateSession(ctx, ended); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.EndSession(ctx, ended.ID); err != nil {
		t.Fatal(err)
	}
	for _, ss := range []session.Session{x.webSession(soon), x.keySession(soon)} {
		if err := x.s.CreateSession(ctx, ss); err != nil {
			t.Fatal(err)
		}
	}
	gone := kernel.NewULID()
	if _, err := x.db.Exec(ctx, `INSERT INTO api_keys (account_id, id, name, type, secret_hash,
		creator, deleted_at) VALUES ($1, $2, 'gone', 'SECRET', $3, 'test', now())`,
		x.acct, gone, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	team := kernel.NewULID()
	if _, err := x.db.Exec(ctx, `INSERT INTO managed_user_groups (account_id, id, name, description,
		created_at) VALUES ($1, $2, 'ops', 'on call', now())`, x.acct, team); err != nil {
		t.Fatal(err)
	}

	users, err := x.s.Candidates(ctx, x.acct, holder.Users)
	if err != nil || len(users) != 1 || users[0].ID != x.member.ULID ||
		users[0].Kind != kernel.PrincipalUser || users[0].Member != x.member ||
		!slices.Equal(users[0].Teams, []string{"ops"}) {
		t.Fatalf("user candidates = %+v, %v", users, err)
	}
	keys, err := x.s.Candidates(ctx, x.acct, holder.Keys)
	if err != nil || len(keys) != 1 || keys[0].ID != x.key ||
		keys[0].Kind != kernel.PrincipalAPIKey ||
		!slices.Equal(keys[0].Teams, []string{"ops"}) {
		t.Fatalf("key candidates = %+v, %v", keys, err)
	}
	people, err := x.s.Candidates(ctx, x.acct, holder.UserSessions)
	if err != nil || len(people) != 1 || people[0].ID != x.member.ULID || people[0].At.IsZero() {
		t.Fatalf("user session candidates = %+v, %v", people, err)
	}
	machines, err := x.s.Candidates(ctx, x.acct, holder.KeySessions)
	if err != nil || len(machines) != 1 || machines[0].ID != x.key {
		t.Fatalf("key session candidates = %+v, %v", machines, err)
	}
	if none, err := x.s.Candidates(ctx, x.acct, holder.Groups); err != nil || len(none) != 0 {
		t.Fatalf("group candidates = %+v, %v", none, err)
	}

	for list, id := range map[holder.List]kernel.ULID{
		holder.Users: x.member.ULID, holder.UserSessions: x.member.ULID,
		holder.Keys: x.key, holder.KeySessions: x.key, holder.Groups: team,
	} {
		page, err := x.s.SearchHolders(ctx, x.acct, list, search.Input{}, []kernel.ULID{id})
		if err != nil || len(page.Edges) != 1 || page.Edges[0].Node.ID != id {
			t.Errorf("%s search = %+v, %v", list, page, err)
		}
		page, err = x.s.SearchHolders(ctx, x.acct, list, search.Input{}, nil)
		if err != nil || len(page.Edges) != 0 {
			t.Errorf("%s search among none = %+v, %v", list, page, err)
		}
		got, err := x.s.SuggestHolders(
			ctx,
			x.acct,
			list,
			search.SuggestionsInput{},
			[]kernel.ULID{id},
		)
		if err != nil || got.Count != 1 {
			t.Errorf("%s suggestions = %+v, %v", list, got, err)
		}
	}
	page, err := x.s.SearchHolders(ctx, x.acct, holder.Users, search.Input{FullText: "ALI"},
		[]kernel.ULID{x.member.ULID})
	if err != nil || len(page.Edges) != 1 || page.Edges[0].Node.Login != "alice" ||
		page.Edges[0].Node.Email != "alice@example.com" {
		t.Errorf("a user by its login = %+v, %v", page, err)
	}
	groups, err := x.s.HolderGroups(ctx, x.acct, []kernel.ULID{team, kernel.NewULID()})
	if err != nil || len(groups) != 1 || groups[0].Name != "ops" ||
		groups[0].Description != "on call" {
		t.Errorf("groups = %+v, %v", groups, err)
	}

	p := pool(t)
	s := pg.New(p)
	p.Close()
	bg := context.Background()
	if _, err := s.Candidates(bg, x.acct, holder.Users); err == nil {
		t.Error("candidates from a closed pool")
	}
	if _, err := s.SearchHolders(bg, x.acct, holder.Users, search.Input{}, nil); err == nil {
		t.Error("search from a closed pool")
	}
	if _, err := s.SuggestHolders(
		bg,
		x.acct,
		holder.Users,
		search.SuggestionsInput{},
		nil,
	); err == nil {
		t.Error("suggestions from a closed pool")
	}
	if _, err := s.HolderGroups(bg, x.acct, nil); err == nil {
		t.Error("groups from a closed pool")
	}
}
