package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Events is the outbox a write appends its configapi.Changed to.
type Events interface {
	Append(ctx context.Context, ev outbox.Event) (outbox.Event, error)
}

// Deps are what the service is constructed over.
type Deps struct {
	Store  Store
	DB     tx.Beginner
	Spaces accessapi.Spaces
	Stacks stacksapi.Stacks
	Authz  accessapi.Authorizer
	Events Events
	Now    func() time.Time
}

// Service is the configuration domain's logic. Every write runs in one
// transaction with its outbox event, holding the spaces and stacks it
// stores the IDs of, so no delete or move removes them first.
type Service struct {
	store  Store
	db     tx.Beginner
	spaces accessapi.Spaces
	stacks stacksapi.Stacks
	authz  accessapi.Authorizer
	events Events
	now    func() time.Time
}

// New returns the service over d.
func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		store: d.Store, db: d.DB, spaces: d.Spaces, stacks: d.Stacks, authz: d.Authz,
		events: d.Events, now: now,
	}
}

var _ stacksapi.StackConfiguration = (*Service)(nil)

// Denied is an action the caller may not perform, beyond the one the
// operation's route decided.
type Denied struct{ Action accessapi.Action }

func (d *Denied) Error() string { return fmt.Sprintf("you may not perform %s here", d.Action) }

// Missing is an entity a request names that the account lacks or the
// caller cannot read, which answers as one that does not exist: a space,
// a stack, a context, a config element or an attachment, by Kind.
type Missing struct{ Kind, ID string }

func (m *Missing) Error() string { return fmt.Sprintf("configuration: no %s %q", m.Kind, m.ID) }

// SpaceOf is the space of acct's context slug, which the access domain's
// middleware decides a context's operations on; accessapi.ErrNoSubject
// when acct lacks it.
func (s *Service) SpaceOf(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (kernel.ULID, error) {
	c, err := s.store.BySlug(ctx, acct, slug, NoLock)
	if errors.Is(err, ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return c.Space, err //nolint:wrapcheck // the store names the read
}

// AttachmentSpaceOf is the space of the stack acct's attachment id
// attaches to, which the middleware decides a detach on;
// accessapi.ErrNoSubject when acct lacks it.
func (s *Service) AttachmentSpaceOf(
	ctx context.Context,
	acct kernel.AccountID,
	id string,
) (kernel.ULID, error) {
	a, err := s.attachment(ctx, acct, id)
	if errors.Is(err, ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	if err != nil {
		return kernel.ULID{}, err
	}
	st, err := s.stacks.Get(ctx, acct, a.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return st.Space, err //nolint:wrapcheck // the stacks domain names the read
}

// Context reads acct's context slug.
func (s *Service) Context(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
) (Context, error) {
	return s.store.BySlug(ctx, acct, slug, NoLock) //nolint:wrapcheck // the store names the read
}

// Search reads a page of the contexts of p's account in the spaces it may
// read, which the query leaves the others out of, so every page before
// the last is full.
func (s *Service) Search(
	ctx context.Context,
	p kernel.Principal,
	in search.Input,
) (search.Result[Context], error) {
	scope, err := s.scope(ctx, p, nil)
	if err != nil {
		return search.Result[Context]{}, err
	}
	res, err := s.store.Search(ctx, p.AccountID, in, scope)
	return res, err //nolint:wrapcheck // the store names the read
}

// Suggest lists the values the contexts of p's account in the spaces it
// may read hold, so no value or count names a context p cannot read.
func (s *Service) Suggest(
	ctx context.Context,
	p kernel.Principal,
	in search.SuggestionsInput,
) (search.Suggestions, error) {
	scope, err := s.scope(ctx, p, nil)
	if err != nil {
		return search.Suggestions{}, err
	}
	return s.store.Suggest(ctx, p.AccountID, in, scope) //nolint:wrapcheck // as above
}

// Attachable reads a page of the contexts a stack in the space slug may
// attach that p may read, in the space or one it inherits from; a space
// p cannot read is a *Missing.
func (s *Service) Attachable(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	in search.Input,
) (search.Result[Context], kernel.ULID, error) {
	space, scope, err := s.attachableScope(ctx, p, slug)
	if err != nil {
		return search.Result[Context]{}, space, err
	}
	out, err := s.store.Search(ctx, p.AccountID, in, scope)
	return out, space, err //nolint:wrapcheck // the store names the read
}

// AttachableSuggest lists the values the contexts Attachable reads hold.
func (s *Service) AttachableSuggest(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	in search.SuggestionsInput,
) (search.Suggestions, error) {
	_, scope, err := s.attachableScope(ctx, p, slug)
	if err != nil {
		return search.Suggestions{}, err
	}
	got, err := s.store.Suggest(ctx, p.AccountID, in, scope)
	return got, err //nolint:wrapcheck // the store names the read
}

func (s *Service) attachableScope(
	ctx context.Context,
	p kernel.Principal,
	slug string,
) (kernel.ULID, Scope, error) {
	space, err := s.readable(ctx, p, slug)
	if err != nil {
		return space, Scope{}, err
	}
	scope, err := s.reachScope(ctx, p, space)
	return space, scope, err
}

// reachScope is the scope of the contexts a stack in space may attach that
// p may read.
func (s *Service) reachScope(
	ctx context.Context,
	p kernel.Principal,
	space kernel.ULID,
) (Scope, error) {
	tree, err := s.spaces.Tree(ctx, p.AccountID)
	if err != nil {
		return Scope{}, err //nolint:wrapcheck // the access domain names the read
	}
	reach := Reach(index(tree), space)
	scope, err := s.scope(ctx, p, tree)
	if err != nil {
		return Scope{}, err
	}
	scope.Reachable = slices.DeleteFunc(scope.Reachable, func(id kernel.ULID) bool {
		return !slices.Contains(reach, id)
	})
	return scope, nil
}

// scope is what p's searches see: the spaces it may read and its
// account's tree, read unless tree is given.
func (s *Service) scope(
	ctx context.Context,
	p kernel.Principal,
	tree []accessapi.SpaceNode,
) (Scope, error) {
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return Scope{}, err //nolint:wrapcheck // the snapshot's read names itself
	}
	if tree == nil {
		if tree, err = s.spaces.Tree(ctx, p.AccountID); err != nil {
			return Scope{}, err //nolint:wrapcheck // the access domain names the read
		}
	}
	return Scope{Reachable: reach, Spaces: tree}, nil
}

// Details are what a view of some contexts reads beside them: each one's
// elements and attachments, keyed by the context's ID, and the stacks
// those attach to, keyed by the stack's ID.
type Details struct {
	Elements    map[kernel.ULID][]Element
	Attachments map[kernel.ULID][]Attachment
	Stacks      map[kernel.ULID]stacksapi.Stack
}

// Details reads the details of acct's contexts all at once.
func (s *Service) Details(
	ctx context.Context,
	acct kernel.AccountID,
	all ...Context,
) (Details, error) {
	ids := make([]kernel.ULID, len(all))
	owners := make([]Owner, len(all))
	for i, c := range all {
		ids[i], owners[i] = c.ID, Owner{Context: c.ID}
	}
	var d Details
	var err error
	if d.Elements, err = s.store.Elements(ctx, acct, owners); err != nil {
		return Details{}, err //nolint:wrapcheck // the store names the read
	}
	if d.Attachments, err = s.store.Attachments(ctx, acct, ids, false); err != nil {
		return Details{}, err //nolint:wrapcheck // as above
	}
	var stacks []kernel.ULID
	for _, as := range d.Attachments {
		for _, a := range as {
			stacks = append(stacks, a.Stack)
		}
	}
	if d.Stacks, err = s.stacks.ByIDs(ctx, acct, stacks); err != nil {
		return Details{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	return d, nil
}

// Element reads acct's context slug's element name.
func (s *Service) Element(
	ctx context.Context,
	acct kernel.AccountID,
	slug, name string,
) (Element, error) {
	c, err := s.store.BySlug(ctx, acct, slug, NoLock)
	if err != nil {
		return Element{}, err //nolint:wrapcheck // the store names the read
	}
	return s.store.Element(ctx, acct, Owner{Context: c.ID}, name) //nolint:wrapcheck // as above
}

// AttachedStack reads acct's context slug's attachment id, with the stack
// it attaches to.
func (s *Service) AttachedStack(
	ctx context.Context,
	acct kernel.AccountID,
	slug, id string,
) (Attachment, stacksapi.Stack, error) {
	c, err := s.store.BySlug(ctx, acct, slug, NoLock)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err //nolint:wrapcheck // the store names the read
	}
	a, err := s.attachment(ctx, acct, id)
	if err != nil || a.Context != c.ID {
		return Attachment{}, stacksapi.Stack{}, &Missing{Kind: "attachment", ID: id}
	}
	st, err := s.stacks.Get(ctx, acct, a.Stack)
	return a, st, err //nolint:wrapcheck // the stacks domain names the read
}

// Create creates a context of p's account from in, with its elements and
// stack attachments. The route has decided CONTEXT_CREATE on in's space;
// each attachment needs STACK_CONTEXT_ATTACH in its stack's space. Its
// refusals point into contextCreateV2's body.
func (s *Service) Create(ctx context.Context, p kernel.Principal, in Input) (Context, error) {
	if err := check("", in); err != nil {
		return Context{}, err
	}
	var c Context
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		space, err := s.space(ctx, p, in.Space)
		if err != nil {
			return err
		}
		now := s.now()
		c = Context{
			ID: kernel.NewULID(), Account: p.AccountID, Slug: kernel.SlugFrom(in.Name).String(),
			CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		apply(&c, space, in)
		if err := s.store.Create(ctx, c); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		for _, e := range in.Config {
			if _, err := s.put(ctx, c.Account, Owner{Context: c.ID}, e); err != nil {
				return err
			}
		}
		for i, a := range in.Stacks {
			if _, err := s.attach(ctx, p, c, a.Stack, a.Priority,
				fmt.Sprintf("/stackAttachments/%d/stack", i)); err != nil {
				return err
			}
		}
		return s.changed(ctx, c, configapi.ChangeCreated, nil)
	})
	return c, err //nolint:wrapcheck // each step in the transaction names itself
}

// Update replaces the fields of p's account's context slug with in's,
// when expect accepts its version, and its elements with in's Config when
// replace is set. The route has decided CONTEXT_UPDATE on the context's
// space; a move to another space also needs CONTEXT_CREATE there, and is
// refused when a stack attached to the context could no longer reach it.
// Its refusals point into contextUpdateV2's body.
func (s *Service) Update(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	expect kernel.Expect,
	in Input,
	replace bool,
) (Context, error) {
	if !replace {
		in.Config = nil
	}
	if err := check("/input", in); err != nil {
		return Context{}, err
	}
	if len(in.Stacks) > 0 {
		return Context{}, &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{{
			Pointer: "/input/stackAttachments", Reason: kernel.ReasonRule,
			Message: "An update attaches no stack; attach one with contextAttach.",
		}}}
	}
	return s.write(ctx, p.AccountID, slug, expect, configapi.ChangeUpdated, nil,
		func(ctx context.Context, c *Context) error {
			space, err := s.space(ctx, p, in.Space)
			if err != nil {
				return err
			}
			if space != c.Space {
				if err := s.require(ctx, p, accessapi.ActionContextCreate, space); err != nil {
					return err
				}
				if err := s.contextMove(ctx, *c, space); err != nil {
					return err
				}
			}
			apply(c, space, in)
			if !replace {
				return nil
			}
			if err := s.store.DeleteElements(
				ctx,
				c.Account,
				Owner{Context: c.ID},
				nil,
			); err != nil {
				return err //nolint:wrapcheck // the store names the write
			}
			for _, e := range in.Config {
				if _, err := s.put(ctx, c.Account, Owner{Context: c.ID}, e); err != nil {
					return err
				}
			}
			return nil
		})
}

// Delete deletes p's account's context slug with its elements, when
// expect accepts its version, and answers it as it was. A context
// attached to a stack is refused unless force is set, which detaches it.
func (s *Service) Delete(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	expect kernel.Expect,
	force bool,
) (Context, error) {
	var c Context
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if c, err = s.store.BySlug(ctx, p.AccountID, slug, Update); err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if err := expect.Check(c.Version); err != nil {
			return err //nolint:wrapcheck // kernel.ErrStale is the answer
		}
		attached, err := s.store.Attachments(ctx, c.Account, []kernel.ULID{c.ID}, false)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if n := len(attached[c.ID]); n > 0 && !force {
			return &kernel.Invalid{Detail: fmt.Sprintf(
				"The context is attached to %d stacks; detach them first, or delete with force.",
				n,
			)}
		}
		if err := s.store.Delete(ctx, c.Account, c.ID); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		c.Version++
		return s.changed(ctx, c, configapi.ChangeDeleted, nil)
	})
	return c, err //nolint:wrapcheck // each step in the transaction names itself
}

// SetElement creates or replaces the element in.Name of p's account's
// context slug and reports whether it created it. The route has decided
// CONTEXT_UPDATE on the context's space. Refusals point under at, the
// element's place in the request body, and its name at at+name.
func (s *Service) SetElement(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	in ElementInput,
	at, name string,
) (Element, bool, error) {
	if errs := checkElement(at, name, in); len(errs) > 0 {
		return Element{}, false, &kernel.Invalid{
			Detail: "The config element is refused.",
			Errors: errs,
		}
	}
	var e Element
	var created bool
	_, err := s.write(ctx, p.AccountID, slug, kernel.Expect{}, configapi.ChangeConfigured, nil,
		func(ctx context.Context, c *Context) error {
			var err error
			e, created, err = s.set(ctx, c.Account, Owner{Context: c.ID}, in, at)
			return err
		})
	return e, created, err
}

// set writes in into owner's elements and answers the element as stored,
// and whether it created it; a refusal's pointers lie under at.
func (s *Service) set(
	ctx context.Context,
	acct kernel.AccountID,
	owner Owner,
	in ElementInput,
	at string,
) (Element, bool, error) {
	old, err := s.put(ctx, acct, owner, in)
	if inv, ok := errors.AsType[*kernel.Invalid](err); ok {
		for i := range inv.Errors {
			inv.Errors[i].Pointer = at + inv.Errors[i].Pointer
		}
	}
	if err != nil {
		return Element{}, false, err
	}
	e, err := s.store.Element(ctx, acct, owner, in.Name)
	return e, old.ID.IsZero(), err //nolint:wrapcheck // the store names the read
}

// put writes in into owner's elements, and answers the element it
// replaced, zero when it created one. A nil value keeps the replaced
// element's value, which neither a new element nor one that changes type
// may do, and a write-only element stays write-only unless its value is
// sent.
func (s *Service) put(
	ctx context.Context,
	acct kernel.AccountID,
	owner Owner,
	in ElementInput,
) (Element, error) {
	old, err := s.store.Element(ctx, acct, owner, in.Name)
	switch {
	case errors.Is(err, ErrNotFound):
		old = Element{}
	case err != nil:
		return Element{}, err //nolint:wrapcheck // the store names the read
	}
	e := Element{
		ID: old.ID, Account: acct, Owner: owner, Name: in.Name, FileMount: in.FileMount,
		Value: in.Value, WriteOnly: in.WriteOnly, FileMode: in.FileMode, CreatedAt: old.CreatedAt,
		Checksum: old.Checksum,
	}
	if in.Description != nil {
		e.Description = *in.Description
	}
	if in.Value == nil {
		var reason string
		switch {
		case old.ID.IsZero():
			reason = "A new config element needs a value."
		case old.FileMount != in.FileMount:
			reason = "A config element that changes type needs a value."
		case old.WriteOnly && !in.WriteOnly:
			reason = "A write-only config element becomes readable only with a new value."
		}
		if reason != "" {
			return Element{}, &kernel.Invalid{
				Detail: "The config element is refused.",
				Errors: []kernel.FieldError{{
					Pointer: "/value", Reason: kernel.ReasonRequired, Message: reason,
				}},
			}
		}
	} else if sum := checksum(*in.Value); sum != old.Checksum {
		e.Checksum, e.CreatedAt = sum, s.now()
	}
	if e.ID.IsZero() {
		e.ID = kernel.NewULID()
	}
	if err := s.store.PutElement(ctx, e); err != nil {
		return Element{}, err //nolint:wrapcheck // the store names the write
	}
	return old, nil
}

// DeleteElement deletes the element name of p's account's context slug
// and answers it as it was. The route has decided CONTEXT_UPDATE.
func (s *Service) DeleteElement(
	ctx context.Context,
	p kernel.Principal,
	slug, name string,
) (Element, error) {
	var e Element
	_, err := s.write(ctx, p.AccountID, slug, kernel.Expect{}, configapi.ChangeConfigured, nil,
		func(ctx context.Context, c *Context) error {
			var err error
			owner := Owner{Context: c.ID}
			if e, err = s.store.Element(ctx, c.Account, owner, name); err != nil {
				return err //nolint:wrapcheck // the store names the read
			}
			//nolint:wrapcheck // as above
			return s.store.DeleteElements(ctx, c.Account, owner, []string{name})
		})
	return e, err
}

// Attach attaches p's account's context slug to the stack stack at
// priority, or sets the priority of the attachment it has, and answers
// the attachment with the stack. The route has decided
// STACK_CONTEXT_ATTACH in the stack's space; the caller must read the
// context too, and a context outside the stack's reach is refused.
func (s *Service) Attach(
	ctx context.Context,
	p kernel.Principal,
	slug, stack string,
	priority int32,
) (Attachment, stacksapi.Stack, error) {
	var a Attachment
	var st stacksapi.Stack
	_, err := s.write(ctx, p.AccountID, slug, kernel.Expect{}, configapi.ChangeAttached, &a.Stack,
		func(ctx context.Context, c *Context) error {
			read, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, c.Space)
			switch {
			case err != nil:
				return err //nolint:wrapcheck // the snapshot's read names itself
			case !read:
				return &Missing{Kind: "context", ID: slug}
			}
			a, err = s.attach(ctx, p, *c, stack, priority, "")
			if err == nil {
				st, err = s.stacks.Get(ctx, c.Account, a.Stack)
			}
			return err //nolint:wrapcheck // the stacks domain names the read
		})
	return a, st, err
}

// attach attaches c to the stack slug at priority, holding the stack and
// its space and c's space so no move or delete passes it unseen. When
// the stack is not the route's, at is its pointer in the request body,
// and p needs STACK_CONTEXT_ATTACH in its space.
func (s *Service) attach(
	ctx context.Context,
	p kernel.Principal,
	c Context,
	slug string,
	priority int32,
	at string,
) (Attachment, error) {
	st, err := s.stacks.BySlug(ctx, c.Account, slug)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return Attachment{}, &Missing{Kind: "stack", ID: slug}
	} else if err != nil {
		return Attachment{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	if at != "" {
		read, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, st.Space)
		switch {
		case err != nil:
			return Attachment{}, err //nolint:wrapcheck // the snapshot's read names itself
		case !read:
			return Attachment{}, &Missing{Kind: "stack", ID: slug}
		}
		if err := s.require(ctx, p, accessapi.ActionStackContextAttach, st.Space); err != nil {
			return Attachment{}, err
		}
	}
	if err := s.stacks.Hold(ctx, c.Account, st.ID); errors.Is(err, stacksapi.ErrNotFound) {
		return Attachment{}, &Missing{Kind: "stack", ID: slug}
	} else if err != nil {
		return Attachment{}, err //nolint:wrapcheck // as above
	}
	for _, sp := range []kernel.ULID{st.Space, c.Space} {
		if err := s.spaces.Hold(ctx, c.Account, sp); err != nil {
			return Attachment{}, err //nolint:wrapcheck // the access domain names the lock
		}
	}
	tree, err := s.spaces.Tree(ctx, c.Account)
	if err != nil {
		return Attachment{}, err //nolint:wrapcheck // the access domain names the read
	}
	if !slices.Contains(Reach(index(tree), st.Space), c.Space) {
		pointer := at
		if pointer == "" {
			pointer = "/stackId"
		}
		return Attachment{}, &kernel.Invalid{
			Detail: fmt.Sprintf("Stack %s cannot reach context %s.", st.Slug, c.Slug),
			Errors: []kernel.FieldError{
				{Pointer: pointer, Reason: kernel.ReasonRule, Message: fmt.Sprintf(
					"A stack attaches the contexts of its space and of the spaces it inherits from; "+
						"context %s is in neither.",
					c.Slug,
				)},
			},
		}
	}
	//nolint:wrapcheck // the store names the write
	return s.store.Attach(ctx, Attachment{
		ID: kernel.NewULID(), Account: c.Account, Context: c.ID, Stack: st.ID, Priority: priority,
		CreatedAt: s.now(),
	})
}

// Detach deletes p's account's attachment id and answers it as it was,
// with the stack it attached to. The route has decided
// STACK_CONTEXT_DETACH in the stack's space.
func (s *Service) Detach(
	ctx context.Context,
	p kernel.Principal,
	id string,
) (Attachment, stacksapi.Stack, error) {
	a, err := s.attachment(ctx, p.AccountID, id)
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err
	}
	cs, err := s.store.ByIDs(ctx, p.AccountID, []kernel.ULID{a.Context})
	if err != nil {
		return Attachment{}, stacksapi.Stack{}, err //nolint:wrapcheck // the store names the read
	}
	var st stacksapi.Stack
	_, err = s.write(
		ctx,
		p.AccountID,
		cs[a.Context].Slug,
		kernel.Expect{},
		configapi.ChangeDetached,
		&a.Stack,
		func(ctx context.Context, _ *Context) error {
			if _, err := s.attachment(ctx, p.AccountID, id); err != nil {
				return err
			}
			if st, err = s.stacks.Get(ctx, p.AccountID, a.Stack); err != nil {
				return err //nolint:wrapcheck // the stacks domain names the read
			}
			return s.store.Detach(
				ctx,
				p.AccountID,
				a.ID,
			) //nolint:wrapcheck // the store names the write
		},
	)
	if errors.Is(err, ErrNotFound) {
		return Attachment{}, stacksapi.Stack{}, &Missing{Kind: "attachment", ID: id}
	}
	return a, st, err //nolint:wrapcheck // the stores name the reads and the write
}

// attachment reads acct's attachment by its ID as the API gives it.
func (s *Service) attachment(
	ctx context.Context,
	acct kernel.AccountID,
	id string,
) (Attachment, error) {
	uid, err := kernel.ParseULID(id)
	if err != nil {
		return Attachment{}, fmt.Errorf("%w: attachment %q", ErrNotFound, id)
	}
	return s.store.Attachment(ctx, acct, uid) //nolint:wrapcheck // the store names the read
}

// write reads acct's context slug locked, checks its version against
// expect, lets change change it, and writes it with its version moved on
// and its Changed event naming stack when it is not nil, in one
// transaction.
func (s *Service) write(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	expect kernel.Expect,
	what string,
	stack *kernel.ULID,
	change func(ctx context.Context, c *Context) error,
) (Context, error) {
	var c Context
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if c, err = s.store.BySlug(ctx, acct, slug, Update); err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if err := expect.Check(c.Version); err != nil {
			return err //nolint:wrapcheck // kernel.ErrStale is the answer
		}
		if err := change(ctx, &c); err != nil {
			return err
		}
		c.Version++
		c.UpdatedAt = s.now()
		if err := s.store.Write(ctx, c); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.changed(ctx, c, what, stack)
	})
	return c, err //nolint:wrapcheck // each step in the transaction names itself
}

// space is the ID of the space slug, the top space when it is empty, held
// in the context's transaction; a space p cannot read is a *Missing.
func (s *Service) space(ctx context.Context, p kernel.Principal, slug string) (kernel.ULID, error) {
	id, err := s.readable(ctx, p, slug)
	if err != nil {
		return id, err
	}
	if err := s.spaces.Hold(ctx, p.AccountID, id); errors.Is(err, accessapi.ErrNoSpace) {
		return kernel.ULID{}, &Missing{Kind: "space", ID: slug}
	} else if err != nil {
		return kernel.ULID{}, err //nolint:wrapcheck // the access domain names the lock
	}
	return id, nil
}

// readable is the ID of the space slug, the top space when it is empty,
// when p may read it; a *Missing otherwise.
func (s *Service) readable(
	ctx context.Context,
	p kernel.Principal,
	slug string,
) (kernel.ULID, error) {
	id := p.AccountID.ULID
	if slug != "" {
		var err error
		if id, err = s.spaces.Find(ctx, p.AccountID, slug); errors.Is(err, accessapi.ErrNoSpace) {
			return kernel.ULID{}, &Missing{Kind: "space", ID: slug}
		} else if err != nil {
			return kernel.ULID{}, err //nolint:wrapcheck // the access domain names the read
		}
	}
	read, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, id)
	switch {
	case err != nil:
		return kernel.ULID{}, err //nolint:wrapcheck // the snapshot's read names itself
	case !read:
		return kernel.ULID{}, &Missing{Kind: "space", ID: slug}
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

// changed appends c's Changed event in the context's transaction.
func (s *Service) changed(ctx context.Context, c Context, change string, stack *kernel.ULID) error {
	doc, err := json.Marshal(
		configapi.Changed{Context: c.ID, Slug: c.Slug, Change: change, Stack: stack},
	)
	if err != nil {
		return fmt.Errorf("configuration: encode %s event: %w", change, err)
	}
	_, err = s.events.Append(ctx, outbox.Event{
		Account:       c.Account.ULID,
		Kind:          configapi.ChangedKind,
		SchemaVersion: configapi.ChangedVersion,
		Space:         c.Space.String(),
		Subject:       outbox.Subject{Kind: "context", ID: c.ID.String()},
		Sequence:      c.Version,
		Document:      doc,
	})
	return err //nolint:wrapcheck // the outbox names the append
}
