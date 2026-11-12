package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// orgSteps drive the Organization tag as an organization admin's browser
// or a token. A request's path and body name the scenario's members and
// organization keys as {name}, which the World's IDs resolve.
type orgSteps struct {
	w *harness.World
	// secrets are the organization keys' secrets by name, which only their
	// creation answered.
	secrets map[string]string
}

func (o *orgSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the organization "([a-z-]+)" has the active members "([^"]+)"$`, o.members)
	sc.Step(`^the home account's member holds organization-admin$`, o.homeAdmin)
	sc.Step(`^the answer lists the accounts "([^"]*)"$`, o.listsAccounts)
	sc.Step(`^the answer sets the account's limit "([a-z-]+)" to (\d+)$`, o.limitSet)
	sc.Step(`^the browser creates the organization key "(\w+)" through ([AB])$`, o.createKey)
	sc.Step(
		`^the organization key "(\w+)" exchanges through ([AB]) for token "(\w+)"$`,
		o.exchangeKey,
	)
	sc.Step(
		`^exchanging the organization key "(\w+)" through ([AB]) answers a (\d+) problem "([a-z-]+)"$`,
		o.exchangeRefused,
	)
	sc.Step(
		`^no answer of the Organization tag names an account's stack, run, state or secret$`,
		o.namesNoAccountData,
	)
	sc.Step(
		`^the usage read through ([AB]) in the browser counts (\d+) seats? in "([a-z-]+)" and (\d+) in "([a-z-]+)"$`,
		o.usage,
	)
}

// members seeds active members of the organization slug names.
func (o *orgSteps) members(ctx context.Context, slug, names string) error {
	var rows []string
	for name := range strings.SplitSeq(names, ",") {
		name = strings.TrimSpace(name)
		id := kernel.NewMemberID()
		o.w.IDs[name] = id.String()
		rows = append(rows, fmt.Sprintf(
			`((SELECT id FROM organizations WHERE slug = %s),
			'%s', %s, %[3]s, %s, 'active')`,
			harness.Literal(
				slug,
			),
			id.UUID(),
			harness.Literal(name),
			harness.Literal(name+"@example.com"),
		))
	}
	_, err := o.w.Query(
		ctx,
		`INSERT INTO members (organization_id, id, login, name, email, status)
		VALUES `+strings.Join(rows, ", "),
	)
	return err
}

// homeAdmin grants the home organization's seeded member the organization
// role, as an organization admin would.
func (o *orgSteps) homeAdmin(ctx context.Context) error {
	_, err := o.w.Query(ctx, fmt.Sprintf(`INSERT INTO organization_admins
		(organization_id, member_id, created_by) VALUES ('%s', '%s', 'acceptance')`,
		harness.UUIDOf(harness.Home.Org), harness.UUIDOf(harness.MemberID)))
	return err
}

// listsAccounts checks the last answer, a page of accounts, against want:
// slug:claimed or slug:unclaimed, comma-separated, oldest first.
func (o *orgSteps) listsAccounts(want string) error {
	var page struct {
		Items []struct {
			Slug    string `json:"slug"`
			Claimed bool   `json:"claimed"`
		} `json:"items"`
	}
	r := o.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &page) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	got := make([]string, 0, len(page.Items))
	for _, a := range page.Items {
		state := "unclaimed"
		if a.Claimed {
			state = "claimed"
		}
		got = append(got, a.Slug+":"+state)
	}
	if strings.Join(got, ", ") != want {
		return fmt.Errorf(
			"%w: the accounts are %q, want %q",
			harness.ErrStep,
			strings.Join(got, ", "),
			want,
		)
	}
	return nil
}

// limitSet checks the last answer, an account's usage, for the limit's
// value.
func (o *orgSteps) limitSet(name string, value int64) error {
	var u struct {
		Limits []struct {
			Name  string `json:"name"`
			Value *int64 `json:"value"`
		} `json:"limits"`
	}
	r := o.w.Last()
	if r.Status == http.StatusOK && json.Unmarshal(r.Body, &u) == nil {
		for _, l := range u.Limits {
			if l.Name == name && l.Value != nil && *l.Value == value {
				return nil
			}
		}
	}
	return fmt.Errorf(
		"%w: the limit %q is not %d: %d %s",
		harness.ErrStep,
		name,
		value,
		r.Status,
		r.Body,
	)
}

// createKey creates an organization key through the browser and keeps its
// ID and secret.
func (o *orgSteps) createKey(ctx context.Context, name, replica string) error {
	if err := o.w.SendAs(ctx, "the browser", http.MethodPost, "/api/v1/organization/keys",
		fmt.Sprintf(`{"name":%q}`, name), replica); err != nil {
		return err
	}
	var k struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	r := o.w.Last()
	if r.Status != http.StatusCreated || json.Unmarshal(r.Body, &k) != nil ||
		!strings.HasPrefix(k.Secret, "iwo_") || len(k.Secret) != len("iwo_")+43 {
		return fmt.Errorf(
			"%w: the key's creation answered %d %s",
			harness.ErrStep,
			r.Status,
			r.Body,
		)
	}
	o.w.IDs[name], o.secrets[name] = k.ID, k.Secret
	return nil
}

func (o *orgSteps) exchange(ctx context.Context, name, replica string) (harness.Response, error) {
	return o.w.Do(ctx, replica, "", http.MethodPost, "/api/v1/auth/token",
		map[string]string{"id": o.w.IDs[name], "secret": o.secrets[name]})
}

// exchangeKey exchanges the organization key for an organization token.
func (o *orgSteps) exchangeKey(ctx context.Context, name, replica, tok string) error {
	r, err := o.exchange(ctx, name, replica)
	if err != nil && r.Status == 0 {
		return err
	}
	var u struct {
		ID  string `json:"id"`
		JWT string `json:"jwt"`
	}
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &u) != nil || u.JWT == "" {
		return fmt.Errorf("%w: the exchange answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	o.w.Tokens[tok] = u.JWT
	return nil
}

func (o *orgSteps) exchangeRefused(ctx context.Context, name, replica, code, slug string) error {
	r, err := o.exchange(ctx, name, replica)
	if err != nil && r.Status == 0 {
		return err
	}
	return harness.IsProblem(r, "the organization key's exchange", code, slug)
}

// accountData are the members an answer naming an account's stacks, runs,
// state or secrets would carry. An organization key's own secret, which
// its creation answers, is no account's.
var accountData = []string{"stack", "stacks", "run", "runs", "state", "secret", "secrets"}

// namesNoAccountData checks every answer of the tag in the scenario for a
// member of accountData at any depth.
func (o *orgSteps) namesNoAccountData() error {
	if len(o.w.Answers) == 0 {
		return fmt.Errorf("%w: the scenario sent the tag nothing", harness.ErrStep)
	}
	var walk func(v any) string
	walk = func(v any) string {
		switch v := v.(type) {
		case map[string]any:
			for k, e := range v {
				if slices.Contains(accountData, k) {
					return k
				}
				if bad := walk(e); bad != "" {
					return bad
				}
			}
		case []any:
			for _, e := range v {
				if bad := walk(e); bad != "" {
					return bad
				}
			}
		}
		return ""
	}
	for req, bodies := range o.w.Answers {
		if req == "POST /api/v1/organization/keys" {
			continue
		}
		for _, b := range bodies {
			var v any
			if json.Unmarshal(b, &v) != nil {
				continue
			}
			if bad := walk(v); bad != "" {
				return fmt.Errorf("%w: %s answered a %q member: %s", harness.ErrStep, req, bad, b)
			}
		}
	}
	return nil
}

// usage reads the organization's usage in the browser and checks two
// accounts' seats in use.
func (o *orgSteps) usage(
	ctx context.Context,
	replica string,
	n1 int64,
	a1 string,
	n2 int64,
	a2 string,
) error {
	if err := o.w.SendAs(ctx, "the browser", http.MethodGet, "/api/v1/organization/usage", "",
		replica); err != nil {
		return err
	}
	var page struct {
		Items []struct {
			Account struct {
				Slug string `json:"slug"`
			} `json:"account"`
			Limits []struct {
				Name  string `json:"name"`
				InUse *int64 `json:"inUse"`
			} `json:"limits"`
		} `json:"items"`
	}
	r := o.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &page) != nil {
		return fmt.Errorf("%w: the usage answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	seats := map[string]string{}
	for _, u := range page.Items {
		for _, l := range u.Limits {
			if l.Name == "seats" && l.InUse != nil {
				seats[u.Account.Slug] = strconv.FormatInt(*l.InUse, 10)
			}
		}
	}
	if seats[a1] != strconv.FormatInt(n1, 10) || seats[a2] != strconv.FormatInt(n2, 10) {
		return fmt.Errorf("%w: the usage counts seats %v: %s", harness.ErrStep, seats, r.Body)
	}
	return nil
}
