package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// orgSteps drive the Organization tag as an organization admin's browser
// or a token. A request's path and body name the scenario's members and
// organization keys as {name}, which ids resolves.
type orgSteps struct {
	a *authSteps
	b *browserSteps
	// ids are the scenario's members and organization keys by name.
	ids map[string]string
	// secrets are the organization keys' secrets by name, which only their
	// creation answered.
	secrets map[string]string
	// tag are the answers of the Organization tag's requests, by the
	// request's method and path.
	tag map[string][][]byte
}

func (o *orgSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the organization "([a-z-]+)" has the active members "([^"]+)"$`, o.members)
	sc.Step(`^the home account's member holds organization-admin$`, o.homeAdmin)
	sc.Step(
		`^(the browser|token "\w+") sends "([A-Z]+) ([^"]+)"(?: with '([^']*)')? through ([AB])$`,
		o.send,
	)
	sc.Step(`^it answers (\d+)$`, o.answers)
	sc.Step(`^it answers a (\d+) problem "([a-z-]+)"$`, o.answersProblem)
	sc.Step(`^the answer lists the accounts "([^"]*)"$`, o.listsAccounts)
	sc.Step(`^the answer's (\w+) is '([^']*)'$`, o.member)
	sc.Step(`^the answer leaves out "([^"]+)"$`, o.leavesOut)
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
		o.ids[name] = id.String()
		rows = append(rows, fmt.Sprintf(`((SELECT id FROM organizations WHERE slug = %s),
			'%s', %s, %[3]s, %s, 'active')`,
			literal(slug), id.UUID(), literal(name), literal(name+"@example.com")))
	}
	_, err := o.a.s.query(
		ctx,
		`INSERT INTO members (organization_id, id, login, name, email, status)
		VALUES `+strings.Join(rows, ", "),
	)
	return err
}

// homeAdmin grants the home organization's seeded member the organization
// role, as an organization admin would.
func (o *orgSteps) homeAdmin(ctx context.Context) error {
	_, err := o.a.s.query(ctx, fmt.Sprintf(`INSERT INTO organization_admins
		(organization_id, member_id, created_by) VALUES ('%s', '%s', 'acceptance')`,
		uuidOf(home.org), uuidOf(memberID)))
	return err
}

// resolve replaces each {name} in s with the scenario's ID of name.
func (o *orgSteps) resolve(s string) string {
	for name, id := range o.ids {
		s = strings.ReplaceAll(s, "{"+name+"}", id)
	}
	return s
}

// send sends the request through the browser, as a write from the public
// URL's page, or with the named token.
func (o *orgSteps) send(ctx context.Context, who, method, path, raw, replica string) error {
	path, raw = o.resolve(path), o.resolve(raw)
	var (
		r   response
		err error
	)
	if who == "the browser" {
		r, err = o.b.do(ctx, replica, method, path, publicURL, body(raw))
	} else {
		name := strings.TrimSuffix(strings.TrimPrefix(who, `token "`), `"`)
		r, err = o.a.do(ctx, replica, name, method, path, body(raw))
	}
	if err != nil && r.status == 0 {
		return err
	}
	if strings.HasPrefix(path, "/api/v1/organization") {
		key := method + " " + path
		o.tag[key] = append(o.tag[key], r.body)
	}
	return nil
}

func (o *orgSteps) answers(code int) error {
	r := o.a.s.last()
	if r.status != code {
		return fmt.Errorf("%w: answered %d, want %d: %s", errStep, r.status, code, r.body)
	}
	return nil
}

func (o *orgSteps) answersProblem(code, slug string) error {
	return isProblem(o.a.s.last(), "the request", code, slug)
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
	r := o.a.s.last()
	if r.status != http.StatusOK || json.Unmarshal(r.body, &page) != nil {
		return fmt.Errorf("%w: answered %d %s", errStep, r.status, r.body)
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
			errStep,
			strings.Join(got, ", "),
			want,
		)
	}
	return nil
}

// member checks the last answer's top-level member name against want as
// JSON, with each {name} resolved.
func (o *orgSteps) member(name, want string) error {
	var got map[string]json.RawMessage
	var g, w any
	r := o.a.s.last()
	if json.Unmarshal(r.body, &got) != nil || json.Unmarshal(got[name], &g) != nil ||
		json.Unmarshal([]byte(o.resolve(want)), &w) != nil || !reflect.DeepEqual(g, w) {
		return fmt.Errorf("%w: %s is not %s: %d %s", errStep, name, want, r.status, r.body)
	}
	return nil
}

// leavesOut checks that the last answer names nothing of text, such as
// another organization's account.
func (o *orgSteps) leavesOut(text string) error {
	r := o.a.s.last()
	if r.status != http.StatusOK || strings.Contains(string(r.body), text) {
		return fmt.Errorf("%w: the answer names %q: %d %s", errStep, text, r.status, r.body)
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
	r := o.a.s.last()
	if r.status == http.StatusOK && json.Unmarshal(r.body, &u) == nil {
		for _, l := range u.Limits {
			if l.Name == name && l.Value != nil && *l.Value == value {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: the limit %q is not %d: %d %s", errStep, name, value, r.status, r.body)
}

// createKey creates an organization key through the browser and keeps its
// ID and secret.
func (o *orgSteps) createKey(ctx context.Context, name, replica string) error {
	if err := o.send(ctx, "the browser", http.MethodPost, "/api/v1/organization/keys",
		fmt.Sprintf(`{"name":%q}`, name), replica); err != nil {
		return err
	}
	var k struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	r := o.a.s.last()
	if r.status != http.StatusCreated || json.Unmarshal(r.body, &k) != nil ||
		!strings.HasPrefix(k.Secret, "iwo_") || len(k.Secret) != len("iwo_")+43 {
		return fmt.Errorf("%w: the key's creation answered %d %s", errStep, r.status, r.body)
	}
	o.ids[name], o.secrets[name] = k.ID, k.Secret
	return nil
}

func (o *orgSteps) exchange(ctx context.Context, name, replica string) (response, error) {
	return o.a.do(ctx, replica, "", http.MethodPost, "/api/v1/auth/token",
		map[string]string{"id": o.ids[name], "secret": o.secrets[name]})
}

// exchangeKey exchanges the organization key for an organization token.
func (o *orgSteps) exchangeKey(ctx context.Context, name, replica, tok string) error {
	r, err := o.exchange(ctx, name, replica)
	if err != nil && r.status == 0 {
		return err
	}
	var u struct {
		ID  string `json:"id"`
		JWT string `json:"jwt"`
	}
	if r.status != http.StatusOK || json.Unmarshal(r.body, &u) != nil || u.JWT == "" {
		return fmt.Errorf("%w: the exchange answered %d %s", errStep, r.status, r.body)
	}
	o.a.tokens[tok] = u.JWT
	return nil
}

func (o *orgSteps) exchangeRefused(ctx context.Context, name, replica, code, slug string) error {
	r, err := o.exchange(ctx, name, replica)
	if err != nil && r.status == 0 {
		return err
	}
	return isProblem(r, "the organization key's exchange", code, slug)
}

// accountData are the members an answer naming an account's stacks, runs,
// state or secrets would carry. An organization key's own secret, which
// its creation answers, is no account's.
var accountData = []string{"stack", "stacks", "run", "runs", "state", "secret", "secrets"}

// namesNoAccountData checks every answer of the tag in the scenario for a
// member of accountData at any depth.
func (o *orgSteps) namesNoAccountData() error {
	if len(o.tag) == 0 {
		return fmt.Errorf("%w: the scenario sent the tag nothing", errStep)
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
	for req, bodies := range o.tag {
		if req == "POST /api/v1/organization/keys" {
			continue
		}
		for _, b := range bodies {
			var v any
			if json.Unmarshal(b, &v) != nil {
				continue
			}
			if bad := walk(v); bad != "" {
				return fmt.Errorf("%w: %s answered a %q member: %s", errStep, req, bad, b)
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
	if err := o.send(ctx, "the browser", http.MethodGet, "/api/v1/organization/usage", "",
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
	r := o.a.s.last()
	if r.status != http.StatusOK || json.Unmarshal(r.body, &page) != nil {
		return fmt.Errorf("%w: the usage answered %d %s", errStep, r.status, r.body)
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
		return fmt.Errorf("%w: the usage counts seats %v: %s", errStep, seats, r.body)
	}
	return nil
}
