package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// managedSteps check the Managed Users tag's answers, sending through
// orgSteps' request steps and naming users and groups as their {name}.
type managedSteps struct {
	w *harness.World
}

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
	r := m.w.Last()
	if json.Unmarshal(r.Body, &v) != nil || v.ID == "" {
		return fmt.Errorf("%w: the answer has no id: %d %s", harness.ErrStep, r.Status, r.Body)
	}
	m.w.IDs[name] = v.ID
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
	r := m.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &page) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	got := make([]string, 0, len(page.Items))
	for _, u := range page.Items {
		got = append(got, u.Username)
	}
	if strings.Join(got, ", ") != want {
		return fmt.Errorf("%w: the managed users are %q, want %q", harness.ErrStep,
			strings.Join(got, ", "), want)
	}
	return nil
}

// groups records the groups the identity provider reported at the home
// member's latest sign-in to the account, as P23's sign-in will.
func (m *managedSteps) groups(ctx context.Context, which, names string) error {
	_, err := m.w.Query(ctx, fmt.Sprintf(
		`UPDATE account_users
		SET latest_groups = string_to_array(%s, ',')
		WHERE account_id = '%s' AND member_id = '%s'`,
		harness.Literal(
			names,
		),
		harness.UUIDOf(harness.Tenants[which].Account),
		harness.UUIDOf(harness.MemberID),
	))
	return err
}
