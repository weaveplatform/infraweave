package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

// Capture exposes an opaque handle and, on protected reads, sanitized JSON.
// A nil Input distinguishes a previously published capture since replaced.
type Capture struct {
	ID    kernel.ULID
	Kind  string
	At    time.Time
	Input *string
}

func (s *Service) readSpace(ctx context.Context, p kernel.Principal, space kernel.ULID) error {
	if err := s.deps.Spaces.Hold(ctx, p.AccountID, space); err != nil {
		if errors.Is(err, accessapi.ErrNoSpace) {
			return policyapi.ErrSampleMissing
		}
		return fmt.Errorf("policies: hold evidence space: %w", err)
	}
	ok, err := s.deps.Authz.Authorize(ctx, p, accessapi.ActionSpaceRead, space)
	if err != nil {
		return fmt.Errorf("policies: authorize evidence space: %w", err)
	}
	if !ok {
		return &policyapi.SampleDenied{Action: string(accessapi.ActionSpaceRead)}
	}
	return nil
}

// Captures lists only current, published, unexpired metadata in the named space.
func (s *Service) Captures(ctx context.Context, p kernel.Principal, space, kind string, in search.Input) (search.Result[Capture], error) {
	id, err := s.deps.Spaces.Find(ctx, p.AccountID, space)
	if errors.Is(err, accessapi.ErrNoSpace) {
		return search.Result[Capture]{}, policyapi.ErrSampleMissing
	}
	if err != nil {
		return search.Result[Capture]{}, fmt.Errorf("policies: capture space: %w", err)
	}
	var out search.Result[Capture]
	err = tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.readSpace(ctx, p, id); err != nil {
			return err
		}
		var err error
		out, err = s.deps.Evidence.ListCaptures(ctx, p.AccountID, id, kind, in)
		return err //nolint:wrapcheck // the store names the query
	})
	if err != nil {
		return out, fmt.Errorf("policies: list capture metadata: %w", err)
	}
	return out, nil
}

// ReadCapture resolves its account and space before root administration. The
// head->input->blob order matches publication and retirement on every replica.
func (s *Service) ReadCapture(ctx context.Context, p kernel.Principal, raw string) (Capture, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return Capture{}, policyapi.ErrSampleMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out Capture
	err = tx.Do(ctx, s.deps.DB, tx.Options{NoRetry: true}, func(ctx context.Context) error {
		if s.deps.Sampling == nil || s.deps.Evidence == nil {
			return kernel.ErrUnavailable
		}
		probe, _, err := s.deps.Evidence.Capture(ctx, p.AccountID, id, false)
		if errors.Is(err, sampling.ErrNotFound) || err == nil && probe.SpaceSnapshot == nil {
			return policyapi.ErrSampleMissing
		}
		if err != nil {
			return fmt.Errorf("policies: capture owner: %w", err)
		}
		if err := s.readSpace(ctx, p, *probe.SpaceSnapshot); err != nil {
			return err
		}
		input, current, err := s.deps.Evidence.Capture(ctx, p.AccountID, id, true)
		if errors.Is(err, sampling.ErrNotFound) || err == nil && (input.SpaceSnapshot == nil || input.PublishedAt == nil || !s.deps.Now().Before(input.ExpiresAt)) {
			return policyapi.ErrSampleMissing
		}
		if err != nil {
			return fmt.Errorf("policies: capture owner: %w", err)
		}
		if input.Account != probe.Account || input.ID != probe.ID || *input.SpaceSnapshot != *probe.SpaceSnapshot || input.CaptureKind != probe.CaptureKind {
			return policyapi.ErrSampleMissing
		}
		if !current && input.State != "superseded" {
			return policyapi.ErrSampleMissing
		}
		if err := s.require(ctx, p, p.AccountID.ULID); err != nil {
			var denied *Denied
			if errors.As(err, &denied) {
				return &policyapi.SampleDenied{Action: string(denied.Action)}
			}
			return err
		}
		out = Capture{ID: input.ID, Kind: input.CaptureKind, At: input.CreatedAt}
		if !current {
			return nil
		}
		doc, err := s.deps.Sampling.ReadDocument(ctx, input)
		if err != nil {
			return err //nolint:wrapcheck // the outer failure path removes backend context
		}
		if !json.Valid(doc) {
			return kernel.ErrUnavailable
		}
		out.Input = new(string(doc))
		return nil
	})
	if err != nil {
		return Capture{}, s.evidenceFailure(ctx, p, err)
	}
	return out, nil
}

// CaptureSpaceOf resolves an owned capture for the shared route authorization.
func (s *Service) CaptureSpaceOf(ctx context.Context, account kernel.AccountID, raw string) (kernel.ULID, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	input, _, err := s.deps.Evidence.Capture(ctx, account, id, false)
	if errors.Is(err, sampling.ErrNotFound) || err == nil && input.SpaceSnapshot == nil {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	if err != nil {
		return kernel.ULID{}, fmt.Errorf("policies: capture subject: %w", err)
	}
	return *input.SpaceSnapshot, nil
}
