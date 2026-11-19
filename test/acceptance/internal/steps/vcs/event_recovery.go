package vcs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func (s *eventSteps) registerRecovery(sc *godog.ScenarioContext) {
	sc.Step(`^two event replicas where only A consumes the VCS inbox$`, s.replicas)
	sc.Step(`^B acknowledges event "(\w+)" from "(\w+)" to "(\w+)" on "([^"]+)" and dies before A processes it$`, s.recover)
	sc.Step(`^the event fixture opens a request on commit "(\w+)" through ([AB])$`, func(ctx context.Context, commit, replica string) error {
		return s.comments.sendRequest(ctx, 12, "open", commit, replica)
	})
}

func (s *eventSteps) replicas(ctx context.Context) error {
	w := s.comments.w
	if _, err := w.StartServer(ctx, nil); err != nil {
		return err
	}
	previous := w.Settings["queue"]
	w.Settings["queue"] = map[string]any{"queues": map[string]any{"vcs-inbox": map[string]any{"workers": 0}}}
	defer func() {
		if previous == nil {
			delete(w.Settings, "queue")
		} else {
			w.Settings["queue"] = previous
		}
	}()
	_, err := w.StartServer(ctx, nil)
	return err
}

// Pause A before intake, while B has no inbox consumer, to prove the
// acknowledged job survives B without racing the workers or waiting on a lease.
func (s *eventSteps) recover(ctx context.Context, name, before, head, branch string) (err error) {
	w := s.comments.w
	pid := strconv.Itoa(w.Servers[0].Pid())
	if err := exec.CommandContext(ctx, "kill", "-STOP", pid).Run(); err != nil {
		return fmt.Errorf("pause event replica A: %w", err)
	}
	defer func() {
		if resumeErr := exec.CommandContext(context.WithoutCancel(ctx), "kill", "-CONT", pid).Run(); err == nil && resumeErr != nil {
			err = fmt.Errorf("resume event replica A: %w", resumeErr)
		}
	}()
	if err = s.push(ctx, name, before, head, branch, "valid", "B", 202); err != nil {
		return err
	}
	if err = s.rows(ctx, name, 1); err != nil {
		return err
	}
	var received bool
	if err = w.Row(
		ctx,
		`SELECT state='received' AND runs_created=0 FROM vcs_deliveries WHERE integration_id=$1 AND delivery_id=$2`,
		[]any{harness.UUIDOf(s.comments.fixture.integration), s.deliveries[name]},
		&received,
	); err != nil {
		return err
	}
	if !received {
		return fmt.Errorf("%w: event was processed before B died", harness.ErrStep)
	}
	return w.Servers[1].Signal(os.Kill)
}
