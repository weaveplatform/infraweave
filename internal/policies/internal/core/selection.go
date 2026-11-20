package core

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

var _ policyapi.Selection = (*Service)(nil)

// HasPolicies shares evaluation's current reach/label selector, then releases
// its holds before a caller makes optional provider reads outside the database.
func (s *Service) HasPolicies(ctx context.Context, request policyapi.SelectionRequest) (bool, error) {
	req := policyapi.EvaluationRequest{Account: request.Account, Type: request.Type, Target: request.Target}
	if err := checkEvaluation(req); err != nil {
		return false, err
	}
	found := false
	err := tx.Do(ctx, s.deps.DB, tx.Options{}, func(ctx context.Context) error {
		rows, _, err := s.selectPolicies(ctx, req)
		if err != nil {
			return err
		}
		found = len(rows) > 0
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("policies: effective presence: %w", err)
	}
	return found, nil
}
