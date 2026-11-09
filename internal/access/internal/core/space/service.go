package space

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

// ErrName is a space created or changed without a name.
var ErrName = errors.New("a space needs a name")

// checkInput refuses a space with no name with ErrName, and labels that
// break the label rule with a *kernel.Invalid naming each at its pointer.
func checkInput(in Space) error {
	if in.Name == "" {
		return ErrName
	}
	if errs := kernel.CheckLabels("/labels", in.Labels); len(errs) > 0 {
		return &kernel.Invalid{Detail: "The space's labels are refused.", Errors: errs}
	}
	return nil
}

// Store keeps the spaces and their closure rows. Every change locks the
// account's tree first, so two changes of one tree never interleave and a
// move can never close a cycle another move opened. A space or account
// that does not exist is the store's not found error.
type Store interface {
	// Spaces lists the account's spaces, top space first.
	Spaces(ctx context.Context, acct kernel.AccountID) ([]Space, error)
	// Space reads the account's space slug.
	Space(ctx context.Context, acct kernel.AccountID, slug string) (Space, error)
	// CreateSpace records s below its parent, with its closure rows.
	CreateSpace(ctx context.Context, s Space) error
	// UpdateSpace changes the account's space slug. It reads the space,
	// the parent parentSlug names, none when it is empty, and, when that
	// parent is a new one, the move of the space's subtree below it: a
	// parent inside the subtree is a *Refusal. edit sees them, with a nil
	// move when there is none, and answers the space to write or refuses,
	// before anything is written.
	UpdateSpace(
		ctx context.Context,
		acct kernel.AccountID,
		slug, parentSlug string,
		edit func(ctx context.Context, current, parent Space, m *accessapi.SpaceMove) (Space, error),
	) (Space, error)
	// HoldSpace locks the account's space id FOR KEY SHARE in the
	// context's transaction; port.ErrNotFound when the account lacks it.
	HoldSpace(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// DeleteSpace deletes the account's space slug. check sees it first
	// and may refuse; a space with children is a *Refusal.
	DeleteSpace(
		ctx context.Context,
		acct kernel.AccountID,
		slug string,
		check func(ctx context.Context, s Space) error,
	) error
	// NonAdminCreation reads the account's allowNonAdminSpaceCreation.
	NonAdminCreation(ctx context.Context, acct kernel.AccountID) (bool, error)
	// ToggleNonAdminCreation flips it and answers the new value.
	ToggleNonAdminCreation(ctx context.Context, acct kernel.AccountID) (bool, error)
	// Search reads a page of the account's spaces among reachable that in
	// keeps, by Fields; an input they cannot answer is a *search.Invalid.
	Search(
		ctx context.Context,
		acct kernel.AccountID,
		in search.Input,
		reachable []kernel.ULID,
	) (search.Result[Space], error)
	// Suggest lists the values Fields hold among the account's spaces in
	// reachable that in keeps.
	Suggest(
		ctx context.Context,
		acct kernel.AccountID,
		in search.SuggestionsInput,
		reachable []kernel.ULID,
	) (search.Suggestions, error)
}

// Authorizer decides whether a principal may perform an action in a
// space; accessapi.Authorizer is one.
type Authorizer interface {
	Authorize(
		ctx context.Context,
		p kernel.Principal,
		action accessapi.Action,
		space kernel.ULID,
	) (bool, error)
}

// Denied is a change the caller may not make, though it may read the
// space, which the Spaces operations answer 403 forbidden naming Action.
type Denied struct {
	Action accessapi.Action
	Reason string
}

func (d *Denied) Error() string { return d.Reason }

// Service keeps each account's tree and decides who changes it (decision
// 0007 § "Spaces"): an account admin, space-admin on the top space,
// changes anything the tree's own rules allow. An admin of a space below
// the top changes only the name, description and labels of the spaces
// they administer while the account's allowNonAdminSpaceCreation is off.
// While it is on they also create spaces below those spaces, which always
// inherit, move them below another space they administer and delete
// them, but never stop a space inheriting. The route table has decided
// SPACE_ADMIN on the space named before a call reaches the service; the
// service decides the rest, as a handler does for any operation that
// needs a second check.
//
// ref: decision 0007 § "Enforcement point"
type Service struct {
	store Store
	authz Authorizer
	drop  func(kernel.AccountID)
	now   func() time.Time

	mu     sync.RWMutex
	guards []accessapi.SpaceGuard
}

// Deps are what the service needs. Drop forgets this replica's
// authorization snapshot of an account after a change of its tree, so
// the answer to the change, and the caller's next request here, see it
// before the change's message arrives.
type Deps struct {
	Store Store
	Authz Authorizer
	Drop  func(kernel.AccountID)
	Now   func() time.Time
}

// NewService returns the service over d.
func NewService(d Deps) *Service {
	return &Service{store: d.Store, authz: d.Authz, drop: d.Drop, now: d.Now}
}

// Guard registers g, which every later delete and move consults.
func (s *Service) Guard(g accessapi.SpaceGuard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guards = append(s.guards, g)
}

func (s *Service) registered() []accessapi.SpaceGuard {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.guards)
}

// Hold locks acct's space id in the context's transaction until it ends,
// so a delete or move waits for it and its guards see what the caller
// wrote; a space acct lacks is accessapi.ErrNoSpace.
func (s *Service) Hold(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	err := s.store.HoldSpace(ctx, acct, id)
	if errors.Is(err, port.ErrNotFound) {
		return fmt.Errorf("%w: %s", accessapi.ErrNoSpace, id)
	}
	return err //nolint:wrapcheck // the store names the read
}

var _ accessapi.Spaces = (*Service)(nil)

// Find is the ID of acct's space slug; accessapi.ErrNoSpace when acct
// lacks it.
func (s *Service) Find(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (kernel.ULID, error) {
	sp, err := s.store.Space(ctx, acct, slug)
	if errors.Is(err, port.ErrNotFound) {
		return kernel.ULID{}, fmt.Errorf("%w: %s", accessapi.ErrNoSpace, slug)
	}
	return sp.ID, err //nolint:wrapcheck // the store names the read
}

// Slugs names each of acct's spaces by its slug.
func (s *Service) Slugs(
	ctx context.Context,
	acct kernel.AccountID,
) (map[kernel.ULID]string, error) {
	all, err := s.store.Spaces(ctx, acct)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	out := make(map[kernel.ULID]string, len(all))
	for _, sp := range all {
		out[sp.ID] = sp.Slug
	}
	return out, nil
}

// Tree lists acct's spaces with their parents, top space first.
func (s *Service) Tree(ctx context.Context, acct kernel.AccountID) ([]accessapi.SpaceNode, error) {
	all, err := s.store.Spaces(ctx, acct)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	out := make([]accessapi.SpaceNode, len(all))
	for i, sp := range all {
		out[i] = accessapi.SpaceNode{ID: sp.ID, Slug: sp.Slug, Name: sp.Name, Parent: sp.ParentID}
	}
	return out, nil
}

// Spaces lists acct's spaces, top space first.
func (s *Service) Spaces(ctx context.Context, acct kernel.AccountID) ([]Space, error) {
	return s.store.Spaces(ctx, acct) //nolint:wrapcheck // the store names the read
}

// Search reads a page of acct's spaces among reachable, the spaces the
// caller may read, that in keeps.
func (s *Service) Search(
	ctx context.Context,
	acct kernel.AccountID,
	in search.Input,
	reachable []kernel.ULID,
) (search.Result[Space], error) {
	return s.store.Search(ctx, acct, in, reachable) //nolint:wrapcheck // the store names the read
}

// Suggest lists the values of acct's spaces among reachable that in
// keeps.
func (s *Service) Suggest(
	ctx context.Context,
	acct kernel.AccountID,
	in search.SuggestionsInput,
	reachable []kernel.ULID,
) (search.Suggestions, error) {
	return s.store.Suggest(ctx, acct, in, reachable) //nolint:wrapcheck // the store names the read
}

// Space reads acct's space slug.
func (s *Service) Space(ctx context.Context, acct kernel.AccountID, slug string) (Space, error) {
	return s.store.Space(ctx, acct, slug) //nolint:wrapcheck // the store names the read
}

// NonAdminCreation reads acct's allowNonAdminSpaceCreation, false until
// an account admin turns it on.
func (s *Service) NonAdminCreation(ctx context.Context, acct kernel.AccountID) (bool, error) {
	return s.store.NonAdminCreation(ctx, acct) //nolint:wrapcheck // the store names the read
}

// ToggleNonAdminCreation flips the setting of p's account and answers the
// new value. The route table admits only an account admin.
func (s *Service) ToggleNonAdminCreation(ctx context.Context, p kernel.Principal) (bool, error) {
	return s.store.ToggleNonAdminCreation(ctx, p.AccountID) //nolint:wrapcheck // as above
}

// rights are what p may do to its account's tree beyond renaming the
// spaces it administers: all of it as an account admin, or the setting's
// share.
type rights struct{ admin, setting bool }

func (s *Service) rightsOf(ctx context.Context, p kernel.Principal) (rights, error) {
	admin, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceAdmin, p.AccountID.ULID)
	if err != nil || admin {
		return rights{admin: admin}, err //nolint:wrapcheck // the snapshot's read names itself
	}
	on, err := s.store.NonAdminCreation(ctx, p.AccountID)
	return rights{setting: on}, err //nolint:wrapcheck // the store names the read
}

// structural refuses a change of the tree's shape to an admin below the
// top space while the setting is off.
func (r rights) structural(what string) error {
	if r.admin || r.setting {
		return nil
	}
	return &Denied{Action: accessapi.ActionSpaceAdmin, Reason: fmt.Sprintf(
		"Only account admins %s while allowNonAdminSpaceCreation is off.", what)}
}

// administers refuses p unless it is space-admin on sp, which an account
// admin is everywhere.
func (s *Service) administers(ctx context.Context, p kernel.Principal, r rights, sp Space) error {
	if r.admin {
		return nil
	}
	ok, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceAdmin, sp.ID)
	switch {
	case err != nil:
		return err //nolint:wrapcheck // the snapshot's read names itself
	case !ok:
		return &Denied{Action: accessapi.ActionSpaceAdmin, Reason: fmt.Sprintf(
			"You may not perform %s on space %s.", accessapi.ActionSpaceAdmin, sp.Slug)}
	}
	return nil
}

// Create creates in below the space parent of p's account. Its slug is
// SlugOf the name and a new ULID. Labels keep the order given; one that
// breaks the label rule is a *kernel.Invalid.
func (s *Service) Create(
	ctx context.Context,
	p kernel.Principal,
	parent string,
	in Space,
) (Space, error) {
	if err := checkInput(in); err != nil {
		return Space{}, err
	}
	pa, err := s.store.Space(ctx, p.AccountID, parent)
	if err != nil {
		return Space{}, err //nolint:wrapcheck // the store names the read
	}
	r, err := s.rightsOf(ctx, p)
	if err != nil {
		return Space{}, err
	}
	if err := s.administers(ctx, p, r, pa); err != nil {
		return Space{}, err
	}
	if err := r.structural("create spaces"); err != nil {
		return Space{}, err
	}
	if !r.admin && !in.InheritEntities {
		return Space{}, refuse(
			"a space an admin below the top space creates inherits its parent's entities",
		)
	}
	in.AccountID, in.ID, in.ParentID, in.CreatedAt = p.AccountID, kernel.NewULID(), pa.ID, s.now()
	in.Slug = SlugOf(in.Name, in.ID)
	if err := s.store.CreateSpace(ctx, in); err != nil {
		return Space{}, err //nolint:wrapcheck // the store names the write
	}
	s.drop(p.AccountID)
	return in, nil
}

// Update gives p's account's space slug in's name, description, labels
// and inheritance, and moves it, with its subtree, below parent when that
// is a new one. The top space's parent is empty and it never inherits; a
// space below it needs a parent. Each registered guard may refuse a move.
// A space whose version expect does not accept, read under the tree's
// lock, is kernel.ErrStale and is not changed.
func (s *Service) Update(
	ctx context.Context,
	p kernel.Principal,
	slug, parent string,
	expect kernel.Expect,
	in Space,
) (Space, error) {
	if err := checkInput(in); err != nil {
		return Space{}, err
	}
	r, err := s.rightsOf(ctx, p)
	if err != nil {
		return Space{}, err
	}
	guards := s.registered()
	out, err := s.store.UpdateSpace(ctx, p.AccountID, slug, parent,
		func(ctx context.Context, cur, pa Space, m *accessapi.SpaceMove) (Space, error) {
			if err := s.administers(ctx, p, r, cur); err != nil {
				return Space{}, err
			}
			if err := expect.Check(cur.Version); err != nil {
				return Space{}, err //nolint:wrapcheck // kernel.ErrStale, answered as it is
			}
			if err := s.reshape(ctx, p, r, cur, pa, m, in); err != nil {
				return Space{}, err
			}
			if err := topKept(cur, pa, in); err != nil {
				return Space{}, err
			}
			if m != nil {
				if err := lostOnMove(ctx, guards, cur, *m); err != nil {
					return Space{}, err
				}
				cur.ParentID = pa.ID
			}
			cur.Name, cur.Description, cur.Labels = in.Name, in.Description, in.Labels
			cur.InheritEntities = in.InheritEntities
			return cur, nil
		})
	if err != nil {
		return Space{}, err //nolint:wrapcheck // the store names the write
	}
	s.drop(p.AccountID)
	return out, nil
}

// reshape decides the parts of an update beyond the name, description and
// labels: a move, which needs SPACE_ADMIN on the new parent too, and a
// change of inheritance, which an admin below the top space makes only
// to start inheriting.
func (s *Service) reshape(
	ctx context.Context,
	p kernel.Principal,
	r rights,
	cur, pa Space,
	m *accessapi.SpaceMove,
	in Space,
) error {
	inherit := in.InheritEntities != cur.InheritEntities
	if m == nil && !inherit {
		return nil
	}
	if err := r.structural("move spaces and change their inheritance"); err != nil {
		return err
	}
	if m != nil {
		if err := s.administers(ctx, p, r, pa); err != nil {
			return err
		}
	}
	if inherit && !in.InheritEntities && !r.admin {
		return &Denied{Action: accessapi.ActionSpaceAdmin, Reason: fmt.Sprintf(
			"Only account admins stop space %s inheriting its parent's entities.", cur.Slug)}
	}
	return nil
}

// topKept refuses a change of the top space's parent or inheritance, and
// a space below it left with no parent.
func topKept(cur, pa, in Space) error {
	switch {
	case !cur.Top() && pa.ID.IsZero():
		return refuse("space %s needs a parent space", cur.Slug)
	case cur.Top() && !pa.ID.IsZero():
		return refuse("the top space %s cannot move", cur.Slug)
	case cur.Top() && in.InheritEntities:
		return refuse("the top space %s has no parent to inherit from", cur.Slug)
	}
	return nil
}

// lostOnMove asks each guard what moving cur cuts off, refusing with
// every loss.
func lostOnMove(
	ctx context.Context,
	guards []accessapi.SpaceGuard,
	cur Space,
	m accessapi.SpaceMove,
) error {
	var r Refusal
	for _, g := range guards {
		lost, err := g.LostOnMove(ctx, m)
		if err != nil {
			return fmt.Errorf("check moving space %s: %w", cur.Slug, err)
		}
		for _, l := range lost {
			r.Reasons = append(r.Reasons,
				fmt.Sprintf("stack %s would no longer reach %s", l.Stack, l.Entity))
		}
	}
	if len(r.Reasons) > 0 {
		return &r
	}
	return nil
}

// Delete deletes p's account's space slug and answers it as it was. The
// top space and legacy are never deleted, and a space a registered guard
// says is in use is refused. A space whose version expect does not
// accept is kernel.ErrStale and is kept.
func (s *Service) Delete(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	expect kernel.Expect,
) (Space, error) {
	r, err := s.rightsOf(ctx, p)
	if err != nil {
		return Space{}, err
	}
	guards := s.registered()
	var gone Space
	err = s.store.DeleteSpace(ctx, p.AccountID, slug, func(ctx context.Context, sp Space) error {
		if err := s.administers(ctx, p, r, sp); err != nil {
			return err
		}
		if err := r.structural("delete spaces"); err != nil {
			return err
		}
		if err := expect.Check(sp.Version); err != nil {
			return err //nolint:wrapcheck // kernel.ErrStale, answered as it is
		}
		switch {
		case sp.Top():
			return refuse("the top space %s cannot be deleted", sp.Slug)
		case sp.Slug == LegacySlug:
			return refuse("the %s space cannot be deleted", LegacySlug)
		}
		for _, g := range guards {
			used, err := g.SpaceInUse(ctx, p.AccountID, sp.ID)
			if err != nil {
				return fmt.Errorf("check deleting space %s: %w", sp.Slug, err)
			}
			if used {
				return refuse("space %s still holds entities", sp.Slug)
			}
		}
		gone = sp
		return nil
	})
	if err != nil {
		return Space{}, err //nolint:wrapcheck // the store names the write
	}
	s.drop(p.AccountID)
	return gone, nil
}
