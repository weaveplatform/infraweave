package source

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// PrepareCheckAction resolves only the delivered immutable SHA. Native reads
// precede the completing transaction; no diff or mutable branch lookup occurs.
func (s *Service) PrepareCheckAction(ctx context.Context, acct kernel.AccountID, id kernel.ULID, provider string, event vcsapi.Event) (port.ActionFacts, error) {
	facts, _, _, err := s.actionFacts(ctx, acct, id, provider, event)
	return facts, err
}

// actionFacts retains the SAME integration snapshot for both the immutable
// source read and any following collaborator permission read.
func (s *Service) actionFacts(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	provider string,
	event vcsapi.Event,
) (port.ActionFacts, port.Repository, port.CheckActions, error) {
	empty := port.ActionFacts{}
	repo := port.Repository{}
	if tx.In(ctx) || !port.ValidCommit(event.Head) || event.CheckAction == nil {
		return empty, repo, nil, port.ErrCheckActionTarget
	}
	src := s.providers[provider]
	actions, ok := src.(port.CheckActions)
	native, nativeOK := src.(port.RepositorySource)
	if !ok || !nativeOK {
		return empty, repo, nil, port.ErrCheckActionTarget
	}
	in, found, err := s.integrations.Use(ctx, acct, provider, &id)
	if err != nil {
		return empty, repo, nil, fmt.Errorf("vcs: check action integration: %w", err)
	}
	if !found {
		return empty, repo, nil, port.ErrCheckActionTarget
	}
	if s.identities != nil {
		if err := s.identities.Check(ctx, acct, id, event.Repository.Namespace, event.Repository.Name); err != nil {
			return empty, repo, nil, fmt.Errorf("vcs: check action installation: %w", err)
		}
	}
	host, err := actions.CheckHost(in.Settings)
	if err != nil {
		return empty, repo, nil, fmt.Errorf("vcs: check action host: %w", err)
	}
	repo = port.Repository{Namespace: event.Repository.Namespace, Name: event.Repository.Name, Integration: &in}
	resolved, err := native.ResolveRepositoryRef(ctx, repo, event.Head)
	if err != nil {
		return empty, repo, nil, fmt.Errorf("vcs: check action commit: %w", err)
	}
	if resolved.Repository.ID != event.Repository.ID || !strings.EqualFold(resolved.Commit.Hash, event.Head) {
		return empty, repo, nil, port.ErrCheckActionTarget
	}
	return port.ActionFacts{Commit: port.ActionCommit(resolved.Commit), NativeHost: host}, repo, actions, nil
}

// CurrentCheckAction is metadata-only. Its caller already holds the effective
// integration row, so a host/App edit or known installation removal cannot
// race the creation authorized by the prepared native observation.
//
//nolint:wrapcheck // metadata and installation boundaries retain their named errors
func (s *Service) CurrentCheckAction(ctx context.Context, acct kernel.AccountID, id kernel.ULID, provider string, repo vcsapi.Repository, facts port.ActionFacts) (bool, error) {
	if !tx.In(ctx) {
		return false, port.ErrCheckActionTarget
	}
	bindings, ok := s.integrations.(interface {
		Binding(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error)
	})
	actions, has := s.providers[provider].(port.CheckActions)
	if !ok || !has {
		return false, port.ErrCheckActionTarget
	}
	in, found, err := bindings.Binding(ctx, acct, provider, &id)
	if err != nil || !found {
		return false, err
	}
	host, err := actions.CheckHost(in.Settings)
	if err != nil || host != facts.NativeHost {
		return false, err
	}
	if s.identities != nil {
		if err := s.identities.Check(ctx, acct, id, repo.Namespace, repo.Name); err != nil {
			var invalid *kernel.Invalid
			if errors.As(err, &invalid) {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}

// RefreshCheckAction rereads source identity and collaborator permission for
// this evaluation attempt, never reusing a permission stored by the parent.
func (s *Service) RefreshCheckAction(ctx context.Context, acct kernel.AccountID, id kernel.ULID, provider string, event vcsapi.Event, facts port.ActionFacts) (bool, error) {
	current, repo, actions, err := s.actionFacts(ctx, acct, id, provider, event)
	if err != nil {
		return false, err
	}
	if current.NativeHost != facts.NativeHost {
		return false, port.ErrCheckActionTarget
	}
	if event.CheckAction.Action != "deploy" {
		return false, nil
	}
	allowed, err := actions.CanWrite(ctx, repo, event.Author)
	if err != nil {
		return false, fmt.Errorf("vcs: check action permission: %w", err)
	}
	return allowed, nil
}
