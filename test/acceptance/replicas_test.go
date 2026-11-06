package acceptance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// scenarioCaller is the caller every scenario's requests count against:
// the home tenant's key, whose token the scenario's clients hold.
var scenarioCaller = home.caller()

// The response a scenario stores under a key, as a replica that served the
// request would have.
const (
	storedLocation = "/api/v1/stacks/replayed"
	storedBody     = `{"id":"replayed"}`
)

func (s *servedSteps) registerReplicas(sc *godog.ScenarioContext) {
	sc.Step(`^two infraweave servers, A and B, on one database$`, s.twoReplicas)
	sc.Step(
		`^two infraweave servers, A and B, on one database whose standard budget is (\d+) at ([0-9.]+) a second$`,
		s.twoReplicasWithBudget,
	)
	sc.Step(
		`^"([A-Z]+) ([^"]+)" through (A|B) answers a (\d+) problem "([a-z-]+)"$`,
		s.throughAnswers,
	)
	sc.Step(`^"([A-Z]+) ([^"]+)" through (A|B) eventually answers a (\d+) problem "([a-z-]+)"$`,
		s.throughEventually)
	sc.Step(`^"GET ([^"]+)" through (A|B) leaves (\d+) requests? in the budget$`, s.leaves)
	sc.Step(`^its Retry-After is (\d+) seconds, give or take (\d+)$`, s.retryAfterNear)
	sc.Step(
		`^a (\d+) response to "POST ([^"]+)" with body '([^']*)' is stored under key "([^"]+)"$`,
		s.stored,
	)
	sc.Step(`^"POST ([^"]+)" with body '([^']*)' under key "([^"]+)" is in flight$`, s.inFlight)
	sc.Step(
		`^"POST ([^"]+)" with body '([^']*)' and key "([^"]+)" through (A|B) replays the stored (\d+)$`,
		s.replays,
	)
	sc.Step(
		`^"POST ([^"]+)" with body '([^']*)' and key "([^"]+)" through (A|B) answers a (\d+) problem "([a-z-]+)"$`,
		s.keyedAnswers,
	)
	sc.Step(`^the lock of key "([^"]+)" lapses$`, s.lockLapses)
	sc.Step(`^key "([^"]+)" is no longer stored$`, s.notStored)
	sc.Step(
		`^the operator disables the caller "([^"]+)" in the table( until a minute ago)?$`,
		s.disable,
	)
	sc.Step(`^the operator removes the caller "([^"]+)" from the table$`, s.enable)
}

func (s *servedSteps) twoReplicas(ctx context.Context) error {
	return s.twoReplicasWithBudget(ctx, 0, "")
}

// twoReplicasWithBudget starts the scenario's two servers. A burst of 0
// keeps the default budget.
func (s *servedSteps) twoReplicasWithBudget(ctx context.Context, burst int, rate string) error {
	var rateLimit map[string]any
	if burst > 0 {
		r, err := strconv.ParseFloat(rate, 64)
		if err != nil {
			return fmt.Errorf("%w: rate %q", errStep, rate)
		}
		rateLimit = map[string]any{"standard": map[string]any{"rate": r, "burst": burst}}
	}
	for range 2 {
		if err := s.start(ctx, rateLimit); err != nil {
			return err
		}
	}
	return nil
}

func (s *servedSteps) replica(name string) *sdk.Client {
	if name == "B" {
		return s.w.apis[1]
	}
	return s.w.apis[0]
}

// sendThrough sends a request through the named replica's API listener.
func (s *servedSteps) sendThrough(
	ctx context.Context,
	name, method, path string,
	body any,
	headers ...sdk.Header,
) (response, error) {
	resp, err := s.replica(name).Do(ctx, method, path, body, headers...)
	if resp == nil {
		return response{}, err
	}
	r := response{status: resp.Status, header: resp.Header, body: resp.Body, err: err}
	s.sent = append(s.sent, r)
	return r, nil
}

func (s *servedSteps) throughAnswers(
	ctx context.Context,
	method, path, name, code, slug string,
) error {
	resp, err := s.sendThrough(ctx, name, method, path, nil)
	if err != nil {
		return err
	}
	return isProblem(resp, method+" "+path+" through "+name, code, slug)
}

// throughEventually waits for a replica to answer as a change in the
// database says it should, which reaches it through a notification.
func (s *servedSteps) throughEventually(
	ctx context.Context,
	method, path, name, code, slug string,
) error {
	return Eventually(ctx, method+" "+path+" through "+name+" to answer "+code,
		func(ctx context.Context) (bool, error) {
			resp, err := s.sendThrough(ctx, name, method, path, nil)
			if err != nil {
				return false, err
			}
			return isProblem(resp, method+" "+path, code, slug) == nil, nil
		})
}

func (s *servedSteps) leaves(ctx context.Context, path, name string, n int) error {
	resp, err := s.sendThrough(ctx, name, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if resp.status == http.StatusTooManyRequests ||
		resp.header.Get("X-RateLimit-Remaining") != strconv.Itoa(n) {
		return fmt.Errorf(
			"%w: GET %s through %s answered %d with X-RateLimit-Remaining %q, want %d",
			errStep,
			path,
			name,
			resp.status,
			resp.header.Get("X-RateLimit-Remaining"),
			n,
		)
	}
	return nil
}

func (s *servedSteps) retryAfterNear(want, slack int) error {
	got, err := strconv.Atoi(s.last().header.Get("Retry-After"))
	if err != nil || math.Abs(float64(got-want)) > float64(slack) {
		return fmt.Errorf("%w: Retry-After %q, want %d give or take %d",
			errStep, s.last().header.Get("Retry-After"), want, slack)
	}
	return nil
}

// fingerprint is decision 0003's identity of a request, as the idempotency
// middleware computes it, so a scenario can store a key's state itself.
func fingerprint(method, path, body string) string {
	sum := sha256.Sum256([]byte(body))
	return method + " " + path + " " + hex.EncodeToString(sum[:])
}

// literal quotes s for SQL.
func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (s *servedSteps) query(ctx context.Context, sql string) (string, error) {
	db, err := s.w.database(ctx)
	if err != nil {
		return "", err
	}
	return db.Query(ctx, sql)
}

func (s *servedSteps) stored(ctx context.Context, status int, path, body, key string) error {
	sealed, err := s.sealBody(ctx, key, []byte(storedBody))
	if err != nil {
		return err
	}
	_, err = s.query(ctx, fmt.Sprintf(`INSERT INTO idempotency_keys
    (caller, key, fingerprint, state, locked_until, status, content_type, location, body_sealed,
     created_at, expires_at)
VALUES (%s, %s, %s, 'done', now(), %d, 'application/json', %s, decode(%s, 'hex'),
    now(), now() + interval '1 hour')`,
		literal(scenarioCaller), literal(key), literal(fingerprint(http.MethodPost, path, body)),
		status, literal(storedLocation), literal(hex.EncodeToString(sealed))))
	return err
}

func (s *servedSteps) inFlight(ctx context.Context, path, body, key string) error {
	_, err := s.query(ctx, fmt.Sprintf(`INSERT INTO idempotency_keys
    (caller, key, fingerprint, state, locked_until, created_at, expires_at)
VALUES (%s, %s, %s, 'in_flight', now() + interval '1 hour', now(), now() + interval '1 day')`,
		literal(scenarioCaller), literal(key), literal(fingerprint(http.MethodPost, path, body))))
	return err
}

func (s *servedSteps) keyed(ctx context.Context, name, path, body, key string) (response, error) {
	return s.sendThrough(ctx, name, http.MethodPost, path, json.RawMessage(body),
		sdk.Header{Name: sdk.IdempotencyKeyHeader, Value: key})
}

func (s *servedSteps) replays(ctx context.Context, path, body, key, name string, status int) error {
	resp, err := s.keyed(ctx, name, path, body, key)
	if err != nil {
		return err
	}
	if resp.status != status || resp.header.Get("Idempotency-Replayed") != "true" ||
		resp.header.Get("Location") != storedLocation || string(resp.body) != storedBody {
		return fmt.Errorf("%w: POST %s through %s answered %d %v: %s, want the stored %d",
			errStep, path, name, resp.status, resp.header, resp.body, status)
	}
	return nil
}

func (s *servedSteps) keyedAnswers(
	ctx context.Context,
	path, body, key, name, code, slug string,
) error {
	resp, err := s.keyed(ctx, name, path, body, key)
	if err != nil {
		return err
	}
	return isProblem(resp, "POST "+path+" with key "+key+" through "+name, code, slug)
}

// lockLapses ages the key's lock as if its replica had crashed an hour ago.
func (s *servedSteps) lockLapses(ctx context.Context, key string) error {
	_, err := s.query(ctx, fmt.Sprintf(
		`UPDATE idempotency_keys SET locked_until = now() - interval '1 second' WHERE key = %s`,
		literal(key)))
	return err
}

func (s *servedSteps) notStored(ctx context.Context, key string) error {
	n, err := s.query(ctx, "SELECT count(*) FROM idempotency_keys WHERE key = "+literal(key))
	if err != nil {
		return err
	}
	if strings.TrimSpace(n) != "0" {
		return fmt.Errorf("%w: key %s is still stored", errStep, key)
	}
	return nil
}

// disable writes the switch as the operator API will, notifying every
// replica in the same transaction, so the notification follows the commit.
func (s *servedSteps) disable(ctx context.Context, caller, past string) error {
	until := "NULL"
	if past != "" {
		until = "now() - interval '1 minute'"
	}
	_, err := s.query(ctx, fmt.Sprintf(
		`INSERT INTO api_disabled_callers (caller, until, reason) VALUES (%s, %s, 'acceptance');
NOTIFY infraweave_invalidate, 'api_disabled_callers'`, literal(caller), until))
	return err
}

func (s *servedSteps) enable(ctx context.Context, caller string) error {
	_, err := s.query(ctx, fmt.Sprintf(`DELETE FROM api_disabled_callers WHERE caller = %s;
NOTIFY infraweave_invalidate, 'api_disabled_callers'`, literal(caller)))
	return err
}
