package source

import (
	"context"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// PreparePush reads the immutable head and every touched path once for all
// affected stacks, before the dispatcher acquires any creation lock.
func (s *Service) PreparePush(ctx context.Context, acct kernel.AccountID, id kernel.ULID, provider string, event vcsapi.Event) (port.PreparedPush, error) {
	source := s.providers[provider]
	if source == nil {
		return port.PreparedPush{}, fmt.Errorf("%w: event source provider", kernel.ErrUnavailable)
	}
	in, found, err := s.integrations.Use(ctx, acct, provider, &id)
	if err != nil {
		return port.PreparedPush{}, fmt.Errorf("vcs: prepare push integration: %w", err)
	}
	if !found {
		return port.PreparedPush{}, port.ErrNoIntegration
	}
	if s.identities != nil {
		if err := s.identities.Check(ctx, acct, in.ID, event.Repository.Namespace, event.Repository.Name); err != nil {
			return port.PreparedPush{}, err //nolint:wrapcheck // the called boundary names the failed operation
		}
	}
	repository := port.Repository{Namespace: event.Repository.Namespace, Name: event.Repository.Name, Integration: &in}
	commit, err := source.ResolveRef(ctx, repository, event.Head)
	if err != nil {
		return port.PreparedPush{}, fmt.Errorf("vcs: prepare push head: %w", err)
	}
	base := event.Before
	var recovered *vcsapi.CommitCollection
	if _, complete := port.NativeCommitFiles(event.Commits); !complete {
		history, ok := source.(port.PushHistory)
		if !ok {
			return port.PreparedPush{}, fmt.Errorf("%w: event source has no push history", kernel.ErrUnavailable)
		}
		rangeFacts, err := history.RecoverPush(ctx, repository, base, event.Head)
		if err != nil {
			return port.PreparedPush{}, fmt.Errorf("vcs: prepare push history: %w", err)
		}
		if _, complete := port.NativeCommitFiles(rangeFacts.Commits); !port.ValidCommit(rangeFacts.Base) || !complete {
			return port.PreparedPush{}, fmt.Errorf("%w: incomplete push history", kernel.ErrUnavailable)
		}
		base, recovered = rangeFacts.Base, rangeFacts.Commits
	}
	files, err := source.AffectedFiles(ctx, repository, base, event.Head, port.PushDiff)
	if err != nil {
		return port.PreparedPush{}, fmt.Errorf("vcs: prepare push diff: %w", err)
	}
	out := stackCommit(commit)
	out.Branch = event.Branch
	collection := event.Commits
	if recovered != nil {
		collection = recovered
	}
	if touched, complete := port.NativeCommitFiles(collection); complete {
		files = append(files, touched...)
		slices.Sort(files)
		files = slices.Compact(files)
	}
	return port.PreparedPush{Commit: out, Files: files, RecoveredCommits: recovered}, nil
}

// PrepareTag resolves only the delivered immutable head, never the tag's
// mutable current name. Providers may peel a tag object to its canonical commit;
// the resolved hash is retained alongside the delivered tag's name.
func (s *Service) PrepareTag(ctx context.Context, acct kernel.AccountID, id kernel.ULID, provider string, event vcsapi.Event) (stacksapi.Commit, error) {
	if len(event.Head) != 40 && len(event.Head) != 64 || port.ZeroCommit(event.Head) {
		return stacksapi.Commit{}, fmt.Errorf("%w: invalid delivered tag head", kernel.ErrUnavailable)
	}
	if _, err := hex.DecodeString(event.Head); err != nil {
		return stacksapi.Commit{}, fmt.Errorf("%w: invalid delivered tag head", kernel.ErrUnavailable)
	}
	source := s.providers[provider]
	if source == nil {
		return stacksapi.Commit{}, fmt.Errorf("%w: tag source provider", kernel.ErrUnavailable)
	}
	in, found, err := s.integrations.Use(ctx, acct, provider, &id)
	if err != nil {
		return stacksapi.Commit{}, fmt.Errorf("vcs: prepare tag integration: %w", err)
	}
	if !found {
		return stacksapi.Commit{}, port.ErrNoIntegration
	}
	repo := port.Repository{Namespace: event.Repository.Namespace, Name: event.Repository.Name, Integration: &in}
	resolve := source.ResolveRef
	if tag, ok := source.(port.TagResolver); ok {
		resolve = tag.ResolveTag
	}
	commit, err := resolve(ctx, repo, event.Head)
	if err != nil {
		return stacksapi.Commit{}, fmt.Errorf("vcs: prepare tagged commit: %w", err)
	}
	out := stackCommit(commit)
	out.Tag = event.Tag
	return out, nil
}
