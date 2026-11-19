package push

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

const (
	actionInvalidTarget = "invalid_target"
	actionUnsupported   = "unsupported_action"
	actionInvalidSender = "invalid_sender"
	actionSourceChanged = "source_changed"
	actionDisabled      = "disabled"
	actionReadOnly      = "insufficient_permission"
	actionStackRefused  = "stack_refused"
)

//nolint:wrapcheck // domain/queue ports retain their named boundary and control errors
func (s *Service) actionTarget(ctx context.Context, d intake.Delivery, event vcsapi.Event, use stacksapi.Use) (stacksapi.Settings, string, error) {
	empty := stacksapi.Settings{}
	if d.Provider != "GITHUB_ENTERPRISE" || event.CheckAction == nil {
		return empty, actionInvalidTarget, nil
	}
	id, err := kernel.ParseULID(event.CheckAction.ExternalID)
	if err != nil || id.IsZero() {
		return empty, actionInvalidTarget, nil
	}
	stack, err := s.d.Stacks.Settings(ctx, d.Account, id)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return empty, actionInvalidTarget, nil
	}
	if err != nil {
		return empty, "", err
	}
	if stack.Account != d.Account || stack.ID != id || !uses(stack, use) ||
		(stack.Provider != "GITHUB_ENTERPRISE" && stack.Provider != "GITHUB") ||
		!strings.EqualFold(stack.Namespace, event.Repository.Namespace) || !strings.EqualFold(stack.Repository, event.Repository.Name) {
		return empty, actionInvalidTarget, nil
	}
	return stack, "", nil
}

//nolint:wrapcheck // domain/queue ports retain their named boundary and control errors
func (s *Service) prepareAction(ctx context.Context, d intake.Delivery, event vcsapi.Event, use stacksapi.Use) (Prepared, string, error) {
	out := Prepared{Event: event}
	if event.CheckAction == nil || (event.CheckAction.Action != "preview" && event.CheckAction.Action != "deploy") {
		return out, actionUnsupported, nil
	}
	if event.Author == "" || strings.ContainsAny(event.Author, "/\\?#%\r\n \t") {
		return out, actionInvalidSender, nil
	}
	if !port.ValidCommit(event.Head) || event.Repository.ID == "" {
		return out, actionInvalidTarget, nil
	}
	stack, reason, err := s.actionTarget(ctx, d, event, use)
	if reason != "" || err != nil {
		return out, reason, err
	}
	if !stack.GithubActionDeploy {
		return out, actionDisabled, nil
	}
	if s.d.Actions == nil {
		return out, "", fmt.Errorf("%w: check action source unavailable", kernel.ErrUnavailable)
	}
	facts, err := s.d.Actions.PrepareCheckAction(ctx, d.Account, d.Integration, d.Provider, event)
	var invalid *kernel.Invalid
	if errors.Is(err, port.ErrCheckActionTarget) || errors.As(err, &invalid) {
		return out, actionSourceChanged, nil
	}
	if err != nil {
		return out, "", err
	}
	if !strings.EqualFold(facts.Commit.Hash, event.Head) || facts.NativeHost == "" {
		return out, actionSourceChanged, nil
	}
	out.Commit = stacksapi.Commit{
		Hash:        facts.Commit.Hash,
		Tag:         facts.Commit.Tag,
		URL:         facts.Commit.URL,
		Message:     facts.Commit.Message,
		AuthorName:  facts.Commit.AuthorName,
		AuthorLogin: facts.Commit.AuthorLogin,
		Timestamp:   facts.Commit.Timestamp,
	}
	out.Action = &facts
	return out, "", nil
}

func actionFor(event vcsapi.Event, stack kernel.ULID) bool {
	if event.Type != "check_action" || event.CheckAction == nil {
		return false
	}
	id, err := kernel.ParseULID(event.CheckAction.ExternalID)
	return err == nil && id == stack
}

//nolint:wrapcheck // domain/queue ports retain their named boundary and control errors
func (s *Service) applyAction(ctx context.Context, input Evaluation, stack stacksapi.Settings, event Prepared, integration kernel.ULID, attempt actionAttempt) (Decision, error) {
	if s.d.Effects == nil {
		return Decision{}, kernel.ErrUnavailable
	}
	done, err := s.d.Effects.HasCheckAction(ctx, input.Account, input.Delivery, input.Stack)
	if err != nil {
		return Decision{}, err
	}
	if done {
		return Decision{Outcome: Duplicate}, nil
	}
	if attempt.completed {
		return Decision{}, errChanged
	}
	refuse := func(reason string) (Decision, error) {
		s.actionRefused(ctx, reason)
		return Decision{Refused, reason}, nil
	}
	if !stack.GithubActionDeploy {
		return refuse(actionDisabled)
	}
	if attempt.reason == actionDisabled {
		return Decision{}, errChanged
	}
	if attempt.reason != "" {
		return refuse(attempt.reason)
	}
	if event.Action == nil || s.d.Actions == nil {
		return refuse(actionSourceChanged)
	}
	current, err := s.d.Actions.CurrentCheckAction(ctx, input.Account, integration, stack.Provider, event.Event.Repository, *event.Action)
	if err != nil {
		return Decision{}, err
	}
	if !current {
		return refuse(actionSourceChanged)
	}
	kind := kernel.RunTypeProposed
	if event.Event.CheckAction.Action == "deploy" {
		if !attempt.canWrite {
			return refuse(actionReadOnly)
		}
		kind = kernel.RunTypeTracked
	} else if event.Event.CheckAction.Action != "preview" {
		return refuse(actionUnsupported)
	}
	if s.d.Creator == nil || s.d.Limits == nil {
		return Decision{}, kernel.ErrUnavailable
	}
	created, err := s.d.Creator.Create(ctx, runsapi.CreateRequest{
		Account: input.Account, Stack: input.Stack, Type: kind,
		Commit: runsapi.Commit(event.Commit), TriggeredBy: "github/" + event.Event.Author, Mode: runsapi.None,
		BeforeCreate: func(ctx context.Context) error { return s.admit(ctx, input) },
	})
	if errors.Is(err, ErrCapped) {
		if s.d.Checks != nil {
			cap, ok := errors.AsType[*capRefusal](err)
			if !ok {
				return Decision{}, kernel.ErrUnavailable
			}
			if err := s.d.Checks.Capped(ctx, input.Account, integration, input.Stack, event.Commit.Hash, kind, cap.limit); err != nil {
				return Decision{}, err
			}
		}
		s.actionRefused(ctx, RunLimitReached)
		return Decision{Capped, RunLimitReached}, nil
	}
	if errors.Is(err, runsapi.ErrCreationRefused) {
		return refuse(actionStackRefused)
	}
	if err != nil {
		return Decision{}, err
	}
	if !created.Created {
		return Decision{}, kernel.ErrUnavailable
	}
	if err := s.d.Effects.RecordCheckAction(ctx, input.Account, input.Delivery, input.Stack); err != nil {
		return Decision{}, err
	}
	outcome := Propose
	if kind == kernel.RunTypeTracked {
		outcome = Track
	}
	return Decision{Outcome: outcome}, nil
}

func (s *Service) actionRefused(ctx context.Context, reason string) {
	tx.AfterCommit(ctx, func(ctx context.Context) {
		s.d.Log.WarnContext(ctx, "VCS check action refused", "reason", reason)
		if s.d.ActionRefused != nil {
			s.d.ActionRefused(ctx, reason)
		}
	})
}

// actionAttempt is evaluation-local authorization, never durable preparation.
var errActionPreparation = errors.New("vcs: check action preparation failed")

type actionAttempt struct {
	fingerprint [32]byte
	completed   bool
	canWrite    bool
	reason      string
}

func actionFingerprint(event Prepared) [32]byte {
	data, _ := json.Marshal(event)
	return sha256.Sum256(data)
}

// actionSnapshot binds original durable identity separately from its current
// path projection. The short read transaction is released before native reads.
type actionSnapshot struct {
	original  Prepared
	completed bool
	event     vcsapi.Event
	reason    string
}

//nolint:wrapcheck // domain/queue ports retain their named boundary and control errors
func (s *Service) actionSnapshots(ctx context.Context, job queue.Job, input Evaluation) (EventContext, stacksapi.Use, map[int]actionSnapshot, error) {
	var meta EventContext
	var use stacksapi.Use
	snapshots := map[int]actionSnapshot{}
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.d.Jobs.HoldTx(ctx, job); err != nil {
			return err
		}
		var found bool
		var err error
		meta, found, err = s.d.Store.EventContext(ctx, input.Account, input.Delivery)
		if err != nil {
			return err
		}
		if !found || meta.Replays != input.Replays || meta.State != intake.Dispatched || !s.d.Now().Before(meta.Deadline) {
			return errExpired
		}
		use, found, err = s.d.Integrations.HeldEventUse(ctx, input.Account, meta.Integration)
		if err != nil {
			return err
		}
		if !found || use.Provider != meta.Provider {
			return errExpired
		}
		events, err := s.d.Store.Prepared(ctx, input.Account, input.Delivery)
		if err != nil {
			return err
		}
		for index, event := range events {
			if !actionFor(event.Event, input.Stack) {
				continue
			}
			item, err := s.snapshotAction(ctx, input, meta, use, event)
			if err != nil {
				return err
			}

			snapshots[index] = item
		}
		return nil
	})
	return meta, use, snapshots, err
}

// snapshotAction runs under the queue, delivery-generation and integration
// holds. A successful original intent skips native authorization, but is checked
// again during completion before suppressing the effect.
//
//nolint:wrapcheck // transactional VCS reads retain their owned error boundary
func (s *Service) snapshotAction(ctx context.Context, input Evaluation, meta EventContext, use stacksapi.Use, event Prepared) (actionSnapshot, error) {
	item := actionSnapshot{original: event, event: event.Event}
	if s.d.Effects == nil {
		return item, kernel.ErrUnavailable
	}
	done, err := s.d.Effects.HasCheckAction(ctx, input.Account, input.Delivery, input.Stack)
	if err != nil {
		return item, err
	}
	if done {
		item.completed = true
		return item, nil
	}
	if s.d.Lifecycle != nil {
		repo, allowed, err := s.d.Lifecycle.FanoutRepository(ctx, input.Account, meta.Integration, time.Time{}, event.Event.Repository)
		if err != nil {
			return item, err
		}
		if !allowed {
			item.reason = actionSourceChanged
		} else {
			item.event.Repository = repo
		}
	}
	stack, reason, err := s.actionTarget(ctx, intake.Delivery{Account: input.Account, Provider: meta.Provider, Integration: meta.Integration}, item.event, use)
	if err != nil {
		return item, err
	}
	if reason != "" {
		item.reason = reason
	}
	if reason == "" && !stack.GithubActionDeploy {
		item.reason = actionDisabled
	}
	if item.reason != "" {
		return item, nil
	}
	if event.Action == nil {
		item.reason = actionSourceChanged
		return item, nil
	}
	current, err := s.d.Actions.CurrentCheckAction(ctx, input.Account, meta.Integration, meta.Provider, item.event.Repository, *event.Action)
	if err != nil {
		return item, err
	}
	if !current {
		item.reason = actionSourceChanged
	}
	return item, nil
}

//nolint:wrapcheck // domain/queue ports retain their named boundary and control errors
func (s *Service) prepareActions(ctx context.Context, job queue.Job, input Evaluation) (map[int]actionAttempt, error) {
	if s.d.Actions == nil {
		return map[int]actionAttempt{}, nil
	}
	events, err := s.d.Store.Prepared(ctx, input.Account, input.Delivery)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(events, func(event Prepared) bool { return actionFor(event.Event, input.Stack) }) {
		return map[int]actionAttempt{}, nil
	}
	meta, use, snapshots, err := s.actionSnapshots(ctx, job, input)
	if err != nil {
		return nil, err
	}
	attempts := map[int]actionAttempt{}
	for index, item := range snapshots {
		attempt := actionAttempt{fingerprint: actionFingerprint(item.original), reason: item.reason}
		if item.completed {
			attempt.completed = true
			attempts[index] = attempt
			continue
		}
		stack, reason, err := s.actionTarget(ctx, intake.Delivery{Account: input.Account, Provider: meta.Provider, Integration: meta.Integration}, item.event, use)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			attempt.reason = reason
		}
		if !stack.GithubActionDeploy && reason == "" {
			attempt.reason = actionDisabled
		}
		if item.original.Action == nil {
			attempt.reason = actionSourceChanged
		}
		if attempt.reason == "" {
			attempt.canWrite, err = s.d.Actions.RefreshCheckAction(ctx, input.Account, meta.Integration, meta.Provider, item.event, *item.original.Action)
			if err != nil {
				var invalid *kernel.Invalid
				switch {
				case errors.Is(err, port.ErrCheckActionTarget) || errors.As(err, &invalid):
					attempt.reason = actionSourceChanged
				case errors.Is(err, context.Canceled):
					return nil, context.Canceled
				case errors.Is(err, context.DeadlineExceeded):
					return nil, context.DeadlineExceeded
				default:
					if limited, ok := errors.AsType[*port.RateLimited](err); ok {
						return nil, limited
					}
					return nil, errActionPreparation
				}
			}
		}
		attempts[index] = attempt
	}
	return attempts, nil
}

type evaluationSelection struct {
	prepared       Prepared
	matched, apply bool
}

// selectEvaluation uses original durable bytes for authorization correlation;
// current native coordinates are only a projection for target/source checks.
//
//nolint:wrapcheck // lifecycle names its metadata read errors
func (s *Service) selectEvaluation(
	ctx context.Context,
	input Evaluation,
	meta EventContext,
	stack stacksapi.Settings,
	event Prepared,
	attempt actionAttempt,
) (evaluationSelection, error) {
	out := evaluationSelection{prepared: event}
	if event.Event.Type == "check_action" {
		if !actionFor(event.Event, input.Stack) {
			return out, nil
		}
		out.matched = true
		if attempt.fingerprint != actionFingerprint(event) {
			s.actionRefused(ctx, actionSourceChanged)
			return out, nil
		}
		if attempt.completed {
			out.apply = true
			return out, nil
		}
		if s.d.Lifecycle != nil {
			repo, allowed, err := s.d.Lifecycle.FanoutRepository(ctx, input.Account, meta.Integration, time.Time{}, event.Event.Repository)
			if err != nil {
				return out, err
			}
			if !allowed {
				s.actionRefused(ctx, actionSourceChanged)
				return out, nil
			}
			out.prepared.Event.Repository = repo
		}
		repo := out.prepared.Event.Repository
		if !strings.EqualFold(repo.Namespace, stack.Namespace) || !strings.EqualFold(repo.Name, stack.Repository) {
			s.actionRefused(ctx, actionInvalidTarget)
			return out, nil
		}
	} else if event.Event.Repository.Namespace != stack.Namespace || event.Event.Repository.Name != stack.Repository {
		return out, nil
	}
	out.matched, out.apply = true, true
	return out, nil
}
