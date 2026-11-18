package push

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ intake.Dispatcher = (*Service)(nil)

type plan struct {
	s        *Service
	delivery intake.Delivery
	events   []Prepared
	stacks   []kernel.ULID
}

// Prepare resolves each diff once, then groups every ref into one child per
// stack. A deleted branch has no lookup, provider call or child job.
func (s *Service) Prepare(ctx context.Context, delivery intake.Delivery, events []vcsapi.Event) (intake.DispatchPlan, error) {
	if !s.d.Now().Before(delivery.Deadline) {
		return nil, nil //nolint:nilnil // no actionable work needs no dispatch plan
	}
	ctx, cancel := context.WithTimeout(ctx, max(0, delivery.Deadline.Sub(s.d.Now())))
	defer cancel()
	selected := slices.DeleteFunc(slices.Clone(events), func(event vcsapi.Event) bool {
		return event.Type != "push" && event.Type != "tag" || event.Type == "push" && port.ZeroCommit(event.Head)
	})
	if len(selected) == 0 {
		return nil, nil //nolint:nilnil // no actionable work needs no dispatch plan
	}
	use, found, err := s.d.Integrations.EventUse(ctx, delivery.Account, delivery.Integration)
	if err != nil || !found {
		return nil, err //nolint:wrapcheck // the integration service names the metadata read
	}
	if use.Provider != delivery.Provider {
		return nil, fmt.Errorf("%w: delivery provider changed", kernel.ErrUnavailable)
	}
	out := &plan{s: s, delivery: delivery}
	targets := map[kernel.ULID]bool{}
	repositories := map[vcsapi.Repository][]stacksapi.Stack{}
	for _, event := range selected {
		if !s.d.Now().Before(delivery.Deadline) {
			return nil, nil //nolint:nilnil // no actionable work needs no dispatch plan
		}
		stacks, seen := repositories[event.Repository]
		if !seen {
			stacks, err = s.d.Stacks.ByVCSRepository(ctx, delivery.Account, use, event.Repository.Namespace, event.Repository.Name)
			if err != nil {
				return nil, err //nolint:wrapcheck // stacks names the account-scoped lookup
			}
			repositories[event.Repository] = stacks
		}
		if len(stacks) == 0 {
			continue
		}
		prepared := Prepared{Event: event}
		if event.Type == "push" {
			prepared.Commit, prepared.Files, err = s.d.Sources.PreparePush(ctx, delivery.Account, delivery.Integration, delivery.Provider, event)
			if err != nil {
				return nil, err //nolint:wrapcheck // intake reduces provider diagnostics to safe errors
			}
			if !strings.EqualFold(prepared.Commit.Hash, event.Head) {
				return nil, fmt.Errorf("%w: provider resolved another push commit", kernel.ErrUnavailable)
			}
			prepared.Commit.Branch = event.Branch
			prepared.Files = slices.Clone(prepared.Files)
		}
		out.events = append(out.events, prepared)
		for _, stack := range stacks {
			targets[stack.ID] = true
		}
	}
	for id := range targets {
		out.stacks = append(out.stacks, id)
	}
	slices.SortFunc(out.stacks, func(a, b kernel.ULID) int { return bytes.Compare(a[:], b[:]) })
	return out, nil
}

// Commit runs only after the parent holds its queue fence. Database errors roll
// back the prepared rows, every child and the parent's dispatch completion.
func (p *plan) Commit(ctx context.Context) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	if len(p.events) == 0 {
		return nil
	}
	if err := p.s.d.Store.PutPrepared(ctx, p.delivery.Account, p.delivery.ID, p.events); err != nil {
		return err //nolint:wrapcheck // the store names the prepared write
	}
	for _, stack := range p.stacks {
		payload := Evaluation{Account: p.delivery.Account, Delivery: p.delivery.ID, Stack: stack, Deadline: p.delivery.Deadline, Replays: p.delivery.Replays}
		if _, err := p.s.d.Jobs.EnqueueTx(ctx, Evaluate, payload,
			queue.Account(p.delivery.Account.ULID), queue.UniqueKey(intake.EvaluationKey(p.delivery.ID, p.delivery.Replays, stack)), queue.OrderingKey("stack:"+stack.String())); err != nil {
			return err //nolint:wrapcheck // the queue names the bounded child write
		}
	}
	return nil
}
