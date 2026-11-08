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

// spaceSteps drive the Spaces tag (decision 0007 § "Spaces"). A created
// space's slug ends in a new ULID, so a scenario names it {name} once its
// creation is answered, as orgSteps resolves members.
type spaceSteps struct{ o *orgSteps }

func (s *spaceSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's id is the space "(\w+)"$`, s.remember)
	sc.Step(`^the answer lists the spaces "([^"]*)"$`, s.lists)
	sc.Step(
		`^the member key is (space-reader|space-writer|space-admin) on the space "([^"]+)"$`,
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
}

// remember names the last answer's space name.
func (s *spaceSteps) remember(name string) error {
	var sp struct {
		ID string `json:"id"`
	}
	r := s.o.a.s.last()
	if json.Unmarshal(r.body, &sp) != nil || sp.ID == "" {
		return fmt.Errorf("%w: no space in %d %s", errStep, r.status, r.body)
	}
	s.o.ids[name] = sp.ID
	return nil
}

// lists checks the last answer, a page of spaces, against want's slugs,
// comma-separated, in order.
func (s *spaceSteps) lists(want string) error {
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	r := s.o.a.s.last()
	if err := json.Unmarshal(r.body, &page); err != nil || r.status != http.StatusOK {
		return fmt.Errorf("%w: not a page of spaces: %d %s", errStep, r.status, r.body)
	}
	got := []string{}
	for _, it := range page.Items {
		got = append(got, it.ID)
	}
	w := []string{}
	if want = s.o.resolve(want); want != "" {
		for slug := range strings.SplitSeq(want, ",") {
			w = append(w, strings.TrimSpace(slug))
		}
	}
	if !reflect.DeepEqual(got, w) {
		return fmt.Errorf("%w: the spaces are %v, want %v", errStep, got, w)
	}
	return nil
}

// bind binds the member key to role on the space slug, as a binding
// operation will, and marks the account's authorization changed.
func (s *spaceSteps) bind(ctx context.Context, role, slug string) error {
	acct := uuidOf(member.account)
	_, err := s.o.a.s.query(ctx, fmt.Sprintf(`
		INSERT INTO role_bindings (account_id, id, actor_kind, actor_id, role_id, space_id, origin,
				created_by)
			SELECT '%[1]s', gen_random_uuid(), 'api_key', '%[2]s', r.id, sp.id, 'binding', 'acceptance'
			FROM roles r, spaces sp
			WHERE r.account_id = '%[1]s' AND r.slug = %[3]s
				AND sp.account_id = '%[1]s' AND sp.slug = %[4]s;%[5]s`,
		acct, uuidOf(member.key), literal(role), literal(s.o.resolve(slug)),
		authzChanged(member.account)))
	return err
}

// matchesViewer checks that the space's allowedActions, read with token
// name, are the actions the viewer lists for the space.
func (s *spaceSteps) matchesViewer(ctx context.Context, name, slug, replica string) error {
	slug = s.o.resolve(slug)
	r, err := s.o.a.do(ctx, replica, name, http.MethodGet, "/api/v1/spaces/"+slug, nil)
	if err != nil {
		return err
	}
	var sp struct {
		AllowedActions []string `json:"allowedActions"`
	}
	if json.Unmarshal(r.body, &sp) != nil || r.status != http.StatusOK {
		return fmt.Errorf("%w: no space %s: %d %s", errStep, slug, r.status, r.body)
	}
	v, err := s.o.a.do(ctx, replica, name, http.MethodGet, "/api/v1/viewer", nil)
	if err != nil {
		return err
	}
	var u struct {
		Actions []struct {
			SpaceID string   `json:"spaceID"` //nolint:tagliatelle // the reference's SpaceActions field
			Actions []string `json:"actions"`
		} `json:"actions"`
	}
	if json.Unmarshal(v.body, &u) != nil || v.status != http.StatusOK {
		return fmt.Errorf("%w: no viewer: %d %s", errStep, v.status, v.body)
	}
	for _, a := range u.Actions {
		if a.SpaceID == slug {
			if !reflect.DeepEqual(a.Actions, sp.AllowedActions) || len(a.Actions) == 0 {
				return fmt.Errorf("%w: the space allows %v, the viewer lists %v",
					errStep, sp.AllowedActions, a.Actions)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: the viewer lists no actions on %s: %s", errStep, slug, v.body)
}

// seeded checks the tree, roles and first binding creating the account
// slug gave it, with admin the member it names.
func (s *spaceSteps) seeded(ctx context.Context, slug, admin string) error {
	out, err := s.o.a.s.query(ctx, fmt.Sprintf(`
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
		literal(slug), uuidOf(s.o.ids[admin])))
	if err != nil {
		return err
	}
	want := fmt.Sprintf("%[1]s:-,legacy:%[1]s "+
		"drift-detection-manager,space-admin,space-reader,space-writer,worker-pool-controller 1", slug)
	if got := strings.TrimSpace(out); got != want {
		return fmt.Errorf("%w: account %s holds %q, want %q", errStep, slug, got, want)
	}
	return nil
}
