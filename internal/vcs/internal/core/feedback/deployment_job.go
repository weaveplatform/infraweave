package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// ErrDeploymentReport contains no provider response body or credential.
var ErrDeploymentReport = errors.New("VCS deployment report failed")

// Reported reports one state under the owner, claim-generation and resource
// fences. It owns its outer transaction: an inherited transaction could retry
// an external effect despite NoRetry. Later queue attempts recover native IDs.
func (s *Service) Reported(ctx context.Context, job queue.Job) error {
	if tx.In(ctx) {
		return fmt.Errorf("%w: inherited transaction", ErrDeploymentReport)
	}
	var request DeploymentRequest
	if s.deployments == nil || json.Unmarshal(job.Payload, &request) != nil || !validDeployment(request) ||
		job.Account != request.Deployment.Account.ULID {
		return queue.Permanent(ErrDeploymentReport) //nolint:wrapcheck // queue control classification
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := tx.Do(ctx, s.deployments.DB, tx.Options{NoRetry: true}, func(ctx context.Context) error {
		if s.hold == nil {
			return ErrAccountHold
		}
		if err := s.hold(ctx, request.Deployment.Account); err != nil {
			return fmt.Errorf("VCS deployment owner hold: %w", err)
		}
		if err := s.jobs.HoldTx(ctx, job); err != nil {
			return err //nolint:wrapcheck // the queue names its fence
		}
		d, err := s.deployments.Store.Deployment(ctx, request.Deployment.Account, request.Deployment.Run.ID)
		if errors.Is(err, ErrNoDeployment) {
			return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // absent metadata has no effect
		}
		if err != nil {
			return err //nolint:wrapcheck // the store names its read
		}
		resource := resourceKey(d)
		cursor, err := s.deployments.Store.HoldDeployment(ctx, d.Account, resource)
		if err != nil {
			return err //nolint:wrapcheck // the store names its fence
		}
		if cursor.Run != d.Run.ID || cursor.Sequence >= request.Sequence {
			return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // stale state is settled atomically
		}
		if err := s.reportDeployment(ctx, d, request.State); err != nil {
			return err
		}
		if err := s.deployments.Store.ReportedDeployment(ctx, d.Account, resource, request.Sequence); err != nil {
			return err //nolint:wrapcheck // the store names its write
		}
		return s.jobs.CompleteTx(ctx, job) //nolint:wrapcheck // cursor and completion commit together
	})
	if err != nil {
		return err //nolint:wrapcheck // each boundary names its failure
	}
	return nil
}

func (s *Service) reportDeployment(ctx context.Context, d Deployment, state kernel.RunState) error {
	provider := s.deployments.Providers[d.Provider]
	if provider == nil {
		return queue.Permanent(ErrDeploymentReport) //nolint:wrapcheck // queue control classification
	}
	in, ok, err := s.deployments.Integrations.Use(ctx, d.Account, d.Provider, &d.Integration)
	if err != nil {
		return ErrDeploymentReport
	}
	if !ok {
		// A deleted binding cannot silently fall back to a replacement default.
		return nil
	}
	if host, ok := provider.(port.DeploymentHost); ok {
		current, err := host.DeploymentHost(in.Settings)
		if err != nil || current != d.NativeHost {
			// Credential rotation can proceed, but retargeting an integration
			// cannot move a recorded report outside its native resource fence.
			return queue.Permanent(ErrDeploymentReport) //nolint:wrapcheck // queue control classification
		}
	}
	next, _ := deploymentState(state)
	err = provider.Report(ctx, port.Repository{Namespace: d.Namespace, Name: d.Repository, Integration: &in}, d.Stack, d.Run, next)
	if limit, ok := errors.AsType[*port.RateLimited](err); ok {
		return queue.Snooze(max(time.Until(limit.Reset), time.Second)) //nolint:wrapcheck // queue control classification
	}
	if err != nil {
		// SDK errors can carry provider response bodies; queue diagnostics cannot.
		return ErrDeploymentReport
	}
	return nil
}
