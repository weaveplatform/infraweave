package sampling

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

type reference struct {
	ID kernel.ULID `json:"id"`
}

// Record joins the evaluation transaction. A quota refusal still records safe
// metadata; staging or database failures roll back the caller, never authorize it.
func (s *Service) Record(ctx context.Context, req policyapi.EvaluationRequest, space kernel.ULID, evaluations []Evaluation) (map[kernel.ULID]kernel.ULID, error) {
	if !tx.In(ctx) {
		return nil, queue.ErrNoTransaction
	}
	linked := make(map[kernel.ULID]kernel.ULID)
	for _, evaluation := range evaluations {
		var input *Input
		if evaluation.Receipt.Sample {
			allowed, err := s.d.Store.Reserve(ctx, req.Account, evaluation.Receipt.Type)
			if err != nil {
				return nil, fmt.Errorf("policies: stage evaluation evidence: %w", err)
			}
			if allowed {
				body, err := json.Marshal(
					Payload{
						Input:          evaluation.Input,
						Body:           evaluation.Body,
						Engine:         evaluation.Receipt.Engine,
						Result:         evaluation.Receipt.Decision,
						Outcome:        evaluation.Receipt.Outcome,
						Error:          evaluation.Receipt.Failure,
						Print:          evaluation.Print,
						PrintTruncated: evaluation.PrintTruncated,
					},
				)
				if err != nil {
					return nil, fmt.Errorf("policies: encode sealed sample: %w", err)
				}
				receipt := evaluation.Receipt
				input = &Input{
					Account:   req.Account,
					ID:        receipt.ID,
					Key:       req.Account.String() + "/" + receipt.Policy.String() + "/" + receipt.ID.String(),
					State:     "pending",
					CreatedAt: receipt.CreatedAt,
					ExpiresAt: receipt.CreatedAt.Add(Retention),
				}
				if err := s.d.Store.CreateInput(ctx, *input, body); err != nil {
					return nil, fmt.Errorf("policies: stage evaluation evidence: %w", err)
				}
				job, err := s.d.Queue.EnqueueTx(ctx, UploadKind, reference{ID: input.ID}, queue.Account(req.Account.ULID), queue.UniqueKey(input.ID.String()))
				if err != nil {
					return nil, fmt.Errorf("policies: enqueue sample upload: %w", err)
				}
				if err := s.d.Store.AttachUploadJob(ctx, req.Account, input.ID, job); err != nil {
					return nil, fmt.Errorf("policies: bind sample upload job: %w", err)
				}
				linked[receipt.ID] = input.ID
			}
		}
		facts, err := inputs.Facts(string(req.Type), evaluation.Input)
		if err != nil {
			return nil, fmt.Errorf("policies: snapshot evaluation metadata: %w", err)
		}
		if err := s.d.Store.RecordEvaluation(ctx, req, space, evaluation.Receipt, input, facts); err != nil {
			return nil, fmt.Errorf("policies: stage evaluation evidence: %w", err)
		}
	}
	if len(evaluations) > 0 {
		if err := s.capture(ctx, req, space, evaluations[0].Input); err != nil {
			return nil, err
		}
	}
	return linked, nil
}
