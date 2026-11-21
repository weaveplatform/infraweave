package authz_test

import (
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitLoginRootKeyExceptionRetainsOnlyItsActualBinding(t *testing.T) {
	for _, source := range []string{"direct root", "child admin", "root reader", "owner", "group", "another key", "custom role"} {
		t.Run(source, func(t *testing.T) {
			x := newAccount()
			x.data.Scheme = accessapi.LoginPolicies
			key, member := kernel.NewULID(), kernel.NewMemberID()
			who := authz.Who{Account: x.id, Kind: kernel.PrincipalAPIKey, Actor: key, Session: kernel.NewULID()}
			var bound kernel.ULID
			switch source {
			case "direct root":
				bound = x.bind(binding.ActorAPIKey, key, role.SpaceAdmin, "top")
				x.bind(binding.ActorAPIKey, key, role.SpaceReader, "legacy")
			case "child admin":
				x.bind(binding.ActorAPIKey, key, role.SpaceAdmin, "legacy")
			case "root reader":
				x.bind(binding.ActorAPIKey, key, role.SpaceReader, "top")
			case "owner":
				who.Member = member
				x.bind(binding.ActorUser, member.ULID, role.SpaceAdmin, "top")
			case "group":
				who.Teams = []string{"ops"}
				x.bind(binding.ActorGroup, x.group("ops"), role.SpaceAdmin, "top")
			case "another key":
				x.bind(binding.ActorAPIKey, kernel.NewULID(), role.SpaceAdmin, "top")
			case "custom role":
				id := kernel.NewULID()
				x.data.Roles = append(x.data.Roles, authz.Role{ID: id, Slug: role.SpaceAdmin})
				x.data.Bindings = append(
					x.data.Bindings,
					binding.Binding{AccountID: x.id, ID: kernel.NewULID(), ActorKind: binding.ActorAPIKey, ActorID: key, RoleID: id, SpaceID: x.spaces["top"]},
				)
			}
			snapshot := authz.Build(x.data)
			if snapshot.Admin(who) != (source == "direct root") {
				t.Fatalf("LOGIN root-key eligibility incorrectly used %s", source)
			}
			if source != "direct root" {
				return
			}
			grants := snapshot.Explain(who, x.spaces["top"])
			if len(grants) != 1 || grants[0].Binding != bound || grants[0].Origin != binding.OriginBinding {
				t.Fatalf("root exception lost its actual binding provenance: %+v", grants)
			}
			if grants := snapshot.Explain(who, x.spaces["legacy"]); len(grants) != 1 || grants[0].Binding != bound {
				t.Fatalf("root exception activated unrelated IAM bindings: %+v", grants)
			}
			x.data.Bindings = slices.DeleteFunc(x.data.Bindings, func(b binding.Binding) bool { return b.ID == bound })
			removed := authz.Build(x.data)
			if removed.Admin(who) || removed.Authorize(who, accessapi.ActionSpaceRead, x.spaces["legacy"]) {
				t.Fatal("removed root binding became a frozen LOGIN grant")
			}
		})
	}
}

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
			if !snap.Admin(ordinary) {
				t.Fatal("a key with its own live root IAM binding lost the LOGIN exception")
			}
			for _, who := range []authz.Who{x.user(member), x.user(kernel.NewMemberID(), "ops")} {
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

func TestUnitLoginSessionGrantsAreScopedAndInactiveInManagedUsers(t *testing.T) {
	for _, origin := range []binding.Origin{"ASSIGNED_ROLE", "ACCESS_LEVEL"} {
		t.Run(string(origin), func(t *testing.T) {
			x := newAccount()
			x.data.Scheme = accessapi.LoginPolicies
			who := authz.Who{Account: x.id, Kind: kernel.PrincipalUser, Actor: kernel.NewULID(), Session: kernel.NewULID()}
			id := kernel.NewULID()
			x.data.Sessions = []authz.SessionGrant{{Session: who.Session, Binding: binding.Binding{
				AccountID: x.id, ID: id, ActorKind: binding.ActorUser, ActorID: who.Actor,
				RoleID: x.roles[role.SpaceReader], SpaceID: x.spaces["legacy"], Origin: origin,
			}}}
			snapshot := authz.Build(x.data)
			if !snapshot.Authorize(who, accessapi.ActionSpaceRead, x.spaces["legacy"]) {
				t.Fatal("the admitted session lost its LOGIN grant")
			}
			grants := snapshot.Explain(who, x.spaces["legacy"])
			if len(grants) != 1 || grants[0].Binding != id || grants[0].Origin != origin {
				t.Fatalf("LOGIN grant lost its immutable identity or provenance: %+v", grants)
			}
			for _, alter := range []func(*authz.Who){
				func(w *authz.Who) { w.Account = kernel.NewAccountID() },
				func(w *authz.Who) { w.Actor = kernel.NewULID() },
				func(w *authz.Who) { w.Session = kernel.NewULID() },
				func(w *authz.Who) { w.Kind = kernel.PrincipalAPIKey },
			} {
				other := who
				alter(&other)
				if snapshot.Authorize(other, accessapi.ActionSpaceRead, x.spaces["legacy"]) {
					t.Fatal("LOGIN grant crossed its account, actor or exact-session boundary")
				}
			}
			x.data.Scheme = accessapi.ManagedUsers
			if authz.Build(x.data).Authorize(who, accessapi.ActionSpaceRead, x.spaces["legacy"]) {
				t.Fatal("an inactive LOGIN grant authorized a managed-user session")
			}
		})
	}
}
