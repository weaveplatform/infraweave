package acceptance

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"
)

// expressionSteps read what the editor endpoints answer.
type expressionSteps struct{ o *orgSteps }

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
	r := e.o.a.s.last()
	if json.Unmarshal(r.body, &chk) == nil {
		for _, d := range chk.Diagnostics {
			if d.Severity == "error" && d.Range.Start == (position{l1, c1}) &&
				d.Range.End == (position{l2, c2}) {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: no error from %d:%d to %d:%d: %d %s",
		errStep, l1, c1, l2, c2, r.status, r.body)
}

// completes checks that the last answer, a completion, holds exactly the
// labels want, comma-separated, in its order.
func (e *expressionSteps) completes(want string) error {
	var res struct {
		Items []struct {
			Label string `json:"label"`
		} `json:"items"`
	}
	r := e.o.a.s.last()
	labels := []string{}
	if json.Unmarshal(r.body, &res) == nil {
		for _, i := range res.Items {
			labels = append(labels, i.Label)
		}
	}
	if !slices.Equal(labels, strings.Split(want, ", ")) {
		return fmt.Errorf(
			"%w: completes %q, want %q: %d %s",
			errStep,
			labels,
			want,
			r.status,
			r.body,
		)
	}
	return nil
}
