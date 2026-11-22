package core

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/policies/internal/catalogue"
)

// CatalogueStore uses the shared typed search contract over immutable templates.
type CatalogueStore interface {
	SearchTemplates(context.Context, kernel.AccountID, []catalogue.Template, search.Input) (search.Result[catalogue.Template], error)
	SuggestTemplates(context.Context, kernel.AccountID, []catalogue.Template, search.SuggestionsInput) (search.Suggestions, error)
}

func (s *Service) templateReader(ctx context.Context, p kernel.Principal) error {
	allowed, err := s.deps.Authz.Holds(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return fmt.Errorf("policies: authorize catalogue: %w", err)
	}
	if !allowed {
		return &Denied{Action: accessapi.ActionSpaceRead}
	}
	return nil
}

// Template returns a supported global example after account space-read access.
func (s *Service) Template(ctx context.Context, p kernel.Principal, id string) (catalogue.Template, error) {
	if err := s.templateReader(ctx, p); err != nil {
		return catalogue.Template{}, err
	}
	row, err := catalogue.Get(id)
	if errors.Is(err, catalogue.ErrMissing) {
		return catalogue.Template{}, &Missing{Kind: "policy template", ID: id}
	}
	if err != nil {
		return catalogue.Template{}, fmt.Errorf("policies: read template: %w", err)
	}
	return row, nil
}

// SearchTemplates exposes only the supported non-LOGIN catalogue membership.
func (s *Service) SearchTemplates(ctx context.Context, p kernel.Principal, in search.Input) (search.Result[catalogue.Template], error) {
	if err := s.templateReader(ctx, p); err != nil {
		return search.Result[catalogue.Template]{}, err
	}
	rows, err := catalogue.All()
	if err != nil {
		return search.Result[catalogue.Template]{}, fmt.Errorf("policies: load catalogue: %w", err)
	}
	return s.deps.Catalogue.SearchTemplates(ctx, p.AccountID, rows, in) //nolint:wrapcheck // store names shared search failures
}

// SuggestTemplates counts the same supported membership and filters as search.
func (s *Service) SuggestTemplates(ctx context.Context, p kernel.Principal, in search.SuggestionsInput) (search.Suggestions, error) {
	if err := s.templateReader(ctx, p); err != nil {
		return search.Suggestions{}, err
	}
	rows, err := catalogue.All()
	if err != nil {
		return search.Suggestions{}, fmt.Errorf("policies: load catalogue: %w", err)
	}
	return s.deps.Catalogue.SuggestTemplates(ctx, p.AccountID, rows, in) //nolint:wrapcheck // store names shared search failures
}

func checkTemplateOrigin(in Input) error {
	if in.Template == nil {
		return nil
	}
	row, err := catalogue.Get(*in.Template)
	if errors.Is(err, catalogue.ErrMissing) || err == nil && row.Type != in.Type {
		return &kernel.Invalid{
			Detail: "The template origin does not identify a supported template of this policy type.",
			Errors: []kernel.FieldError{{Pointer: "/policyTemplateULID", Reason: kernel.ReasonRule, Message: "Choose a served template of the policy type."}},
		}
	}
	if err != nil {
		return fmt.Errorf("policies: resolve template origin: %w", err)
	}
	return nil
}
