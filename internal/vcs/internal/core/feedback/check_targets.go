package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func eligibleCheck(r runsapi.CheckRun) bool {
	return (r.Type == kernel.RunTypeTracked || r.Type == kernel.RunTypeProposed) && !r.Local && !r.DriftDetection && port.ValidCommit(r.CommitSHA)
}

func (s *Service) checkTargets(ctx context.Context, event outbox.Event) ([]vcsapi.FeedbackTarget, error) {
	if s.checks == nil || event.Kind != runsapi.UpdatedKind || event.SchemaVersion != runsapi.UpdatedVersion || event.Account.IsZero() || event.ID.IsZero() || event.Sequence <= 0 {
		return nil, nil
	}
	var update runsapi.Updated
	if json.Unmarshal(event.Document, &update) != nil || update.Run.ID.IsZero() || update.State != update.Run.State || event.Subject.Kind != "run" ||
		event.Subject.ID != update.Run.ID.String() {
		return nil, nil
	}
	account := kernel.AccountID{ULID: event.Account}
	fact, err := s.checks.Facts.Run(ctx, account, update.Run.ID)
	if errors.Is(err, runsapi.ErrCheckRunNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("VCS check run facts: %w", err)
	}
	if !eligibleCheck(fact) || fact.Stack != update.Stack.ULID || fact.Type != update.Run.Type || fact.CommitSHA != update.Run.Commit.Hash || fact.Sequence < event.Sequence {
		return nil, nil
	}
	valid, err := s.checkHistory(ctx, account, fact, event.Sequence, update.State)
	if err != nil || !valid {
		return nil, err
	}
	binding, err := s.checks.Store.CheckBinding(ctx, account, checkBindingKey(fact.ID))
	if errors.Is(err, ErrNoCheck) {
		binding, err = s.selectCheck(ctx, account, fact)
	}
	if errors.Is(err, ErrNoCheck) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	requests := checkRequests(binding, fact, event.Sequence)
	targets := make([]vcsapi.FeedbackTarget, 0, len(requests))
	for _, request := range requests {
		body, err := json.Marshal(request)
		if err != nil {
			return nil, fmt.Errorf("VCS check target: %w", err)
		}
		targets = append(
			targets,
			vcsapi.FeedbackTarget{
				Account: event.Account,
				EventID: event.ID,
				Key:     "check:" + binding.Key + ":" + checkClass(request.Aggregate) + ":" + request.Name,
				Job:     ReportCheck.Name,
				Payload: body,
			},
		)
	}
	return targets, nil
}

func checkRequests(binding CheckBinding, fact runsapi.CheckRun, sequence int64) []CheckRequest {
	state, ok := checkState(fact.State, fact.Delta)
	if !ok {
		return nil
	}

	requests := []CheckRequest{}
	if binding.Policy.Mode == "INDIVIDUAL" || binding.Policy.Mode == "ALL" {
		if individualCheck(state, fact.Delta, binding.Policy.FailuresOnly, binding.Policy.SkipUnchanged) && !sharedNameCollision(binding) {
			requests = append(requests, CheckRequest{Binding: binding, Name: individualName(binding), Sequence: sequence})
		}
	}
	if binding.Policy.Mode == "AGGREGATED" || binding.Policy.Mode == "ALL" {
		for _, name := range []string{"infraweave/tracked", "infraweave/proposed", "infraweave/modules"} {
			requests = append(requests, CheckRequest{Binding: binding, Name: name, Aggregate: true, Sequence: sequence})
		}
	}
	return requests
}

func (s *Service) checkHistory(ctx context.Context, account kernel.AccountID, fact runsapi.CheckRun, sequence int64, state kernel.RunState) (bool, error) {
	history, err := s.checks.Histories.HistoryAt(ctx, account, fact.ID, sequence)
	if errors.Is(err, runsapi.ErrHistoryNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("VCS check run history: %w", err)
	}
	if history.ID != fact.ID || history.Stack != fact.Stack || history.Type != fact.Type || history.CommitHash != fact.CommitSHA || history.Local ||
		history.Through != sequence ||
		len(history.Transitions) == 0 {
		return false, nil
	}
	last := history.Transitions[len(history.Transitions)-1]
	if last.Sequence != sequence || last.State != state {
		return false, nil
	}
	return true, nil
}

func (s *Service) selectCheck(ctx context.Context, account kernel.AccountID, r runsapi.CheckRun) (CheckBinding, error) {
	d := s.checks
	stack, err := d.Stacks.Settings(ctx, account, r.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) || err == nil && (stack.VCSDetached || stack.Provider == "GIT") {
		return CheckBinding{}, ErrNoCheck
	}
	if err != nil {
		return CheckBinding{}, fmt.Errorf("VCS check stack: %w", err)
	}
	in, found, err := d.Integrations.Binding(ctx, account, stack.Provider, stack.VCSIntegration)
	if err != nil {
		return CheckBinding{}, fmt.Errorf("VCS check integration: %w", err)
	}
	provider := d.Providers[in.Provider]
	if !found || provider == nil {
		return CheckBinding{}, ErrNoCheck
	}
	host, err := provider.CheckHost(in.Settings)
	if err != nil {
		return CheckBinding{}, ErrCheckReport
	}
	policy := in.Checks
	if policy.Mode == "" {
		policy.Mode = "INDIVIDUAL"
	}
	return CheckBinding{
		Account:     account,
		Key:         checkBindingKey(r.ID),
		Run:         r.ID,
		Stack:       r.Stack,
		Slug:        stack.Slug,
		Integration: in.ID,
		Provider:    in.Provider,
		NativeHost:  host,
		Namespace:   stack.Namespace,
		Repository:  stack.Repository,
		Commit:      r.CommitSHA,
		Ref:         r.Branch,
		Type:        r.Type,
		Policy:      policy,
		CreatedAt:   r.CreatedAt,
	}, nil
}

func validCheckBinding(b CheckBinding) bool {
	return !b.Account.IsZero() && b.Key != "" && !b.Stack.IsZero() && b.Slug != "" && !b.Integration.IsZero() && b.Provider != "" && b.NativeHost != "" && b.Namespace != "" &&
		b.Repository != "" &&
		port.ValidCommit(b.Commit) &&
		(b.Type == kernel.RunTypeTracked || b.Type == kernel.RunTypeProposed) &&
		!b.CreatedAt.IsZero() &&
		(b.Policy.Mode == "INDIVIDUAL" || b.Policy.Mode == "AGGREGATED" || b.Policy.Mode == "ALL") &&
		(b.Cap && b.Run.IsZero() && b.Limit > 0 || !b.Cap && !b.Run.IsZero() && b.Key == checkBindingKey(b.Run))
}

func validCheckRequest(r CheckRequest) bool {
	b := r.Binding
	return validCheckBinding(b) && r.Sequence > 0 &&
		(!r.Aggregate && r.Name == individualName(b) && (b.Policy.Mode == "INDIVIDUAL" || b.Policy.Mode == "ALL") || r.Aggregate && (r.Name == "infraweave/tracked" || r.Name == "infraweave/proposed" || r.Name == "infraweave/modules") && (b.Policy.Mode == "AGGREGATED" || b.Policy.Mode == "ALL"))
}

func (s *Service) enqueueCheck(ctx context.Context, target vcsapi.FeedbackTarget) error {
	var request CheckRequest
	if json.Unmarshal(target.Payload, &request) != nil || !validCheckRequest(request) || request.Binding.Account.ULID != target.Account {
		return vcsapi.ErrFeedbackTarget
	}
	if err := s.checks.Stacks.Hold(ctx, request.Binding.Account, request.Binding.Stack); errors.Is(err, stacksapi.ErrNotFound) {
		return nil
	} else if err != nil {
		return fmt.Errorf("VCS check stack hold: %w", err)
	}
	binding, err := s.checks.Store.BindCheck(ctx, request.Binding)
	if errors.Is(err, ErrNoCheck) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("VCS check bind: %w", err)
	}
	request.Binding = binding
	inserted, err := s.checks.Store.RequestCheck(ctx, request)
	if err != nil {
		return fmt.Errorf("VCS check request: %w", err)
	}
	if !inserted {
		return nil
	}
	if !request.Aggregate {
		owner := binding.Run
		if binding.Cap {
			owner = binding.Stack
		}
		if err := s.checks.Store.ReserveCheck(
			ctx,
			CheckResource{Account: binding.Account, Key: checkResourceKey(binding, request.Name, request.Aggregate), Binding: binding.Key, Owner: owner, Epoch: binding.CreatedAt},
		); err != nil {
			return fmt.Errorf("VCS check ownership: %w", err)
		}
	}
	order := "vcs.check.run:" + binding.Account.String() + ":" + binding.Key
	if request.Aggregate {
		order = "vcs.check.aggregate:" + binding.Account.String() + ":" + checkResourceKey(binding, request.Name, request.Aggregate)
	}
	_, err = s.jobs.EnqueueTx(
		ctx,
		ReportCheck,
		request,
		queue.Account(target.Account),
		queue.UniqueKey(binding.Account.String()+":"+binding.Key+":"+checkClass(request.Aggregate)+":"+request.Name),
		queue.OrderingKey(order),
	)
	if err != nil {
		return fmt.Errorf("enqueue VCS commit check: %w", err)
	}
	return nil
}

func checkClass(aggregate bool) string {
	if aggregate {
		return "aggregate"
	}
	return "individual"
}
