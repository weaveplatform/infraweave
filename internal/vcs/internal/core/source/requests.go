package source

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// PrepareRequest resolves native context and immutable compared commits before
// any queue completion or stack creation lock. A delivered head never follows a
// newer native request head silently; a retry uses persisted prepared facts.
func (s *Service) PrepareRequest(ctx context.Context, account kernel.AccountID, id kernel.ULID, provider string, event vcsapi.Event) (port.RequestFacts, error) {
	if tx.In(ctx) {
		return port.RequestFacts{}, fmt.Errorf("%w: native request preparation inside a transaction", kernel.ErrUnavailable)
	}
	source := s.providers[provider]
	reader, ok := source.(port.RequestReader)
	if !ok {
		return port.RequestFacts{}, fmt.Errorf("%w: native request source is unavailable", kernel.ErrUnavailable)
	}
	number := 0
	if event.PullRequest != nil {
		number = event.PullRequest.Number
	}
	if event.Comment != nil {
		if number != 0 && number != event.Comment.PullRequest {
			return port.RequestFacts{}, fmt.Errorf("%w: native comment request identity differs", kernel.ErrUnavailable)
		}
		number = event.Comment.PullRequest
	}
	if number < 1 {
		return port.RequestFacts{}, fmt.Errorf("%w: native request identity is absent", kernel.ErrUnavailable)
	}
	in, found, err := s.integrations.Use(ctx, account, provider, &id)
	if err != nil {
		return port.RequestFacts{}, fmt.Errorf("VCS request integration: %w", err)
	}
	if !found {
		return port.RequestFacts{}, port.ErrNoIntegration
	}
	if s.identities != nil {
		if err := s.identities.Check(ctx, account, in.ID, event.Repository.Namespace, event.Repository.Name); err != nil {
			return port.RequestFacts{}, fmt.Errorf("VCS request repository identity: %w", err)
		}
	}
	target := port.Repository{Namespace: event.Repository.Namespace, Name: event.Repository.Name, Integration: &in}
	snapshot, err := reader.ReadRequest(ctx, target, number)
	if err != nil {
		return port.RequestFacts{}, fmt.Errorf("VCS native request snapshot: %w", err)
	}
	if err := validateRequestSnapshot(snapshot, event, number); err != nil {
		return port.RequestFacts{}, err
	}
	snapshot.Request.Fork = !sameRequestRepository(snapshot.HeadRepository, snapshot.BaseRepository)
	revision, err := resolveRequestRevision(ctx, source, target, snapshot)
	if err != nil {
		return port.RequestFacts{}, fmt.Errorf("VCS immutable request revision: %w", err)
	}
	if !strings.EqualFold(revision.Head.Hash, snapshot.Head) || !strings.EqualFold(revision.Base.Hash, snapshot.Base) {
		return port.RequestFacts{}, fmt.Errorf("%w: native request commit changed", kernel.ErrUnavailable)
	}
	out := port.RequestFacts{Snapshot: snapshot, Head: stackCommit(revision.Head), Base: stackCommit(revision.Base), Files: slices.Clone(revision.Files), Actor: event.Author}
	out.Head.Branch = snapshot.Request.HeadBranch
	out.Base.Branch = snapshot.Request.BaseBranch
	if event.Comment != nil {
		out.Comment = new(event.Comment.Body)
		out.Actor = event.Comment.Author
	}
	return out, nil
}

func resolveRequestRevision(ctx context.Context, source port.Source, target port.Repository, snapshot port.RequestSnapshot) (port.RequestRevision, error) {
	if native, ok := source.(port.RequestSource); ok {
		return native.ResolveRequest(ctx, target, snapshot) //nolint:wrapcheck // the caller names the immutable revision boundary
	}
	headSource := port.Repository{Namespace: snapshot.HeadRepository.Namespace, Name: snapshot.HeadRepository.Name, Integration: target.Integration}
	head, err := source.ResolveRef(ctx, headSource, snapshot.Head)
	if err != nil {
		return port.RequestRevision{}, fmt.Errorf("request head: %w", err)
	}
	base, err := source.ResolveRef(ctx, target, snapshot.Base)
	if err != nil {
		return port.RequestRevision{}, fmt.Errorf("request base: %w", err)
	}
	files, err := source.AffectedFiles(ctx, target, snapshot.Base, snapshot.Head)
	if err != nil {
		return port.RequestRevision{}, fmt.Errorf("request diff: %w", err)
	}
	return port.RequestRevision{Head: head, Base: base, Files: files}, nil
}

func sameRequestRepository(native, delivered vcsapi.Repository) bool {
	if delivered.ID != "" {
		return strings.EqualFold(strings.Trim(native.ID, "{}"), strings.Trim(delivered.ID, "{}"))
	}
	return native.Namespace == delivered.Namespace && native.Name == delivered.Name
}

func validateRequestSnapshot(snapshot port.RequestSnapshot, event vcsapi.Event, number int) error {
	if snapshot.Request.Number != number || !port.ValidCommit(snapshot.Head) || !port.ValidCommit(snapshot.Base) ||
		snapshot.HeadRepository.ID == "" || snapshot.HeadRepository.Namespace == "" || snapshot.HeadRepository.Name == "" ||
		snapshot.BaseRepository.ID == "" || !sameRequestRepository(snapshot.BaseRepository, event.Repository) {
		return fmt.Errorf("%w: invalid native request snapshot", kernel.ErrUnavailable)
	}
	if event.Head != "" && !strings.HasPrefix(strings.ToLower(snapshot.Head), strings.ToLower(event.Head)) {
		return fmt.Errorf("%w: native request head changed", kernel.ErrUnavailable)
	}
	if event.Comment != nil && event.Comment.Head != "" && !strings.HasPrefix(strings.ToLower(snapshot.Head), strings.ToLower(event.Comment.Head)) {
		return fmt.Errorf("%w: native comment head changed", kernel.ErrUnavailable)
	}
	return nil
}
