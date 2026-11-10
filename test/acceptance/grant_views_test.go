package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/cucumber/godog"
)

// grantViewSteps drive the views of who holds a space.
type grantViewSteps struct{ o *orgSteps }

// grantViews are each view's path by the name a step gives it.
var grantViews = map[string]string{
	"users":            "users-with-access-to-space",
	"user groups":      "user-groups-with-access-to-space",
	"API keys":         "api-keys-with-access-to-space",
	"user sessions":    "user-sessions-with-access-to-space",
	"API key sessions": "api-key-sessions-with-access-to-space",
}

func (s *grantViewSteps) register(sc *godog.ScenarioContext) {
	views := `(users|user groups|API keys|user sessions|API key sessions)`
	sc.Step(`^(the browser|token "\w+") lists the `+views+
		` with access to the space "([^"]+)"(?: searching "([^"]*)")?(?: ordered by "(\w+)")? through ([AB])$`, s.list)
	sc.Step(`^(the browser|token "\w+") asks for the suggestions of the `+views+
		` with access to the space "([^"]+)" through ([AB])$`, s.suggest)
	sc.Step(`^the answer lists (\d+) holders?$`, s.count)
	sc.Step(
		`^the holder "([^"]+)" holds the role "([a-z0-9-]+)" on the space "([^"]+)" as (RESIDENT|PROPAGATED|INHERITED)$`,
		s.holds,
	)
	sc.Step(
		`^the holder "([^"]+)" holds the role "([a-z0-9-]+)" through (\d+) live sessions?$`,
		s.sessions,
	)
	sc.Step(`^the suggestions count (\d+)$`, s.suggestions)
}

// list reads a page of the view of slug, keeping the rows that contain
// text and ordered by field when they are given.
func (s *grantViewSteps) list(
	ctx context.Context,
	who, view, slug, text, field, replica string,
) error {
	in := map[string]any{}
	if text != "" {
		in["fullTextSearch"] = text
	}
	if field != "" {
		in["orderBy"] = []map[string]string{{"field": field}}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("%w: %w", errStep, err)
	}
	q := url.Values{"space": {s.o.resolve(slug)}, "input": {string(raw)}}
	return s.o.send(
		ctx,
		who,
		http.MethodGet,
		"/api/v1/"+grantViews[view]+"?"+q.Encode(),
		"",
		replica,
	)
}

// suggest reads the suggestions of the view of slug.
func (s *grantViewSteps) suggest(ctx context.Context, who, view, slug, replica string) error {
	q := url.Values{"space": {s.o.resolve(slug)}, "input": {"{}"}}
	return s.o.send(ctx, who, http.MethodGet,
		"/api/v1/"+grantViews[view]+"-suggestions?"+q.Encode(), "", replica)
}

// grantRole is a grant as the views answer it.
type grantRole struct {
	Role struct {
		ID string `json:"id"`
	} `json:"role"`
	Space struct {
		ID string `json:"id"`
	} `json:"space"`
	Source string `json:"source"`
}

// holderNode is a row of any of the views: the actor it names and its
// grants.
type holderNode struct {
	User struct {
		ID    string `json:"id"`
		Login string `json:"login"`
	} `json:"user"`
	UserGroup struct {
		ID string `json:"id"`
	} `json:"userGroup"`
	APIKey struct {
		ID string `json:"id"`
	} `json:"apiKey"`
	Roles        []grantRole `json:"roles"`
	SessionCount int         `json:"sessionCount"`
}

// sessionsOf is how many sessions the row counts, which a user's row
// answers in its user.
func (n holderNode) sessionsOf(raw json.RawMessage) int {
	var u struct {
		User struct {
			SessionCount int `json:"sessionCount"`
		} `json:"user"`
	}
	if json.Unmarshal(raw, &u) == nil && u.User.SessionCount > 0 {
		return u.User.SessionCount
	}
	return n.SessionCount
}

// edges are the last answer's rows.
func (s *grantViewSteps) edges() ([]json.RawMessage, error) {
	var page struct {
		Edges []struct {
			Node json.RawMessage `json:"node"`
		} `json:"edges"`
	}
	last := s.o.a.s.last()
	if last.status != http.StatusOK || json.Unmarshal(last.body, &page) != nil {
		return nil, fmt.Errorf("%w: no page: %d %s", errStep, last.status, last.body)
	}
	out := make([]json.RawMessage, 0, len(page.Edges))
	for _, e := range page.Edges {
		out = append(out, e.Node)
	}
	return out, nil
}

// row is the last answer's row naming name, by ID or by login.
func (s *grantViewSteps) row(name string) (holderNode, json.RawMessage, error) {
	id := s.o.resolve(name)
	edges, err := s.edges()
	if err != nil {
		return holderNode{}, nil, err
	}
	for _, raw := range edges {
		var n holderNode
		if json.Unmarshal(raw, &n) != nil {
			continue
		}
		if id == n.User.ID || id == n.User.Login || id == n.UserGroup.ID || id == n.APIKey.ID {
			return n, raw, nil
		}
	}
	return holderNode{}, nil, fmt.Errorf("%w: no holder %s: %s", errStep, id, s.o.a.s.last().body)
}

func (s *grantViewSteps) count(n int) error {
	edges, err := s.edges()
	if err != nil {
		return err
	}
	if len(edges) != n {
		return fmt.Errorf(
			"%w: %d holders, want %d: %s",
			errStep,
			len(edges),
			n,
			s.o.a.s.last().body,
		)
	}
	return nil
}

// holds checks that name's row holds role on slug, reaching the space as
// source.
func (s *grantViewSteps) holds(name, role, slug, source string) error {
	n, _, err := s.row(name)
	if err != nil {
		return err
	}
	slug = s.o.resolve(slug)
	for _, g := range n.Roles {
		if g.Role.ID == role && g.Space.ID == slug && g.Source == source {
			return nil
		}
	}
	return fmt.Errorf(
		"%w: %s holds %+v, not %s on %s as %s",
		errStep,
		name,
		n.Roles,
		role,
		slug,
		source,
	)
}

// sessions checks that name's row holds role through n live sessions.
func (s *grantViewSteps) sessions(name, role string, n int) error {
	h, raw, err := s.row(name)
	if err != nil {
		return err
	}
	if got := h.sessionsOf(raw); got != n {
		return fmt.Errorf("%w: %s has %d sessions, want %d", errStep, name, got, n)
	}
	for _, g := range h.Roles {
		if g.Role.ID == role {
			return nil
		}
	}
	return fmt.Errorf("%w: %s holds %+v, not %s", errStep, name, h.Roles, role)
}

// suggestions checks the last answer's count.
func (s *grantViewSteps) suggestions(n int) error {
	var out struct {
		FilteredCount int `json:"filteredCount"`
	}
	last := s.o.a.s.last()
	if last.status != http.StatusOK || json.Unmarshal(last.body, &out) != nil ||
		out.FilteredCount != n {
		return fmt.Errorf("%w: want a count of %d: %d %s", errStep, n, last.status, last.body)
	}
	return nil
}
