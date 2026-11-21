package authz

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type schemeSteps struct {
	w        *harness.World
	saved    [sha256.Size]byte
	statuses []int
}

func (s *schemeSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the inactive authorization configuration is recorded$`, s.record)
	sc.Step(`^the saved authorization configuration is unchanged$`, s.unchanged)
	sc.Step(`^the session view names the SCHEME_SWITCH origin$`, s.origin)
	sc.Step(`^the home account has exactly (\d+) live sessions and (\d+) retained scheme grants$`, s.sessions)
	sc.Step(`^the account records exactly (\d+) credential-free scheme audit and outbox events$`, s.events)
	sc.Step(`^tokens "(\w+)" and "(\w+)" concurrently select LOGIN_POLICIES through A and B$`, s.concurrent)
	sc.Step(`^exactly one scheme switch succeeds and the other caller is refused$`, s.oneWins)
	sc.Step(`^the existing home key is exchanged without reseeding through ([AB]) for token "(\w+)"$`, s.exchange)
	sc.Step(`^token "(\w+)" selects "(MANAGED_USERS|LOGIN_POLICIES)" with idempotency key "([^"]+)" through ([AB])$`, s.keyed)
	sc.Step(`^the scheme switch response was replayed$`, func() error {
		if s.w.Last().Header.Get("Idempotency-Replayed") != "true" {
			return fmt.Errorf("%w: no idempotency replay header", harness.ErrStep)
		}
		return nil
	})
}

func (s *schemeSteps) origin() error {
	var body struct {
		Edges []struct {
			Node struct {
				Roles []struct {
					Origin string `json:"origin"`
				} `json:"roles"`
			} `json:"node"`
		} `json:"edges"`
	}
	if s.w.Last().Status != http.StatusOK || json.Unmarshal(s.w.Last().Body, &body) != nil || len(body.Edges) != 1 ||
		len(body.Edges[0].Node.Roles) != 1 || body.Edges[0].Node.Roles[0].Origin != "SCHEME_SWITCH" {
		return fmt.Errorf("%w: session view lost scheme-switch provenance: %s", harness.ErrStep, s.w.Last().Body)
	}
	return nil
}

// configuration independently compares complete persisted inactive rows. The
// report retains only their digest, never policy source or credential data.
func (s *schemeSteps) configuration(ctx context.Context) ([sha256.Size]byte, error) {
	db, err := s.w.Querier(ctx)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	acct := harness.Literal(harness.UUIDOf(harness.Tenants["home"].Account))
	parts := []string{}
	for _, table := range []string{"role_bindings", "account_users", "managed_user_groups", "policies", "account_settings"} {
		parts = append(parts, harness.Literal(table), `(SELECT coalesce(jsonb_agg(row ORDER BY row::text), '[]')
			FROM (SELECT to_jsonb(t) AS row FROM `+table+` t WHERE account_id = `+acct+`) AS rows)`)
	}
	got, err := db.Query(ctx, `SELECT jsonb_build_object(`+strings.Join(parts, ",")+`)::text`)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256([]byte(got)), nil
}

func (s *schemeSteps) record(ctx context.Context) error {
	var err error
	s.saved, err = s.configuration(ctx)
	return err
}

func (s *schemeSteps) unchanged(ctx context.Context) error {
	got, err := s.configuration(ctx)
	if err != nil {
		return err
	}
	if got != s.saved {
		return fmt.Errorf("%w: saved authorization configuration changed", harness.ErrStep)
	}
	return nil
}

func (s *schemeSteps) sessions(ctx context.Context, live, retained int) error {
	db, err := s.w.Querier(ctx)
	if err != nil {
		return err
	}
	acct := harness.Literal(harness.UUIDOf(harness.Tenants["home"].Account))
	got, err := db.Query(ctx, `SELECT (SELECT count(*) FROM sessions WHERE account_id = `+acct+`
		AND revoked_at IS NULL AND expires_at > now() AND (idle_expires_at IS NULL OR idle_expires_at > now()))::text
		|| ',' || (SELECT count(*) FROM session_grants g JOIN sessions s ON s.id = g.session_id
		WHERE g.account_id = `+acct+` AND s.revoked_at IS NULL AND s.expires_at > now()
		AND (s.idle_expires_at IS NULL OR s.idle_expires_at > now()) AND g.origin = 'SCHEME_SWITCH')::text`)
	if err != nil {
		return err
	}
	if strings.TrimSpace(got) != fmt.Sprintf("%d,%d", live, retained) {
		return fmt.Errorf("%w: live sessions/retained grants = %q, want %d,%d", harness.ErrStep, got, live, retained)
	}
	return nil
}

func (s *schemeSteps) events(ctx context.Context, count int) error {
	db, err := s.w.Querier(ctx)
	if err != nil {
		return err
	}
	acct := harness.Literal(harness.UUIDOf(harness.Tenants["home"].Account))
	got, err := db.Query(ctx, `SELECT jsonb_build_object(
		'audit', (SELECT count(*) FROM audit_trail_entries WHERE account_id = `+acct+`
		AND action = 'account.update_authorization_scheme' AND body ?& ARRAY['previous','current']
		AND (body - 'previous' - 'current') = '{}'::jsonb),
		'events', (SELECT count(*) FROM events WHERE account_id = `+acct+`
		AND kind = 'account.authorization_scheme_changed' AND document ?& ARRAY['previous','current']
		AND (document - 'previous' - 'current') = '{}'::jsonb))::text`)
	if err != nil {
		return err
	}
	var totals struct {
		Audit  int `json:"audit"`
		Events int `json:"events"`
	}
	if json.Unmarshal([]byte(got), &totals) != nil || totals.Audit != count || totals.Events != count {
		return fmt.Errorf("%w: scheme audit/events = %q, want %d each", harness.ErrStep, got, count)
	}
	return nil
}

func (s *schemeSteps) concurrent(ctx context.Context, first, second string) error {
	// World records responses in scenario order, so concurrent requests use
	// independent SDK clients and collect answers without touching World.
	left, err := s.w.Client("A", s.w.Tokens[first])
	if err != nil {
		return err
	}
	right, err := s.w.Client("B", s.w.Tokens[second])
	if err != nil {
		return err
	}
	type answer struct {
		status int
		err    error
	}
	answers := make(chan answer, 2)
	start := make(chan struct{})
	for _, client := range []*sdk.Client{left, right} {
		go func() {
			<-start
			r, err := client.Do(ctx, http.MethodPost, "/api/v1/account/update-authorization-scheme",
				map[string]string{"schema": "LOGIN_POLICIES"})
			if r == nil {
				answers <- answer{err: err}
				return
			}
			answers <- answer{status: r.Status}
		}()
	}
	close(start)
	for range 2 {
		r := <-answers
		if r.err != nil {
			return fmt.Errorf("%w: concurrent switch: %w", harness.ErrStep, r.err)
		}
		s.statuses = append(s.statuses, r.status)
	}
	return nil
}

func (s *schemeSteps) oneWins() error {
	slices.Sort(s.statuses)
	if len(s.statuses) == 2 && s.statuses[0] == http.StatusOK &&
		(s.statuses[1] == http.StatusUnauthorized || s.statuses[1] == http.StatusForbidden) {
		return nil
	}
	return fmt.Errorf("%w: concurrent scheme responses %v", harness.ErrStep, s.statuses)
}

func (s *schemeSteps) exchange(ctx context.Context, replica, name string) error {
	c, err := s.w.Client(replica, "")
	if err != nil {
		return err
	}
	token, err := harness.Tenants["home"].Exchange(ctx, c)
	if err != nil {
		return err
	}
	s.w.Tokens[name] = token
	return nil
}

func (s *schemeSteps) keyed(ctx context.Context, name, scheme, key, replica string) error {
	_, err := s.w.Do(ctx, replica, name, http.MethodPost, "/api/v1/account/update-authorization-scheme",
		map[string]string{"schema": scheme}, sdk.Header{Name: sdk.IdempotencyKeyHeader, Value: key})
	return err
}
