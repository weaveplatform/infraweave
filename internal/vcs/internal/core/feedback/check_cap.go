package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// Capped joins the completing evaluator's transaction. It enqueues feedback
// without creating a run or calling a provider inside the policy decision.
func (s *Service) Capped(ctx context.Context, account kernel.AccountID, integration, stack kernel.ULID, sha string, kind kernel.RunType, limit int) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	if s.checks == nil {
		return ErrCheckReport
	}
	now := s.checks.Now().UTC().Truncate(time.Microsecond)
	b, err := s.selectCheck(ctx, account, runsapi.CheckRun{Stack: stack, Type: kind, CommitSHA: sha, CreatedAt: now})
	if errors.Is(err, ErrNoCheck) {
		return nil
	}
	if err != nil {
		return err
	}
	if b.Integration != integration || limit <= 0 {
		return ErrCheckReport
	}
	b.Cap = true
	b.Limit = limit
	b.CreatedAt = now
	data, _ := json.Marshal([]any{integration, stack, sha, kind})
	sum := sha256.Sum256(data)
	b.Key = "cap:" + hex.EncodeToString(sum[:])
	fact := runsapi.CheckRun{State: kernel.RunStateFailed}
	for _, request := range checkRequests(b, fact, now.UnixNano()) {
		body, err := json.Marshal(request)
		if err != nil {
			return fmt.Errorf("VCS cap check encoding: %w", err)
		}
		if err := s.enqueueCheck(ctx, vcsapi.FeedbackTarget{Account: account.ULID, Job: ReportCheck.Name, Payload: body}); err != nil {
			return err
		}
	}
	return nil
}
