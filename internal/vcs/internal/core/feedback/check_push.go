package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// ReportPush commits a no-run check intent without a provider call. Its binding
// follows the same native ownership, rename, recovery and account fences as runs.
func (s *Service) ReportPush(ctx context.Context, input port.PushFeedback) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	if s.checks == nil || input.Account.IsZero() || input.Stack.IsZero() || input.Delivery.IsZero() || input.Integration.IsZero() || !port.ValidCommit(input.Commit) ||
		(input.Type != kernel.RunTypeTracked && input.Type != kernel.RunTypeProposed) {
		return ErrCheckReport
	}
	now := s.checks.Now().UTC().Truncate(time.Microsecond)
	b, err := s.selectCheck(ctx, input.Account, runsapi.CheckRun{Stack: input.Stack, Type: input.Type, CommitSHA: input.Commit, CreatedAt: now})
	if errors.Is(err, ErrNoCheck) {
		return nil
	}
	if err != nil {
		return err
	}
	if b.Integration != input.Integration {
		return ErrCheckReport
	}
	b.Push = &PushCheck{Failed: input.Failed, Message: port.CheckText(strings.Join(input.Messages, "\n"), 8192), Policies: port.CheckText(strings.Join(input.Policies, ", "), 2048)}
	b.CreatedAt = now
	data, _ := json.Marshal([]any{input.Delivery, input.Stack, input.Commit, input.Type})
	sum := sha256.Sum256(data)
	b.Key = "push:" + hex.EncodeToString(sum[:])
	state := kernel.RunStateFinished
	if input.Failed {
		state = kernel.RunStateFailed
	}
	for _, request := range checkRequests(b, runsapi.CheckRun{State: state}, now.UnixNano()) {
		body, err := json.Marshal(request)
		if err != nil {
			return fmt.Errorf("VCS push check encoding: %w", err)
		}
		if err := s.enqueueCheck(ctx, vcsapi.FeedbackTarget{Account: input.Account.ULID, Job: ReportCheck.Name, Payload: body}); err != nil {
			return err
		}
	}
	return nil
}

func pushCheckContent(p *PushCheck) (port.CheckState, string, string) {
	state, title := port.CheckSkipped, "Push ignored"
	if p.Failed {
		state, title = port.CheckFailure, "Push policy failed"
	}
	summary := p.Message
	if p.Policies != "" {
		summary = "Policies: " + p.Policies + ".\n" + summary
	}
	if summary == "" {
		summary = title + "."
	}
	return state, title, summary
}
