package authz

import (
	"bytes"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Data is what an account's snapshot is built from: its authorization rows
// at one authz_version, read in one transaction.
type Data struct {
	Account  kernel.AccountID
	Version  int64
	Spaces   []Space
	Roles    []Role
	Bindings []role.Binding
	Groups   []Group
}

// Space is a node of the tree as the evaluator needs it. Parent is zero
// only for the top space.
type Space struct {
	ID      kernel.ULID
	Parent  kernel.ULID
	Inherit bool
}

// Role is a role as the evaluator needs it. Actions are a custom role's;
// a system role's come from role.SystemActions.
type Role struct {
	ID      kernel.ULID
	Slug    string
	System  bool
	Actions []accessapi.Action
}

// Group is a managed user group, which a session's teams name.
type Group struct {
	ID   kernel.ULID
	Name string
}

// actor is one member of a principal's actor set.
type actor struct {
	kind role.ActorKind
	id   kernel.ULID
}

// grantRole is a role's slug and actions, as a grant applies them.
type grantRole struct {
	id      kernel.ULID
	slug    string
	system  bool
	actions map[accessapi.Action]bool
}

// Snapshot is one account's authorization at one version: the tree, the
// roles with their actions and the bindings by actor. It is never changed
// once built, so checks read it without a lock.
type Snapshot struct {
	account  kernel.AccountID
	version  int64
	top      kernel.ULID
	order    []kernel.ULID
	parent   map[kernel.ULID]kernel.ULID
	inherit  map[kernel.ULID]bool
	children map[kernel.ULID][]kernel.ULID
	roles    map[kernel.ULID]grantRole
	reader   grantRole
	bindings map[actor][]role.Binding
	groups   map[string]kernel.ULID
}

// holds reports whether space is in the snapshot's tree.
func (s *Snapshot) holds(space kernel.ULID) bool {
	_, ok := s.parent[space]
	return ok
}

// Build is the snapshot of d. A binding whose role or space d lacks is
// left out, as the foreign keys make it impossible.
func Build(d Data) *Snapshot {
	s := &Snapshot{
		account:  d.Account,
		version:  d.Version,
		parent:   map[kernel.ULID]kernel.ULID{},
		inherit:  map[kernel.ULID]bool{},
		children: map[kernel.ULID][]kernel.ULID{},
		roles:    map[kernel.ULID]grantRole{},
		bindings: map[actor][]role.Binding{},
		groups:   map[string]kernel.ULID{},
	}
	for _, sp := range d.Spaces {
		s.parent[sp.ID] = sp.Parent
		s.inherit[sp.ID] = sp.Inherit
		if sp.Parent.IsZero() {
			s.top = sp.ID
		} else {
			s.children[sp.Parent] = append(s.children[sp.Parent], sp.ID)
		}
	}
	s.order = s.subtree(s.top, nil)
	readerActions, _ := role.SystemActions(role.SpaceReader)
	s.reader = grantRole{slug: role.SpaceReader, system: true, actions: set(readerActions)}
	for _, r := range d.Roles {
		actions := r.Actions
		if r.System {
			actions, _ = role.SystemActions(r.Slug)
		}
		s.roles[r.ID] = grantRole{id: r.ID, slug: r.Slug, system: r.System, actions: set(actions)}
		if r.System && r.Slug == role.SpaceReader {
			s.reader.id = r.ID
		}
	}
	for _, b := range d.Bindings {
		if _, ok := s.roles[b.RoleID]; !ok {
			continue
		}
		if _, ok := s.parent[b.SpaceID]; !ok {
			continue
		}
		a := actor{b.ActorKind, b.ActorID}
		s.bindings[a] = append(s.bindings[a], b)
	}
	for _, g := range d.Groups {
		s.groups[g.Name] = g.ID
	}
	return s
}

func set(actions []accessapi.Action) map[accessapi.Action]bool {
	out := make(map[accessapi.Action]bool, len(actions))
	for _, a := range actions {
		out[a] = true
	}
	return out
}

// Account is the account the snapshot is of.
func (s *Snapshot) Account() kernel.AccountID { return s.account }

// Version is the authz_version the snapshot was built at.
func (s *Snapshot) Version() int64 { return s.version }

// subtree appends id and every space below it to out, parents first and
// children by ID, so lists from the snapshot have one order.
func (s *Snapshot) subtree(id kernel.ULID, out []kernel.ULID) []kernel.ULID {
	if id.IsZero() {
		return out
	}
	out = append(out, id)
	kids := slices.Clone(s.children[id])
	slices.SortFunc(kids, func(a, b kernel.ULID) int { return bytes.Compare(a[:], b[:]) })
	for _, k := range kids {
		out = s.subtree(k, out)
	}
	return out
}

// ancestor reports whether a is a strict ancestor of d.
func (s *Snapshot) ancestor(a, d kernel.ULID) bool {
	for x := s.parent[d]; !x.IsZero(); x = s.parent[x] {
		if x == a {
			return true
		}
	}
	return false
}

// inheritedBy reports whether a grant bound on d is inherited by its
// strict ancestor a: every space from d up to a's child on that path has
// inherit_entities.
func (s *Snapshot) inheritedBy(d, a kernel.ULID) bool {
	for x := d; x != a; x = s.parent[x] {
		if x.IsZero() || !s.inherit[x] {
			return false
		}
	}
	return true
}
