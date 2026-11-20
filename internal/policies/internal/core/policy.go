package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
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
	Create(context.Context, Policy) error
	Get(context.Context, kernel.AccountID, kernel.ULID, bool) (Policy, error)
	Update(context.Context, Policy) error
	InSpace(context.Context, kernel.AccountID, kernel.ULID) (bool, error)
}

// ErrNotFound is an account-qualified lookup with no policy row.
var ErrNotFound = errors.New("policy not found")

// Deps are the policy service's domain ports and bounded compiler.
type Deps struct {
	Store   Store
	DB      tx.Beginner
	Spaces  accessapi.Spaces
	Authz   accessapi.Authorizer
	Runtime *rego.Engine
	Now     func() time.Time
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
		return Policy{}, s.compileError(err)
	}
	var out Policy
	err := tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		space, err := s.writeSpace(ctx, p, in.Space)
		if err != nil {
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
		return s.deps.Store.Create(ctx, out)
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
		if in.Engine == "" {
			in.Engine = out.Engine
		}
		if err := s.deps.Runtime.Check(ctx, rego.Policy{Type: out.Type, Engine: in.Engine, Body: in.Body}); err != nil {
			return s.compileError(err)
		}
		space, err := s.writeSpace(ctx, p, in.Space)
		if err != nil {
			return err
		}
		out.Space = space
		out.Name = strings.TrimSpace(in.Name)
		out.Body = in.Body
		out.Description = in.Description
		out.Labels = labels(in.Labels)
		out.Engine = in.Engine
		out.UpdatedAt = s.deps.Now()
		out.Version++
		return s.deps.Store.Update(ctx, out)
	})
	if err != nil {
		return Policy{}, fmt.Errorf("policies: update: %w", err)
	}
	return out, nil
}

func (s *Service) compileError(err error) error {
	var compile *rego.CompileError
	if errors.As(err, &compile) {
		fields := make([]kernel.FieldError, 0, len(compile.Diagnostics))
		for _, d := range compile.Diagnostics {
			field := kernel.FieldError{Pointer: "/body", Reason: kernel.ReasonRule, Message: d.Message}
			if d.Row > 0 && d.Column > 0 {
				field.Range = &kernel.Range{Start: kernel.Position{Line: d.Row, Column: d.Column}, End: kernel.Position{Line: d.Row, Column: d.Column + max(d.Length, 1)}}
			}
			fields = append(fields, field)
		}
		return &kernel.Invalid{Detail: "The policy does not compile.", Errors: fields}
	}
	if errors.Is(err, rego.ErrPolicy) || errors.Is(err, rego.ErrLimit) {
		return &kernel.Invalid{Detail: "The policy settings are refused.", Errors: []kernel.FieldError{{Pointer: "/body", Reason: kernel.ReasonRule, Message: err.Error()}}}
	}
	return fmt.Errorf("policies: compile: %w", err)
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

// LostOnMove has no attachment reach to validate for management-only policies.
func (s *Service) LostOnMove(context.Context, accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	return nil, nil
}
