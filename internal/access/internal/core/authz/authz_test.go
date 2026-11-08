package authz_test

import (
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// account is a test account's IDs: its spaces by name, its system roles by
// slug, and the snapshot's rows.
type account struct {
	id     kernel.AccountID
	spaces map[string]kernel.ULID
	roles  map[string]kernel.ULID
	data   authz.Data
}

// newAccount builds the tree
//
//	top
//	├── a           inherit off
//	│   └── a1      inherit on
//	│       └── a11 inherit on
//	│           └── a111 inherit off
//	└── legacy
//
// with the five system roles and a custom role holding WORKER_POOL_CYCLE.
func newAccount() *account {
	x := &account{
		id:     kernel.NewAccountID(),
		spaces: map[string]kernel.ULID{},
		roles:  map[string]kernel.ULID{},
	}
	x.data.Account = x.id
	add := func(name, parent string, inherit bool) {
		id := kernel.NewULID()
		if name == "top" {
			id = x.id.ULID
		}
		x.spaces[name] = id
		x.data.Spaces = append(
			x.data.Spaces,
			authz.Space{ID: id, Parent: x.spaces[parent], Inherit: inherit},
		)
	}
	add("top", "", false)
	add("a", "top", false)
	add("a1", "a", true)
	add("a11", "a1", true)
	add("a111", "a11", false)
	add("legacy", "top", false)
	for _, r := range role.Seed(x.id) {
		x.roles[r.Slug] = r.ID
		x.data.Roles = append(x.data.Roles, authz.Role{ID: r.ID, Slug: r.Slug, System: true})
	}
	custom := kernel.NewULID()
	x.roles["custom"] = custom
	x.data.Roles = append(x.data.Roles, authz.Role{
		ID: custom, Slug: "custom", Actions: []accessapi.Action{accessapi.ActionWorkerPoolCycle},
	})
	return x
}

// bind binds the role slug on space to the actor, and returns the
// binding's ID.
func (x *account) bind(kind role.ActorKind, actor kernel.ULID, slug, space string) kernel.ULID {
	id := kernel.NewULID()
	x.data.Bindings = append(x.data.Bindings, role.Binding{
		AccountID: x.id, ID: id, ActorKind: kind, ActorID: actor, RoleID: x.roles[slug],
		SpaceID: x.spaces[space], Origin: role.OriginBinding,
	})
	return id
}

func (x *account) group(name string) kernel.ULID {
	id := kernel.NewULID()
	x.data.Groups = append(x.data.Groups, authz.Group{ID: id, Name: name})
	return id
}

func (x *account) user(m kernel.MemberID, teams ...string) authz.Who {
	return authz.Who{
		Account: x.id,
		Kind:    kernel.PrincipalUser,
		Actor:   m.ULID,
		Member:  m,
		Teams:   teams,
	}
}

// check is one decision: who may or may not perform action in space.
type check struct {
	name   string
	who    authz.Who
	action accessapi.Action
	space  string
	want   bool
}

// TestUnitAuthorize runs the reference's worked cases and infraweave's own
// evaluation choices on one tree.
//
// ref: report 14, decision 0007
func TestUnitAuthorize(t *testing.T) {
	x := newAccount()
	admin, writer, inheritor, deep := kernel.NewMemberID(), kernel.NewMemberID(),
		kernel.NewMemberID(), kernel.NewMemberID()
	grouped, owner, outsider := kernel.NewMemberID(), kernel.NewMemberID(), kernel.NewMemberID()
	x.bind(role.ActorUser, admin.ULID, role.SpaceAdmin, "top")
	x.bind(role.ActorUser, writer.ULID, role.SpaceWriter, "top")
	x.bind(role.ActorUser, inheritor.ULID, role.SpaceAdmin, "a11")
	x.bind(role.ActorUser, deep.ULID, role.SpaceAdmin, "a111")
	ops := x.group("ops")
	x.bind(role.ActorGroup, ops, role.WorkerPoolController, "legacy")
	x.bind(role.ActorUser, grouped.ULID, "custom", "legacy")
	x.bind(role.ActorUser, owner.ULID, role.SpaceWriter, "a")
	key := kernel.NewULID()
	x.bind(role.ActorAPIKey, key, role.SpaceReader, "legacy")
	adminStack, plainStack := kernel.NewULID(), kernel.NewULID()
	x.bind(role.ActorStack, adminStack, role.SpaceAdmin, "top")
	snap := authz.Build(x.data)

	other := newAccount()
	other.bind(role.ActorUser, admin.ULID, role.SpaceAdmin, "top")
	otherSnap := authz.Build(other.data)

	personal := authz.Who{
		Account: x.id, Kind: kernel.PrincipalAPIKey, Actor: kernel.NewULID(),
		Member: owner,
	}
	plainKey := authz.Who{Account: x.id, Kind: kernel.PrincipalAPIKey, Actor: key}
	run := func(stack kernel.ULID, home string) authz.Who {
		return authz.Who{
			Account: x.id, Kind: kernel.PrincipalRun, Actor: kernel.NewULID(),
			Stack: stack, StackHome: x.spaces[home],
		}
	}
	for _, c := range []check{
		// An account admin may do everything in its account, the disabled
		// and pseudo-actions included, but not the organization's.
		{"admin reads", x.user(admin), accessapi.ActionSpaceRead, "a111", true},
		{"admin's disabled action", x.user(admin), accessapi.ActionStackWebhookCreate, "a", true},
		{"admin's pseudo-action", x.user(admin), accessapi.ActionAccountAdmin, "top", true},
		{
			"no account grants ORGANIZATION_ADMIN", x.user(admin), accessapi.ActionOrganizationAdmin,
			"top", false,
		},
		{
			"every principal of the account is AUTHENTICATED", x.user(outsider),
			accessapi.ActionAuthenticated, "a", true,
		},
		// Propagation from the top space.
		{"writer on top writes below", x.user(writer), accessapi.ActionStackLock, "a111", true},
		{"writer on top does not administer", x.user(writer), accessapi.ActionStackCreate, "a", false},
		{"writer is not ACCOUNT_ADMIN", x.user(writer), accessapi.ActionAccountAdmin, "top", false},
		// Inheritance read-up: a11 and a1 inherit, so a binding on a11
		// reads a1 and, past its immediate parent, a; a does not, so top is
		// not read.
		{"inherited by the parent", x.user(inheritor), accessapi.ActionSpaceRead, "a1", true},
		{"climbs past the immediate parent", x.user(inheritor), accessapi.ActionSpaceRead, "a", true},
		{"stops at a space without inherit", x.user(inheritor), accessapi.ActionSpaceRead, "top", false},
		{"inherited folds to space-reader", x.user(inheritor), accessapi.ActionStackLock, "a1", false},
		{"resident admin keeps its actions", x.user(inheritor), accessapi.ActionStackCreate, "a11", true},
		{"propagated admin below", x.user(inheritor), accessapi.ActionStackCreate, "a111", true},
		{"a space without inherit lends nothing up", x.user(deep), accessapi.ActionSpaceRead, "a11", false},
		// Group and direct bindings combine.
		{"group's role", x.user(grouped, "ops"), accessapi.ActionWorkerPoolCreate, "legacy", true},
		{"direct custom role", x.user(grouped, "ops"), accessapi.ActionWorkerPoolCycle, "legacy", true},
		{"group not named by teams", x.user(grouped), accessapi.ActionWorkerPoolCreate, "legacy", false},
		{
			"an unknown team grants nothing", x.user(outsider, "devs"), accessapi.ActionSpaceRead, "legacy",
			false,
		},
		// A personal key acts with its owner's set; another key with its
		// own.
		{"personal key as its owner", personal, accessapi.ActionStackLock, "a", true},
		{"personal key not beyond its owner", personal, accessapi.ActionStackLock, "legacy", false},
		{"key's own binding", plainKey, accessapi.ActionSpaceRead, "legacy", true},
		{"key's own binding only", plainKey, accessapi.ActionSpaceRead, "a", false},
		// A stack reads its home, below it, and up through inheriting
		// spaces; a run never performs the in-run restriction, even for an
		// admin's stack.
		{"stack reads its home", run(plainStack, "a11"), accessapi.ActionSpaceRead, "a11", true},
		{"stack reads below its home", run(plainStack, "a11"), accessapi.ActionSpaceRead, "a111", true},
		{"stack's read climbs", run(plainStack, "a11"), accessapi.ActionSpaceRead, "a", true},
		{"stack's read stops", run(plainStack, "a11"), accessapi.ActionSpaceRead, "top", false},
		{"stack's read is a read", run(plainStack, "a11"), accessapi.ActionStackLock, "a11", false},
		{"admin's stack locks", run(adminStack, "legacy"), accessapi.ActionStackLock, "a", true},
		{
			"admin's stack reads no state", run(adminStack, "legacy"), accessapi.ActionStackStateRead,
			"legacy", false,
		},
		{
			"admin's stack triggers no run", run(adminStack, "legacy"), accessapi.ActionRunTrigger, "a",
			false,
		},
		{
			"admin's stack creates no task", run(adminStack, "legacy"), accessapi.ActionTaskCreate, "a",
			false,
		},
		{
			"a run is no account admin", run(adminStack, "legacy"), accessapi.ActionAccountAdmin, "top",
			false,
		},
		// Nothing reaches across accounts.
		{"another account's space", x.user(admin), accessapi.ActionSpaceRead, "elsewhere", false},
		{"no principal kind outside the account's", authz.Who{
			Account: x.id,
			Kind:    kernel.PrincipalOrganization,
		}, accessapi.ActionSpaceRead, "a", false},
	} {
		space := x.spaces[c.space]
		if c.space == "elsewhere" {
			space = other.spaces["a"]
		}
		if got := snap.Authorize(c.who, c.action, space); got != c.want {
			t.Errorf(
				"%s: Authorize(%s in %s) = %t, want %t",
				c.name,
				c.action,
				c.space,
				got,
				c.want,
			)
		}
	}
	// The admin of x is an admin of another account only where that one
	// binds them, and x's principal decides nothing in other's snapshot.
	if !otherSnap.Admin(other.user(admin)) || otherSnap.Admin(x.user(admin)) ||
		otherSnap.Authorize(x.user(admin), accessapi.ActionSpaceRead, other.spaces["a"]) {
		t.Error("a principal of one account decides in another")
	}
	if snap.Explain(x.user(admin), other.spaces["a"]) != nil ||
		otherSnap.Reachable(x.user(admin), accessapi.ActionSpaceRead) != nil {
		t.Error("another account's space explains or reaches")
	}
}

// TestUnitExplain checks each grant's source, binding and role.
func TestUnitExplain(t *testing.T) {
	x := newAccount()
	m := kernel.NewMemberID()
	onTop := x.bind(role.ActorUser, m.ULID, role.SpaceWriter, "top")
	onA11 := x.bind(role.ActorUser, m.ULID, "custom", "a11")
	onA1 := x.bind(role.ActorUser, m.ULID, role.WorkerPoolController, "a1")
	snap := authz.Build(x.data)
	got := snap.Explain(x.user(m), x.spaces["a1"])
	want := []authz.Grant{
		{
			Source: authz.Propagated, Binding: onTop, Actor: role.ActorUser, ActorID: m.ULID,
			Space: x.spaces["top"], RoleID: x.roles[role.SpaceWriter], Role: role.SpaceWriter,
		},
		{
			Source: authz.Inherited, Binding: onA11, Actor: role.ActorUser, ActorID: m.ULID,
			Space: x.spaces["a11"], RoleID: x.roles[role.SpaceReader], Role: role.SpaceReader,
		},
		{
			Source:  authz.Resident,
			Binding: onA1,
			Actor:   role.ActorUser,
			ActorID: m.ULID,
			Space:   x.spaces["a1"],
			RoleID:  x.roles[role.WorkerPoolController],
			Role:    role.WorkerPoolController,
		},
	}
	if len(got) != len(want) {
		t.Fatalf("Explain = %+v", got)
	}
	for i := range want {
		g := got[i]
		if g.Source != want[i].Source || g.Binding != want[i].Binding || g.Actor != want[i].Actor ||
			g.ActorID != want[i].ActorID || g.Space != want[i].Space || g.RoleID != want[i].RoleID ||
			g.Role != want[i].Role {
			t.Errorf("grant %d = %+v, want %+v", i, g, want[i])
		}
	}
	if got[1].Holds(accessapi.ActionWorkerPoolCycle) || !got[1].Holds(accessapi.ActionSpaceRead) {
		t.Error("an inherited custom grant holds other than space-reader's actions")
	}
	stack := kernel.NewULID()
	run := authz.Who{
		Account:   x.id,
		Kind:      kernel.PrincipalRun,
		Stack:     stack,
		StackHome: x.spaces["a"],
	}
	implicit := snap.Explain(run, x.spaces["a1"])
	if len(implicit) != 1 || !implicit[0].Binding.IsZero() ||
		implicit[0].Source != authz.Propagated ||
		implicit[0].Role != role.SpaceReader ||
		implicit[0].ActorID != stack {
		t.Errorf("a stack's implicit read = %+v", implicit)
	}
	if snap.Explain(x.user(m), kernel.NewULID()) != nil {
		t.Error("a space the snapshot lacks explains")
	}
	if g := snap.Explain(
		authz.Who{Account: x.id, Kind: kernel.PrincipalRun},
		x.spaces["a"],
	); g != nil {
		t.Errorf("a run with no stack = %+v", g)
	}
}

func TestUnitRunDenied(t *testing.T) {
	for _, c := range []struct {
		action  accessapi.Action
		subject string
		want    bool
	}{
		{accessapi.ActionStackStateRead, "", true},
		{accessapi.ActionStackStateDownload, "", true},
		{accessapi.ActionRunConfirm, "", true},
		{accessapi.ActionTaskCreate, "", true},
		{accessapi.ActionSpaceRead, "run", true},
		{accessapi.ActionSpaceRead, "task", true},
		{accessapi.ActionSpaceRead, "log", true},
		{accessapi.ActionSpaceRead, "stack", false},
		{accessapi.ActionStackLock, "", false},
	} {
		if got := authz.RunDenied(c.action, c.subject); got != c.want {
			t.Errorf("RunDenied(%s, %q) = %t", c.action, c.subject, got)
		}
	}
}

// TestUnitBuildSkipsDangling checks that a binding naming a role or space
// the rows lack is left out rather than granting.
func TestUnitBuildSkipsDangling(t *testing.T) {
	x := newAccount()
	m := kernel.NewMemberID()
	x.data.Bindings = append(x.data.Bindings,
		role.Binding{
			ID: kernel.NewULID(), ActorKind: role.ActorUser, ActorID: m.ULID,
			RoleID: kernel.NewULID(), SpaceID: x.spaces["a"],
		},
		role.Binding{
			ID: kernel.NewULID(), ActorKind: role.ActorUser, ActorID: m.ULID,
			RoleID: x.roles[role.SpaceAdmin], SpaceID: kernel.NewULID(),
		})
	snap := authz.Build(x.data)
	if snap.Explain(x.user(m), x.spaces["a"]) != nil || snap.Account() != x.id ||
		snap.Version() != 0 {
		t.Error("a dangling binding grants")
	}
	if got := snap.Reachable(x.user(m), accessapi.ActionSpaceRead); len(got) != 0 {
		t.Errorf("Reachable = %v", got)
	}
	if !slices.Equal(snap.Reachable(x.user(m), accessapi.ActionAuthenticated)[:1],
		[]kernel.ULID{x.spaces["top"]}) {
		t.Error("Reachable does not start at the top space")
	}
	empty := authz.Build(authz.Data{Account: x.id})
	if got := empty.Reachable(x.user(m), accessapi.ActionAuthenticated); len(got) != 0 {
		t.Errorf("an account with no tree reaches %v", got)
	}
}

// TestUnitActions checks a space's actions: the space's own for the
// enum's, the top space's for infraweave's account actions, and any
// space's for an anywhere one, as the reference lets a space's admin read
// keys and users but manage neither.
//
// ref: report 14
func TestUnitActions(t *testing.T) {
	x := newAccount()
	admin, below, writer := kernel.NewMemberID(), kernel.NewMemberID(), kernel.NewMemberID()
	x.bind(role.ActorUser, admin.ULID, role.SpaceAdmin, "top")
	x.bind(role.ActorUser, below.ULID, role.SpaceAdmin, "a11")
	x.bind(role.ActorUser, writer.ULID, role.SpaceWriter, "top")
	snap := authz.Build(x.data)
	has := slices.Contains[[]accessapi.Action]

	if got := snap.Actions(x.user(admin), x.spaces["top"]); len(got) != 145 {
		t.Errorf("the account admin holds %d actions on the top space, want 145", len(got))
	}
	got := snap.Actions(x.user(below), x.spaces["a11"])
	if !has(got, accessapi.ActionStackCreate) || !has(got, accessapi.ActionApiKeyRead) ||
		has(got, accessapi.ActionApiKeyDelete) || has(got, accessapi.ActionStackWebhookCreate) {
		t.Errorf("a space's admin's actions there = %v", got)
	}
	got = snap.Actions(x.user(below), x.spaces["top"])
	if !has(got, accessapi.ActionApiKeyRead) || !has(got, accessapi.ActionManagedUserRead) ||
		has(got, accessapi.ActionStackCreate) || has(got, accessapi.ActionManagedUserInvite) {
		t.Errorf("a space's admin's actions on the top space = %v", got)
	}
	got = snap.Actions(x.user(writer), x.spaces["a"])
	if !has(got, accessapi.ActionStackLock) || has(got, accessapi.ActionApiKeyRead) {
		t.Errorf("a writer's actions = %v", got)
	}
	if !snap.Holds(x.user(below), accessapi.ActionApiKeyRead) ||
		snap.Holds(x.user(writer), accessapi.ActionApiKeyRead) {
		t.Error("Holds disagrees with the bindings")
	}
	if got := snap.Actions(x.user(admin), kernel.NewULID()); got != nil {
		t.Errorf("actions in a space of no account = %v", got)
	}
	if got := snap.Actions(authz.Who{Account: kernel.NewAccountID(), Kind: kernel.PrincipalUser},
		x.spaces["top"]); got != nil {
		t.Errorf("actions of another account's principal = %v", got)
	}
}
