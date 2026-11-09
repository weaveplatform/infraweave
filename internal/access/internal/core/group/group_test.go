package group_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/group"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var (
	t0      = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	errDown = errors.New("down")
)

// store is group.Store over one account's groups; fail makes every
// method answer errDown.
type store struct {
	acct   kernel.AccountID
	groups map[kernel.ULID]group.Group
	fail   bool
	trail  []kernel.AuditEntry
}

func (s *store) group(acct kernel.AccountID, id kernel.ULID) (group.Group, error) {
	if s.fail {
		return group.Group{}, errDown
	}
	g, ok := s.groups[id]
	if !ok || acct != s.acct {
		return group.Group{}, fmt.Errorf("%w: %s", port.ErrNotFound, id)
	}
	return g, nil
}

func (s *store) Groups(
	_ context.Context,
	acct kernel.AccountID,
	_ kernel.ULID,
	_ int,
) ([]group.Group, error) {
	if s.fail {
		return nil, errDown
	}
	var out []group.Group
	for _, g := range s.groups {
		if acct == s.acct {
			out = append(out, g)
		}
	}
	return out, nil
}

func (s *store) Group(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (group.Group, error) {
	return s.group(acct, id)
}

func (s *store) CreateGroup(
	_ context.Context,
	g group.Group,
	e kernel.AuditEntry,
) (group.Group, error) {
	if s.fail {
		return group.Group{}, errDown
	}
	for _, o := range s.groups {
		if o.Name == g.Name {
			return group.Group{}, port.ErrSlugTaken
		}
	}
	s.groups[g.ID] = g
	s.trail = append(s.trail, e)
	return g, nil
}

func (s *store) SetDescription(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	expect kernel.Expect,
	description string,
	e kernel.AuditEntry,
) (group.Group, error) {
	g, err := s.group(acct, id)
	if err != nil {
		return g, err
	}
	if err := expect.Check(g.Version); err != nil {
		return group.Group{}, err
	}
	g.Description, g.Version = description, g.Version+1
	s.groups[id] = g
	s.trail = append(s.trail, e)
	return g, nil
}

func (s *store) DeleteGroup(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	expect kernel.Expect,
	e kernel.AuditEntry,
) error {
	g, err := s.group(acct, id)
	if err != nil {
		return err
	}
	if err := expect.Check(g.Version); err != nil {
		return err
	}
	delete(s.groups, id)
	s.trail = append(s.trail, e)
	return nil
}

func (s *store) GroupUsers(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	_ kernel.MemberID,
	_ int,
) ([]user.User, error) {
	if _, err := s.group(acct, id); err != nil {
		return nil, err
	}
	return []user.User{{AccountID: acct}}, nil
}

func (s *store) AvailableGroups(
	_ context.Context,
	_ kernel.AccountID,
	after string,
	_ int,
) ([]string, error) {
	if s.fail {
		return nil, errDown
	}
	if after == "" {
		return []string{"dev", "ops"}, nil
	}
	return []string{"ops"}, nil
}

// deleteFails answers DeleteGroup with errDown after a read that succeeds.
type deleteFails struct{ *store }

func (deleteFails) DeleteGroup(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
	kernel.Expect,
	kernel.AuditEntry,
) error {
	return errDown
}

func TestUnitGroups(t *testing.T) {
	ctx := t.Context()
	s := &store{acct: kernel.NewAccountID(), groups: map[kernel.ULID]group.Group{}}
	svc := group.NewService(group.Deps{Store: s, Now: func() time.Time { return t0 }})
	p := kernel.Principal{Kind: kernel.PrincipalUser, AccountID: s.acct, Login: "alice"}

	g, err := svc.Create(ctx, p, "192.0.2.1", "ops", "operators", 0)
	if err != nil || g.Name != "ops" || g.Description != "operators" || !g.CreatedAt.Equal(t0) ||
		g.AccountID != s.acct {
		t.Fatalf("create = %+v, %v", g, err)
	}
	if e := s.trail[0]; e.Action != group.ActionCreate || e.RemoteIP != "192.0.2.1" ||
		e.AffectedResource.ID != g.ID.String() || len(e.Body) == 0 {
		t.Errorf("entry = %+v", e)
	}
	if _, err := svc.Create(ctx, p, "", "ops", "", 0); !errors.Is(err, port.ErrSlugTaken) {
		t.Errorf("a second ops: %v", err)
	}
	if _, err := svc.Create(ctx, p, "", "qa", "", 2); !errors.Is(err, user.ErrBindingsUnsupported) {
		t.Errorf("with role bindings: %v", err)
	}
	var fe *organization.FieldError
	if _, err := svc.Create(
		ctx,
		p,
		"",
		"",
		"",
		0,
	); !errors.As(err, &fe) ||
		fe.Field != "groupName" {
		t.Errorf("no name: %v", err)
	}

	id := g.ID.String()
	if got, err := svc.Group(ctx, p, id); err != nil || got.ID != g.ID {
		t.Errorf("group = %+v, %v", got, err)
	}
	if gs, err := svc.Groups(ctx, p, kernel.ULID{}, 10); err != nil || len(gs) != 1 {
		t.Errorf("groups = %d, %v", len(gs), err)
	}
	for _, bad := range []string{"nope", kernel.NewULID().String()} {
		if _, err := svc.Group(ctx, p, bad); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("group %q: %v", bad, err)
		}
	}
	q := p
	q.AccountID = kernel.NewAccountID()
	if _, err := svc.Group(ctx, q, id); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("group from another account: %v", err)
	}

	if got, err := svc.Update(
		ctx,
		p,
		"",
		id,
		kernel.Expect{},
		"ops people",
	); err != nil ||
		got.Description != "ops people" {
		t.Errorf("update = %+v, %v", got, err)
	}
	if _, err := svc.Update(
		ctx,
		p,
		"",
		"nope",
		kernel.Expect{},
		"",
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("update of no group: %v", err)
	}
	if us, err := svc.Users(ctx, p, id, kernel.MemberID{}, 10); err != nil || len(us) != 1 {
		t.Errorf("users = %d, %v", len(us), err)
	}
	if _, err := svc.Users(
		ctx,
		p,
		"nope",
		kernel.MemberID{},
		10,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("users of no group: %v", err)
	}
	if names, err := svc.Available(ctx, p, "", 10); err != nil || len(names) != 2 {
		t.Errorf("available = %v, %v", names, err)
	}

	// The store sees the expect each change carries.
	stale := kernel.ExpectVersions(9)
	if _, err := svc.Update(ctx, p, "", id, stale, "x"); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale update: %v", err)
	}
	if _, err := svc.Delete(ctx, p, "", id, stale); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale delete: %v", err)
	}

	failing := group.NewService(
		group.Deps{Store: deleteFails{s}, Now: func() time.Time { return t0 }},
	)
	if _, err := failing.Delete(ctx, p, "", id, kernel.Expect{}); !errors.Is(err, errDown) {
		t.Errorf("a failing delete: %v", err)
	}
	if got, err := svc.Delete(ctx, p, "", id, kernel.Expect{}); err != nil || got.ID != g.ID {
		t.Errorf("delete = %+v, %v", got, err)
	}
	if _, err := svc.Delete(ctx, p, "", id, kernel.Expect{}); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("delete again: %v", err)
	}
}
