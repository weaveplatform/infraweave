package push

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

var errRequestPreparation = errors.New("vcs: selected request policy needs native preparation")

// ErrRequestChanged refuses a backfill after its original stored event changed.
var ErrRequestChanged = errors.New("vcs: native request preparation changed")

type nativeRequestFailure struct{ cause error }

// Native error strings can contain private repository or request details. The
// queue needs only the fixed message and the cause's retry/cancellation class.
func (nativeRequestFailure) Error() string   { return "vcs: native request preparation failed" }
func (e nativeRequestFailure) Unwrap() error { return e.cause }

// RequestBackfiller installs missing native facts once, joining the child queue
// completion transaction. A concurrent child returns the first committed facts.
type RequestBackfiller interface {
	BackfillRequest(context.Context, kernel.AccountID, kernel.ULID, int, vcsapi.Event, Prepared) (Prepared, error)
}

type requestBackfill struct {
	original vcsapi.Event
	prepared Prepared
}

// prepareRequestBackfills runs only after effective selection discovered missing
// facts and rolled back its transaction. The read fence is released before HTTP.
func (s *Service) prepareRequestBackfills(ctx context.Context, job queue.Job, input Evaluation) (map[int]requestBackfill, error) {
	if tx.In(ctx) || s.d.Requests == nil {
		return nil, fmt.Errorf("%w: native request preparation is unavailable", kernel.ErrUnavailable)
	}
	var meta EventContext
	var events []Prepared
	var stack stacksapi.Settings
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.d.Jobs.HoldTx(ctx, job); err != nil {
			return fmt.Errorf("VCS request backfill queue fence: %w", err)
		}
		var found bool
		var err error
		meta, found, err = s.d.Store.EventContext(ctx, input.Account, input.Delivery)
		if err != nil {
			return fmt.Errorf("VCS request backfill delivery: %w", err)
		}
		if !found || meta.State != intake.Dispatched || meta.Replays != input.Replays || !s.d.Now().Before(meta.Deadline) {
			return errExpired
		}
		events, err = s.d.Store.Prepared(ctx, input.Account, input.Delivery)
		if err != nil {
			return fmt.Errorf("VCS request backfill prepared events: %w", err)
		}
		stack, err = s.d.Stacks.Settings(ctx, input.Account, input.Stack)
		if errors.Is(err, stacksapi.ErrNotFound) {
			return errExpired
		}
		if err != nil {
			return fmt.Errorf("VCS request backfill stack: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("VCS request backfill snapshot: %w", err)
	}
	out := map[int]requestBackfill{}
	for index, event := range events {
		if !requestEvent(event.Event) || event.Request != nil || event.Event.Repository.Namespace != stack.Namespace || event.Event.Repository.Name != stack.Repository {
			continue
		}
		facts, err := s.d.Requests.PrepareRequest(ctx, input.Account, meta.Integration, meta.Provider, event.Event)
		if err != nil {
			return nil, nativeRequestFailure{cause: err}
		}
		prepared := event
		prepared.Request = &facts
		prepared.Commit, prepared.Files = facts.Head, facts.Files
		prepared.Event.Head, prepared.Event.Branch = facts.Head.Hash, facts.Head.Branch
		prepared.Event.PullRequest = &facts.Snapshot.Request
		out[index] = requestBackfill{original: event.Event, prepared: prepared}
	}
	if len(out) == 0 {
		return nil, errRequestPreparation
	}
	return out, nil
}

func (s *Service) commitRequestBackfills(ctx context.Context, input Evaluation, use stacksapi.Use, events []Prepared, requests map[int]requestBackfill) error {
	if len(requests) == 0 {
		return nil
	}
	store, ok := s.d.Store.(RequestBackfiller)
	if !ok {
		return fmt.Errorf("%w: native request backfill storage is unavailable", kernel.ErrUnavailable)
	}
	for index := range events {
		request, exists := requests[index]
		if !exists {
			continue
		}
		stacks, err := s.d.Stacks.ByVCSRepository(ctx, input.Account, use, request.prepared.Event.Repository.Namespace, request.prepared.Event.Repository.Name)
		if err != nil {
			return fmt.Errorf("VCS request backfill targets: %w", err)
		}
		var affectedCount int64
		for _, stack := range stacks {
			settings, err := s.d.Stacks.Settings(ctx, input.Account, stack.ID)
			if err != nil {
				return fmt.Errorf("VCS request backfill settings: %w", err)
			}
			if affected(settings, request.prepared.Files) {
				affectedCount++
			}
		}
		prepared := request.prepared
		prepared.AffectedStacksCount = &affectedCount
		current, err := store.BackfillRequest(ctx, input.Account, input.Delivery, index, request.original, prepared)
		if errors.Is(err, ErrRequestChanged) {
			return errExpired
		}
		if err != nil {
			return fmt.Errorf("VCS native request backfill: %w", err)
		}
		if current.Request == nil {
			return errRequestPreparation
		}
		events[index] = current
	}
	return nil
}
