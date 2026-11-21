package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Policy is one account's policy. Identity and creation time survive updates;
// Version fences writes independently of the second-resolution public timestamp.
type Policy struct {
	Account              kernel.AccountID
	ID, Space            kernel.ULID
	Name, Body           string
	Description          *string
	Labels               []string
	Type                 rego.Type
	Engine               rego.EngineType
	Template             *string
	CreatedAt, UpdatedAt time.Time
	Version              int64
}

// Input is the mutable policy data and its create-only type/template origin.
// An empty engine chooses V1 on create and preserves the old engine on update.
type Input struct {
	Space, Name, Body string
	Description       *string
	Labels            []string
	Type              rego.Type
	Engine            rego.EngineType
	Template          *string
}

// Store keeps policies under their owning account even when IDs are known.
type Store interface {
	LockAutoattach(context.Context, kernel.AccountID) error
	Create(context.Context, Policy) error
	Get(context.Context, kernel.AccountID, kernel.ULID, bool) (Policy, error)
	Update(context.Context, Policy) error
	Delete(context.Context, kernel.AccountID, kernel.ULID) error
	AttachmentCount(context.Context, kernel.AccountID, kernel.ULID) (int, error)
	Attach(context.Context, Attachment) (Attachment, error)
	AutoAttach(context.Context, Attachment) (bool, error)
	Labelled(context.Context, kernel.AccountID, []kernel.ULID, []string) ([]Policy, error)
	AutomaticPolicies(context.Context, kernel.AccountID) ([]Policy, error)
	AccountAttachments(context.Context, kernel.AccountID) ([]Attachment, error)
	DeleteStack(context.Context, kernel.AccountID, kernel.ULID) error
	Attachment(context.Context, kernel.AccountID, kernel.ULID) (Attachment, error)
	Detach(context.Context, kernel.AccountID, kernel.ULID) error
	Attachments(context.Context, kernel.AccountID, []kernel.ULID, bool) (map[kernel.ULID][]Attachment, error)
	ByIDs(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]Policy, error)
	Effective(context.Context, kernel.AccountID, rego.Type, []kernel.ULID, *kernel.ULID, []string) ([]Policy, error)
	Search(context.Context, kernel.AccountID, search.Input, Scope) (search.Result[Policy], error)
	Suggest(context.Context, kernel.AccountID, search.SuggestionsInput, Scope) (search.Suggestions, error)
	InSpace(context.Context, kernel.AccountID, kernel.ULID) (bool, error)
}

// ErrNotFound is an account-qualified lookup with no policy row.
var ErrNotFound = errors.New("policy not found")

// Deps are the policy service's domain ports and bounded compiler.
type Deps struct {
	Store        Store
	DB           tx.Beginner
	Spaces       accessapi.Spaces
	Authz        accessapi.Authorizer
	Runtime      *rego.Engine
	Events       Events
	Stacks       stacksapi.Stacks
	Targets      stacksapi.AttachmentTargets
	Queue        *queue.Queue
	Now          func() time.Time
	LoginChanges accessapi.LoginPolicyChanges
	Sampling     *sampling.Service
}

// Events appends policy changes in the transaction carried by the context.
type Events interface {
	Append(context.Context, outbox.Event) (outbox.Event, error)
}

// Service compiles policy bodies before their transactional writes.
type Service struct{ deps Deps }

// New constructs the service without starting background work.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{deps: d}
}

// Missing conceals a resource that the account lacks or cannot read.
type Missing struct{ Kind, ID string }

func (e *Missing) Error() string { return fmt.Sprintf("policies: no %s %q", e.Kind, e.ID) }

// Create compiles a new policy, holds its space and persists the policy atomically.
func (s *Service) Create(ctx context.Context, p kernel.Principal, in Input) (Policy, error) {
	if err := checkInput(in); err != nil {
		return Policy{}, err
	}
	if in.Engine == "" {
		in.Engine = rego.V1
	}
	if err := s.deps.Runtime.Check(ctx, rego.Policy{Type: in.Type, Engine: in.Engine, Body: in.Body}); err != nil {
		return Policy{}, s.compileError(err, in.Body)
	}
	var out Policy
	err := tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.holdAccount(ctx, p.AccountID); err != nil {
			return err
		}
		if err := s.deps.Store.LockAutoattach(ctx, p.AccountID); err != nil {
			return err //nolint:wrapcheck // the store names the lock
		}
		space, err := s.writeSpace(ctx, p, in.Space)
		if err != nil {
			return err
		}
		if err := checkPlacement(in.Type, space, p.AccountID); err != nil {
			return err
		}
		now := s.deps.Now()
		out = Policy{
			Account:     p.AccountID,
			ID:          kernel.NewULID(),
			Space:       space,
			Name:        strings.TrimSpace(in.Name),
			Body:        in.Body,
			Description: in.Description,
			Labels:      labels(in.Labels),
			Type:        in.Type,
			Engine:      in.Engine,
			Template:    in.Template,
			CreatedAt:   now,
			UpdatedAt:   now,
			Version:     1,
		}
		if err := s.deps.Store.Create(ctx, out); err != nil {
			return err //nolint:wrapcheck // the transaction adds the create operation
		}
		if _, err := s.autoattach(ctx, out); err != nil {
			return err
		}
		if err := s.loginChanged(ctx, p, out.Type); err != nil {
			return err
		}
		return s.changed(ctx, out, policyapi.ChangeCreated)
	})
	if err != nil {
		return Policy{}, fmt.Errorf("policies: create: %w", err)
	}
	return out, nil
}

func checkInput(in Input) error {
	var invalid []kernel.FieldError
	if strings.TrimSpace(in.Name) == "" {
		invalid = append(invalid, kernel.FieldError{Pointer: "/name", Reason: kernel.ReasonRequired, Message: "A policy needs a name."})
	}
	invalid = append(invalid, kernel.CheckLabels("/labels", in.Labels)...)
	if len(invalid) > 0 {
		return &kernel.Invalid{Detail: "The policy settings are refused.", Errors: invalid}
	}
	return nil
}

func labels(in []string) []string {
	out := slices.Sorted(slices.Values(in))
	if out == nil {
		return []string{}
	}
	return out
}

// Denied is a transaction-time authorization refusal after resolving a space.
type Denied struct{ Action accessapi.Action }

func (e *Denied) Error() string { return fmt.Sprintf("policies: action %s is denied", e.Action) }

func (s *Service) require(ctx context.Context, p kernel.Principal, space kernel.ULID) error {
	ok, err := s.deps.Authz.Authorize(ctx, p, accessapi.ActionSpaceAdmin, space)
	if err != nil {
		return fmt.Errorf("policies: authorize write: %w", err)
	}
	if !ok {
		return &Denied{Action: accessapi.ActionSpaceAdmin}
	}
	return nil
}

func (s *Service) writeSpace(ctx context.Context, p kernel.Principal, slug string) (kernel.ULID, error) {
	space := p.AccountID.ULID
	if slug != "" {
		var err error
		space, err = s.deps.Spaces.Find(ctx, p.AccountID, slug)
		if errors.Is(err, accessapi.ErrNoSpace) {
			return kernel.ULID{}, &Missing{Kind: "space", ID: slug}
		}
		if err != nil {
			return kernel.ULID{}, fmt.Errorf("policies: resolve space: %w", err)
		}
	}
	if err := s.deps.Spaces.Hold(ctx, p.AccountID, space); err != nil {
		if errors.Is(err, accessapi.ErrNoSpace) {
			return kernel.ULID{}, &Missing{Kind: "space", ID: slug}
		}
		return kernel.ULID{}, fmt.Errorf("policies: hold space: %w", err)
	}
	if err := s.require(ctx, p, space); err != nil {
		return kernel.ULID{}, err
	}
	return space, nil
}

// Update preserves identity, type, template origin and an omitted engine while
// replacing mutable settings. The row lock fences compilation against concurrent
// engine changes, and a failed compile commits no policy change.
func (s *Service) Update(ctx context.Context, p kernel.Principal, id string, expect kernel.Expect, in Input) (Policy, error) {
	if err := checkInput(in); err != nil {
		return Policy{}, err
	}
	parsed, err := kernel.ParseULID(id)
	if err != nil {
		return Policy{}, &Missing{Kind: "policy", ID: id}
	}
	var out Policy
	err = tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.holdAccount(ctx, p.AccountID); err != nil {
			return err
		}
		if err := s.deps.Store.LockAutoattach(ctx, p.AccountID); err != nil {
			return err //nolint:wrapcheck // the store names the lock
		}
		out, err = s.deps.Store.Get(ctx, p.AccountID, parsed, true)
		if errors.Is(err, ErrNotFound) {
			return &Missing{Kind: "policy", ID: id}
		}
		if err != nil {
			return fmt.Errorf("policies: lock for update: %w", err)
		}
		if err := expect.Check(out.Version); err != nil {
			return fmt.Errorf("policies: update version: %w", err)
		}
		if err := s.require(ctx, p, out.Space); err != nil {
			return err
		}
		engine := in.Engine
		if engine == "" {
			engine = out.Engine
		}
		if err := s.deps.Runtime.Check(ctx, rego.Policy{Type: out.Type, Engine: engine, Body: in.Body}); err != nil {
			return s.compileError(err, in.Body)
		}
		space, err := s.writeSpace(ctx, p, in.Space)
		if err != nil {
			return err
		}
		if err := checkPlacement(out.Type, space, p.AccountID); err != nil {
			return err
		}
		if out.Space != space {
			if err := s.policyMoving(ctx, out, space); err != nil {
				return err
			}
		}
		out.Space = space
		out.Name = strings.TrimSpace(in.Name)
		out.Body = in.Body
		out.Description = in.Description
		out.Labels = labels(in.Labels)
		out.Engine = engine
		out.UpdatedAt = s.deps.Now()
		out.Version++
		if err := s.deps.Store.Update(ctx, out); err != nil {
			return err //nolint:wrapcheck // the transaction adds the update operation
		}
		if _, err := s.autoattach(ctx, out); err != nil {
			return err
		}
		if err := s.loginChanged(ctx, p, out.Type); err != nil {
			return err
		}
		return s.changed(ctx, out, policyapi.ChangeUpdated)
	})
	if err != nil {
		return Policy{}, fmt.Errorf("policies: update: %w", err)
	}
	return out, nil
}

func checkPlacement(kind rego.Type, space kernel.ULID, acct kernel.AccountID) error {
	if kind == rego.Login && space != acct.ULID {
		return &kernel.Invalid{
			Detail: "Login policies belong to the root space.",
			Errors: []kernel.FieldError{{Pointer: "/space", Reason: kernel.ReasonRule, Message: "A login policy must stay in the account's root space."}},
		}
	}
	return nil
}

func (s *Service) compileError(err error, body string) error {
	var compile *rego.CompileError
	if errors.As(err, &compile) {
		if len(compile.Diagnostics) == 0 {
			return fmt.Errorf("policies: compile without source diagnostics: %w", err)
		}
		fields := make([]kernel.FieldError, 0, len(compile.Diagnostics))
		for _, d := range compile.Diagnostics {
			field := kernel.FieldError{Pointer: "/body", Reason: kernel.ReasonRule, Message: d.Message}
			field.Range = d.SourceRange(body)
			fields = append(fields, field)
		}
		return &kernel.Invalid{Detail: "The policy does not compile.", Errors: fields}
	}
	if errors.Is(err, rego.ErrPolicy) || errors.Is(err, rego.ErrLimit) {
		return &kernel.Invalid{Detail: "The policy settings are refused.", Errors: []kernel.FieldError{{Pointer: "/body", Reason: kernel.ReasonRule, Message: err.Error()}}}
	}
	return fmt.Errorf("policies: compile: %w", err)
}

// Delete returns the persisted policy before removing it. Attached policies need
// an explicit force request; their owned attachment rows are removed atomically.
func (s *Service) Delete(ctx context.Context, p kernel.Principal, id string, expect kernel.Expect, force bool) (Policy, error) {
	parsed, err := kernel.ParseULID(id)
	if err != nil {
		return Policy{}, &Missing{Kind: "policy", ID: id}
	}
	var out Policy
	err = tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.holdAccount(ctx, p.AccountID); err != nil {
			return err
		}
		if err := s.deps.Store.LockAutoattach(ctx, p.AccountID); err != nil {
			return err //nolint:wrapcheck // the store names the lock
		}
		out, err = s.deps.Store.Get(ctx, p.AccountID, parsed, true)
		if errors.Is(err, ErrNotFound) {
			return &Missing{Kind: "policy", ID: id}
		}
		if err != nil {
			return fmt.Errorf("policies: lock for delete: %w", err)
		}
		if err := s.require(ctx, p, out.Space); err != nil {
			return err
		}
		if err := expect.Check(out.Version); err != nil {
			return fmt.Errorf("policies: delete version: %w", err)
		}
		count, err := s.deps.Store.AttachmentCount(ctx, p.AccountID, parsed)
		if err != nil {
			return fmt.Errorf("policies: count attachments: %w", err)
		}
		if count > 0 && !force {
			return &kernel.Invalid{Detail: "The policy still has attachments; detach them first, or delete with force."}
		}
		if err := s.deps.Store.Delete(ctx, p.AccountID, parsed); err != nil {
			return err //nolint:wrapcheck // the transaction adds the delete operation
		}
		if err := s.loginChanged(ctx, p, out.Type); err != nil {
			return err
		}
		deleted := out
		deleted.Version++
		return s.changed(ctx, deleted, policyapi.ChangeDeleted)
	})
	if err != nil {
		return Policy{}, fmt.Errorf("policies: delete: %w", err)
	}
	return out, nil
}

func (s *Service) holdAccount(ctx context.Context, account kernel.AccountID) error {
	if s.deps.LoginChanges != nil {
		if err := s.deps.LoginChanges.HoldAccount(ctx, account); err != nil {
			return fmt.Errorf("policies: hold account authorization fence: %w", err)
		}
	}
	return nil
}

func (s *Service) loginChanged(ctx context.Context, p kernel.Principal, kind rego.Type) error {
	if kind != rego.Login || s.deps.LoginChanges == nil {
		return nil
	}
	err := s.deps.LoginChanges.LoginPolicyChanged(ctx, p)
	if errors.Is(err, accessapi.ErrLoginPolicyEditor) {
		return &Denied{Action: accessapi.ActionSpaceAdmin}
	}
	if err != nil {
		return fmt.Errorf("policies: commit LOGIN security effects: %w", err)
	}
	return nil
}

func (s *Service) changed(ctx context.Context, p Policy, change string) error {
	doc, err := json.Marshal(policyapi.Changed{Policy: p.ID, Change: change})
	if err != nil {
		return fmt.Errorf("policies: encode change: %w", err)
	}
	_, err = s.deps.Events.Append(
		ctx,
		outbox.Event{
			Account:       p.Account.ULID,
			Kind:          policyapi.ChangedKind,
			SchemaVersion: policyapi.ChangedVersion,
			Space:         p.Space.String(),
			Subject:       outbox.Subject{Kind: "policy", ID: p.ID.String()},
			Sequence:      p.Version,
			Document:      doc,
		},
	)
	if err != nil {
		return fmt.Errorf("policies: append change: %w", err)
	}
	return nil
}

// Get reads an account's immutable policy ID.
func (s *Service) Get(ctx context.Context, acct kernel.AccountID, id string) (Policy, error) {
	parsed, err := kernel.ParseULID(id)
	if err != nil {
		return Policy{}, &Missing{Kind: "policy", ID: id}
	}
	out, err := s.deps.Store.Get(ctx, acct, parsed, false)
	if errors.Is(err, ErrNotFound) {
		return Policy{}, &Missing{Kind: "policy", ID: id}
	}
	return out, err //nolint:wrapcheck // the store names the lookup
}

// SpaceOf resolves policy subjects without disclosing another account's row.
func (s *Service) SpaceOf(ctx context.Context, acct kernel.AccountID, id string) (kernel.ULID, error) {
	p, err := s.Get(ctx, acct, id)
	var missing *Missing
	if errors.As(err, &missing) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return p.Space, err //nolint:wrapcheck // the policy lookup names its failure
}

// SpaceInUse prevents deleting a space while it owns a policy.
func (s *Service) SpaceInUse(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error) {
	return s.deps.Store.InSpace(ctx, acct, space) //nolint:wrapcheck // the store names the read
}

// LostOnMove is covered by SpaceChanging, which also checks inheritance and
// changes of both ends of a relation in the prospective tree.
func (s *Service) LostOnMove(context.Context, accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	return nil, nil
}
