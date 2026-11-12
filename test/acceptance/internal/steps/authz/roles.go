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

// roleSteps drive the Roles tag.
type roleSteps struct{ o *orgSteps }

func (s *roleSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^token "(\w+)"'s read of the role "([a-z0-9-]+)" through ([AB]) holds every assignable action$`,
		s.holdsAll,
	)
	sc.Step(`^the problem refuses the field "([^"]+)" naming "([A-Z_]+)"$`, s.names)
}

// holdsAll checks that the role slug, read with token name, holds every
// action of the catalogue that is neither disabled nor deprecated, in
// the catalogue's order.
func (s *roleSteps) holdsAll(ctx context.Context, name, slug, replica string) error {
	c, err := s.o.a.do(ctx, replica, name, http.MethodGet, "/api/v1/actions", nil)
	if err != nil {
		return err
	}
	var catalogue []struct {
		Action     string `json:"action"`
		Deprecated bool   `json:"deprecated"`
		Disabled   bool   `json:"disabled"`
	}
	if json.Unmarshal(c.body, &catalogue) != nil || c.status != http.StatusOK {
		return fmt.Errorf("%w: no catalogue: %d %.200s", errStep, c.status, c.body)
	}
	want := []string{}
	for _, a := range catalogue {
		if !a.Deprecated && !a.Disabled {
			want = append(want, a.Action)
		}
	}
	r, err := s.o.a.do(ctx, replica, name, http.MethodGet, "/api/v1/roles/"+slug, nil)
	if err != nil {
		return err
	}
	var role struct {
		Actions []string `json:"actions"`
	}
	if json.Unmarshal(r.body, &role) != nil || r.status != http.StatusOK {
		return fmt.Errorf("%w: no role %s: %d %s", errStep, slug, r.status, r.body)
	}
	if !reflect.DeepEqual(role.Actions, want) {
		return fmt.Errorf("%w: %s holds %d actions, want the %d assignable", errStep, slug,
			len(role.Actions), len(want))
	}
	return nil
}

// names checks that the last answer refuses the field pointer with a
// message naming action.
func (s *roleSteps) names(pointer, action string) error {
	var p struct {
		Errors []struct {
			Pointer string `json:"pointer"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	r := s.o.a.s.last()
	if json.Unmarshal(r.body, &p) == nil {
		for _, e := range p.Errors {
			if e.Pointer == pointer && strings.Contains(e.Message, action) {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: no error at %s naming %s: %d %s", errStep, pointer, action, r.status,
		r.body)
}
