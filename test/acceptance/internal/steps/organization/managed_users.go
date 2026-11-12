package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"
)

// managedSteps check the Managed Users tag's answers, sending through
// orgSteps' request steps and naming users and groups as their {name}.
type managedSteps struct{ o *orgSteps }

func (m *managedSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's id names "(\w+)"$`, m.names)
	sc.Step(`^the answer lists the managed users "([^"]*)"$`, m.lists)
	sc.Step(
		`^the home account's member's latest sign-in to the (home|sibling) account reported the groups "([^"]*)"$`,
		m.groups,
	)
}

// names keeps the last answer's id as name, for later paths to name.
func (m *managedSteps) names(name string) error {
	var v struct {
		ID string `json:"id"`
	}
	r := m.o.a.s.last()
	if json.Unmarshal(r.body, &v) != nil || v.ID == "" {
		return fmt.Errorf("%w: the answer has no id: %d %s", errStep, r.status, r.body)
	}
	m.o.ids[name] = v.ID
	return nil
}

// lists checks the last answer, a page of managed users, against want:
// their usernames, comma-separated, in the page's order.
func (m *managedSteps) lists(want string) error {
	var page struct {
		Items []struct {
			Username string `json:"username"`
		} `json:"items"`
	}
	r := m.o.a.s.last()
	if r.status != http.StatusOK || json.Unmarshal(r.body, &page) != nil {
		return fmt.Errorf("%w: answered %d %s", errStep, r.status, r.body)
	}
	got := make([]string, 0, len(page.Items))
	for _, u := range page.Items {
		got = append(got, u.Username)
	}
	if strings.Join(got, ", ") != want {
		return fmt.Errorf("%w: the managed users are %q, want %q", errStep,
			strings.Join(got, ", "), want)
	}
	return nil
}

// groups records the groups the identity provider reported at the home
// member's latest sign-in to the account, as P23's sign-in will.
func (m *managedSteps) groups(ctx context.Context, which, names string) error {
	_, err := m.o.a.s.query(ctx, fmt.Sprintf(`UPDATE account_users
		SET latest_groups = string_to_array(%s, ',')
		WHERE account_id = '%s' AND member_id = '%s'`,
		literal(names), uuidOf(tenants[which].account), uuidOf(memberID)))
	return err
}
