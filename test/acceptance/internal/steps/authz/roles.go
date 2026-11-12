package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// roleSteps drive the Roles tag.
type roleSteps struct {
	w *harness.World
}

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
	c, err := s.w.Do(ctx, replica, name, http.MethodGet, "/api/v1/actions", nil)
	if err != nil {
		return err
	}
	var catalogue []struct {
		Action     string `json:"action"`
		Deprecated bool   `json:"deprecated"`
		Disabled   bool   `json:"disabled"`
	}
	if json.Unmarshal(c.Body, &catalogue) != nil || c.Status != http.StatusOK {
		return fmt.Errorf("%w: no catalogue: %d %.200s", harness.ErrStep, c.Status, c.Body)
	}
	want := []string{}
	for _, a := range catalogue {
		if !a.Deprecated && !a.Disabled {
			want = append(want, a.Action)
		}
	}
	r, err := s.w.Do(ctx, replica, name, http.MethodGet, "/api/v1/roles/"+slug, nil)
	if err != nil {
		return err
	}
	var role struct {
		Actions []string `json:"actions"`
	}
	if json.Unmarshal(r.Body, &role) != nil || r.Status != http.StatusOK {
		return fmt.Errorf("%w: no role %s: %d %s", harness.ErrStep, slug, r.Status, r.Body)
	}
	if !reflect.DeepEqual(role.Actions, want) {
		return fmt.Errorf("%w: %s holds %d actions, want the %d assignable", harness.ErrStep, slug,
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
	r := s.w.Last()
	if json.Unmarshal(r.Body, &p) == nil {
		for _, e := range p.Errors {
			if e.Pointer == pointer && strings.Contains(e.Message, action) {
				return nil
			}
		}
	}
	return fmt.Errorf(
		"%w: no error at %s naming %s: %d %s",
		harness.ErrStep,
		pointer,
		action,
		r.Status,
		r.Body,
	)
}
