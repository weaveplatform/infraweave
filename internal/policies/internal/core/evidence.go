package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

// EvaluationRecord is metadata only. Key is an evaluation ID, never a blob path.
type EvaluationRecord struct {
	ID      kernel.ULID
	Outcome string
	At      time.Time
}

// EvidenceStore keeps metadata queries separate from the sealed object reader.
type EvidenceStore interface {
	OwnsEvaluation(context.Context, kernel.AccountID, kernel.ULID, kernel.ULID) (bool, error)
	SearchEvaluations(context.Context, kernel.AccountID, Policy, search.Input) (search.Result[EvaluationRecord], error)
	SuggestEvaluations(context.Context, kernel.AccountID, Policy, search.SuggestionsInput) (search.Suggestions, error)
	Capture(context.Context, kernel.AccountID, kernel.ULID, bool) (sampling.Input, bool, error)
	ListCaptures(context.Context, kernel.AccountID, kernel.ULID, string, search.Input) (search.Result[Capture], error)
}

func (s *Service) readablePolicy(ctx context.Context, p kernel.Principal, id kernel.ULID, lock bool) (Policy, error) {
	row, err := s.deps.Store.Get(ctx, p.AccountID, id, lock)
	if errors.Is(err, ErrNotFound) {
		return Policy{}, policyapi.ErrSampleMissing
	}
	if err != nil {
		return Policy{}, fmt.Errorf("policies: resolve evidence owner: %w", err)
	}
	ok, err := s.deps.Authz.Authorize(ctx, p, accessapi.ActionSpaceRead, row.Space)
	if err != nil {
		return Policy{}, fmt.Errorf("policies: authorize evidence metadata: %w", err)
	}
	if !ok {
		return Policy{}, &policyapi.SampleDenied{Action: string(accessapi.ActionSpaceRead)}
	}
	return row, nil
}

// ReadSample holds the current policy against deletion or moves through the
// bounded object read. NoRetry is outermost: an external read is never replayed.
func (s *Service) ReadSample(ctx context.Context, p kernel.Principal, policy, evaluation kernel.ULID) (policyapi.Sample, error) {
	nested := tx.In(ctx)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out policyapi.Sample
	err := tx.Do(ctx, s.deps.DB, tx.Options{NoRetry: true}, func(ctx context.Context) error {
		if s.deps.Evidence == nil || s.deps.Sampling == nil {
			return kernel.ErrUnavailable
		}
		if _, err := s.readablePolicy(ctx, p, policy, true); err != nil {
			return err
		}
		owned, err := s.deps.Evidence.OwnsEvaluation(ctx, p.AccountID, policy, evaluation)
		if err != nil {
			return fmt.Errorf("policies: resolve evaluation owner: %w", err)
		}
		if !owned {
			return policyapi.ErrSampleMissing
		}
		if err := s.require(ctx, p, p.AccountID.ULID); err != nil {
			var denied *Denied
			if errors.As(err, &denied) {
				return &policyapi.SampleDenied{Action: string(denied.Action)}
			}
			return err
		}
		doc, err := s.deps.Sampling.Read(ctx, p.AccountID, evaluation)
		if err != nil {
			return err //nolint:wrapcheck // classified without private context below
		}
		out = policyapi.Sample{
			Body:           doc.Body,
			Engine:         doc.Engine,
			Print:          doc.Print,
			Input:          doc.Input,
			Result:         doc.Result,
			Outcome:        doc.Outcome,
			Error:          doc.Error,
			PrintTruncated: doc.PrintTruncated,
		}
		return nil
	})
	if err != nil {
		if nested {
			return policyapi.Sample{}, err //nolint:wrapcheck // caller audits after its outer owner fence rolls back
		}
		return policyapi.Sample{}, s.evidenceFailure(ctx, p, err)
	}
	return out, nil
}

// ReadFailure lets a receipt reader audit after releasing its own outer fence.
func (s *Service) ReadFailure(ctx context.Context, p kernel.Principal, err error) error {
	if tx.In(ctx) {
		return kernel.ErrUnavailable
	}
	return s.evidenceFailure(ctx, p, err)
}

// evidenceFailure commits only a fixed classification after the failed read has
// rolled back. Raw input, arbitrary handles, object keys and backend errors are
// never copied to the trail or the public error. Cancellation cannot erase it.
func (s *Service) evidenceFailure(ctx context.Context, p kernel.Principal, readErr error) error {
	classification := "unavailable"
	var denied *policyapi.SampleDenied
	switch {
	case errors.As(readErr, &denied):
		classification = "denied"
	case errors.Is(readErr, policyapi.ErrSampleMissing), errors.Is(readErr, sampling.ErrNotFound):
		classification = "missing"
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	err := tx.Do(auditCtx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		_, err := audit.Record(ctx, kernel.AuditEntry{
			AccountID: p.AccountID.ULID,
			Actor:     kernel.AuditActor{Username: p.Caller()},
			Action:    "policy_evidence.read", EventType: "FAILURE",
			AffectedResource: kernel.AuditResource{Type: "ACCOUNT", ID: p.AccountID.String()},
			Body:             []byte(`{"reason":"` + classification + `"}`),
		})
		return err //nolint:wrapcheck // sanitized below
	})
	if err != nil {
		return kernel.ErrUnavailable
	}
	if classification == "missing" {
		return policyapi.ErrSampleMissing
	}
	if classification == "denied" {
		return readErr
	}
	return kernel.ErrUnavailable
}

// SearchEvaluations filters safe snapshots using the current policy's space.
// Historical evaluation spaces confer no access after a policy is moved.
func (s *Service) SearchEvaluations(ctx context.Context, p kernel.Principal, id string, in search.Input) (search.Result[EvaluationRecord], error) {
	policy, err := kernel.ParseULID(id)
	if err != nil {
		return search.Result[EvaluationRecord]{}, policyapi.ErrSampleMissing
	}
	var out search.Result[EvaluationRecord]
	err = tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		row, err := s.readablePolicy(ctx, p, policy, true)
		if err != nil {
			return err
		}
		out, err = s.deps.Evidence.SearchEvaluations(ctx, p.AccountID, row, in)
		return err //nolint:wrapcheck // the store names the query
	})
	if err != nil {
		return out, fmt.Errorf("policies: search evaluation metadata: %w", err)
	}
	return out, nil
}

// SuggestEvaluations uses exactly the search's owned, readable metadata scope.
func (s *Service) SuggestEvaluations(ctx context.Context, p kernel.Principal, id string, in search.SuggestionsInput) (search.Suggestions, error) {
	policy, err := kernel.ParseULID(id)
	if err != nil {
		return search.Suggestions{}, policyapi.ErrSampleMissing
	}
	var out search.Suggestions
	err = tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		row, err := s.readablePolicy(ctx, p, policy, true)
		if err != nil {
			return err
		}
		out, err = s.deps.Evidence.SuggestEvaluations(ctx, p.AccountID, row, in)
		return err //nolint:wrapcheck // the store names the query
	})
	if err != nil {
		return out, fmt.Errorf("policies: suggest evaluation metadata: %w", err)
	}
	return out, nil
}
