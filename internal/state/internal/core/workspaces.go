package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// DefaultName is the name a stack's default workspace answers, whose
// stored name is empty.
//
// ref: decision 0010 § "Storage"
const DefaultName = "default"

// ErrNotFound reports a workspace the caller's account lacks.
var ErrNotFound = errors.New("state: no such Tofu workspace")

// Workspace is a stack's workspace as the Tofu Workspaces operations
// answer it: Name is as stored, empty for the default workspace; Locked
// is whether any lock is held on it, and TriggeredBy the login or trigger
// that created the run whose lock holds it, empty when none is held or
// that run names none.
type Workspace struct {
	ID          kernel.ULID
	Stack       kernel.ULID
	Name        string
	CreatedAt   time.Time
	Locked      bool
	TriggeredBy string
}

// DisplayName is w's name, DefaultName for the default workspace.
func (w Workspace) DisplayName() string {
	if w.Name == "" {
		return DefaultName
	}
	return w.Name
}

// LockedBy is who triggered the run whose lock holds w, nil when w is
// free or that run names no one.
func (w Workspace) LockedBy() *string {
	if !w.Locked || w.TriggeredBy == "" {
		return nil
	}
	return new(w.TriggeredBy)
}

// target is w's workspace in acct.
func (w Workspace) target(acct kernel.AccountID) Target {
	return Target{Account: acct, Stack: w.Stack, Workspace: w.Name}
}

// Fields are the fields a workspace search filters, and the orderable
// ones order: its name, DefaultName for the default workspace, its
// stack's slug, and whether a lock is held on it.
//
// ref: decision 0010 § "Rollback, searches and retention"
var Fields = []search.Field{
	{Name: "name", Type: search.String, Orderable: true},
	{Name: "stack", Type: search.String, Orderable: true},
	{Name: "isLocked", Type: search.Boolean},
}

// suggested are the fields of Fields whose values a suggestion lists.
var suggested = []string{"name", "stack"}

// WorkspaceScope is what a workspace search sees: the spaces the caller
// may read and the stacks in them, whose workspaces it reads.
type WorkspaceScope struct {
	Reachable []kernel.ULID
	Stacks    []stacksapi.Stack
}

// Authorizer is what the state domain asks of the access domain's
// authorization; accessapi.Authorizer is one.
type Authorizer interface {
	// Reachable lists the spaces of p's account where p holds action.
	Reachable(ctx context.Context, p kernel.Principal, action accessapi.Action) ([]kernel.ULID, error)
}

// SearchWorkspaces reads a page of the workspaces of p's account's
// stacks in the spaces p may read, which the query leaves the others out
// of, by Fields, in name order unless the input names one.
//
// ref: decision 0010 § "Rollback, searches and retention"
func (s *Service) SearchWorkspaces(ctx context.Context, p kernel.Principal, in search.Input) (search.Result[Workspace], error) {
	scope, err := s.workspaceScope(ctx, p)
	if err != nil {
		return search.Result[Workspace]{}, err
	}
	//nolint:wrapcheck // the store names the read; a refusal is answered as it is
	return s.store.SearchWorkspaces(ctx, p.AccountID, in, scope)
}

// SuggestWorkspaces lists the values of the name and stack of the
// workspaces of p's account's stacks in the spaces p may read, under in's
// predicates, so no value or count names a workspace p cannot read.
// Every field of Fields filters, but isLocked lists no values.
func (s *Service) SuggestWorkspaces(ctx context.Context, p kernel.Principal, in search.SuggestionsInput) (search.Suggestions, error) {
	scope, err := s.workspaceScope(ctx, p)
	if err != nil {
		return search.Suggestions{}, err
	}
	listed := []string{}
	for _, f := range suggested {
		if in.Fields == nil || slices.Contains(in.Fields, f) {
			listed = append(listed, f)
		}
	}
	in.Fields = listed
	return s.store.SuggestWorkspaces(ctx, p.AccountID, in, scope) //nolint:wrapcheck // as the store's
}

// workspaceScope is what p's workspace searches see: the spaces it may
// read and the stacks in them.
func (s *Service) workspaceScope(ctx context.Context, p kernel.Principal) (WorkspaceScope, error) {
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return WorkspaceScope{}, err //nolint:wrapcheck // the snapshot's read names itself
	}
	stacks, err := s.stacks.InSpaces(ctx, p.AccountID, reach)
	if err != nil {
		return WorkspaceScope{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	return WorkspaceScope{Reachable: reach, Stacks: stacks}, nil
}

// SpaceOf is the space of the stack of acct's workspace raw, which the
// access domain decides an action on: accessapi.ErrNoSubject when acct
// lacks the workspace or its stack.
//
// ref: decision 0010 § "Storage"
func (s *Service) SpaceOf(ctx context.Context, acct kernel.AccountID, raw string) (kernel.ULID, error) {
	w, err := s.workspace(ctx, acct, raw)
	if errors.Is(err, ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	if err != nil {
		return kernel.ULID{}, err
	}
	st, err := s.stacks.Get(ctx, acct, w.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return st.Space, err //nolint:wrapcheck // the stacks domain names the read
}

// workspace reads acct's workspace raw; ErrNotFound when raw is no
// workspace ID or acct lacks it.
func (s *Service) workspace(ctx context.Context, acct kernel.AccountID, raw string) (Workspace, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return Workspace{}, fmt.Errorf("%w: %q", ErrNotFound, raw)
	}
	w, ok, err := s.store.WorkspaceByID(ctx, acct, id)
	switch {
	case err != nil:
		return Workspace{}, err //nolint:wrapcheck // the store names the read
	case !ok:
		return Workspace{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return w, nil
}

// UnlockWorkspace releases every lock held on p's account's workspace
// raw, in one transaction under the workspace row's lock, and answers the
// workspace, free. A free workspace answers as it is, recording nothing;
// releasing any lock records an audit entry naming p, calling from
// remoteIP, and each LockInfo released. A run whose lock goes this way
// gets 423 on its next write and on its own release of it. The route has
// decided STACK_WORKSPACE_UNLOCK on the stack's space.
//
// ref: decision 0010 § "Locks"
func (s *Service) UnlockWorkspace(ctx context.Context, p kernel.Principal, remoteIP, raw string) (Workspace, error) {
	var out Workspace
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		w, err := s.workspace(ctx, p.AccountID, raw)
		if err != nil {
			return err
		}
		ok, err := s.store.LockWorkspace(ctx, w.target(p.AccountID))
		switch {
		case err != nil:
			return err //nolint:wrapcheck // the store names the read
		case !ok:
			return fmt.Errorf("%w: %s", ErrNotFound, w.ID)
		}
		if _, err := s.release(ctx, p, remoteIP, w); err != nil {
			return err
		}
		w.Locked, w.TriggeredBy = false, ""
		out = w
		return nil
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// UnlockStack releases every lock held on each workspace of p's
// account's stack slug, in one transaction under the rows' locks, taken
// in name order, and answers the workspaces it unlocked, by name; a free
// workspace is left out and records nothing. Each one unlocked records an
// audit entry as UnlockWorkspace does. The route has decided
// STACK_WORKSPACE_UNLOCK on the stack's space.
//
// ref: decision 0010 § "Locks"
func (s *Service) UnlockStack(ctx context.Context, p kernel.Principal, remoteIP, slug string) ([]Workspace, error) {
	st, err := s.stacks.BySlug(ctx, p.AccountID, slug)
	if err != nil {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	var out []Workspace
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		out = []Workspace{}
		all, err := s.store.LockStackWorkspaces(ctx, p.AccountID, st.ID)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		for _, w := range all {
			released, err := s.release(ctx, p, remoteIP, w)
			if err != nil {
				return err
			}
			if released {
				w.Locked, w.TriggeredBy = false, ""
				out = append(out, w)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // each step in the transaction names itself
	}
	return out, nil
}

// release deletes every lock held on w, whose row the context's
// transaction has locked, records each as broken, so its run's own
// release answers 423, and records p's break of them from remoteIP on the
// trail; false when none was held.
//
// ref: decision 0010 § "Locks"
func (s *Service) release(ctx context.Context, p kernel.Principal, remoteIP string, w Workspace) (bool, error) {
	t := w.target(p.AccountID)
	held, err := s.store.Locks(ctx, t)
	if err != nil || len(held) == 0 {
		return false, err //nolint:wrapcheck // the store names the read
	}
	ids := make([]string, len(held))
	for i, l := range held {
		ids[i] = l.ID
	}
	if err := s.store.RemoveLocks(ctx, t, ids); err != nil {
		return false, err //nolint:wrapcheck // the store names the write
	}
	if err := s.store.BreakLocks(ctx, t, held); err != nil {
		return false, err //nolint:wrapcheck // the store names the write
	}
	//nolint:wrapcheck // the store names the write
	return true, s.store.Audit(ctx, unlocked(p, remoteIP, w, held))
}

// unlocked is the audit entry of p releasing held, the locks on w, from
// remoteIP, with p as the actor.
//
// ref: decision 0015 § "Audit trail"
func unlocked(p kernel.Principal, remoteIP string, w Workspace, held []Lock) kernel.AuditEntry {
	released := make([]json.RawMessage, len(held))
	for i, l := range held {
		released[i] = l.Info
	}
	// Each LockInfo was checked as JSON before it was stored, so the
	// metadata always encodes.
	meta, _ := json.Marshal(struct {
		Workspace   string            `json:"workspace"`
		WorkspaceID string            `json:"workspaceId"`
		Released    []json.RawMessage `json:"released"`
	}{w.Name, w.ID.String(), released})
	return kernel.AuditEntry{
		AccountID: p.AccountID.ULID,
		Actor:     actorOf(p),
		Action:    ActionBreakLock,
		EventType: "DELETE",
		AffectedResource: kernel.AuditResource{
			Type: ResourceStack, ID: w.Stack.String(), Metadata: meta,
		},
		RemoteIP: remoteIP,
	}
}

// actorOf is the actor of an entry p's call records: a run's in-run
// token acts as the administrative run it is, under Actor, and any other
// caller under its username.
func actorOf(p kernel.Principal) kernel.AuditActor {
	if p.Kind == kernel.PrincipalRun {
		return kernel.AuditActor{Username: Actor, Run: p.ActorID}
	}
	return kernel.AuditActor{Username: username(p)}
}
