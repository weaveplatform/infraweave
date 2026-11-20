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
	var invalid []kernel.FieldError
	if strings.TrimSpace(in.Name) == "" {
		invalid = append(invalid, kernel.FieldError{Pointer: "/name", Reason: kernel.ReasonRequired, Message: "A policy needs a name."})
	}
	invalid = append(invalid, kernel.CheckLabels("/labels", in.Labels)...)
	if len(invalid) > 0 {
		return Policy{}, &kernel.Invalid{Detail: "The policy settings are refused.", Errors: invalid}
	}
	if in.Engine == "" {
		in.Engine = rego.V1
	}
	if err := s.deps.Runtime.Check(ctx, rego.Policy{Type: in.Type, Engine: in.Engine, Body: in.Body}); err != nil {
		return Policy{}, s.compileError(err)
	}
	var out Policy
	err := tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		space := p.AccountID.ULID
		if in.Space != "" {
			var err error
			space, err = s.deps.Spaces.Find(ctx, p.AccountID, in.Space)
			if errors.Is(err, accessapi.ErrNoSpace) {
				return &Missing{Kind: "space", ID: in.Space}
			}
			if err != nil {
				return fmt.Errorf("policies: resolve space: %w", err)
			}
		}
		if err := s.deps.Spaces.Hold(ctx, p.AccountID, space); err != nil {
			return fmt.Errorf("policies: hold space: %w", err)
		}
		now := s.deps.Now()
		out = Policy{Account: p.AccountID, ID: kernel.NewULID(), Space: space, Name: strings.TrimSpace(in.Name), Body: in.Body, Description: in.Description, Labels: slices.Sorted(slices.Values(in.Labels)), Type: in.Type, Engine: in.Engine, Template: in.Template, CreatedAt: now, UpdatedAt: now, Version: 1}
		if out.Labels == nil {
			out.Labels = []string{}
		}
		return s.deps.Store.Create(ctx, out)
	})
	if err != nil {
		return Policy{}, fmt.Errorf("policies: create: %w", err)
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
