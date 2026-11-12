package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func (s *servedSteps) registerFeatures(sc *godog.ScenarioContext) {
	sc.Step(
		`^a running infraweave server whose features\.disabled is "([^"]+)"$`,
		s.runningSwitchedOff,
	)
	sc.Step(`^the problem names the feature "([a-z-]+)"$`, s.namesFeature)
	sc.Step(`^"([A-Z]+) ([^"]+)" on the API listener answers (\d+)$`, s.apiAnswers)
	sc.Step(`^"([A-Z]+) ([^"]+)" on the API listener answers '([^']*)'$`, s.apiAnswersJSON)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" with '([^']*)' on the API listener answers a (\d+) problem "([a-z-]+)"$`,
		s.apiBodyProblem,
	)
	sc.Step(`^the tier features read on the API listener (hold|lack) "([a-z-]+)"$`, s.tierFeatures)
	sc.Step(
		`^the home organization is on the Free tier with billing on, its 2 seats taken$`,
		s.seatsTaken,
	)
	sc.Step(
		`^the sibling key's first use through (A|B) answers a (\d+) problem "([a-z-]+)"$`,
		s.firstUse,
	)
	sc.Step(
		`^the problem names the limit "([a-z-]+)" of the (organization|account) at (\d+)$`,
		s.namesLimit,
	)
}

// seatsTaken puts home's organization on Free, whose seats limit is 2, and
// fills both: the home key, which every launch uses, and an active member.
// The sibling key, seeded unused, holds none. The notification stands in
// for the operator API's, so every replica rereads the switch.
func (s *servedSteps) seatsTaken(ctx context.Context) error {
	db, err := s.w.querier(ctx)
	if err != nil {
		return err
	}
	if err := sibling.seed(ctx, db); err != nil {
		return fmt.Errorf("seed the sibling tenant: %w", err)
	}
	_, err = db.Query(ctx, fmt.Sprintf(`
		UPDATE organization_entitlements SET tier = 'Free' WHERE organization_id = '%[1]s';
		INSERT INTO members (organization_id, id, login, name, email, status)
			VALUES ('%[1]s', '%[2]s', 'alice', 'Alice', 'alice@example.com', 'active');
		INSERT INTO account_users (account_id, organization_id, member_id)
			VALUES ('%[3]s', '%[1]s', '%[2]s');
		UPDATE billing SET enabled = true;
		SELECT pg_notify('infraweave_invalidate', 'features:billing');`,
		uuidOf(home.org), kernel.NewMemberID().UUID(), uuidOf(home.account)))
	return err //nolint:wrapcheck // the querier names the query
}

// firstUse exchanges the sibling key, which has never been used.
func (s *servedSteps) firstUse(ctx context.Context, name, code, slug string) error {
	resp, err := s.sendThrough(ctx, name, http.MethodPost, "/api/v1/auth/token",
		map[string]string{"id": sibling.key, "secret": sibling.secret()})
	if err != nil {
		return err
	}
	return isProblem(resp, "the sibling key's first use", code, slug)
}

// namesLimit checks the last answer's limit-reached members.
func (s *servedSteps) namesLimit(limit, scope string, value int64) error {
	var p struct {
		Limit string `json:"limit"`
		Scope string `json:"scope"`
		Value *int64 `json:"value"`
	}
	if err := json.Unmarshal(s.last().body, &p); err != nil {
		return fmt.Errorf("%w: the answer is not a problem: %w: %s", errStep, err, s.last().body)
	}
	if p.Limit != limit || p.Scope != scope || p.Value == nil || *p.Value != value {
		return fmt.Errorf("%w: the problem names %s", errStep, s.last().body)
	}
	return nil
}

func (s *servedSteps) apiAnswers(ctx context.Context, method, path, code string) error {
	resp, err := s.send(ctx, method, "API", path)
	if err != nil {
		return err
	}
	if strconv.Itoa(resp.status) != code {
		return fmt.Errorf(
			"%w: %s %s answered %d, want %s: %s",
			errStep,
			method,
			path,
			resp.status,
			code,
			resp.body,
		)
	}
	return nil
}

// apiBodyProblem sends body, a JSON document, on the API listener: an
// option of a feature is a member of a request's body.
func (s *servedSteps) apiBodyProblem(
	ctx context.Context,
	method, path, body, code, slug string,
) error {
	resp, err := s.w.api.Do(ctx, method, path, json.RawMessage(body))
	if resp == nil {
		return err //nolint:wrapcheck // the client names the request
	}
	r := response{status: resp.Status, header: resp.Header, body: resp.Body, err: err}
	s.sent = append(s.sent, r)
	return isProblem(r, method+" "+path, code, slug)
}

func (s *servedSteps) apiAnswersJSON(ctx context.Context, method, path, want string) error {
	resp, err := s.send(ctx, method, "API", path)
	if err != nil {
		return err
	}
	return answered(resp, method+" "+path, want)
}

// tierFeatures reads every page of the entitlement and checks that it
// holds or lacks feature.
func (s *servedSteps) tierFeatures(ctx context.Context, which, feature string) error {
	var held []string
	path := "/api/v1/tier-features"
	for {
		resp, err := s.send(ctx, http.MethodGet, "API", path)
		if err != nil {
			return err
		}
		var page struct {
			Items      []string `json:"items"`
			NextCursor *string  `json:"nextCursor"`
		}
		if resp.status != http.StatusOK || json.Unmarshal(resp.body, &page) != nil {
			return fmt.Errorf("%w: GET %s answered %d: %s", errStep, path, resp.status, resp.body)
		}
		held = append(held, page.Items...)
		if page.NextCursor == nil {
			break
		}
		path = "/api/v1/tier-features?cursor=" + url.QueryEscape(*page.NextCursor)
	}
	if slices.Contains(held, feature) != (which == "hold") {
		return fmt.Errorf("%w: the tier features are %v, which do not %s %q", errStep, held,
			which, feature)
	}
	return nil
}

// runningSwitchedOff starts the scenario's server with the comma-separated
// features switched off for the installation.
func (s *servedSteps) runningSwitchedOff(ctx context.Context, names string) error {
	off := strings.Split(names, ",")
	for i := range off {
		off[i] = strings.TrimSpace(off[i])
	}
	s.w.features = map[string]any{"disabled": off}
	_, err := s.w.startServer(ctx, nil)
	return err
}

// namesFeature checks the last answer's feature member, which the SDK's
// Problem does not carry.
func (s *servedSteps) namesFeature(feature string) error {
	var p struct {
		Feature string `json:"feature"`
	}
	if err := json.Unmarshal(s.last().body, &p); err != nil {
		return fmt.Errorf("%w: the answer is not a problem: %w: %s", errStep, err, s.last().body)
	}
	if p.Feature != feature {
		return fmt.Errorf("%w: the problem names feature %q, want %q", errStep, p.Feature, feature)
	}
	return nil
}
