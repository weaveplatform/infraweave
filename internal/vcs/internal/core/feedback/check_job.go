package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Checked owns a non-retrying transaction around its bounded provider effect.
// Account, binding, job-generation and resource holds remain live until native
// metadata and queue acknowledgement commit together. Later attempts recover
// the native identity when a post succeeded but the local transaction did not.
func (s *Service) Checked(ctx context.Context, job queue.Job) error {
	if tx.In(ctx) {
		return fmt.Errorf("%w: inherited transaction", ErrCheckReport)
	}
	var request CheckRequest
	if s.checks == nil || json.Unmarshal(job.Payload, &request) != nil || !validCheckRequest(request) || job.Account != request.Binding.Account.ULID {
		return queue.Permanent(ErrCheckReport) //nolint:wrapcheck // queue control classification
	}
	work, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := tx.Do(work, s.checks.DB, tx.Options{NoRetry: true}, func(ctx context.Context) error {
		if s.hold == nil {
			return ErrAccountHold
		}
		if err := s.hold(ctx, request.Binding.Account); err != nil {
			return fmt.Errorf("VCS check owner hold: %w", err)
		}
		if err := s.jobs.HoldTx(ctx, job); err != nil {
			return err //nolint:wrapcheck // boundary names its failure
		}
		binding, err := s.checks.Store.CheckBinding(ctx, request.Binding.Account, request.Binding.Key)
		if errors.Is(err, ErrNoCheck) {
			return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // queue acknowledgement joins the fenced transaction
		}
		if err != nil {
			return err //nolint:wrapcheck // boundary names its failure
		}
		resource, err := s.checks.Store.HoldCheck(
			ctx,
			CheckResource{Account: binding.Account, Key: checkResourceKey(binding, request.Name, request.Aggregate), Binding: binding.Key},
		)
		if err != nil {
			return err //nolint:wrapcheck // boundary names its failure
		}
		return s.checkHeld(ctx, job, request, binding, resource)
	})
	if limited, ok := errors.AsType[*port.RateLimited](err); ok {
		return queue.Snooze(max(limited.Reset.Sub(s.checks.Now()), time.Second)) //nolint:wrapcheck // queue control classification
	}
	if err != nil && job.MaxAttempts > 0 && job.Attempt >= job.MaxAttempts && !errors.Is(err, queue.ErrLeaseLost) && !errors.Is(err, context.Canceled) {
		s.checks.Log.ErrorContext(ctx, "VCS commit check exhausted attempts", "run", request.Binding.Run.String(), "check", request.Name)
	}
	return err //nolint:wrapcheck // transaction boundaries name their failure
}

func (s *Service) checkHeld(ctx context.Context, job queue.Job, request CheckRequest, b CheckBinding, resource CheckResource) error {
	individual := !request.Aggregate
	if individual {
		owner := b.Run
		if b.Cap {
			owner = b.Stack
		}
		if resource.Owner != owner || !resource.Epoch.Equal(b.CreatedAt) || resource.Aggregate {
			return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // queue acknowledgement joins the fenced transaction
		}
	}
	report, owner, sequence, found, err := s.checkReport(ctx, b, request)
	if err != nil {
		return err //nolint:wrapcheck // boundary names its failure
	}
	if !found || individual && resource.Sequence >= sequence {
		return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // queue acknowledgement joins the fenced transaction
	}
	// Within one aggregate cohort, every refresh recomputes the newest owner
	// from current facts, so older run transitions still contribute. A different
	// integration sharing the native context cannot move that owner backwards.
	if request.Aggregate && newerCheckOwner(checkOwner{ID: resource.Owner, CreatedAt: resource.Epoch}, owner) {
		return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // older native cohort is settled atomically
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("VCS check report encoding: %w", err)
	}
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	// Azure must also resolve a PR's latest iteration, even when the commit
	// context itself has not changed. Other adapters can omit a duplicate post.
	if resource.Digest != digest || b.Provider == "AZURE_DEVOPS" {
		report.NativeID = resource.NativeID
		id, err := s.postCheck(ctx, b, report)
		if err != nil {
			return err //nolint:wrapcheck // boundary names its failure
		}
		resource.NativeID = id
	}
	resource.Binding = b.Key
	resource.Digest = digest
	resource.Owner = owner.ID
	resource.Epoch = owner.CreatedAt
	resource.Sequence = sequence
	resource.Aggregate = request.Aggregate
	if err := s.checks.Store.ReportedCheck(ctx, resource); err != nil {
		return err //nolint:wrapcheck // boundary names its failure
	}
	return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // queue acknowledgement joins the fenced transaction
}

func (s *Service) postCheck(ctx context.Context, b CheckBinding, report port.CheckReport) (string, error) {
	provider := s.checks.Providers[b.Provider]
	if provider == nil {
		return "", queue.Permanent(ErrCheckReport) //nolint:wrapcheck // queue control classification
	}
	in, found, err := s.checks.Integrations.Use(ctx, b.Account, b.Provider, &b.Integration)
	if err != nil {
		return "", ErrCheckReport
	}
	if !found {
		return "", nil
	}
	host, err := provider.CheckHost(in.Settings)
	if err != nil || host != b.NativeHost {
		return "", queue.Permanent(ErrCheckReport) //nolint:wrapcheck // queue control classification
	}
	if b.Provider == "AZURE_DEVOPS" {
		report.PullRequests, err = s.checks.Store.CheckPullRequests(ctx, b.Account, b.Integration, b.Namespace, b.Repository, b.Commit)
		if err != nil {
			return "", fmt.Errorf("VCS check pull requests: %w", err)
		}
	}
	id, err := provider.Set(ctx, port.Repository{Namespace: b.Namespace, Name: b.Repository, Integration: &in}, b.Commit, report)
	if limited, ok := errors.AsType[*port.RateLimited](err); ok {
		return "", limited
	}
	if err != nil {
		return "", ErrCheckReport
	}
	return id, nil
}
