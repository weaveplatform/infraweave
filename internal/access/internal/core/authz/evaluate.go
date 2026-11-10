package authz

import (
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Who is a principal as the evaluator sees it. Member is the user a
// person's session acts as, which a personal key's session acts as too;
// Teams name the managed user groups it belongs to. A run's Stack and
// StackHome, the space its stack lives in, come with the run's token.
type Who struct {
	Account   kernel.AccountID
	Kind      kernel.PrincipalKind
	Actor     kernel.ULID
	Member    kernel.MemberID
	Teams     []string
	Stack     kernel.ULID
	StackHome kernel.ULID
}

// Source is how a grant reaches a space.
//
// ref: decision 0007 § "Evaluation"
type Source string

const (
	// Resident is a grant bound on the space itself.
	Resident Source = "RESIDENT"
	// Propagated is a grant bound on an ancestor.
	Propagated Source = "PROPAGATED"
	// Inherited is a grant bound on a descendant whose path up inherits
	// entities, which contributes only space-reader's actions.
	Inherited Source = "INHERITED"
)

// Grant is one role reaching a space, for the grant views. Binding is
// zero for a stack's implicit read of its home space, and Role is
// space-reader for that and for every inherited grant, whatever role the
// binding names.
type Grant struct {
	Source  Source
	Binding kernel.ULID
	Actor   binding.ActorKind
	ActorID kernel.ULID
	Space   kernel.ULID
	RoleID  kernel.ULID
	Role    string
	actions map[accessapi.Action]bool
}

// Holds reports whether the grant gives action.
func (g Grant) Holds(action accessapi.Action) bool { return g.actions[action] }

// runDenied are the actions a run's token never performs, beside every
// RUN_ action.
var runDenied = []accessapi.Action{
	accessapi.ActionStackStateRead, accessapi.ActionStackStateDownload, accessapi.ActionTaskCreate,
}

// runRead are the subjects whose reads a run's token never makes.
var runRead = []string{"run", "task", "log"}

// RunDenied reports whether a run's token is refused action on subject,
// whatever its stack holds: the state reads, every RUN_ action,
// TASK_CREATE, and SPACE_READ of a run, a task or a log.
//
// ref: decision 0007 § "Evaluation"
func RunDenied(action accessapi.Action, subject string) bool {
	return slices.Contains(runDenied, action) || strings.HasPrefix(string(action), "RUN_") ||
		action == accessapi.ActionSpaceRead && slices.Contains(runRead, subject)
}

// actors is w's actor set: a person's user and the groups its teams name,
// an API key and its teams (a personal key's owner and teams), or a run's
// stack.
func (s *Snapshot) actors(w Who) []actor {
	var out []actor
	switch {
	case w.Kind == kernel.PrincipalRun:
		if !w.Stack.IsZero() {
			out = append(out, actor{binding.ActorStack, w.Stack})
		}
		return out
	case w.Kind == kernel.PrincipalUser, w.Kind == kernel.PrincipalAPIKey && !w.Member.IsZero():
		out = append(out, actor{binding.ActorUser, w.Member.ULID})
	case w.Kind == kernel.PrincipalAPIKey:
		out = append(out, actor{binding.ActorAPIKey, w.Actor})
	default:
		return nil
	}
	for _, t := range w.Teams {
		if id, ok := s.groups[t]; ok {
			out = append(out, actor{binding.ActorGroup, id})
		}
	}
	return out
}

// bound is every binding of w's actors, and a run's implicit read of its
// stack's home space as a binding of space-reader with no ID.
func (s *Snapshot) bound(w Who) []binding.Binding {
	var out []binding.Binding
	for _, a := range s.actors(w) {
		out = append(out, s.bindings[a]...)
	}
	if w.Kind == kernel.PrincipalRun && !w.Stack.IsZero() {
		if _, ok := s.parent[w.StackHome]; ok {
			out = append(out, binding.Binding{
				ActorKind: binding.ActorStack,
				ActorID:   w.Stack,
				RoleID:    s.reader.id,
				SpaceID:   w.StackHome,
			})
		}
	}
	return out
}

// roleOf is b's role; a binding with no ID is a stack's implicit read.
func (s *Snapshot) roleOf(b binding.Binding) grantRole {
	if b.ID.IsZero() {
		return s.reader
	}
	return s.roles[b.RoleID]
}

// in reports whether w acts in the snapshot's account at all.
func (s *Snapshot) in(w Who) bool { return !w.Account.IsZero() && w.Account == s.account }

// Admin reports whether w is an account admin: one of its actors holds
// space-admin on the account's top space.
func (s *Snapshot) Admin(w Who) bool {
	if !s.in(w) || w.Kind == kernel.PrincipalRun {
		return false
	}
	for _, b := range s.bound(w) {
		if r := s.roleOf(b); b.SpaceID == s.top && r.system && r.slug == role.SpaceAdmin {
			return true
		}
	}
	return false
}

// Explain is every grant that reaches space for w, in binding order.
func (s *Snapshot) Explain(w Who, space kernel.ULID) []Grant {
	if _, ok := s.parent[space]; !ok || !s.in(w) {
		return nil
	}
	var out []Grant
	for _, b := range s.bound(w) {
		r := s.roleOf(b)
		g := Grant{
			Binding: b.ID, Actor: b.ActorKind, ActorID: b.ActorID, Space: b.SpaceID,
			RoleID: r.id, Role: r.slug, actions: r.actions,
		}
		switch {
		case b.SpaceID == space:
			g.Source = Resident
		case s.ancestor(b.SpaceID, space):
			g.Source = Propagated
		case s.ancestor(space, b.SpaceID) && s.inheritedBy(b.SpaceID, space):
			g.Source, g.RoleID, g.Role, g.actions = Inherited, s.reader.id, s.reader.slug, s.reader.actions
		default:
			continue
		}
		out = append(out, g)
	}
	return out
}

// Authorize decides whether w may perform action in space. An account
// admin may perform every action, the pseudo-action ACCOUNT_ADMIN among
// them; any principal of the account is AUTHENTICATED; a run never
// performs what RunDenied refuses. ORGANIZATION_ADMIN is the organization
// role's, which no account grants. Another account's space, or one the
// snapshot lacks, is refused.
func (s *Snapshot) Authorize(w Who, action accessapi.Action, space kernel.ULID) bool {
	if _, ok := s.parent[space]; !ok || !s.in(w) {
		return false
	}
	switch {
	case action == accessapi.ActionOrganizationAdmin,
		w.Kind == kernel.PrincipalRun && RunDenied(action, ""):
		return false
	case action == accessapi.ActionAuthenticated:
		return true
	case s.Admin(w):
		return true
	}
	for _, g := range s.Explain(w, space) {
		if g.Holds(action) {
			return true
		}
	}
	return false
}

// Reachable is every space in which w may perform action, in the
// snapshot's order: parents first. It is the set of spaces Authorize
// allows, computed from the bindings rather than space by space.
func (s *Snapshot) Reachable(w Who, action accessapi.Action) []kernel.ULID {
	if !s.in(w) || action == accessapi.ActionOrganizationAdmin ||
		w.Kind == kernel.PrincipalRun && RunDenied(action, "") {
		return nil
	}
	if action == accessapi.ActionAuthenticated || s.Admin(w) {
		return slices.Clone(s.order)
	}
	reach := map[kernel.ULID]bool{}
	for _, b := range s.bound(w) {
		if s.roleOf(b).actions[action] {
			for _, id := range s.subtree(b.SpaceID, nil) {
				reach[id] = true
			}
		}
		if s.reader.actions[action] {
			for x := b.SpaceID; s.inherit[x] && !s.parent[x].IsZero(); {
				x = s.parent[x]
				reach[x] = true
			}
		}
	}
	var out []kernel.ULID
	for _, id := range s.order {
		if reach[id] {
			out = append(out, id)
		}
	}
	return out
}

// Holds reports whether w may perform action in at least one space of
// its account, as an anywhere action is decided.
func (s *Snapshot) Holds(w Who, action accessapi.Action) bool {
	return len(s.Reachable(w, action)) > 0
}

// Actions is every assignable catalogue action w may perform in space, in
// the catalogue's order. An account action of infraweave's is decided on
// the top space whatever space is asked, as nothing it acts on lives in
// one, and an anywhere one wherever w holds it.
func (s *Snapshot) Actions(w Who, space kernel.ULID) []accessapi.Action {
	if _, ok := s.parent[space]; !ok || !s.in(w) {
		return nil
	}
	var out []accessapi.Action
	for _, a := range accessapi.Actions() {
		var ok bool
		switch {
		case !a.Assignable():
		case a.Anywhere:
			ok = s.Holds(w, a.Action)
		case a.Infraweave:
			ok = s.Authorize(w, a.Action, s.top)
		default:
			ok = s.Authorize(w, a.Action, space)
		}
		if ok {
			out = append(out, a.Action)
		}
	}
	return out
}
