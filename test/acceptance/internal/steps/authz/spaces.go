package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// spaceSteps drive the Spaces tag. A created space's slug ends in a new
// ULID, so a scenario names it {name} once its creation is answered, as
// orgSteps resolves members.
//
// ref: decision 0007
type spaceSteps struct {
	w *harness.World
}

func (s *spaceSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's id is the space "(\w+)"$`, s.remember)
	sc.Step(
		`^the answer lists the (?:spaces|stacks|contexts|roles|integrations|worker pools) "([^"]*)"$`,
		s.lists,
	)
	sc.Step(
		`^the member key is ([a-z0-9-]+) on the space "([^"]+)"$`,
		s.bind,
	)
	sc.Step(
		`^token "(\w+)"'s read of the space "([^"]+)" through ([AB]) allows what the viewer lists for it$`,
		s.matchesViewer,
	)
	sc.Step(
		`^the account "([a-z-]+)" has its top space, legacy below it, the system roles and "(\w+)" as space-admin on its top space$`,
		s.seeded,
	)
	sc.Step(
		`^token "(\w+)" searching the spaces through ([AB]) in pages of (\d+) finds "([^"]*)", every page full but the last$`,
		s.searches,
	)
	sc.Step(`^the answer suggests the ids "([^"]*)" of (\d+) spaces?$`, s.suggests)
	sc.Step(`^the problem refuses the field "([^"]+)" as "([a-z-]+)"$`, s.refuses)
	sc.Step(`^a caller with no token sends "([A-Z]+) ([^"]+)" through ([AB])$`, s.anonymous)
	sc.Step(
		`^the answer lists (\d+) actions, "([A-Z_]+)" deprecated and "([A-Z_]+)" disabled$`,
		s.catalogue,
	)
}

// remember names the last answer's space name.
func (s *spaceSteps) remember(name string) error {
	var sp struct {
		ID string `json:"id"`
	}
	r := s.w.Last()
	if json.Unmarshal(r.Body, &sp) != nil || sp.ID == "" {
		return fmt.Errorf("%w: no space in %d %s", harness.ErrStep, r.Status, r.Body)
	}
	s.w.IDs[name] = sp.ID
	return nil
}

// lists checks the last answer, a page or a search of spaces, stacks,
// contexts or roles, against want's slugs, comma-separated, in order.
func (s *spaceSteps) lists(want string) error {
	type ref struct {
		ID string `json:"id"`
	}
	var page struct {
		Items []ref `json:"items"`
		Edges []struct {
			Node ref `json:"node"`
		} `json:"edges"`
	}
	r := s.w.Last()
	if err := json.Unmarshal(r.Body, &page); err != nil || r.Status != http.StatusOK {
		return fmt.Errorf("%w: not a page: %d %s", harness.ErrStep, r.Status, r.Body)
	}
	got := []string{}
	for _, it := range page.Items {
		got = append(got, it.ID)
	}
	for _, e := range page.Edges {
		got = append(got, e.Node.ID)
	}
	if w := s.w.SlugList(want); !reflect.DeepEqual(got, w) {
		return fmt.Errorf("%w: the page lists %v, want %v", harness.ErrStep, got, w)
	}
	return nil
}

// bind binds the member key to role on the space slug, as a binding
// operation will, and marks the account's authorization changed.
func (s *spaceSteps) bind(ctx context.Context, role, slug string) error {
	acct := harness.UUIDOf(harness.Member.Account)
	_, err := s.w.Query(ctx, fmt.Sprintf(
		`
		INSERT INTO role_bindings (account_id, id, actor_kind, actor_id, role_id, space_id, origin,
				created_by)
			SELECT '%[1]s', gen_random_uuid(), 'api_key', '%[2]s', r.id, sp.id, 'binding', 'acceptance'
			FROM roles r, spaces sp
			WHERE r.account_id = '%[1]s' AND r.slug = %[3]s
				AND sp.account_id = '%[1]s' AND sp.slug = %[4]s;%[5]s`,
		acct,
		harness.UUIDOf(harness.Member.Key),
		harness.Literal(role),
		harness.Literal(s.w.Resolve(slug)),
		harness.AuthzChanged(harness.Member.Account),
	))
	return err
}

// matchesViewer checks that the space's allowedActions, read with token
// name, are the actions the viewer lists for the space.
func (s *spaceSteps) matchesViewer(ctx context.Context, name, slug, replica string) error {
	slug = s.w.Resolve(slug)
	r, err := s.w.Do(ctx, replica, name, http.MethodGet, "/api/v1/spaces/"+slug, nil)
	if err != nil {
		return err
	}
	var sp struct {
		AllowedActions []string `json:"allowedActions"`
	}
	if json.Unmarshal(r.Body, &sp) != nil || r.Status != http.StatusOK {
		return fmt.Errorf("%w: no space %s: %d %s", harness.ErrStep, slug, r.Status, r.Body)
	}
	v, err := s.w.Do(ctx, replica, name, http.MethodGet, "/api/v1/viewer", nil)
	if err != nil {
		return err
	}
	var u struct {
		Actions []struct {
			SpaceID string   `json:"spaceID"` //nolint:tagliatelle // the reference's SpaceActions field
			Actions []string `json:"actions"`
		} `json:"actions"`
	}
	if json.Unmarshal(v.Body, &u) != nil || v.Status != http.StatusOK {
		return fmt.Errorf("%w: no viewer: %d %s", harness.ErrStep, v.Status, v.Body)
	}
	for _, a := range u.Actions {
		if a.SpaceID == slug {
			if !reflect.DeepEqual(a.Actions, sp.AllowedActions) || len(a.Actions) == 0 {
				return fmt.Errorf("%w: the space allows %v, the viewer lists %v",
					harness.ErrStep, sp.AllowedActions, a.Actions)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: the viewer lists no actions on %s: %s", harness.ErrStep, slug, v.Body)
}

// searches pages through searchSpaces with token name, n at a time,
// following each page's endCursor, and checks the spaces it finds, in
// order, and that no page before the last holds fewer than n.
func (s *spaceSteps) searches(ctx context.Context, name, replica string, n int, want string) error {
	got := []string{}
	after := ""
	for range 20 {
		in := map[string]any{"first": n}
		if after != "" {
			in["after"] = after
		}
		r, err := s.w.Do(ctx, replica, name, http.MethodPost, "/api/v1/spaces/search", in)
		if err != nil {
			return err
		}
		var page struct {
			Edges []struct {
				Node struct {
					ID string `json:"id"`
				} `json:"node"`
			} `json:"edges"`
			PageInfo struct {
				EndCursor   string `json:"endCursor"`
				HasNextPage bool   `json:"hasNextPage"`
			} `json:"pageInfo"`
		}
		if json.Unmarshal(r.Body, &page) != nil || r.Status != http.StatusOK {
			return fmt.Errorf("%w: not a page of spaces: %d %s", harness.ErrStep, r.Status, r.Body)
		}
		for _, e := range page.Edges {
			got = append(got, e.Node.ID)
		}
		if !page.PageInfo.HasNextPage {
			if w := s.w.SlugList(want); !reflect.DeepEqual(got, w) {
				return fmt.Errorf("%w: the search found %v, want %v", harness.ErrStep, got, w)
			}
			return nil
		}
		if len(page.Edges) != n {
			return fmt.Errorf("%w: a page before the last holds %d spaces: %s", harness.ErrStep,
				len(page.Edges), r.Body)
		}
		after = page.PageInfo.EndCursor
	}
	return fmt.Errorf("%w: the search did not end after 20 pages", harness.ErrStep)
}

// suggests checks the last answer, the suggestions of the id field, for
// the slugs it lists and the spaces it counts.
func (s *spaceSteps) suggests(want string, count int) error {
	var out struct {
		Fields []struct {
			Name                 string `json:"name"`
			PossibleValuesString *struct {
				Values []string `json:"values"`
			} `json:"possibleValuesString"`
		} `json:"fields"`
		FilteredCount int `json:"filteredCount"`
	}
	r := s.w.Last()
	if json.Unmarshal(r.Body, &out) != nil || r.Status != http.StatusOK {
		return fmt.Errorf("%w: not suggestions: %d %s", harness.ErrStep, r.Status, r.Body)
	}
	for _, f := range out.Fields {
		if f.Name != "id" || f.PossibleValuesString == nil {
			continue
		}
		got := slices.Clone(f.PossibleValuesString.Values)
		slices.Sort(got)
		w := s.w.SlugList(want)
		slices.Sort(w)
		if !reflect.DeepEqual(got, w) || out.FilteredCount != count {
			return fmt.Errorf("%w: suggested %v of %d, want %v of %d", harness.ErrStep, got,
				out.FilteredCount, w, count)
		}
		return nil
	}
	return fmt.Errorf("%w: no values of id: %s", harness.ErrStep, r.Body)
}

// refuses checks that the last answer, a problem, names the field at
// pointer with reason.
func (s *spaceSteps) refuses(pointer, reason string) error {
	var p struct {
		Errors []struct {
			Pointer string `json:"pointer"`
			Reason  string `json:"reason"`
		} `json:"errors"`
	}
	r := s.w.Last()
	if json.Unmarshal(r.Body, &p) == nil {
		for _, e := range p.Errors {
			if e.Pointer == pointer && e.Reason == reason {
				return nil
			}
		}
	}
	return fmt.Errorf(
		"%w: no %s error at %s: %d %s",
		harness.ErrStep,
		reason,
		pointer,
		r.Status,
		r.Body,
	)
}

// anonymous sends a request with no credential.
func (s *spaceSteps) anonymous(ctx context.Context, method, path, replica string) error {
	_, err := s.w.Do(ctx, replica, "", method, path, nil)
	return err
}

// catalogue checks the last answer, the action catalogue, for its size
// and two actions' flags.
func (s *spaceSteps) catalogue(n int, deprecated, disabled string) error {
	var out []struct {
		Action     string `json:"action"`
		Deprecated bool   `json:"deprecated"`
		Disabled   bool   `json:"disabled"`
	}
	r := s.w.Last()
	if json.Unmarshal(r.Body, &out) != nil || r.Status != http.StatusOK || len(out) != n {
		return fmt.Errorf(
			"%w: not a catalogue of %d actions: %d %.200s",
			harness.ErrStep,
			n,
			r.Status,
			r.Body,
		)
	}
	flags := map[string][2]bool{}
	for _, a := range out {
		flags[a.Action] = [2]bool{a.Deprecated, a.Disabled}
	}
	if flags[deprecated] != [2]bool{true, false} || flags[disabled] != [2]bool{false, true} {
		return fmt.Errorf("%w: %s is %v and %s %v", harness.ErrStep, deprecated, flags[deprecated],
			disabled, flags[disabled])
	}
	return nil
}

// seeded checks the tree, roles and first binding creating the account
// slug gave it, with admin the member it names.
func (s *spaceSteps) seeded(ctx context.Context, slug, admin string) error {
	out, err := s.w.Query(ctx, fmt.Sprintf(`
		WITH a AS (SELECT id FROM accounts WHERE slug = %[1]s)
		SELECT
			(SELECT string_agg(sp.slug || ':' || coalesce(p.slug, '-'), ',' ORDER BY sp.slug)
				FROM spaces sp LEFT JOIN spaces p ON p.id = sp.parent_id
				WHERE sp.account_id = (SELECT id FROM a))
			|| ' ' ||
			(SELECT string_agg(slug, ',' ORDER BY slug) FROM roles
				WHERE account_id = (SELECT id FROM a) AND is_system)
			|| ' ' ||
			(SELECT count(*) FROM role_bindings b JOIN roles r ON r.id = b.role_id
				WHERE b.account_id = (SELECT id FROM a) AND b.space_id = (SELECT id FROM a)
					AND r.slug = 'space-admin' AND b.actor_kind = 'user'
					AND b.actor_id = '%[2]s')`,
		harness.Literal(slug), harness.UUIDOf(s.w.IDs[admin])))
	if err != nil {
		return err
	}
	want := fmt.Sprintf("%[1]s:-,legacy:%[1]s "+
		"drift-detection-manager,space-admin,space-reader,space-writer,worker-pool-controller 1", slug)
	if got := strings.TrimSpace(out); got != want {
		return fmt.Errorf("%w: account %s holds %q, want %q", harness.ErrStep, slug, got, want)
	}
	return nil
}
