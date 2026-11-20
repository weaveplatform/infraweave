package feedback

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func (s *Service) checkReport(ctx context.Context, b CheckBinding, r CheckRequest) (port.CheckReport, checkOwner, int64, bool, error) {
	if r.Aggregate {
		return s.aggregateReport(ctx, b, r.Name)
	}
	owner := checkOwner{ID: b.Run, CreatedAt: b.CreatedAt}
	report := port.CheckReport{Name: r.Name, ExternalID: b.Stack.String(), Actions: []port.CheckAction{}, RecoveryID: "infraweave/" + b.Key, URL: s.runURL(b)}
	sequence := r.Sequence
	switch {
	case b.Push != nil:
		owner.ID = b.Stack
		report.State, report.Title, report.Summary = pushCheckContent(b.Push)
	case b.Cap:
		owner.ID = b.Stack
		report.State = port.CheckFailure
		report.Title = "Event run limit reached"
		report.Summary = fmt.Sprintf("The event reached the limit of %d runs before this stack could create a run.", b.Limit)
	default:
		fact, err := s.checks.Facts.Run(ctx, b.Account, b.Run)
		if errors.Is(err, runsapi.ErrCheckRunNotFound) {
			return report, owner, sequence, false, nil
		}
		if err != nil {
			return report, owner, sequence, false, fmt.Errorf("VCS check current run: %w", err)
		}
		if !eligibleCheck(fact) || fact.Stack != b.Stack || fact.CommitSHA != b.Commit || fact.Type != b.Type {
			return report, owner, sequence, false, nil
		}
		if fact.Sequence < r.Sequence {
			return report, owner, sequence, false, ErrCheckReport
		}
		state, ok := checkState(fact.State, fact.Delta)
		if !ok || !individualCheck(state, fact.Delta, b.Policy.FailuresOnly, b.Policy.SkipUnchanged) {
			return report, owner, sequence, false, nil
		}
		sequence = fact.Sequence
		owner = checkOwner{ID: fact.ID, CreatedAt: fact.CreatedAt}
		report.State = state
		report.Title = string(fact.State)
		report.Summary = changeSummary(fact.Delta)
		if state == port.CheckNeutral {
			report.Title = "No changes"
		}
	}
	stack, err := s.checks.Stacks.Settings(ctx, b.Account, b.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return report, owner, sequence, false, nil
	}
	if err != nil {
		return report, owner, sequence, false, fmt.Errorf("VCS check actions: %w", err)
	}
	if !b.noRun() && b.Provider == "GITHUB_ENTERPRISE" && stack.GithubActionDeploy {
		report.Actions = []port.CheckAction{
			{Label: "Preview", Description: "Propose a run on this commit", Identifier: "preview"},
			{Label: "Deploy", Description: "Track a run on this commit", Identifier: "deploy"},
		}
	}
	return report, owner, sequence, true, nil
}

func (s *Service) runURL(b CheckBinding) string {
	base := strings.TrimSuffix(s.checks.Public, "/") + "/api/v1/"
	if b.noRun() {
		return base + "stacks/" + url.PathEscape(b.Slug)
	}
	return base + "runs/" + b.Run.String()
}
