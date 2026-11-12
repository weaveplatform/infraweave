package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// settingsSteps send the account settings' requests with a token or in
// the member's browser.
type settingsSteps struct {
	w *harness.World
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

func (s *settingsSteps) answers(
	ctx context.Context,
	method, path, replica, tok, raw, want string,
) error {
	r, err := s.w.Do(ctx, replica, tok, method, path, harness.Body(raw))
	if err != nil {
		return err
	}
	return harness.Answered(r, method+" "+path, want)
}

func (s *settingsSteps) browserAnswers(
	ctx context.Context,
	method, path, replica, want string,
) error {
	r, err := s.w.BrowserDo(ctx, replica, method, path, harness.PublicURL, nil)
	if err != nil {
		return err
	}
	return harness.Answered(r, method+" "+path+" in the browser", want)
}

// recent checks an answer of Unix seconds within the last minute, as a
// deletion mark's time is the server's clock when it was set.
func (s *settingsSteps) recent(ctx context.Context, path, replica, tok string) error {
	r, err := s.w.Do(ctx, replica, tok, "GET", path, nil)
	if err != nil {
		return err
	}
	var at int64
	if err := json.Unmarshal(r.Body, &at); err != nil || r.Status != 200 ||
		time.Since(time.Unix(at, 0)) > time.Minute || time.Until(time.Unix(at, 0)) > time.Minute {
		return fmt.Errorf("%w: GET %s answered %d %s", harness.ErrStep, path, r.Status, r.Body)
	}
	return nil
}

func (s *settingsSteps) detailNames(text string) error {
	var p struct {
		Detail string `json:"detail"`
	}
	last := s.w.Last()
	if err := json.Unmarshal(last.Body, &p); err != nil || !strings.Contains(p.Detail, text) {
		return fmt.Errorf(
			"%w: the problem's detail does not name %q: %s",
			harness.ErrStep,
			text,
			last.Body,
		)
	}
	return nil
}

func (s *settingsSteps) trailHolds(ctx context.Context, who, action string) error {
	n, err := s.number(ctx, fmt.Sprintf(`SELECT count(*) FROM audit_trail_entries
		WHERE account_id = '%s' AND action = '%s'`, harness.UUIDOf(harness.Tenants[who].Account), action))
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf(
			"%w: the %s account's trail holds %d %q entries",
			harness.ErrStep,
			who,
			n,
			action,
		)
	}
	return nil
}

// gone counts what remains of the account and subtracts its deletion's
// entry on the organization's trail, so 0 is an account wholly deleted
// with its deletion recorded once.
func (s *settingsSteps) gone(ctx context.Context, who string) error {
	t := harness.Tenants[who]
	n, err := s.number(ctx, fmt.Sprintf(`SELECT
		(SELECT count(*) FROM accounts WHERE id = '%[1]s')
		+ (SELECT count(*) FROM api_keys WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM sessions WHERE account_id = '%[1]s')
		+ (SELECT count(*) FROM account_settings WHERE account_id = '%[1]s')
		+ 1 - (SELECT count(*) FROM organization_audit_trail_entries
			WHERE organization_id = '%[2]s' AND action = 'account.delete')`,
		harness.UUIDOf(t.Account), harness.UUIDOf(t.Org)))
	if err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf(
			"%w: %d rows of the %s account remain, or its deletion is not on the trail",
			harness.ErrStep,
			n,
			who,
		)
	}
	return nil
}

// number runs a query that answers one integer.
func (s *settingsSteps) number(ctx context.Context, sql string) (int, error) {
	out, err := s.w.Query(ctx, sql)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("%w: %q from %s", harness.ErrStep, out, sql)
	}
	return n, nil
}
