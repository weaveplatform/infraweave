package sampling

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func captureKind(kind policyapi.Type) string {
	switch kind {
	case policyapi.Approval, policyapi.Login, policyapi.Notification, policyapi.Plan, policyapi.Push, policyapi.Task:
		return string(kind)
	case policyapi.Trigger:
		return "TRIGGER_RUN_FINISH"
	default:
		return ""
	}
}

// capture uses the valid document actually evaluated, independently of sample
// requests and quota. Unsupported capture kinds and unavailable input stay absent.
func (s *Service) capture(ctx context.Context, req policyapi.EvaluationRequest, space kernel.ULID, raw json.RawMessage) error {
	kind := captureKind(req.Type)
	if kind == "" || len(raw) == 0 {
		return nil
	}
	slot, allowed, err := s.d.Store.CaptureSlot(ctx, req.Account, space, kind)
	if err != nil {
		return fmt.Errorf("policies: reserve input capture: %w", err)
	}
	if !allowed {
		return nil
	}
	document, err := inputs.Capture(string(req.Type), raw)
	if err != nil {
		return fmt.Errorf("policies: sanitize input capture: %w", err)
	}
	if slot.Pending != nil {
		previous, err := s.d.Store.Input(ctx, req.Account, *slot.Pending, true)
		if err != nil {
			return fmt.Errorf("policies: read superseded pending capture: %w", err)
		}
		if err := s.retireInput(ctx, previous, "superseded"); err != nil {
			return err
		}
	}
	input := Input{Account: req.Account, ID: kernel.NewULID(), SpaceSnapshot: &space, CaptureKind: kind, State: "pending", CreatedAt: slot.At, ExpiresAt: slot.At.Add(Retention)}
	input.Key = req.Account.String() + "/captures/" + space.String() + "/" + kind + "/" + input.ID.String()
	if err := s.d.Store.CreateInput(ctx, input, document); err != nil {
		return fmt.Errorf("policies: stage input capture: %w", err)
	}
	if err := s.d.Store.SetCapturePending(ctx, input); err != nil {
		return fmt.Errorf("policies: stage input capture head: %w", err)
	}
	job, err := s.d.Queue.EnqueueTx(ctx, UploadKind, reference{ID: input.ID}, queue.Account(req.Account.ULID), queue.UniqueKey(input.ID.String()))
	if err != nil {
		return fmt.Errorf("policies: enqueue capture upload: %w", err)
	}
	if err := s.d.Store.AttachUploadJob(ctx, req.Account, input.ID, job); err != nil {
		return fmt.Errorf("policies: bind capture upload job: %w", err)
	}
	return nil
}
