package expressions

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// expressionSteps read what the editor endpoints answer.
type expressionSteps struct {
	w *harness.World
}

func (e *expressionSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer flags an error from (\d+):(\d+) to (\d+):(\d+)$`, e.flags)
	sc.Step(`^the answer completes "([^"]*)"$`, e.completes)
}

// position is a place in source text, as the API answers it.
type position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// flags checks the last answer, a check, for an error diagnostic whose
// range runs from one line and column up to another.
func (e *expressionSteps) flags(l1, c1, l2, c2 int) error {
	var chk struct {
		Diagnostics []struct {
			Severity string `json:"severity"`
			Range    struct {
				Start position `json:"start"`
				End   position `json:"end"`
			} `json:"range"`
		} `json:"diagnostics"`
	}
	r := e.w.Last()
	if json.Unmarshal(r.Body, &chk) == nil {
		for _, d := range chk.Diagnostics {
			if d.Severity == "error" && d.Range.Start == (position{l1, c1}) &&
				d.Range.End == (position{l2, c2}) {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: no error from %d:%d to %d:%d: %d %s",
		harness.ErrStep, l1, c1, l2, c2, r.Status, r.Body)
}

// completes checks that the last answer, a completion, holds exactly the
// labels want, comma-separated, in its order.
func (e *expressionSteps) completes(want string) error {
	var res struct {
		Items []struct {
			Label string `json:"label"`
		} `json:"items"`
	}
	r := e.w.Last()
	labels := []string{}
	if json.Unmarshal(r.Body, &res) == nil {
		for _, i := range res.Items {
			labels = append(labels, i.Label)
		}
	}
	if !slices.Equal(labels, strings.Split(want, ", ")) {
		return fmt.Errorf(
			"%w: completes %q, want %q: %d %s",
			harness.ErrStep,
			labels,
			want,
			r.Status,
			r.Body,
		)
	}
	return nil
}
