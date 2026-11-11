package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"
)

// runSteps read runs, against the run IDs the scenario kept with "the
// answer's id names", which its requests name as {key}.
type runSteps struct{ o *orgSteps }

func (r *runSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^token "(\w+)" eventually reads the run "(\w+)" in the state "([A-Z_]+)" through ([AB])$`,
		r.eventually,
	)
	sc.Step(`^the answer's (items|edges) are the runs "([^"]*)"$`, r.lists)
}

// eventually reads the run kept as key until it is in state, which the
// scheduler's job moves it to after the write that queued it commits.
func (r *runSteps) eventually(ctx context.Context, token, key, state, replica string) error {
	//nolint:wrapcheck // names what it waited for
	return Eventually(ctx, "run "+key+" to be "+state, func(ctx context.Context) (bool, error) {
		if err := r.o.send(ctx, `token "`+token+`"`, "GET", "/api/v1/runs/"+r.o.ids[key], "",
			replica); err != nil {
			return false, err
		}
		var run struct {
			State string `json:"state"`
		}
		last := r.o.a.s.last()
		if err := json.Unmarshal(last.body, &run); err != nil || last.status != 200 {
			return false, fmt.Errorf("%w: no run in %d %s", errStep, last.status, last.body)
		}
		return run.State == state, nil
	})
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
	last := r.o.a.s.last()
	if err := json.Unmarshal(last.body, &page); err != nil {
		return fmt.Errorf("%w: no runs in %d %s", errStep, last.status, last.body)
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
		for key, kept := range r.o.ids {
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
		return fmt.Errorf("%w: the runs are %q, not %q", errStep, got, wanted)
	}
	return nil
}
