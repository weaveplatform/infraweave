package pullrequest

import (
	"context"
	"fmt"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type plan struct {
	service  *Service
	requests []Request
	pushes   intake.DispatchPlan
}

// Prepare keeps all remote lookups outside the processor's completion fence.
func (s *Service) Prepare(ctx context.Context, d intake.Delivery, events []vcsapi.Event) (intake.DispatchPlan, error) {
	if !d.Deadline.IsZero() {
		ctx, cancel := context.WithTimeout(ctx, max(0, d.Deadline.Sub(s.d.Now())))
		defer cancel()
		return s.prepareAll(ctx, d, events)
	}
	return s.prepareAll(ctx, d, events)
}

func (s *Service) prepareAll(ctx context.Context, d intake.Delivery, events []vcsapi.Event) (intake.DispatchPlan, error) {
	out := &plan{service: s}
	if s.d.Pushes != nil {
		p, err := s.d.Pushes.Prepare(ctx, d, events)
		if err != nil {
			return nil, fmt.Errorf("vcs: prepare compound delivery: %w", err)
		}
		out.pushes = p
	}
	for _, event := range events {
		if event.Type != "pull_request" || event.PullRequest == nil {
			continue
		}
		request, err := s.prepare(ctx, d, event)
		if err != nil {
			return nil, err
		}
		out.requests = append(out.requests, request)
	}
	return out, nil
}

func (s *Service) prepare(ctx context.Context, d intake.Delivery, event vcsapi.Event) (Request, error) {
	p := event.PullRequest
	r := Request{
		ID: kernel.NewULID(), Account: d.Account, Integration: d.Integration,
		RepositoryID: event.Repository.ID, Namespace: event.Repository.Namespace, Repository: event.Repository.Name,
		Number: strconv.Itoa(p.Number), BaseBranch: p.BaseBranch, HeadBranch: p.HeadBranch, HeadSHA: event.Head,
		AuthorLogin: p.AuthorLogin, Title: p.Title, URL: p.URL, HeadBranchURL: p.HeadBranchURL,
		Draft: p.Draft, State: p.State, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	if r.State == "" {
		switch event.Action {
		case "closed":
			r.State = Closed
		case "merged":
			r.State = Merged
		default:
			r.State = Open
		}
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = d.ReceivedAt
	}
	if r.State == Open && !port.ValidCommit(r.HeadSHA) {
		return Request{}, ErrHead
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = d.ReceivedAt
	}
	if r.AuthorLogin == "" && p.AuthorID != "" {
		login, err := s.d.Store.Author(ctx, d.Account, d.Integration, r.RepositoryID, r.Number)
		if err != nil {
			return Request{}, fmt.Errorf("vcs: read retained request creator: %w", err)
		}
		if login == "" {
			provider := s.d.Authors[d.Provider]
			if provider == nil {
				return Request{}, fmt.Errorf("%w: request author lookup unavailable", kernel.ErrUnavailable)
			}
			in, found, err := s.d.Integrations.Use(ctx, d.Account, d.Provider, &d.Integration)
			if err != nil {
				return Request{}, fmt.Errorf("vcs: resolve request creator integration: %w", err)
			}
			if found {
				login, err = provider.AuthorLogin(ctx, in, p.AuthorID)
				if err != nil {
					return Request{}, fmt.Errorf("vcs: resolve request creator: %w", err)
				}
				if login == "" {
					return Request{}, fmt.Errorf("%w: request author is unavailable", kernel.ErrUnavailable)
				}
			}
		}
		r.AuthorLogin = login
	}
	return r, nil
}

// Commit is called only within the live inbox job's held completion fence.
func (p *plan) Commit(ctx context.Context) error {
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	for _, request := range p.requests {
		if err := p.service.d.Store.Upsert(ctx, request); err != nil {
			return fmt.Errorf("vcs: commit request upkeep: %w", err)
		}
	}
	if p.pushes != nil {
		return p.pushes.Commit(ctx) //nolint:wrapcheck // the push plan names its fenced write
	}
	return nil
}
