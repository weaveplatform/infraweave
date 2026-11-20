package vcs

import (
	"context"
	"fmt"
	"net/http"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func (s *eventSteps) policyRequest(ctx context.Context, name, replica string) error {
	if err := s.comments.sendRequest(ctx, 12, "open", "seed", replica); err != nil {
		return err
	}
	s.deliveries[name] = s.comments.lastDelivery
	return nil
}

func (s *eventSteps) policyComment(ctx context.Context, name, _ string) error {
	id := kernel.NewULID().UUID()
	s.deliveries[name] = id
	s.comments.w.IDs["proposed"] = s.comments.w.IDs["seed"]
	status, err := s.sendComment(ctx, id, "plan this commit")
	if err != nil {
		return err
	}
	if status != http.StatusAccepted {
		return fmt.Errorf("%w: policy comment answered %d", harness.ErrStep, status)
	}
	return nil
}
