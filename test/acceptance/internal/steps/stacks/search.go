package stacks

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// stackSearchSteps drive the stack search, which pages through
// searchStacks both ways and reads searchStacksSuggestions' values.
type stackSearchSteps struct {
	w *harness.World
}

func (st *stackSearchSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^token "(\w+)" searching the stacks(?: where '([^']*)')?(?: by (\w+)( descending)?)? `+
			`through ([AB]) in pages of (\d+) finds "([^"]*)" forwards and back$`,
		st.searches,
	)
	sc.Step(
		`^the answer suggests the (\w+) values "([^"]*)" of (\d+) (?:stacks?|contexts?|integrations?|worker pools?)$`,
		st.suggests,
	)
	sc.Step(
		`^token "(\w+)" searches the stacks by (\w+) through ([AB]) after a cursor of a search by (\w+)(, cut short)?$`,
		st.resumes,
	)
}

// stackPage is a page of searchStacks.
type stackPage struct {
	Edges []struct {
		Node struct {
			ID string `json:"id"`
		} `json:"node"`
	} `json:"edges"`
	PageInfo struct {
		StartCursor     string `json:"startCursor"`
		EndCursor       string `json:"endCursor"`
		HasNextPage     bool   `json:"hasNextPage"`
		HasPreviousPage bool   `json:"hasPreviousPage"`
	} `json:"pageInfo"`
}

// searches pages through searchStacks with token name, n at a time, under
// the predicates where names, in the order by names: forwards from the
// first page by each endCursor, then backwards from the last by each
// startCursor. Both ways find want's stacks in order, and every page is
// full but the one read last.
func (st *stackSearchSteps) searches(
	ctx context.Context,
	name, where, by, desc, replica string,
	n int,
	want string,
) error {
	base := map[string]any{"first": n}
	if where != "" {
		base["predicates"] = json.RawMessage(st.w.Resolve(where))
	}
	if by != "" {
		order := map[string]any{"field": by}
		if desc != "" {
			order["direction"] = "DESC"
		}
		base["orderBy"] = []any{order}
	}
	read := func(extra map[string]any) (stackPage, error) {
		in := map[string]any{}
		for k, v := range base {
			in[k] = v
		}
		for k, v := range extra {
			in[k] = v
		}
		r, err := st.w.Do(ctx, replica, name, http.MethodPost, "/api/v1/stacks/search", in)
		if err != nil {
			return stackPage{}, err
		}
		var p stackPage
		if json.Unmarshal(r.Body, &p) != nil || r.Status != http.StatusOK {
			return stackPage{}, fmt.Errorf(
				"%w: not a page of stacks: %d %s",
				harness.ErrStep,
				r.Status,
				r.Body,
			)
		}
		return p, nil
	}
	ids := func(p stackPage) []string {
		out := make([]string, len(p.Edges))
		for i, e := range p.Edges {
			out[i] = e.Node.ID
		}
		return out
	}
	w := st.w.SlugList(want)

	got, extra := []string{}, map[string]any{}
	for i := 0; ; i++ {
		p, err := read(extra)
		if err != nil {
			return err
		}
		got = append(got, ids(p)...)
		if !p.PageInfo.HasNextPage {
			break
		}
		if len(p.Edges) != n || i == 20 {
			return fmt.Errorf(
				"%w: page %d forwards holds %d stacks",
				harness.ErrStep,
				i,
				len(p.Edges),
			)
		}
		extra = map[string]any{"after": p.PageInfo.EndCursor, "requestedPage": "NEXT"}
	}
	if !reflect.DeepEqual(got, w) {
		return fmt.Errorf("%w: forwards the search found %v, want %v", harness.ErrStep, got, w)
	}

	got, extra = []string{}, map[string]any{"requestedPage": "LAST"}
	for i := 0; ; i++ {
		p, err := read(extra)
		if err != nil {
			return err
		}
		got = append(ids(p), got...)
		if !p.PageInfo.HasPreviousPage {
			break
		}
		if len(p.Edges) != n || i == 20 {
			return fmt.Errorf(
				"%w: page %d backwards holds %d stacks",
				harness.ErrStep,
				i,
				len(p.Edges),
			)
		}
		extra = map[string]any{"after": p.PageInfo.StartCursor, "requestedPage": "PREVIOUS"}
	}
	if !reflect.DeepEqual(got, w) {
		return fmt.Errorf("%w: backwards the search found %v, want %v", harness.ErrStep, got, w)
	}
	return nil
}

// suggests checks the last answer, suggestions, for the values field
// lists with their counts, as "value:count", comma-separated, in the order
// it lists them, and for how many stacks it counts. A hierarchy's values
// are its nodes' IDs.
func (st *stackSearchSteps) suggests(field, want string, count int) error {
	type values struct {
		Values []json.RawMessage `json:"values"`
		Counts []int             `json:"counts"`
	}
	type suggested struct {
		Name      string  `json:"name"`
		String    *values `json:"possibleValuesString"`
		Enum      *values `json:"possibleValuesEnum"`
		Boolean   *values `json:"possibleValuesBoolean"`
		Hierarchy *values `json:"possibleValuesHierarchy"`
	}
	var out struct {
		Fields        []suggested `json:"fields"`
		FilteredCount int         `json:"filteredCount"`
	}
	r := st.w.Last()
	if json.Unmarshal(r.Body, &out) != nil || r.Status != http.StatusOK {
		return fmt.Errorf("%w: not suggestions: %d %s", harness.ErrStep, r.Status, r.Body)
	}
	i := slices.IndexFunc(out.Fields, func(f suggested) bool { return f.Name == field })
	if i < 0 {
		return fmt.Errorf("%w: no field %s: %s", harness.ErrStep, field, r.Body)
	}
	f := out.Fields[i]
	v := cmp.Or(f.String, f.Enum, f.Boolean, f.Hierarchy)
	if v == nil {
		return fmt.Errorf("%w: %s lists no values: %s", harness.ErrStep, field, r.Body)
	}
	got := []string{}
	for j, raw := range v.Values {
		var node struct {
			ID string `json:"id"`
		}
		value := strings.Trim(string(raw), `"`)
		if json.Unmarshal(raw, &node) == nil && node.ID != "" {
			value = node.ID
		}
		got = append(got, value+":"+strconv.Itoa(v.Counts[j]))
	}
	if w := st.w.SlugList(want); !reflect.DeepEqual(got, w) || out.FilteredCount != count {
		return fmt.Errorf("%w: %s suggests %v of %d, want %v of %d", harness.ErrStep, field, got,
			out.FilteredCount, w, count)
	}
	return nil
}

// resumes reads the first stack of a search ordered by from with token
// name, and sends a search ordered by by after its endCursor, cut to half
// its length when short is set, for the scenario to check the answer.
func (st *stackSearchSteps) resumes(
	ctx context.Context,
	name, by, replica, from, short string,
) error {
	first := map[string]any{"first": 1, "orderBy": []any{map[string]any{"field": from}}}
	r, err := st.w.Do(ctx, replica, name, http.MethodPost, "/api/v1/stacks/search", first)
	if err != nil {
		return err
	}
	var p stackPage
	if json.Unmarshal(r.Body, &p) != nil || r.Status != http.StatusOK ||
		p.PageInfo.EndCursor == "" {
		return fmt.Errorf("%w: no cursor in %d %s", harness.ErrStep, r.Status, r.Body)
	}
	after := p.PageInfo.EndCursor
	if short != "" {
		after = after[:len(after)/2]
	}
	_, err = st.w.Do(
		ctx,
		replica,
		name,
		http.MethodPost,
		"/api/v1/stacks/search",
		map[string]any{
			"after": after, "orderBy": []any{map[string]any{"field": by}},
		},
	)
	return err
}
