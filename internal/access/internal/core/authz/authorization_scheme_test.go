package authz_test

import (
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitAuthorizationSchemeSources(t *testing.T) {
	for _, scheme := range []accessapi.AuthorizationScheme{accessapi.ManagedUsers, accessapi.LoginPolicies} {
		t.Run(string(scheme), func(t *testing.T) {
			x := newAccount()
			x.data.Scheme = scheme
			member, key, stack := kernel.NewMemberID(), kernel.NewULID(), kernel.NewULID()
			x.bind(binding.ActorUser, member.ULID, role.SpaceAdmin, "top")
			x.bind(binding.ActorAPIKey, key, role.SpaceAdmin, "top")
			x.bind(binding.ActorGroup, x.group("ops"), role.SpaceAdmin, "top")
			x.bind(binding.ActorStack, stack, role.SpaceWriter, "a")
			retained := authz.Who{Account: x.id, Kind: kernel.PrincipalAPIKey, Actor: key, Session: kernel.NewULID()}
			x.data.Sessions = []authz.SessionGrant{{Session: retained.Session, Binding: binding.Binding{
				AccountID: x.id, ID: kernel.NewULID(), ActorKind: binding.ActorAPIKey, ActorID: key,
				RoleID: x.roles[role.SpaceAdmin], SpaceID: x.spaces["top"], Origin: binding.OriginSchemeSwitch,
			}}}
			snap := authz.Build(x.data)
			if !snap.Admin(retained) {
				t.Fatal("the exact retained caller lost administration")
			}
			ordinary := retained
			ordinary.Session = kernel.NewULID()
			for _, who := range []authz.Who{ordinary, x.user(member), x.user(kernel.NewMemberID(), "ops")} {
				if snap.Admin(who) != (scheme == accessapi.ManagedUsers) {
					t.Errorf("ordinary IAM grant active under %s", scheme)
				}
			}
			if gs := snap.Explain(retained, x.spaces["top"]); len(gs) == 0 || gs[len(gs)-1].Origin != binding.OriginSchemeSwitch {
				t.Fatalf("session view omitted retained-grant provenance: %+v", gs)
			}
			run := authz.Who{Account: x.id, Kind: kernel.PrincipalRun, Stack: stack, StackHome: x.spaces["a"]}
			if !snap.Authorize(run, accessapi.ActionStackLock, x.spaces["a"]) {
				t.Fatal("stack binding was disabled")
			}
			retained.Account = kernel.NewAccountID()
			if snap.Admin(retained) {
				t.Fatal("retained grant crossed an account boundary")
			}
			retained.Account, retained.Actor = x.id, kernel.NewULID()
			if snap.Admin(retained) {
				t.Fatal("retained grant crossed an actor boundary")
			}
		})
	}
}
