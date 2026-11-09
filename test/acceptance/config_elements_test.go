package acceptance

import (
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
