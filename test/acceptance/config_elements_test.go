package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/cucumber/godog"
)

// configSteps read the config elements an answer carries.
type configSteps struct{ o *orgSteps }

func (c *configSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's config lists the elements "([^"]*)"$`, c.lists)
	sc.Step(
		`^the stack "([^"]+)" read through ([AB]) with token "(\w+)" eventually attaches the contexts "([^"]*)"$`,
		c.attaches,
	)
}

// attaches waits for the stack slug, read through replica, to attach the
// contexts want, comma-separated, in the stack view's order, each it
// attaches automatically marked "auto:".
func (c *configSteps) attaches(ctx context.Context, slug, replica, tok, want string) error {
	w := []string{}
	if want != "" {
		w = strings.Split(want, ", ")
	}
	var got []string
	err := Eventually(
		ctx,
		"stack "+slug+" to attach "+want,
		func(ctx context.Context) (bool, error) {
			r, err := c.o.a.do(ctx, replica, tok, http.MethodGet, "/api/v1/stacks/"+slug, nil)
			if err != nil {
				return false, err
			}
			var v struct {
				AttachedContexts []struct {
					ContextID      string `json:"contextId"`
					IsAutoattached bool   `json:"isAutoattached"`
				} `json:"attachedContexts"`
			}
			if err := json.Unmarshal(r.body, &v); err != nil || r.status != http.StatusOK {
				return false, fmt.Errorf("%w: not a stack: %d %s", errStep, r.status, r.body)
			}
			got = []string{}
			for _, a := range v.AttachedContexts {
				if a.IsAutoattached {
					got = append(got, "auto:"+a.ContextID)
				} else {
					got = append(got, a.ContextID)
				}
			}
			return reflect.DeepEqual(got, w), nil
		},
	)
	if err != nil {
		return fmt.Errorf("%w (last %v)", err, got)
	}
	return nil
}

// lists checks the last answer, a stack read or created, for the IDs of its own config
// elements, comma-separated, in order.
func (c *configSteps) lists(want string) error {
	var v struct {
		Config []struct {
			ID string `json:"id"`
		} `json:"config"`
	}
	r := c.o.a.s.last()
	if err := json.Unmarshal(
		r.body,
		&v,
	); err != nil ||
		r.status != http.StatusOK && r.status != http.StatusCreated {
		return fmt.Errorf("%w: not a stack: %d %s", errStep, r.status, r.body)
	}
	got := []string{}
	for _, e := range v.Config {
		got = append(got, e.ID)
	}
	w := []string{}
	if want != "" {
		w = strings.Split(want, ", ")
	}
	if !reflect.DeepEqual(got, w) {
		return fmt.Errorf("%w: the config lists %v, want %v", errStep, got, w)
	}
	return nil
}
