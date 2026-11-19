package push

import (
	"context"
	"errors"
	"testing"
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

type contractActionSource struct {
	*actionSources
	prepareCalls, currentCalls int
	prepareErr                 error
	changeFacts                func(*port.ActionFacts)
	currentFault               func(int) error
	currentValue               func(int) bool
}

func (a *contractActionSource) PrepareCheckAction(ctx context.Context, account kernel.AccountID, id kernel.ULID, provider string, event vcsapi.Event) (port.ActionFacts, error) {
	a.prepareCalls++
	facts, err := a.actionSources.PrepareCheckAction(ctx, account, id, provider, event)
	if a.prepareErr != nil {
		return port.ActionFacts{}, a.prepareErr
	}
	if a.changeFacts != nil {
		a.changeFacts(&facts)
	}
	return facts, err
}

func (a *contractActionSource) CurrentCheckAction(
	ctx context.Context,
	account kernel.AccountID,
	id kernel.ULID,
	provider string,
	repo vcsapi.Repository,
	facts port.ActionFacts,
) (bool, error) {
	a.currentCalls++
	current, err := a.actionSources.CurrentCheckAction(ctx, account, id, provider, repo, facts)
	if a.currentFault != nil {
		if fault := a.currentFault(a.currentCalls); fault != nil {
			return false, fault
		}
	}
	if a.currentValue != nil {
		return a.currentValue(a.currentCalls), err
	}
	return current, err
}

type contractActionStore struct {
	Store
	reads, contexts, writes int
	readFault, contextFault func(int) error
	writeErr                error
}

func (s *contractActionStore) Prepared(ctx context.Context, account kernel.AccountID, id kernel.ULID) ([]Prepared, error) {
	s.reads++
	if s.readFault != nil {
		if err := s.readFault(s.reads); err != nil {
			return nil, err
		}
	}
	return s.Store.Prepared(ctx, account, id)
}

func (s *contractActionStore) EventContext(ctx context.Context, account kernel.AccountID, id kernel.ULID) (EventContext, bool, error) {
	s.contexts++
	if s.contextFault != nil {
		if err := s.contextFault(s.contexts); err != nil {
			return EventContext{}, false, err
		}
	}
	return s.Store.EventContext(ctx, account, id)
}

func (s *contractActionStore) PutPrepared(ctx context.Context, account kernel.AccountID, id kernel.ULID, events []Prepared) error {
	s.writes++
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.Store.PutPrepared(ctx, account, id, events)
}

type contractActionUse struct {
	Integrations
	heldCalls int
	change    func(int, stacksapi.Use, bool, error) (stacksapi.Use, bool, error)
}

func (u *contractActionUse) HeldEventUse(ctx context.Context, account kernel.AccountID, id kernel.ULID) (stacksapi.Use, bool, error) {
	u.heldCalls++
	use, found, err := u.Integrations.HeldEventUse(ctx, account, id)
	if u.change != nil {
		return u.change(u.heldCalls, use, found, err)
	}
	return use, found, err
}

type contractActionLifecycle struct {
	*reviewCurrentRepository
	calls  int
	failAt int
	denyAt int
	cause  error
}

func (l *contractActionLifecycle) FanoutRepository(
	ctx context.Context,
	account kernel.AccountID,
	id kernel.ULID,
	at time.Time,
	repo vcsapi.Repository,
) (vcsapi.Repository, bool, error) {
	l.calls++
	if l.calls == l.failAt {
		return vcsapi.Repository{}, false, l.cause
	}
	if l.calls == l.denyAt {
		return vcsapi.Repository{}, false, nil
	}
	return l.reviewCurrentRepository.FanoutRepository(ctx, account, id, at, repo)
}

type contractActionEffects struct {
	*actionEffects
	reads, records int
	failReadAt     int
	recordErr      error
	missingAt      int
	cause          error
}

func (e *contractActionEffects) HasCheckAction(ctx context.Context, account kernel.AccountID, delivery, stack kernel.ULID) (bool, error) {
	e.reads++
	if !tx.In(ctx) {
		return false, ErrNoTransaction
	}
	if e.reads == e.failReadAt {
		return false, e.cause
	}
	if e.reads == e.missingAt {
		return false, nil
	}
	return e.actionEffects.HasCheckAction(ctx, account, delivery, stack)
}

func (e *contractActionEffects) RecordCheckAction(ctx context.Context, account kernel.AccountID, delivery, stack kernel.ULID) error {
	e.records++
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	if e.recordErr != nil {
		return e.recordErr
	}
	return e.actionEffects.RecordCheckAction(ctx, account, delivery, stack)
}

type contractActionJobs struct {
	*preparedJobs
	err error
}

func (j *contractActionJobs) EnqueueTx(ctx context.Context, kind queue.Kind, payload any, options ...queue.Option) (int64, error) {
	if j.err != nil {
		return 0, j.err
	}
	return j.preparedJobs.EnqueueTx(ctx, kind, payload, options...)
}

// Parent refusal/error contracts drive the whole dispatcher. Invalid authority
// never reaches a native source, and no failed preparation publishes a child.
func TestUnitCheckActionParentPreparationAuthority(t *testing.T) {
	for _, name := range []string{"unsupported action", "invalid sender", "invalid SHA", "missing native identity", "missing stack", "stack unavailable", "foreign provider action", "source declined", "source invalid", "source unavailable", "wrong immutable result", "missing native origin"} {
		t.Run(name, func(t *testing.T) {
			w, base := newActionEvaluation(t)
			source := &contractActionSource{actionSources: base}
			w.s.d.Actions = source
			store := &contractActionStore{Store: w.s.d.Store}
			jobs := &preparedJobs{}
			w.s.d.Store, w.s.d.Jobs = store, jobs
			event := w.events[0].Event
			action := *event.CheckAction
			event.CheckAction = &action
			delivery := intake.Delivery{Account: w.input.Account, ID: w.input.Delivery, Integration: w.meta.Integration, Provider: w.meta.Provider, Deadline: w.input.Deadline}
			cause := errors.New("source authority unavailable")
			var wantErr error
			wantReason := actionInvalidTarget
			wantCalls := 0
			switch name {
			case "unsupported action":
				event.CheckAction.Action = "rebuild"
				wantReason = actionUnsupported
			case "invalid sender":
				event.Author = "not/a/login"
				wantReason = actionInvalidSender
			case "invalid SHA":
				event.Head = "mutable-main"
			case "missing native identity":
				event.Repository.ID = ""
			case "missing stack":
				w.initialErr = stacksapi.ErrNotFound
			case "stack unavailable":
				w.initialErr = cause
				wantErr = cause
			case "foreign provider action":
				delivery.Provider = "GITLAB"
				w.s.d.Integrations = eventUse{use: stacksapi.Use{Integration: delivery.Integration, Provider: "GITLAB"}, found: true}
			case "source declined":
				source.prepareErr = port.ErrCheckActionTarget
				wantReason = actionSourceChanged
				wantCalls = 1
			case "source invalid":
				source.prepareErr = &kernel.Invalid{Detail: "private native refusal"}
				wantReason = actionSourceChanged
				wantCalls = 1
			case "source unavailable":
				source.prepareErr = cause
				wantErr = cause
				wantCalls = 1
			case "wrong immutable result":
				source.changeFacts = func(f *port.ActionFacts) { f.Commit.Hash = "different-head" }
				wantReason = actionSourceChanged
				wantCalls = 1
			case "missing native origin":
				source.changeFacts = func(f *port.ActionFacts) { f.NativeHost = "" }
				wantReason = actionSourceChanged
				wantCalls = 1
			}
			var reasons []string
			w.s.d.ActionRefused = func(_ context.Context, reason string) { reasons = append(reasons, reason) }
			prepared, err := w.s.Prepare(t.Context(), delivery, []vcsapi.Event{event})
			if !errors.Is(err, wantErr) {
				t.Fatal("preparation lost authority failure", err)
			}
			if wantErr != nil {
				if prepared != nil {
					t.Fatal("failed preparation returned publishable work")
				}
			} else {
				if prepared == nil {
					t.Fatal("safe refusal lost parent completion")
				}
				if len(reasons) != 0 {
					t.Fatal("refusal escaped parent commit")
				}
				if err := tx.Do(t.Context(), evaluationDB{}, tx.Options{}, prepared.Commit); err != nil {
					t.Fatal(err)
				}
				if len(reasons) != 1 || reasons[0] != wantReason {
					t.Fatal("refusal leaked/changed safe reason", reasons)
				}
			}
			if source.prepareCalls != wantCalls || source.currentCalls != 0 || base.refresh != 0 || store.writes != 0 || len(jobs.inputs) != 0 || len(w.requests) != 0 {
				t.Fatal("refused parent reached native/completion effects", source.prepareCalls, source.currentCalls, store.writes, jobs.inputs)
			}
		})
	}
}

func TestUnitCheckActionParentCompletionRetainsCurrentAuthority(t *testing.T) {
	for _, name := range []string{"integration lookup fails", "integration provider changes", "stack lookup fails", "source metadata fails", "source no longer current", "repository removed", "repository lookup fails", "prepared facts write fails", "child enqueue fails"} {
		t.Run(name, func(t *testing.T) {
			w, base := newActionEvaluation(t)
			source := &contractActionSource{actionSources: base}
			w.s.d.Actions = source
			store := &contractActionStore{Store: w.s.d.Store}
			jobs := &contractActionJobs{preparedJobs: &preparedJobs{}}
			w.s.d.Store, w.s.d.Jobs = store, jobs
			use := &contractActionUse{Integrations: w.s.d.Integrations}
			w.s.d.Integrations = use
			delivery := intake.Delivery{Account: w.input.Account, ID: w.input.Delivery, Integration: w.meta.Integration, Provider: w.meta.Provider, Deadline: w.input.Deadline}
			prepared, err := w.s.Prepare(t.Context(), delivery, []vcsapi.Event{w.events[0].Event})
			if err != nil || prepared == nil {
				t.Fatal(err)
			}
			cause := errors.New("completion authority unavailable")
			wantErr := cause
			wantReason := ""
			switch name {
			case "integration lookup fails":
				use.change = func(_ int, u stacksapi.Use, _ bool, _ error) (stacksapi.Use, bool, error) { return u, false, cause }
			case "integration provider changes":
				use.change = func(_ int, u stacksapi.Use, f bool, e error) (stacksapi.Use, bool, error) {
					u.Provider = "GITLAB"
					return u, f, e
				}
				wantErr = kernel.ErrUnavailable
			case "stack lookup fails":
				w.initialErr = cause
			case "source metadata fails":
				source.currentFault = func(int) error { return cause }
			case "source no longer current":
				base.current = false
				wantErr = nil
				wantReason = actionSourceChanged
			case "repository removed":
				w.s.d.Lifecycle = &contractActionLifecycle{reviewCurrentRepository: &reviewCurrentRepository{current: w.events[0].Event.Repository}, denyAt: 1}
				wantErr = nil
				wantReason = actionSourceChanged
			case "repository lookup fails":
				w.s.d.Lifecycle = &contractActionLifecycle{reviewCurrentRepository: &reviewCurrentRepository{current: w.events[0].Event.Repository}, failAt: 1, cause: cause}
			case "prepared facts write fails":
				store.writeErr = cause
			case "child enqueue fails":
				jobs.err = cause
			}
			var reasons []string
			w.s.d.ActionRefused = func(_ context.Context, reason string) { reasons = append(reasons, reason) }
			err = tx.Do(t.Context(), evaluationDB{}, tx.Options{}, prepared.Commit)
			if !errors.Is(err, wantErr) || len(jobs.inputs) != 0 || source.prepareCalls != 1 || base.refresh != 0 {
				t.Fatal("parent lost current binding/error or repeated native IO", err, jobs.inputs, source.prepareCalls, base.refresh)
			}
			if wantReason != "" {
				if len(reasons) != 1 || reasons[0] != wantReason {
					t.Fatal("committed refusal not counted", reasons)
				}
			} else if len(reasons) != 0 {
				t.Fatal("failed completion published refusal", reasons)
			}
			if wantErr != nil && name != "prepared facts write fails" && name != "child enqueue fails" && store.writes != 0 {
				t.Fatal("invalid completion published prepared facts")
			}
		})
	}
}

// Each retryable boundary retains the original job and immutable preparation.
// These port-level recorders establish call/ack ordering; the real store/app
// contracts separately establish rollback of persisted runs, slots and receipts.
func TestUnitCheckActionAttemptBoundaryRecovery(t *testing.T) {
	for _, name := range []string{
		"initial prepared read", "held prepared read", "completion prepared read",
		"preflight delivery read", "completion delivery read",
		"preflight integration hold", "completion integration hold",
		"preflight repository read", "completion repository read",
		"preflight source metadata", "completion source metadata",
		"preflight success receipt", "completion success receipt", "success receipt write",
	} {
		t.Run(name, func(t *testing.T) {
			w, base := newActionEvaluation(t)
			cause := errors.New("action dependency temporarily unavailable")
			source := &contractActionSource{actionSources: base}
			store := &contractActionStore{Store: w.s.d.Store}
			use := &contractActionUse{Integrations: w.s.d.Integrations}
			lifecycle := &contractActionLifecycle{reviewCurrentRepository: &reviewCurrentRepository{current: w.events[0].Event.Repository}, cause: cause}
			effects := &contractActionEffects{actionEffects: w.s.d.Effects.(*actionEffects), cause: cause}
			w.s.d.Actions, w.s.d.Store, w.s.d.Integrations, w.s.d.Lifecycle, w.s.d.Effects = source, store, use, lifecycle, effects
			wantRefresh := 0
			switch name {
			case "initial prepared read", "held prepared read", "completion prepared read":
				at := map[string]int{"initial prepared read": 1, "held prepared read": 2, "completion prepared read": 3}[name]
				store.readFault = func(call int) error {
					if call == at {
						return cause
					}
					return nil
				}
				if at == 3 {
					wantRefresh = 1
				}
			case "preflight delivery read", "completion delivery read":
				at := 1
				if name == "completion delivery read" {
					at, wantRefresh = 2, 1
				}
				store.contextFault = func(call int) error {
					if call == at {
						return cause
					}
					return nil
				}
			case "preflight integration hold", "completion integration hold":
				at := 1
				if name == "completion integration hold" {
					at, wantRefresh = 2, 1
				}
				use.change = func(call int, u stacksapi.Use, found bool, err error) (stacksapi.Use, bool, error) {
					if call == at {
						return u, false, cause
					}
					return u, found, err
				}
			case "preflight repository read":
				lifecycle.failAt = 1
			case "completion repository read":
				lifecycle.failAt, wantRefresh = 2, 1
			case "preflight source metadata", "completion source metadata":
				at := 1
				if name == "completion source metadata" {
					at, wantRefresh = 2, 1
				}
				source.currentFault = func(call int) error {
					if call == at {
						return cause
					}
					return nil
				}
			case "preflight success receipt":
				effects.failReadAt = 1
			case "completion success receipt":
				effects.failReadAt, wantRefresh = 2, 1
			case "success receipt write":
				effects.recordErr, wantRefresh = cause, 1
			}
			var reasons []string
			w.s.d.ActionRefused = func(_ context.Context, reason string) { reasons = append(reasons, reason) }
			if err := w.s.Evaluated(t.Context(), w.job); !errors.Is(err, cause) {
				t.Fatal("lost retryable boundary cause", err)
			}
			attempts := 0
			if name == "success receipt write" {
				attempts = 1
			}
			if w.complete != 0 || effects.done || len(reasons) != 0 || base.refresh != wantRefresh || len(w.requests) != attempts || w.used != attempts {
				t.Fatal("failed boundary acknowledged or escaped its expected call boundary", w.complete, effects.done, reasons, base.refresh, len(w.requests), w.used)
			}
			// The same job and preparation can be retried once its dependency recovers.
			store.readFault, store.contextFault, use.change, source.currentFault = nil, nil, nil, nil
			lifecycle.failAt, effects.failReadAt, effects.recordErr = 0, 0, nil
			if attempts != 0 {
				// The fake DB does not roll its recorders back. Reset only attempted
				// Create/admission observations, not the durable input or job.
				w.requests, w.used = nil, 0
			}
			if err := w.s.Evaluated(t.Context(), w.job); err != nil {
				t.Fatal(err)
			}
			if w.complete != 1 || !effects.done || len(w.requests) != 1 || w.used != 1 || effects.records != attempts+1 || len(reasons) != 0 || len(w.tracked) != 0 ||
				len(w.heads) != 0 {
				t.Fatal("same original intent did not recover exactly once", w.complete, effects.done, len(w.requests), w.used, effects.records, reasons)
			}
		})
	}
}

func TestUnitCheckActionCurrentAuthorityRefusals(t *testing.T) {
	for _, name := range []string{
		"removed before permission", "removed after permission", "source replaced after permission",
		"target moved after permission", "default moved after permission", "generation replaced after permission",
		"missing immutable facts", "native source declined", "native source invalid",
	} {
		t.Run(name, func(t *testing.T) {
			w, base := newActionEvaluation(t)
			lifecycle := &contractActionLifecycle{reviewCurrentRepository: &reviewCurrentRepository{current: w.events[0].Event.Repository}}
			use := &contractActionUse{Integrations: w.s.d.Integrations}
			w.s.d.Lifecycle, w.s.d.Integrations = lifecycle, use
			wantReason, wantRefresh := actionSourceChanged, 1
			switch name {
			case "removed before permission":
				lifecycle.denyAt, wantRefresh = 1, 0
			case "removed after permission":
				lifecycle.denyAt = 2
			case "source replaced after permission":
				base.mutate = func() { base.current = false }
			case "target moved after permission":
				base.mutate = func() { w.stack.Repository = "other" }
				wantReason = actionInvalidTarget
			case "default moved after permission":
				w.stack.VCSIntegration = nil
				use.change = func(call int, u stacksapi.Use, found bool, err error) (stacksapi.Use, bool, error) {
					u.Default = call == 1
					return u, found, err
				}
				wantReason = actionInvalidTarget
			case "generation replaced after permission":
				base.mutate = func() { w.meta.Replays++ }
				wantReason = ""
			case "missing immutable facts":
				w.events[0].Action, wantRefresh = nil, 0
			case "native source declined":
				base.err = port.ErrCheckActionTarget
			case "native source invalid":
				base.err = &kernel.Invalid{Detail: "private native detail"}
			}
			var reasons []string
			w.s.d.ActionRefused = func(_ context.Context, reason string) { reasons = append(reasons, reason) }
			if err := w.s.Evaluated(t.Context(), w.job); err != nil {
				t.Fatal(err)
			}
			if w.complete != 1 || len(w.requests) != 0 || w.used != 0 || w.s.d.Effects.(*actionEffects).done || base.refresh != wantRefresh || len(w.tracked) != 0 ||
				len(w.heads) != 0 {
				t.Fatal("current authority loss escaped refusal", w.complete, len(w.requests), w.used, base.refresh)
			}
			if wantReason == "" && len(reasons) != 0 || wantReason != "" && (len(reasons) != 1 || reasons[0] != wantReason) {
				t.Fatal("refusal was not a single fixed committed reason", reasons)
			}
		})
	}
}

func TestUnitCheckActionVanishedSuccessDoesNotBorrowAuthorization(t *testing.T) {
	w, base := newActionEvaluation(t)
	effects := &contractActionEffects{actionEffects: &actionEffects{done: true}, missingAt: 2}
	w.s.d.Effects = effects
	if err := w.s.Evaluated(t.Context(), w.job); !errors.Is(err, errChanged) {
		t.Fatal(err)
	}
	if base.refresh != 0 || w.complete != 0 || len(w.requests) != 0 || w.used != 0 || effects.records != 0 {
		t.Fatal("a vanished receipt authorized a new effect without fresh permission")
	}
	// A retry observes the same surviving successful receipt and completes
	// without a native lookup, cap slot, or another run.
	if err := w.s.Evaluated(t.Context(), w.job); err != nil {
		t.Fatal(err)
	}
	if base.refresh != 0 || w.complete != 1 || len(w.requests) != 0 || w.used != 0 || effects.records != 0 {
		t.Fatal("successful intent was repeated")
	}
}

type contractActionCreator struct{ w *evaluationWorld }

func (r contractActionCreator) Create(ctx context.Context, request runsapi.CreateRequest) (runsapi.CreatedRun, error) {
	r.w.step(ctx, "create")
	if request.Mode != runsapi.None || request.TriggeredBy != "github/developer" {
		r.w.t.Fatal("lost new-intent policy")
	}
	return runsapi.CreatedRun{ID: kernel.NewULID(), Created: false}, nil
}

func TestUnitCheckActionUnexpectedCreatorDedupDoesNotConsumeIntent(t *testing.T) {
	w, _ := newActionEvaluation(t)
	w.s.d.Creator = contractActionCreator{w}
	if err := w.s.Evaluated(t.Context(), w.job); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatal(err)
	}
	if w.complete != 0 || w.s.d.Effects.(*actionEffects).done || w.used != 0 {
		t.Fatal("unexpected dedup consumed a new press")
	}
	w.s.d.Creator = actionRuns{w}
	if err := w.s.Evaluated(t.Context(), w.job); err != nil {
		t.Fatal(err)
	}
	if w.complete != 1 || !w.s.d.Effects.(*actionEffects).done || len(w.requests) != 1 || w.used != 1 {
		t.Fatal("original intent failed to recover")
	}
}

func TestUnitCheckActionCapFeedbackMustSucceedBeforeRefusal(t *testing.T) {
	w, _ := newActionEvaluation(t)
	w.events[0].Event.CheckAction.Action = "deploy"
	w.used = w.cap
	cause := errors.New("cap feedback temporarily unavailable")
	reporter := &capReporter{w: w, cause: cause}
	w.s.d.Checks = reporter
	var reasons []string
	w.s.d.ActionRefused = func(_ context.Context, reason string) { reasons = append(reasons, reason) }
	if err := w.s.Evaluated(t.Context(), w.job); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if w.complete != 0 || w.s.d.Effects.(*actionEffects).done || len(reasons) != 0 || len(w.requests) != 0 || len(reporter.rows) != 1 {
		t.Fatal("failed cap feedback consumed or reported the intent")
	}
	reporter.cause = nil
	if err := w.s.Evaluated(t.Context(), w.job); err != nil {
		t.Fatal(err)
	}
	if w.complete != 1 || w.s.d.Effects.(*actionEffects).done || len(w.requests) != 0 || len(reasons) != 1 || reasons[0] != RunLimitReached || len(reporter.rows) != 2 ||
		reporter.rows[1] != (capturedCap{w.input.Account, w.meta.Integration, w.input.Stack, w.events[0].Commit.Hash, kernel.RunTypeTracked, w.cap}) {
		t.Fatal("cap recovery lost fixed scope or refused intent retryability", reporter.rows, reasons)
	}
}

func TestUnitCheckActionRefusalWaitsForSuccessfulParentCommit(t *testing.T) {
	w, base := newActionEvaluation(t)
	source := &contractActionSource{actionSources: base}
	w.s.d.Actions = source
	cause := errors.New("shared facts temporarily unavailable")
	store := &contractActionStore{Store: w.s.d.Store, writeErr: cause}
	jobs := &preparedJobs{}
	w.s.d.Store, w.s.d.Jobs = store, jobs
	event := w.events[0].Event
	refused := event
	action := *event.CheckAction
	action.Action = "unsupported"
	refused.CheckAction = &action
	delivery := intake.Delivery{Account: w.input.Account, ID: w.input.Delivery, Integration: w.meta.Integration, Provider: w.meta.Provider, Deadline: w.input.Deadline}
	var reasons []string
	w.s.d.ActionRefused = func(_ context.Context, reason string) { reasons = append(reasons, reason) }
	prepared, err := w.s.Prepare(t.Context(), delivery, []vcsapi.Event{refused, event})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(t.Context(), evaluationDB{}, tx.Options{}, prepared.Commit); !errors.Is(err, cause) || len(reasons) != 0 || len(jobs.inputs) != 0 {
		t.Fatal("aborted dispatch published refusal or work", err, reasons, jobs.inputs)
	}
	store.writeErr = nil
	if err := tx.Do(t.Context(), evaluationDB{}, tx.Options{}, prepared.Commit); err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 1 || reasons[0] != actionUnsupported || len(jobs.inputs) != 1 || jobs.inputs[0].Stack != w.input.Stack || source.prepareCalls != 1 {
		t.Fatal("same prepared plan did not publish once after recovery", reasons, jobs.inputs, source.prepareCalls)
	}
}
