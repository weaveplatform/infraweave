package core

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

// ErrNotFound reports a context, config element or attachment the account
// lacks.
var ErrNotFound = errors.New("configuration: not found")

// SlugTaken is a slug another context of the account holds.
type SlugTaken struct{ Slug string }

func (e *SlugTaken) Error() string {
	return fmt.Sprintf("configuration: slug %q is taken", e.Slug)
}

// Lock is how a read locks a context's row until the context's
// transaction ends.
type Lock int

const (
	// NoLock reads the row without locking it.
	NoLock Lock = iota
	// Update locks the row FOR UPDATE, for a write of the context.
	Update
)

// Fields are what a context search filters and orders by: id, the slug;
// label, any of its labels; space, its place in the tree, which keeps a
// node's contexts and, asked, those of the spaces below it. A search that
// names no order reads by id.
var Fields = []search.Field{
	{Name: "id", Type: search.String, Orderable: true},
	{Name: "name", Type: search.String, Orderable: true},
	{Name: "description", Type: search.String},
	{Name: "label", Type: search.String},
	{Name: "space", Type: search.Hierarchy},
	{Name: "createdAt", Type: search.Time, Orderable: true},
	{Name: "updatedAt", Type: search.Time, Orderable: true},
}

// Scope is what a context search sees: the spaces whose contexts it may
// keep and the account's space tree, which the space field reads.
type Scope struct {
	Reachable []kernel.ULID
	Spaces    []accessapi.SpaceNode
}

// Store is what the service needs of the configuration's storage. Every
// method joins the transaction its context carries.
type Store interface {
	// Create writes a new context; a *SlugTaken when another context of
	// its account holds its slug.
	Create(ctx context.Context, c Context) error
	// Write replaces the context's fields and sets its version.
	Write(ctx context.Context, c Context) error
	// BySlug reads acct's context slug, locked as lock says; ErrNotFound
	// when acct lacks it.
	BySlug(ctx context.Context, acct kernel.AccountID, slug string, lock Lock) (Context, error)
	// ByIDs reads those of acct's contexts ids that it has, keyed by ID.
	ByIDs(
		ctx context.Context,
		acct kernel.AccountID,
		ids []kernel.ULID,
	) (map[kernel.ULID]Context, error)
	// Delete deletes acct's context id with its elements and attachments.
	Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// InSpace reports whether a context of acct lives in space.
	InSpace(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error)
	// Search reads a page of acct's contexts in the scope that in keeps, by
	// Fields; an input they cannot answer is a *search.Invalid.
	Search(
		ctx context.Context,
		acct kernel.AccountID,
		in search.Input,
		scope Scope,
	) (search.Result[Context], error)
	// Suggest lists the values of Fields that acct's contexts in the scope
	// that in keeps hold, and counts them.
	Suggest(
		ctx context.Context,
		acct kernel.AccountID,
		in search.SuggestionsInput,
		scope Scope,
	) (search.Suggestions, error)

	// Elements reads the config elements of each of owners, which all hold
	// either contexts or stacks, by name, keyed by the owner's ID. A
	// write-only element's value is left nil, so it is never opened.
	Elements(
		ctx context.Context,
		acct kernel.AccountID,
		owners []Owner,
	) (map[kernel.ULID][]Element, error)
	// Element reads owner's element name; ErrNotFound when it has none.
	Element(ctx context.Context, acct kernel.AccountID, owner Owner, name string) (Element, error)
	// PutElement writes e, sealing its value, or keeping the value of the
	// row e.ID names when e.Value is nil.
	PutElement(ctx context.Context, e Element) error
	// LockElements locks owner's elements until the transaction ends, so
	// two writers of them take turns.
	LockElements(ctx context.Context, acct kernel.AccountID, owner Owner) error
	// DeleteElements deletes owner's elements names, every one when names
	// is nil.
	DeleteElements(ctx context.Context, acct kernel.AccountID, owner Owner, names []string) error

	// Labelled reads acct's contexts in spaces that carry any of labels,
	// by name.
	Labelled(
		ctx context.Context,
		acct kernel.AccountID,
		spaces []kernel.ULID,
		labels []string,
	) ([]Context, error)
	// Attach writes a, or the priority and kind of the attachment its
	// context already has to its stack, and answers the attachment as
	// stored.
	Attach(ctx context.Context, a Attachment) (Attachment, error)
	// AutoAttach writes a as an auto-attachment unless its context is
	// attached to its stack already, and reports whether it wrote it.
	AutoAttach(ctx context.Context, a Attachment) (bool, error)
	// LockAutoattach locks acct's auto-attachments until the transaction
	// ends, so a context's change and a stack's take turns matching them.
	LockAutoattach(ctx context.Context, acct kernel.AccountID) error
	// Attachment reads acct's attachment id; ErrNotFound when acct lacks
	// it.
	Attachment(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Attachment, error)
	// Detach deletes acct's attachment id.
	Detach(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// Attachments reads the attachments of contexts, or of stacks when
	// byStack is set, keyed by that ID, by priority, then context and
	// stack.
	Attachments(
		ctx context.Context,
		acct kernel.AccountID,
		ids []kernel.ULID,
		byStack bool,
	) (map[kernel.ULID][]Attachment, error)
	// AttachedStacks lists the stacks any of acct's contexts is attached
	// to.
	AttachedStacks(ctx context.Context, acct kernel.AccountID) ([]kernel.ULID, error)
	// DeleteStack deletes stack's attachments and elements.
	DeleteStack(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error
}
