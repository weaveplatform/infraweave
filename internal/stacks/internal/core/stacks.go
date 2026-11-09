package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// SlugTaken is a slug another stack of the account holds.
type SlugTaken struct{ Slug string }

func (e *SlugTaken) Error() string { return fmt.Sprintf("stacks: slug %q is taken", e.Slug) }

// Fields are what a stack search filters and orders by, named as the
// reference's stack filters name them: id, the slug; label, any of its
// labels; space, its place in the tree, which keeps a node's stacks and,
// asked, those of the spaces below it; vendor, the vendorConfig member it
// sets; locked, whether anyone holds its lock; and starred, whether the
// caller has starred it. A search that names no order reads by id. The
// fields of data later phases own, such as a stack's run state, commit
// and drift, join with those phases.
var Fields = []search.Field{
	{Name: "id", Type: search.String, Orderable: true},
	{Name: "name", Type: search.String, Orderable: true},
	{Name: "description", Type: search.String},
	{Name: "label", Type: search.String},
	{Name: "space", Type: search.Hierarchy},
	{Name: "repository", Type: search.String, Orderable: true},
	{Name: "branch", Type: search.String, Orderable: true},
	{Name: "projectRoot", Type: search.String},
	{Name: "provider", Type: search.String},
	{Name: "vendor", Type: search.Enum, Values: vendors()},
	{Name: "autodeploy", Type: search.Boolean},
	{Name: "disabled", Type: search.Boolean},
	{Name: "locked", Type: search.Boolean},
	{Name: "starred", Type: search.Boolean},
	{Name: "createdAt", Type: search.Time, Orderable: true},
}

// vendors are the kinds of the registered vendors, the values a stack's
// vendor takes.
func vendors() []string {
	all := iac.Vendors()
	out := make([]string, len(all))
	for i, v := range all {
		out[i] = string(v.Kind())
	}
	return out
}

// Scope is what a stack search sees: the spaces the caller may read, the
// account's space tree, which the space field reads, and the caller,
// whose stars the starred field reads.
type Scope struct {
	Reachable []kernel.ULID
	Spaces    []accessapi.SpaceNode
	Actor     Actor
}

// Store is what the service needs of the stacks' storage. Every method
// joins the transaction its context carries.
type Store interface {
	// Get reads acct's stack id; stacksapi.ErrNotFound when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Stack, error)
	// Read reads acct's stack id whole, locking its row FOR UPDATE when
	// lock is set; stacksapi.ErrNotFound when acct lacks it.
	Read(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock bool) (Stack, error)
	// BySlug reads acct's stack slug whole, locking its row FOR UPDATE
	// when lock is set; stacksapi.ErrNotFound when acct lacks it.
	BySlug(ctx context.Context, acct kernel.AccountID, slug string, lock bool) (Stack, error)
	// ByRepository lists acct's stacks that track branch of repository
	// through integration, nil for none, by slug.
	ByRepository(
		ctx context.Context,
		acct kernel.AccountID,
		integration *kernel.ULID,
		repository, branch string,
	) ([]stacksapi.Stack, error)
	// Create writes a new stack with its hooks and labels; a *SlugTaken
	// when another stack of its account holds its slug.
	Create(ctx context.Context, st Stack) error
	// Write replaces the stack's settings, hooks and labels, its slug and
	// its lock, and sets its version to st.Version; a *SlugTaken when
	// another stack of its account holds its slug.
	Write(ctx context.Context, st Stack) error
	// Delete deletes acct's stack id with its hooks, labels and stars.
	Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// SetStar stars or unstars acct's stack id for the actor.
	SetStar(ctx context.Context, acct kernel.AccountID, id kernel.ULID, actor Actor, on bool) error
	// Starred is which of acct's stacks ids the actor has starred.
	Starred(
		ctx context.Context,
		acct kernel.AccountID,
		ids []kernel.ULID,
		actor Actor,
	) (map[kernel.ULID]bool, error)
	// SpaceInUse reports whether a stack of acct lives in space.
	SpaceInUse(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error)
	// Hold locks acct's stack id FOR KEY SHARE until the context's
	// transaction ends; stacksapi.ErrNotFound when acct lacks it.
	Hold(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// Find lists acct's stacks in spaces that carry any of labels, by
	// slug.
	Find(
		ctx context.Context,
		acct kernel.AccountID,
		spaces []kernel.ULID,
		labels []string,
	) ([]stacksapi.Stack, error)
	// Search reads a page of acct's stacks in the scope that in keeps, by
	// Fields; an input they cannot answer is a *search.Invalid.
	Search(
		ctx context.Context,
		acct kernel.AccountID,
		in search.Input,
		scope Scope,
	) (search.Result[Stack], error)
	// Suggest lists the values of Fields that acct's stacks in the scope
	// that in keeps hold, and counts them; predicates they cannot answer
	// are a *search.Invalid.
	Suggest(
		ctx context.Context,
		acct kernel.AccountID,
		in search.SuggestionsInput,
		scope Scope,
	) (search.Suggestions, error)
}

// Actor is who stars a stack: a principal's kind and actor ID.
type Actor struct {
	Kind kernel.PrincipalKind
	ID   kernel.ULID
}

// ActorOf is p's actor.
func ActorOf(p kernel.Principal) Actor { return Actor{Kind: p.Kind, ID: p.ActorID} }

// Events is the outbox a write appends its stacksapi.Changed to.
type Events interface {
	Append(ctx context.Context, ev outbox.Event) (outbox.Event, error)
}

// Deps are what the service is constructed over.
type Deps struct {
	Store  Store
	DB     tx.Beginner
	Spaces accessapi.Spaces
	Authz  accessapi.Authorizer
	Events Events
	Now    func() time.Time
}

// Service is the stacks domain's logic. Every write runs in one
// transaction with its outbox event, holding the stack's space so no
// delete or move removes it first.
type Service struct {
	store  Store
	db     tx.Beginner
	spaces accessapi.Spaces
	authz  accessapi.Authorizer
	events Events
	now    func() time.Time

	mu     sync.RWMutex
	guards []stacksapi.StackGuard
}

// New returns the service over d.
func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		store: d.Store, db: d.DB, spaces: d.Spaces, authz: d.Authz, events: d.Events, now: now,
	}
}

var (
	_ stacksapi.Stacks     = (*Service)(nil)
	_ accessapi.SpaceGuard = (*Service)(nil)
)

// Guard registers g, which every later move and delete of a stack asks.
func (s *Service) Guard(g stacksapi.StackGuard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guards = append(s.guards, g)
}

func (s *Service) registered() []stacksapi.StackGuard {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.guards)
}

// Denied is an action the caller may not perform, beyond the one the
// operation's route decided.
type Denied struct{ Action accessapi.Action }

func (d *Denied) Error() string { return fmt.Sprintf("you may not perform %s here", d.Action) }

// Locked is a lock another caller holds.
type Locked struct{ By string }

func (l *Locked) Error() string { return "the stack is locked by " + l.By }

// SpaceError is a space a write names that the account lacks or the
// caller cannot read, which answers as one that does not exist.
type SpaceError struct{ Slug string }

func (e *SpaceError) Error() string { return fmt.Sprintf("stacks: no space %q", e.Slug) }

// Get reads acct's stack id.
func (s *Service) Get(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Stack, error) {
	return s.store.Get(ctx, acct, id) //nolint:wrapcheck // the store names the read
}

// Settings reads acct's stack id's settings.
func (s *Service) Settings(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Settings, error) {
	st, err := s.store.Read(ctx, acct, id, false)
	return st.Settings, err //nolint:wrapcheck // as above
}

// ByRepository lists acct's stacks that track branch of repository
// through integration.
func (s *Service) ByRepository(
	ctx context.Context,
	acct kernel.AccountID,
	integration *kernel.ULID,
	repository, branch string,
) ([]stacksapi.Stack, error) {
	//nolint:wrapcheck // as above
	return s.store.ByRepository(ctx, acct, integration, repository, branch)
}

// LockForRun reads acct's stack id's settings, locking its row in the
// context's transaction.
func (s *Service) LockForRun(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Settings, error) {
	st, err := s.store.Read(ctx, acct, id, true)
	return st.Settings, err //nolint:wrapcheck // as above
}

// Hold locks acct's stack id in the context's transaction until it ends,
// so a move or delete waits for it and its guards see what the caller
// wrote.
func (s *Service) Hold(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	return s.store.Hold(ctx, acct, id) //nolint:wrapcheck // the store names the lock
}

// Find lists acct's stacks in spaces that carry any of labels.
func (s *Service) Find(
	ctx context.Context,
	acct kernel.AccountID,
	spaces []kernel.ULID,
	labels []string,
) ([]stacksapi.Stack, error) {
	if len(spaces) == 0 || len(labels) == 0 {
		return nil, nil
	}
	return s.store.Find(ctx, acct, spaces, labels) //nolint:wrapcheck // the store names the read
}

// SpaceOf is the space of acct's stack slug, which the access domain's
// middleware decides a stack's operations on; accessapi.ErrNoSubject when
// acct lacks it.
func (s *Service) SpaceOf(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (kernel.ULID, error) {
	st, err := s.store.BySlug(ctx, acct, slug, false)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return st.Space, err //nolint:wrapcheck // as above
}

// Stack reads acct's stack slug.
func (s *Service) Stack(ctx context.Context, acct kernel.AccountID, slug string) (Stack, error) {
	return s.store.BySlug(ctx, acct, slug, false) //nolint:wrapcheck // as above
}

// Search reads a page of the stacks of p's account in the spaces it may
// read, which the query leaves the others out of, so every page before
// the last is full.
func (s *Service) Search(
	ctx context.Context,
	p kernel.Principal,
	in search.Input,
) (search.Result[Stack], error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return search.Result[Stack]{}, err
	}
	return s.store.Search(ctx, p.AccountID, in, scope) //nolint:wrapcheck // as the store's
}

// Suggest lists the values the stacks of p's account in the spaces it may
// read hold, under in's predicates, so no value or count names a stack p
// cannot read.
func (s *Service) Suggest(
	ctx context.Context,
	p kernel.Principal,
	in search.SuggestionsInput,
) (search.Suggestions, error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return search.Suggestions{}, err
	}
	return s.store.Suggest(ctx, p.AccountID, in, scope) //nolint:wrapcheck // as the store's
}

// scope is what p's searches see: the spaces it may read, its account's
// tree and its stars.
func (s *Service) scope(ctx context.Context, p kernel.Principal) (Scope, error) {
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return Scope{}, err //nolint:wrapcheck // the snapshot's read names itself
	}
	tree, err := s.spaces.Tree(ctx, p.AccountID)
	if err != nil {
		return Scope{}, err //nolint:wrapcheck // the access domain names the read
	}
	return Scope{Reachable: reach, Spaces: tree, Actor: ActorOf(p)}, nil
}

// Starred is which of stacks p has starred.
func (s *Service) Starred(
	ctx context.Context,
	p kernel.Principal,
	stacks []Stack,
) (map[kernel.ULID]bool, error) {
	ids := make([]kernel.ULID, len(stacks))
	for i, st := range stacks {
		ids[i] = st.ID
	}
	return s.store.Starred(ctx, p.AccountID, ids, ActorOf(p)) //nolint:wrapcheck // as above
}

// Create creates a stack of p's account from in, slugged slug when it is
// not empty and from in's name otherwise, whose state infraweave keeps
// when manageState is set. The route has decided
// STACK_CREATE on in's space. Its refusals point into stackCreate's body,
// where in is input.
func (s *Service) Create(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	manageState bool,
	in Input,
) (Stack, error) {
	if err := check("/input", in); err != nil {
		return Stack{}, err
	}
	if slug == "" {
		slug = kernel.SlugFrom(in.Name).String()
	} else if !kernel.Slug(slug).Valid() {
		return Stack{}, &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{{
			Pointer: "/slug", Reason: kernel.ReasonPattern,
			Message: "A slug is lowercase letters and digits, in runs joined by single hyphens.",
		}}}
	}
	var st Stack
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		space, err := s.space(ctx, p, in.Space)
		if err != nil {
			return err
		}
		st = Stack{CreatedAt: s.now(), ManagesState: manageState}
		st.ID, st.Account, st.Slug, st.Version = kernel.NewULID(), p.AccountID, slug, 1
		apply(&st, space, in)
		if err := s.store.Create(ctx, st); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.changed(ctx, st, stacksapi.ChangeCreated)
	})
	return st, err //nolint:wrapcheck // each step in the transaction names itself
}

// Update replaces the settings of p's account's stack slug with in's,
// when expect accepts its version. The route has decided STACK_UPDATE on
// the stack's space; a move to another space also needs STACK_CREATE
// there, and is refused when a registered guard names an entity the stack
// would no longer reach.
func (s *Service) Update(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	expect kernel.Expect,
	in Input,
) (Stack, error) {
	if err := check("", in); err != nil {
		return Stack{}, err
	}
	return s.write(
		ctx,
		p.AccountID,
		slug,
		expect,
		stacksapi.ChangeUpdated,
		func(ctx context.Context, st *Stack) error {
			space, err := s.space(ctx, p, in.Space)
			if err != nil {
				return err
			}
			if space != st.Space {
				if err := s.require(ctx, p, accessapi.ActionStackCreate, space); err != nil {
					return err
				}
				if err := s.lostOnMove(
					ctx,
					stacksapi.StackMove{Stack: st.Stack, To: space},
				); err != nil {
					return err
				}
			}
			apply(st, space, in)
			return nil
		},
	)
}

// Reslug gives p's account's stack slug the slug its name derives; a
// *SlugTaken when another stack holds that one.
func (s *Service) Reslug(ctx context.Context, p kernel.Principal, slug string) (Stack, error) {
	return s.write(
		ctx,
		p.AccountID,
		slug,
		kernel.Expect{},
		stacksapi.ChangeReslugged,
		func(_ context.Context, st *Stack) error {
			st.Slug = kernel.SlugFrom(st.Name).String()
			return nil
		},
	)
}

// SetDisabled disables or enables p's account's stack slug.
func (s *Service) SetDisabled(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	disabled bool,
) (Stack, error) {
	change := stacksapi.ChangeEnabled
	if disabled {
		change = stacksapi.ChangeDisabled
	}
	return s.write(
		ctx,
		p.AccountID,
		slug,
		kernel.Expect{},
		change,
		func(_ context.Context, st *Stack) error {
			st.Disabled = disabled
			return nil
		},
	)
}

// Lock locks p's account's stack slug for p with note, or changes the
// note of p's own lock; a lock another caller holds is a *Locked. A lock
// has no expiry.
func (s *Service) Lock(ctx context.Context, p kernel.Principal, slug, note string) (Stack, error) {
	return s.write(
		ctx,
		p.AccountID,
		slug,
		kernel.Expect{},
		stacksapi.ChangeLocked,
		func(_ context.Context, st *Stack) error {
			if st.Lock != nil && st.Lock.Holder != p.Caller() {
				return &Locked{By: st.Lock.By}
			}
			st.Lock = &stacksapi.Lock{Holder: p.Caller(), By: nameOf(p), Note: note, At: s.now()}
			return nil
		},
	)
}

// Unlock unlocks p's account's stack slug. The route has decided
// STACK_UNLOCK, which unlocks p's own lock; another caller's needs
// STACK_UNLOCK_FORCE, since the operation has no force argument. An
// unlocked stack is answered as it is.
func (s *Service) Unlock(ctx context.Context, p kernel.Principal, slug string) (Stack, error) {
	return s.write(
		ctx,
		p.AccountID,
		slug,
		kernel.Expect{},
		stacksapi.ChangeUnlocked,
		func(ctx context.Context, st *Stack) error {
			if st.Lock == nil {
				return errUnchanged
			}
			if st.Lock.Holder != p.Caller() {
				if err := s.require(
					ctx,
					p,
					accessapi.ActionStackUnlockForce,
					st.Space,
				); err != nil {
					return err
				}
			}
			st.Lock = nil
			return nil
		},
	)
}

// Delete deletes p's account's stack slug, when expect accepts its
// version, and answers it as it was; a stack protected from deletion is
// refused. Each registered guard deletes its rows of the stack first, in
// the same transaction.
func (s *Service) Delete(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	expect kernel.Expect,
) (Stack, error) {
	var st Stack
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if st, err = s.store.BySlug(ctx, p.AccountID, slug, true); err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if err := expect.Check(st.Version); err != nil {
			return err //nolint:wrapcheck // kernel.ErrStale is the answer
		}
		if st.ProtectFromDeletion {
			return &kernel.Invalid{Detail: "The stack is protected from deletion; " +
				"turn protectFromDeletion off first."}
		}
		for _, g := range s.registered() {
			if err := g.Deleting(ctx, st.Stack); err != nil {
				return fmt.Errorf("stacks: delete what holds stack %s: %w", st.Slug, err)
			}
		}
		if err := s.store.Delete(ctx, st.Account, st.ID); err != nil {
			return err //nolint:wrapcheck // as above
		}
		st.Version++
		return s.changed(ctx, st, stacksapi.ChangeDeleted)
	})
	return st, err //nolint:wrapcheck // each step in the transaction names itself
}

// SetStar stars or unstars p's account's stack slug for p alone.
func (s *Service) SetStar(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	on bool,
) (Stack, error) {
	st, err := s.store.BySlug(ctx, p.AccountID, slug, false)
	if err != nil {
		return Stack{}, err //nolint:wrapcheck // as above
	}
	if err := s.store.SetStar(ctx, p.AccountID, st.ID, ActorOf(p), on); err != nil {
		return Stack{}, err //nolint:wrapcheck // as above
	}
	return st, nil
}

// SpaceInUse reports whether a stack lives in space, which refuses its
// deletion.
func (s *Service) SpaceInUse(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) (bool, error) {
	return s.store.SpaceInUse(ctx, acct, space) //nolint:wrapcheck // as above
}

// LostOnMove refuses no move: a stack keeps its space through a move, and
// nothing it reaches through its space is attached to it.
func (*Service) LostOnMove(context.Context, accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	return nil, nil
}

// lostOnMove asks each registered guard what m cuts the stack off from,
// refusing the move with every entity named. The stack's row is locked
// FOR UPDATE already, so each guard sees every committed hold.
func (s *Service) lostOnMove(ctx context.Context, m stacksapi.StackMove) error {
	var lost []string
	for _, g := range s.registered() {
		l, err := g.LostOnMove(ctx, m)
		if err != nil {
			return fmt.Errorf("stacks: check moving stack %s: %w", m.Slug, err)
		}
		lost = append(lost, l...)
	}
	if len(lost) == 0 {
		return nil
	}
	inv := &kernel.Invalid{Detail: fmt.Sprintf(
		"The move is refused: stack %s would no longer reach %s.",
		m.Slug,
		strings.Join(lost, ", "),
	)}
	for _, l := range lost {
		inv.Errors = append(inv.Errors, kernel.FieldError{
			Pointer: "/space", Reason: kernel.ReasonRule,
			Message: fmt.Sprintf("Stack %s would no longer reach %s.", m.Slug, l),
		})
	}
	return inv
}

// errUnchanged ends a write that changes nothing, answering the stack as
// it is.
var errUnchanged = errors.New("unchanged")

// write reads acct's stack slug locked, checks its version against
// expect, lets change change it, and writes it with its version moved on
// and its Changed event, in one transaction. A version expect refuses is
// kernel.ErrStale, and nothing is written.
func (s *Service) write(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	expect kernel.Expect,
	what string,
	change func(ctx context.Context, st *Stack) error,
) (Stack, error) {
	var st Stack
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if st, err = s.store.BySlug(ctx, acct, slug, true); err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if err := expect.Check(st.Version); err != nil {
			return err //nolint:wrapcheck // kernel.ErrStale is the answer
		}
		if err := change(ctx, &st); err != nil {
			return err
		}
		st.Version++
		if err := s.store.Write(ctx, st); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.changed(ctx, st, what)
	})
	if errors.Is(err, errUnchanged) {
		return st, nil
	}
	return st, err //nolint:wrapcheck // each step in the transaction names itself
}

// space is the ID of the space slug, the top space when it is empty,
// held in the context's transaction; a space p cannot read is a
// *SpaceError, as one the account lacks is.
func (s *Service) space(ctx context.Context, p kernel.Principal, slug string) (kernel.ULID, error) {
	id := p.AccountID.ULID
	if slug != "" {
		var err error
		if id, err = s.spaces.Find(ctx, p.AccountID, slug); errors.Is(err, accessapi.ErrNoSpace) {
			return kernel.ULID{}, &SpaceError{Slug: slug}
		} else if err != nil {
			return kernel.ULID{}, err //nolint:wrapcheck // the access domain names the read
		}
	}
	read, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, id)
	if err != nil {
		return kernel.ULID{}, err //nolint:wrapcheck // the snapshot's read names itself
	}
	if !read {
		return kernel.ULID{}, &SpaceError{Slug: slug}
	}
	if err := s.spaces.Hold(ctx, p.AccountID, id); errors.Is(err, accessapi.ErrNoSpace) {
		return kernel.ULID{}, &SpaceError{Slug: slug}
	} else if err != nil {
		return kernel.ULID{}, err //nolint:wrapcheck // as above
	}
	return id, nil
}

// require refuses p action in space, as a *Denied.
func (s *Service) require(
	ctx context.Context,
	p kernel.Principal,
	action accessapi.Action,
	space kernel.ULID,
) error {
	ok, err := s.authz.Authorize(ctx, p, action, space)
	switch {
	case err != nil:
		return err //nolint:wrapcheck // the snapshot's read names itself
	case !ok:
		return &Denied{Action: action}
	}
	return nil
}

// changed appends st's Changed event in the context's transaction.
func (s *Service) changed(ctx context.Context, st Stack, change string) error {
	doc, err := json.Marshal(stacksapi.Changed{Stack: st.ID, Slug: st.Slug, Change: change})
	if err != nil {
		return fmt.Errorf("stacks: encode %s event: %w", change, err)
	}
	_, err = s.events.Append(ctx, outbox.Event{
		Account:       st.Account.ULID,
		Kind:          stacksapi.ChangedKind,
		SchemaVersion: stacksapi.ChangedVersion,
		Space:         st.Space.String(),
		Subject:       outbox.Subject{Kind: "stack", ID: st.ID.String()},
		Sequence:      st.Version,
		Document:      doc,
	})
	return err //nolint:wrapcheck // the outbox names the append
}

// nameOf is the name a lock shows for p: a member's login or an API key's
// ID, as its session recorded.
func nameOf(p kernel.Principal) string {
	if p.Login != "" {
		return p.Login
	}
	return p.ActorID.String()
}
