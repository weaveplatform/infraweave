package vcs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func (s *eventSteps) workflowFeedback(ctx context.Context, run string, request int, state string) error {
	c := s.comments
	checks := &checkSteps{
		w: c.w, provider: c.fixture.provider, namespace: c.fixture.namespace,
		github: c.github, gitlab: c.gitlab, cloud: c.peers.cloud, dc: c.peers.dc, azure: c.peers.azure,
	}
	if err := checks.settled(ctx, run); err != nil {
		return err
	}
	var actual []string
	for _, check := range checks.snapshots(true) {
		if check.Name == "infraweave/infra" && check.SHA == c.w.IDs["seed"] && strings.HasSuffix(check.URL, "/runs/"+c.w.IDs[run]) &&
			(len(actual) == 0 || actual[len(actual)-1] != check.State) {
			actual = append(actual, check.State)
		}
	}
	if len(actual) == 0 || actual[len(actual)-1] != state {
		return fmt.Errorf("%w: settled native workflow checks %v, want terminal %s", harness.ErrStep, actual, state)
	}
	matched := 0
	for _, check := range checks.snapshots(false) {
		if check.Name == "infraweave/infra" && check.SHA == c.w.IDs["seed"] && strings.HasSuffix(check.URL, "/runs/"+c.w.IDs[run]) {
			if check.ID == "" || check.State != state || !strings.Contains(check.Summary, "1 added") {
				return fmt.Errorf("%w: native workflow check lost its identity or real tool change summary", harness.ErrStep)
			}
			matched++
		}
	}
	if matched != 1 {
		return fmt.Errorf("%w: deduplicated proposal has %d current native checks", harness.ErrStep, matched)
	}
	if len(c.records(strconv.Itoa(request))) != 1 {
		return fmt.Errorf("%w: deduplicated proposal did not produce exactly one native plan comment", harness.ErrStep)
	}
	return c.native(run, "FINISHED", request)
}
