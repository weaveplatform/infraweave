package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// settingsSteps send the account settings' requests with a token or in
// the member's browser.
type settingsSteps struct {
	a *authSteps
	b *browserSteps
}

func (s *settingsSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^"([A-Z]+) ([^"]+)" through ([AB]) with token "(\w+)"(?: and '([^']*)')? answers '([^']*)'$`,
		s.answers,
	)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" through ([AB]) in the browser answers '([^']*)'$`,
		s.browserAnswers,
	)
	sc.Step(
		`^"GET ([^"]+)" through ([AB]) with token "(\w+)" answers a time within the last minute$`,
		s.recent,
	)
	sc.Step(`^the problem's detail names "([^"]+)"$`, s.detailNames)
	sc.Step(`^the (home) account's audit trail holds "([a-z_.]+)"$`, s.trailHolds)
	sc.Step(
		`^the (sibling) account no longer exists, and its organization's trail holds its deletion$`,
		s.gone,
	)
}

// body is a step's JSON body, or none.
func body(raw string) any {
	if raw == "" {
		return nil
	}
	return json.RawMessage(raw)
}

// answered checks that r is a 200 whose body is want as JSON.
func answered(r response, what, want string) error {
	var got, exp any
	if err := json.Unmarshal([]byte(want), &exp); err != nil {
		return fmt.Errorf("%w: the step's JSON %q: %w", errStep, want, err)
	}
	if r.status != 200 || json.Unmarshal(r.body, &got) != nil || !reflect.DeepEqual(got, exp) {
		return fmt.Errorf("%w: %s answered %d %s, want 200 %s", errStep, what, r.status,
			strings.TrimSpace(string(r.body)), want)
	}
	return nil
}

func (s *settingsSteps) answers(
	ctx context.Context,
	method, path, replica, tok, raw, want string,
) error {
	r, err := s.a.do(ctx, replica, tok, method, path, body(raw))
	if err != nil {
		return err
	}
	return answered(r, method+" "+path, want)
}

func (s *settingsSteps) browserAnswers(
	ctx context.Context,
	method, path, replica, want string,
) error {
	r, err := s.b.do(ctx, replica, method, path, publicURL, nil)
	if err != nil {
		return err
	}
	return answered(r, method+" "+path+" in the browser", want)
}

// recent checks an answer of Unix seconds within the last minute, as a
// deletion mark's time is the server's clock when it was set.
func (s *settingsSteps) recent(ctx context.Context, path, replica, tok string) error {
	r, err := s.a.do(ctx, replica, tok, "GET", path, nil)
	if err != nil {
		return err
	}
	var at int64
	if err := json.Unmarshal(r.body, &at); err != nil || r.status != 200 ||
		time.Since(time.Unix(at, 0)) > time.Minute || time.Until(time.Unix(at, 0)) > time.Minute {
		return fmt.Errorf("%w: GET %s answered %d %s", errStep, path, r.status, r.body)
	}
	return nil
}

func (s *settingsSteps) detailNames(text string) error {
	var p struct {
		Detail string `json:"detail"`
	}
	last := s.a.s.last()
	if err := json.Unmarshal(last.body, &p); err != nil || !strings.Contains(p.Detail, text) {
		return fmt.Errorf("%w: the problem's detail does not name %q: %s", errStep, text, last.body)
	}
	return nil
}

func (s *settingsSteps) trailHolds(ctx context.Context, who, action string) error {
	n, err := s.number(ctx, fmt.Sprintf(`SELECT count(*) FROM audit_trail_entries
		WHERE account_id = '%s' AND action = '%s'`, uuidOf(tenants[who].account), action))
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: the %s account's trail holds %d %q entries", errStep, who, n, action)
	}
	return nil
}

// gone counts what remains of the account and subtracts its deletion's
// entry on the organization's trail, so 0 is an account wholly deleted
// with its deletion recorded once.
func (s *settingsSteps) gone(ctx context.Context, who string) error {
	t := tenants[who]
	n, err := s.number(ctx, fmt.Sprintf(`SELECT
		(SELECT count(*) FROM accounts WHERE id = '%[1]s')
		+ (SELECT count(*) FROM api_keys WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM sessions WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM account_settings WHERE account_id = '%[1]s')
		+ 1 - (SELECT count(*) FROM organization_audit_trail_entries
			WHERE organization_id = '%[2]s' AND action = 'account.delete')`,
		uuidOf(t.account), uuidOf(t.org)))
	if err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf(
			"%w: %d rows of the %s account remain, or its deletion is not on the trail",
			errStep,
			n,
			who,
		)
	}
	return nil
}

// number runs a query that answers one integer.
func (s *settingsSteps) number(ctx context.Context, sql string) (int, error) {
	out, err := s.a.s.query(ctx, sql)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("%w: %q from %s", errStep, out, sql)
	}
	return n, nil
}
