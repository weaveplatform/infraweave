package feedback

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type aggregateFacts struct {
	runs  []runsapi.CheckRun
	caps  []CheckBinding
	slugs map[kernel.ULID]string
	owner checkOwner
}

func (s *Service) aggregateFacts(ctx context.Context, b CheckBinding) (aggregateFacts, error) {
	out := aggregateFacts{slugs: map[kernel.ULID]string{}}
	use, found, err := s.checks.Integrations.EventUse(ctx, b.Account, b.Integration)
	if err != nil {
		return out, fmt.Errorf("VCS aggregate integration: %w", err)
	}
	if !found {
		return out, nil
	}
	stacks, err := s.checks.Stacks.ByVCSRepository(ctx, b.Account, use, b.Namespace, b.Repository)
	if err != nil {
		return out, fmt.Errorf("VCS aggregate stacks: %w", err)
	}
	ids := make([]kernel.ULID, 0, len(stacks))
	for _, stack := range stacks {
		ids = append(ids, stack.ID)
		out.slugs[stack.ID] = stack.Slug
	}
	if len(ids) == 0 {
		return out, nil
	}
	facts, err := s.checks.Facts.OnCommit(ctx, b.Account, ids, b.Commit)
	if err != nil {
		return out, fmt.Errorf("VCS aggregate runs: %w", err)
	}
	for _, fact := range facts {
		if !eligibleCheck(fact) || fact.CommitSHA != b.Commit || !slices.Contains(ids, fact.Stack) {
			continue
		}
		out.runs = append(out.runs, fact)
		candidate := checkOwner{ID: fact.ID, CreatedAt: fact.CreatedAt}
		if newerCheckOwner(candidate, out.owner) {
			out.owner = candidate
		}
	}
	for _, kind := range []kernel.RunType{kernel.RunTypeTracked, kernel.RunTypeProposed} {
		caps, err := s.checks.Store.CapChecks(ctx, b, kind)
		if err != nil {
			return out, fmt.Errorf("VCS aggregate cap facts: %w", err)
		}
		for _, cap := range caps {
			if !slices.Contains(ids, cap.Stack) || slices.ContainsFunc(out.runs, func(r runsapi.CheckRun) bool { return r.Stack == cap.Stack && r.Type == cap.Type }) {
				continue
			}
			out.caps = append(out.caps, cap)
			candidate := checkOwner{ID: cap.Stack, CreatedAt: cap.CreatedAt}
			if newerCheckOwner(candidate, out.owner) {
				out.owner = candidate
			}
		}
	}
	return out, nil
}

func newerCheckOwner(a, b checkOwner) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.ID.String() > b.ID.String()
}

func (s *Service) aggregateReport(ctx context.Context, b CheckBinding, name string) (port.CheckReport, checkOwner, int64, bool, error) {
	facts, err := s.aggregateFacts(ctx, b)
	report := port.CheckReport{Name: name, Actions: []port.CheckAction{}, RecoveryID: "infraweave/aggregate/" + name + "/" + b.Commit, URL: s.runURL(b)}
	if err != nil {
		return report, facts.owner, 0, false, err
	}
	if facts.owner.ID.IsZero() {
		return report, facts.owner, 0, false, nil
	}
	concluded := aggregateConcluded(facts, kernel.RunTypeTracked) || aggregateConcluded(facts, kernel.RunTypeProposed)
	if name == "infraweave/modules" {
		if !concluded {
			return report, facts.owner, 0, false, nil
		}
		report.State = port.CheckSkipped
		report.Title = "0 runs"
		report.Summary = "0 runs."
		return report, facts.owner, 0, true, nil
	}
	kind := kernel.RunTypeProposed
	if name == "infraweave/tracked" {
		kind = kernel.RunTypeTracked
	}
	runs := slices.DeleteFunc(slices.Clone(facts.runs), func(r runsapi.CheckRun) bool { return r.Type != kind })
	caps := slices.DeleteFunc(slices.Clone(facts.caps), func(c CheckBinding) bool { return c.Type != kind })
	if len(runs) == 0 && len(caps) == 0 {
		if !concluded {
			return report, facts.owner, 0, false, nil
		}
		report.State = port.CheckSkipped
		report.Title = "0 runs"
		report.Summary = "0 runs."
		return report, facts.owner, 0, true, nil
	}
	report.State = aggregateState(runs, caps)
	report.Title = fmt.Sprintf("%d %s runs", len(runs), strings.ToLower(string(kind)))
	report.Summary = aggregateSummary(runs, caps, facts.slugs)
	return report, facts.owner, 0, true, nil
}

func aggregateConcluded(f aggregateFacts, kind kernel.RunType) bool {
	count := 0
	for _, r := range f.runs {
		if r.Type == kind {
			count++
			if !r.State.Terminal() {
				return false
			}
		}
	}
	for _, c := range f.caps {
		if c.Type == kind {
			count++
		}
	}
	return count > 0
}

func aggregateState(runs []runsapi.CheckRun, caps []CheckBinding) port.CheckState {
	queued, active, failed := true, false, len(caps) > 0
	for _, run := range runs {
		state, ok := checkState(run.State, run.Delta)
		if !ok {
			state = port.CheckInProgress
		}
		if !run.State.Terminal() {
			active = true
			if state != port.CheckQueued {
				queued = false
			}
		}
		if unsuccessful(state) {
			failed = true
		}
	}
	if active {
		if queued {
			return port.CheckQueued
		}
		return port.CheckInProgress
	}
	if failed {
		return port.CheckFailure
	}
	return port.CheckSuccess
}

func aggregateSummary(runs []runsapi.CheckRun, caps []CheckBinding, slugs map[kernel.ULID]string) string {
	rows := slices.Clone(runs)
	slices.SortFunc(rows, func(a, b runsapi.CheckRun) int {
		return cmp.Or(strings.Compare(slugs[a.Stack], slugs[b.Stack]), strings.Compare(a.ID.String(), b.ID.String()))
	})
	var out strings.Builder
	for _, run := range rows {
		fmt.Fprintf(&out, "%s: %s; %s\n", slugs[run.Stack], run.State, changeSummary(run.Delta))
	}
	slices.SortFunc(caps, func(a, b CheckBinding) int { return strings.Compare(a.Slug, b.Slug) })
	for _, cap := range caps {
		fmt.Fprintf(&out, "%s: failed; event reached the limit of %d runs.\n", cap.Slug, cap.Limit)
	}
	return out.String()
}
