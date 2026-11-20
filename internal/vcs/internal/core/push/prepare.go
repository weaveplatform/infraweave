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
	s          *Service
	delivery   intake.Delivery
	events     []Prepared
	normalized []vcsapi.Event
	noSources  bool
	refused    []string
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
		return event.Type != "check_action" && (event.Type != "push" && event.Type != "tag" || port.ZeroCommit(event.Head))
	})

	if len(selected) == 0 && s.d.Lifecycle == nil {
		return nil, nil //nolint:nilnil // no lifecycle or actionable work needs a plan
	}
	use, found, err := s.d.Integrations.EventUse(ctx, delivery.Account, delivery.Integration)
	if err != nil || !found {
		return nil, err //nolint:wrapcheck // the integration service names the metadata read
	}
	if use.Provider != delivery.Provider {
		return nil, fmt.Errorf("%w: delivery provider changed", kernel.ErrUnavailable)
	}
	out := &plan{s: s, delivery: delivery, normalized: slices.Clone(events)}
	if slices.ContainsFunc(selected, func(event vcsapi.Event) bool { return event.Type != "check_action" }) {
		repositories, err := s.d.Stacks.VCSRepositories(ctx, delivery.Account, use)
		if err != nil {
			return nil, err //nolint:wrapcheck // the stack boundary names the metadata read
		}
		out.noSources = len(repositories) == 0
	}
	for _, event := range selected {
		if !s.d.Now().Before(delivery.Deadline) {
			return nil, nil //nolint:nilnil // no actionable work needs no dispatch plan
		}
		if s.d.Lifecycle != nil {
			event.Repository, err = s.d.Lifecycle.EventRepository(ctx, delivery.Account, delivery.Integration, delivery.ReceivedAt, event.Repository)
			if err != nil {
				return nil, err //nolint:wrapcheck // the called boundary names the failed operation
			}
		}
		prepared := Prepared{Event: event}
		if event.Type == "check_action" {
			var reason string
			prepared, reason, err = s.prepareAction(ctx, delivery, event, use)
			if err != nil {
				return nil, err
			}
			if reason != "" {
				out.refused = append(out.refused, reason)
				continue
			}
			out.events = append(out.events, prepared)
			continue
		}
		if out.noSources {
			out.events = append(out.events, prepared)
			continue
		}
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
		} else {
			prepared.Commit, err = s.d.Sources.PrepareTag(ctx, delivery.Account, delivery.Integration, delivery.Provider, event)
			if err != nil {
				return nil, fmt.Errorf("vcs: prepare ignored tag: %w", err)
			}
		}
		out.events = append(out.events, prepared)
	}
	return out, nil
}

// Commit runs only after the parent holds its queue fence. Database errors roll
// back the prepared rows, every child and the parent's dispatch completion.
func (p *plan) Commit(ctx context.Context) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	use, found, err := p.s.d.Integrations.HeldEventUse(ctx, p.delivery.Account, p.delivery.Integration)
	if err != nil || !found {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	} //nolint:wrapcheck // integration names the metadata read
	if use.Provider != p.delivery.Provider {
		return fmt.Errorf("%w: delivery provider changed", kernel.ErrUnavailable)
	}
	if p.s.d.Lifecycle != nil {
		if err := p.s.d.Lifecycle.Apply(ctx, p.delivery.Account, p.delivery.Integration, use, p.delivery.ReceivedAt, p.normalized); err != nil {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		}
	}
	events := make([]Prepared, 0, len(p.events))
	for _, prepared := range p.events {
		if p.s.d.Lifecycle != nil {
			repo, allowed, err := p.s.d.Lifecycle.FanoutRepository(ctx, p.delivery.Account, p.delivery.Integration, p.delivery.ReceivedAt, prepared.Event.Repository)
			if err != nil {
				return err //nolint:wrapcheck // the called boundary names the failed operation
			} //nolint:wrapcheck // lifecycle names the read
			if !allowed {
				if prepared.Event.Type == "check_action" {
					p.s.actionRefused(ctx, actionSourceChanged)
				}
				continue
			}
			prepared.Event.Repository = repo
		}
		events = append(events, prepared)
	}
	for _, reason := range p.refused {
		p.s.actionRefused(ctx, reason)
	}
	if len(events) == 0 {
		return nil
	}
	admitted, targets, err := p.selectTargets(ctx, use, events)
	if err != nil {
		return err
	}
	if p.noSources {
		// A stack/default binding can appear after the repository-only preflight.
		// Retry outside the transaction rather than publish missing native facts.
		if len(targets) != 0 {
			return fmt.Errorf("%w: repository users appeared after preparation", kernel.ErrUnavailable)
		}
		return nil
	}
	if err := p.s.d.Store.PutPrepared(ctx, p.delivery.Account, p.delivery.ID, admitted); err != nil {
		return err //nolint:wrapcheck // the store names the prepared write
	}
	ids := make([]kernel.ULID, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b kernel.ULID) int { return bytes.Compare(a[:], b[:]) })
	for _, stack := range ids {
		payload := Evaluation{Account: p.delivery.Account, Delivery: p.delivery.ID, Stack: stack, Deadline: p.delivery.Deadline, Replays: p.delivery.Replays}
		if _, err := p.s.d.Jobs.EnqueueTx(
			ctx,
			Evaluate,
			payload,
			queue.Account(
				p.delivery.Account.ULID,
			),
			queue.UniqueKey(intake.EvaluationKey(p.delivery.ID, p.delivery.Replays, stack)),
			queue.OrderingKey("stack:"+stack.String()),
		); err != nil {
			return err //nolint:wrapcheck // the queue names the bounded child write
		}
	}
	return nil
}

// selectTargets keeps explicit action targets separate from repository fanout.
//
//nolint:wrapcheck // the VCS/store ports already name the failed boundary
func (p *plan) selectTargets(ctx context.Context, use stacksapi.Use, events []Prepared) ([]Prepared, map[kernel.ULID]bool, error) {
	targets := map[kernel.ULID]bool{}
	repositories := map[vcsapi.Repository][]stacksapi.Stack{}
	admitted := make([]Prepared, 0, len(events))
	for _, prepared := range events {
		if prepared.Event.Type == "check_action" {
			stack, reason, err := p.s.actionTarget(ctx, p.delivery, prepared.Event, use)
			if err != nil {
				return nil, nil, err
			}
			if prepared.Action == nil || p.s.d.Actions == nil {
				reason = actionSourceChanged
			}
			if reason == "" {
				current, err := p.s.d.Actions.CurrentCheckAction(ctx, p.delivery.Account, p.delivery.Integration, p.delivery.Provider, prepared.Event.Repository, *prepared.Action)
				if err != nil {
					return nil, nil, err
				}
				if !current {
					reason = actionSourceChanged
				}
			}
			if reason != "" {
				p.s.actionRefused(ctx, reason)
				continue
			}
			targets[stack.ID] = true
			admitted = append(admitted, prepared)
			continue
		}
		stacks, seen := repositories[prepared.Event.Repository]
		if !seen {
			var err error
			stacks, err = p.s.d.Stacks.ByVCSRepository(ctx, p.delivery.Account, use, prepared.Event.Repository.Namespace, prepared.Event.Repository.Name)
			if err != nil {
				return nil, nil, err //nolint:wrapcheck // the called boundary names the failed operation
			}
			repositories[prepared.Event.Repository] = stacks
		}
		var affectedCount int64
		for _, stack := range stacks {
			targets[stack.ID] = true
			settings, err := p.s.d.Stacks.Settings(ctx, p.delivery.Account, stack.ID)
			if err != nil {
				return nil, nil, err
			}
			if affected(settings, prepared.Files) {
				affectedCount++
			}
		}
		prepared.AffectedStacksCount = &affectedCount
		admitted = append(admitted, prepared)
	}
	return admitted, targets, nil
}
