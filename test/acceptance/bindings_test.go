package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/cucumber/godog"
)

// bindingSteps drive the role binding operations.
type bindingSteps struct{ o *orgSteps }

func (s *bindingSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^(the browser|token "\w+")'s read of the space "([^"]+)" through ([AB]) allows exactly the role "([a-z0-9-]+)"'s actions$`,
		s.allowsExactly,
	)
	sc.Step(`^the answer lists (\d+) bindings?$`, s.count)
	sc.Step(`^the answer's binding of the role "([a-z0-9-]+)" names "(\w+)"$`, s.keep)
	sc.Step(`^the problem refuses the field "([^"]+)"$`, s.refuses)
}

// allowsExactly checks that the space's allowedActions, read by who, are
// the role slug's actions, in the catalogue's order.
func (s *bindingSteps) allowsExactly(ctx context.Context, who, slug, replica, role string) error {
	if err := s.o.send(ctx, who, http.MethodGet, "/api/v1/roles/"+role, "", replica); err != nil {
		return err
	}
	var r struct {
		Actions []string `json:"actions"`
	}
	if last := s.o.a.s.last(); json.Unmarshal(last.body, &r) != nil ||
		last.status != http.StatusOK {
		return fmt.Errorf("%w: no role %s: %d %s", errStep, role, last.status, last.body)
	}
	if err := s.o.send(ctx, who, http.MethodGet, "/api/v1/spaces/"+slug, "", replica); err != nil {
		return err
	}
	var sp struct {
		AllowedActions []string `json:"allowedActions"`
	}
	last := s.o.a.s.last()
	if json.Unmarshal(last.body, &sp) != nil || last.status != http.StatusOK {
		return fmt.Errorf("%w: no space %s: %d %s", errStep, slug, last.status, last.body)
	}
	if !slices.Equal(sp.AllowedActions, r.Actions) {
		return fmt.Errorf("%w: the space allows %v, the role holds %v", errStep,
			sp.AllowedActions, r.Actions)
	}
	return nil
}

// count checks that the last answer is an array of n.
func (s *bindingSteps) count(n int) error {
	var items []json.RawMessage
	last := s.o.a.s.last()
	if json.Unmarshal(last.body, &items) != nil || len(items) != n {
		return fmt.Errorf("%w: want %d bindings: %d %s", errStep, n, last.status, last.body)
	}
	return nil
}

// refuses checks that the last answer refuses the field pointer.
func (s *bindingSteps) refuses(pointer string) error {
	var p struct {
		Errors []struct {
			Pointer string `json:"pointer"`
		} `json:"errors"`
	}
	last := s.o.a.s.last()
	if json.Unmarshal(last.body, &p) == nil {
		for _, e := range p.Errors {
			if e.Pointer == pointer {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: no refusal of %s: %d %s", errStep, pointer, last.status, last.body)
}

// keep names the binding of the role slug in the last answer, a list of
// bindings, as name, for later paths to name.
func (s *bindingSteps) keep(slug, name string) error {
	var items []struct {
		ID     string `json:"id"`
		RoleID string `json:"roleID"` //nolint:tagliatelle // the API's name
	}
	last := s.o.a.s.last()
	if json.Unmarshal(last.body, &items) == nil {
		for _, b := range items {
			if b.RoleID == slug {
				s.o.ids[name] = b.ID
				return nil
			}
		}
	}
	return fmt.Errorf("%w: no binding of %s: %d %s", errStep, slug, last.status, last.body)
}
