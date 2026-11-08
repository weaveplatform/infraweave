package http

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	managed "github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// TestUnitManagedError checks the problem each service error answers, and
// that an error none names passes through as it is.
func TestUnitManagedError(t *testing.T) {
	for _, c := range []struct {
		err  error
		want problem.Type
	}{
		{
			&featuresapi.LimitError{Limit: "seats", Scope: "organization", Value: 3},
			problem.LimitReached,
		},
		{managed.ErrBindingsUnsupported, problem.InvalidRequest},
		{managed.ErrNotMember, problem.Forbidden},
		{fmt.Errorf("%w: bob", port.ErrNotFound), problem.NotFound},
		{port.ErrSlugTaken, problem.AlreadyExists},
		{managed.ErrInvalidState, problem.InvalidState},
		{organization.ErrInvalid, problem.ValidationFailed},
	} {
		pe, ok := errors.AsType[*problem.Error](managedError(c.err, "managed user"))
		if want := c.want.New(""); !ok || pe.Problem.Status != want.Status ||
			pe.Problem.Type != want.Type {
			t.Errorf("%v = %+v", c.err, pe)
		}
	}
	down := errors.New("down")
	if err := managedError(down, "managed user"); !errors.Is(err, down) {
		t.Errorf("an unnamed error = %v", err)
	}
}

// TestUnitManagedUserSeen checks that a member's last sign-in is served as
// lastLoginTime.
func TestUnitManagedUserSeen(t *testing.T) {
	seen := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	u := managedUser(managed.User{Member: member.Member{LastSeenAt: &seen}}, nil)
	if u.LastLoginTime == nil || *u.LastLoginTime != unix(seen) {
		t.Errorf("lastLoginTime = %v", u.LastLoginTime)
	}
}

// TestUnitManagedUsersNoPrincipal checks that each operation refuses a
// request that reaches it with no principal.
func TestUnitManagedUsersNoPrincipal(t *testing.T) {
	s := Server{}
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"users": func() error {
			_, err := s.ManagedUsers(ctx, api.ManagedUsersRequestObject{})
			return err
		},
		"user": func() error {
			_, err := s.ManagedUser(ctx, api.ManagedUserRequestObject{})
			return err
		},
		"invite": func() error {
			_, err := s.ManagedUserInviteV2(ctx, api.ManagedUserInviteV2RequestObject{})
			return err
		},
		"update": func() error {
			_, err := s.ManagedUserUpdate(ctx, api.ManagedUserUpdateRequestObject{})
			return err
		},
		"delete": func() error {
			_, err := s.ManagedUserDelete(ctx, api.ManagedUserDeleteRequestObject{})
			return err
		},
		"resend": func() error {
			_, err := s.ManagedUserResendInvite(ctx, api.ManagedUserResendInviteRequestObject{})
			return err
		},
		"request invite": func() error {
			_, err := s.ManagedUserRequestInvite(ctx, api.ManagedUserRequestInviteRequestObject{})
			return err
		},
		"request role change": func() error {
			_, err := s.ManagedUserRequestRoleChange(ctx,
				api.ManagedUserRequestRoleChangeRequestObject{})
			return err
		},
		"personal keys": func() error {
			_, err := s.ManagedUserPersonalApiKeys(ctx, api.ManagedUserPersonalApiKeysRequestObject{})
			return err
		},
		"groups": func() error {
			_, err := s.ManagedUserGroups(ctx, api.ManagedUserGroupsRequestObject{})
			return err
		},
		"group": func() error {
			_, err := s.ManagedUserGroup(ctx, api.ManagedUserGroupRequestObject{})
			return err
		},
		"create group": func() error {
			_, err := s.ManagedUserGroupCreateV2(ctx, api.ManagedUserGroupCreateV2RequestObject{})
			return err
		},
		"update group": func() error {
			_, err := s.ManagedUserGroupUpdateV2(ctx, api.ManagedUserGroupUpdateV2RequestObject{})
			return err
		},
		"delete group": func() error {
			_, err := s.ManagedUserGroupDelete(ctx, api.ManagedUserGroupDeleteRequestObject{})
			return err
		},
		"group users": func() error {
			_, err := s.ManagedUserGroupManagedUsers(ctx,
				api.ManagedUserGroupManagedUsersRequestObject{})
			return err
		},
		"available": func() error {
			_, err := s.AvailableUserGroups(ctx, api.AvailableUserGroupsRequestObject{})
			return err
		},
	} {
		if pe, ok := errors.AsType[*problem.Error](call()); !ok ||
			pe.Problem.Status != http.StatusUnauthorized {
			t.Errorf("%s without a principal: %v", name, pe)
		}
	}
}
