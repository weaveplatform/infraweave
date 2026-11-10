package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// grantsWithin bounds how long a binding written through one replica takes
// to reach the other's decisions, which drop the account's snapshot on its
// authz:<account> message.
//
// ref: decision 0007 § "Evaluation"
const grantsWithin = time.Second

// bindingSteps drive the role binding operations.
type bindingSteps struct{ o *orgSteps }

func (s *bindingSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^(the browser|token "\w+")'s read of the space "([^"]+)" through ([AB]) allows exactly the role "([a-z0-9-]+)"'s actions$`,
		s.allowsExactly,
	)
	sc.Step(
		`^(the browser|token "\w+")'s read of the space "([^"]+)" through ([AB]) is not found$`,
		s.hidden,
	)
	sc.Step(`^the answer lists (\d+) bindings?$`, s.count)
	sc.Step(`^the answer's binding of the role "([a-z0-9-]+)" names "(\w+)"$`, s.keep)
	sc.Step(`^the problem refuses the field "([^"]+)"$`, s.refuses)
	sc.Step(`^the home account holds (\d+) stack role bindings?$`, s.stackRows)
}

// listed are the bindings of the last answer: a list, or a page's items.
func (s *bindingSteps) listed() ([]json.RawMessage, bool) {
	var items []json.RawMessage
	body := s.o.a.s.last().body
	if json.Unmarshal(body, &items) == nil {
		return items, true
	}
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	if json.Unmarshal(body, &page) == nil && page.Items != nil {
		return page.Items, true
	}
	return nil, false
}

// stackRows checks that the home account's role_bindings hold n rows of
// stacks, whichever stack each names.
func (s *bindingSteps) stackRows(ctx context.Context, n int) error {
	out, err := s.o.a.s.query(ctx, fmt.Sprintf(`SELECT count(*) FROM role_bindings
		WHERE account_id = '%s' AND actor_kind = 'stack'`, uuidOf(tenants["home"].account)))
	if err != nil {
		return err
	}
	if got, err := strconv.Atoi(strings.TrimSpace(out)); err != nil || got != n {
		return fmt.Errorf("%w: %q stack role bindings, want %d", errStep, out, n)
	}
	return nil
}

// allowsExactly waits at most grantsWithin for the space's allowedActions,
// read by who, to be the role slug's actions in the catalogue's order.
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
	return s.converge(ctx, "the role "+role+" to reach "+replica, func() error {
		if err := s.o.send(
			ctx,
			who,
			http.MethodGet,
			"/api/v1/spaces/"+slug,
			"",
			replica,
		); err != nil {
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
	})
}

// hidden waits at most grantsWithin for who's read of the space to answer
// a not-found problem.
func (s *bindingSteps) hidden(ctx context.Context, who, slug, replica string) error {
	return s.converge(ctx, "the space "+slug+" to leave "+replica, func() error {
		if err := s.o.send(
			ctx,
			who,
			http.MethodGet,
			"/api/v1/spaces/"+slug,
			"",
			replica,
		); err != nil {
			return err
		}
		return isProblem(s.o.a.s.last(), "the space", "404", "not-found")
	})
}

// converge polls check until it passes, for at most grantsWithin; a
// timeout carries check's last error.
func (s *bindingSteps) converge(ctx context.Context, what string, check func() error) error {
	ctx, cancel := context.WithTimeout(ctx, grantsWithin)
	defer cancel()
	return Eventually(ctx, what, func(context.Context) (bool, error) {
		err := check()
		return err == nil, err
	})
}

// count checks that the last answer is an array of n.
func (s *bindingSteps) count(n int) error {
	items, ok := s.listed()
	if last := s.o.a.s.last(); !ok || len(items) != n {
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

// keep names the binding of the role slug in the last answer, a list or
// a page of bindings, as name, for later paths to name. A binding names
// its role by roleID or by its role's id.
func (s *bindingSteps) keep(slug, name string) error {
	items, _ := s.listed()
	for _, raw := range items {
		var b struct {
			ID     string `json:"id"`
			RoleID string `json:"roleID"` //nolint:tagliatelle // the API's name
			Role   struct {
				ID string `json:"id"`
			} `json:"role"`
		}
		if json.Unmarshal(raw, &b) == nil && (b.RoleID == slug || b.Role.ID == slug) {
			s.o.ids[name] = b.ID
			return nil
		}
	}
	last := s.o.a.s.last()
	return fmt.Errorf("%w: no binding of %s: %d %s", errStep, slug, last.status, last.body)
}
