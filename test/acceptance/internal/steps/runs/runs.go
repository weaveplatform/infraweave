package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// runSteps read runs, against the run IDs the scenario kept with "the
// answer's id names", which its requests name as {key}.
type runSteps struct {
	w *harness.World
}

func (r *runSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^token "(\w+)" eventually reads the run "(\w+)" in the state "([A-Z_]+)" through ([AB])$`,
		r.eventually,
	)
	sc.Step(`^the answer's (items|edges) are the runs "([^"]*)"$`, r.lists)
	sc.Step(`^two infraweave servers, A and B, on one database, that run no default-queue jobs$`,
		r.idle)
	sc.Step(`^a third infraweave server runs the default queue's jobs$`, r.third)
	sc.Step(`^the "run.schedule" jobs are (\d+), (pending|succeeded)$`, r.scheduled)
}

// idle starts the scenario's two servers with no worker on the default
// queue, so the run.schedule jobs a burst of triggers enqueues wait until
// a server that runs them starts.
func (r *runSteps) idle(ctx context.Context) error {
	w := r.w
	w.Settings["queue"] = map[string]any{
		"queues": map[string]any{"default": map[string]any{"workers": 0}},
	}
	defer delete(w.Settings, "queue")
	return r.w.TwoReplicas(ctx)
}

// third starts a server with the default queue's workers on the
// scenario's database.
func (r *runSteps) third(ctx context.Context) error {
	_, err := r.w.StartServer(ctx, nil)
	return err
}

// scheduled waits until every run.schedule job is in state and checks
// that there are want of them, so a burst that coalesced into one job
// shows as one, and one that did not as more.
func (r *runSteps) scheduled(ctx context.Context, want int, state string) error {
	var got string
	err := deps.Eventually(ctx, "run.schedule jobs to be "+state,
		func(ctx context.Context) (bool, error) {
			out, err := r.w.Query(ctx, `SELECT count(*) FILTER (WHERE state = `+
				harness.Literal(state)+`) || ' ' || count(*) FROM jobs WHERE kind = 'run.schedule'`)
			got = strings.TrimSpace(out)
			n := strings.Fields(got)
			return err == nil && len(n) == 2 && n[0] == n[1], err
		})
	if err != nil {
		return err //nolint:wrapcheck // names what it waited for
	}
	if want := fmt.Sprintf("%d %d", want, want); got != want {
		return fmt.Errorf(
			"%w: run.schedule jobs %s are %q, want %q",
			harness.ErrStep,
			state,
			got,
			want,
		)
	}
	return nil
}

// eventually reads the run kept as key until it is in state, which the
// scheduler's job moves it to after the write that queued it commits.
func (r *runSteps) eventually(ctx context.Context, token, key, state, replica string) error {
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(
		ctx,
		"run "+key+" to be "+state,
		func(ctx context.Context) (bool, error) {
			if err := r.w.SendAs(ctx, `token "`+token+`"`, "GET", "/api/v1/runs/"+r.w.IDs[key], "",
				replica); err != nil {
				return false, err
			}
			var run struct {
				State string `json:"state"`
			}
			last := r.w.Last()
			if err := json.Unmarshal(last.Body, &run); err != nil || last.Status != 200 {
				return false, fmt.Errorf(
					"%w: no run in %d %s",
					harness.ErrStep,
					last.Status,
					last.Body,
				)
			}
			return run.State == state, nil
		},
	)
}

// lists checks the last answer, a page of runs or a run search, against
// want: the keys of the runs it holds, comma-separated, in its order.
func (r *runSteps) lists(field, want string) error {
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Edges []struct {
			Node struct {
				Run struct {
					ID string `json:"id"`
				} `json:"run"`
			} `json:"node"`
		} `json:"edges"`
	}
	last := r.w.Last()
	if err := json.Unmarshal(last.Body, &page); err != nil {
		return fmt.Errorf("%w: no runs in %d %s", harness.ErrStep, last.Status, last.Body)
	}
	var ids []string
	for _, it := range page.Items {
		ids = append(ids, it.ID)
	}
	if field == "edges" {
		for _, e := range page.Edges {
			ids = append(ids, e.Node.Run.ID)
		}
	}
	got := make([]string, len(ids))
	for i, id := range ids {
		got[i] = id
		for key, kept := range r.w.IDs {
			if kept == id {
				got[i] = key
			}
		}
	}
	wanted := []string{}
	if want != "" {
		wanted = strings.Split(want, ", ")
	}
	if !slices.Equal(got, wanted) {
		return fmt.Errorf("%w: the runs are %q, not %q", harness.ErrStep, got, wanted)
	}
	return nil
}
