package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cucumber/godog"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type deliverySteps struct {
	w      *harness.World
	gitlab *gitlabSteps
	hooks  *gitlabWebhookSteps
}

func (s *deliverySteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^GitLab source requests fail before event run creation$`, func() { s.gitlab.fake.FailPath("/projects/", gitlabfake.ServerError, 0) })
	sc.Step(`^GitLab source requests recover$`, func() { s.gitlab.fake.Fail(gitlabfake.None, 0) })
	sc.Step(`^delivery "(\w+)" of integration "(\w+)" is kept as "(\w+)"$`, s.keep)
	sc.Step(`^delivery "(\w+)" exhausts processing before creating runs$`, s.failed)
	sc.Step(`^token "(\w+)" lists integration "(\w+)" deliveries with page size (\d+) after "([^"]*)" through ([AB])$`, s.list)
	sc.Step(`^the delivery page contains only "(\w+)" in state "(RECEIVED|DISPATCHED|FAILED|EXPIRED)" and keeps cursor "(\w+)"$`, s.page)
	sc.Step(`^token "(\w+)" replays delivery "(\w+)" of integration "(\w+)" with key "([^"]*)" through ([AB])$`, s.replay)
	sc.Step(`^the delivery replay answer is cached$`, func() error {
		if s.w.Last().Header.Get("Idempotency-Replayed") != "true" {
			return fmt.Errorf("%w: replay response was not cached", harness.ErrStep)
		}
		return nil
	})
	sc.Step(`^delivery "(\w+)" generation (\d+) settles with (\d+) newly created runs$`, s.settled)
	sc.Step(`^the home account has (\d+) caller-attributed delivery replay audits$`, s.audits)
	sc.Step(`^GitLab sends a large retained push "(\w+)" for integration "(\w+)" through ([AB])$`, s.large)
	sc.Step(`^delivery "(\w+)" has an elapsed but unswept inbox body$`, s.expire)
	sc.Step(`^delivery "(\w+)" keeps replay generation (\d+)$`, s.generation)
}
func (s *deliverySteps) keep(ctx context.Context, name, integration, key string) error {
	delivery, ok := s.hooks.deliveries[name]
	if !ok {
		return fmt.Errorf("%w: missing provider delivery", harness.ErrStep)
	}
	id, err := kernel.ParseULID(s.w.IDs[integration])
	if err != nil {
		return err
	}
	var kept kernel.ULID
	if err := s.w.Row(ctx, `SELECT id FROM vcs_deliveries WHERE integration_id=$1 AND delivery_id=$2`, []any{id.UUID(), delivery.ID}, &kept); err != nil {
		return err
	}
	s.w.IDs[key] = kept.String()
	return nil
}
func (s *deliverySteps) failed(ctx context.Context, key string) error {
	return deps.Eventually(ctx, "real processing attempts exhaust before run creation", func(ctx context.Context) (bool, error) {
		var state, last string
		var runs int
		var dead bool
		err := s.w.Row(ctx, `SELECT state,last_error,runs_created,EXISTS(SELECT 1 FROM jobs WHERE account_id=d.account_id AND kind='vcs.process_delivery' AND unique_key=$2 AND state='dead') FROM vcs_deliveries d WHERE id=$1`, []any{harness.UUIDOf(s.w.IDs[key]), s.w.IDs[key]}, &state, &last, &runs, &dead)
		if err != nil {
			return false, err
		}
		if state != "failed" || !dead {
			return false, nil
		}
		if runs != 0 || last == "" {
			return false, fmt.Errorf("%w: failure did not precede run creation", harness.ErrStep)
		}
		return true, nil
	})
}
func (s *deliverySteps) list(ctx context.Context, token, integration string, first int, after, replica string) error {
	input := map[string]any{"first": first}
	if after != "" {
		input["after"] = s.w.IDs[after]
	}
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	path := "/api/v1/vcs-integrations/" + s.w.IDs[integration] + "/deliveries?input=" + url.QueryEscape(string(body))
	r, err := s.w.Do(ctx, replica, token, http.MethodGet, path, nil)
	if err != nil && r.Status == 0 {
		return err
	}
	return nil
}
func (s *deliverySteps) page(key, state, cursor string) error {
	var page struct {
		Edges []struct {
			Cursor string                     `json:"cursor"`
			Node   map[string]json.RawMessage `json:"node"`
		} `json:"edges"`
	}
	r := s.w.Last()
	if r.Status != 200 || json.Unmarshal(r.Body, &page) != nil || len(page.Edges) != 1 {
		return fmt.Errorf("%w: expected one delivery edge", harness.ErrStep)
	}
	node := page.Edges[0].Node
	for _, field := range []string{"id", "deliveryId", "event", "receivedAt", "state", "lastError", "runsCreated"} {
		if _, ok := node[field]; !ok {
			return fmt.Errorf("%w: missing public delivery field %s", harness.ErrStep, field)
		}
	}
	if len(node) != 7 || string(node["id"]) != `"`+s.w.IDs[key]+`"` || string(node["state"]) != `"`+state+`"` {
		return fmt.Errorf("%w: delivery projection/identity incorrect", harness.ErrStep)
	}
	if state == "FAILED" && string(node["lastError"]) == `""` {
		return fmt.Errorf("%w: failed delivery omitted safe diagnostic", harness.ErrStep)
	}
	s.w.IDs[cursor] = page.Edges[0].Cursor
	fmt.Printf("Delivery API evidence: %s\n", r.Body)
	return nil
}
func (s *deliverySteps) replay(ctx context.Context, token, key, integration, idempotency, replica string) error {
	path := "/api/v1/vcs-integrations/" + s.w.IDs[integration] + "/deliveries/" + s.w.IDs[key] + "/replay"
	var headers []sdk.Header
	if idempotency != "" {
		headers = append(headers, sdk.Header{Name: "Idempotency-Key", Value: idempotency})
	}
	r, err := s.w.Do(ctx, replica, token, http.MethodPost, path, nil, headers...)
	if err != nil && r.Status == 0 {
		return err
	}
	return nil
}
func (s *deliverySteps) settled(ctx context.Context, key string, generation, runs int) error {
	return deps.Eventually(ctx, "replayed parent and children settle", func(ctx context.Context) (bool, error) {
		var state string
		var replay, created, active, dead int
		err := s.w.Row(ctx, `SELECT d.state,d.replays,d.runs_created,
   (SELECT count(*) FROM jobs j WHERE j.account_id=d.account_id AND j.kind IN ('vcs.process_delivery','vcs.evaluate_event') AND (j.payload->>'id'=$2 OR j.payload->>'delivery'=$2) AND coalesce((j.payload->>'replays')::bigint,0)=$3 AND j.state IN ('pending','running')),
   (SELECT count(*) FROM jobs j WHERE j.account_id=d.account_id AND j.kind IN ('vcs.process_delivery','vcs.evaluate_event') AND (j.payload->>'id'=$2 OR j.payload->>'delivery'=$2) AND coalesce((j.payload->>'replays')::bigint,0)=$3 AND j.state='dead')
   FROM vcs_deliveries d WHERE d.id=$1`, []any{harness.UUIDOf(s.w.IDs[key]), s.w.IDs[key], generation}, &state, &replay, &created, &active, &dead)
		if err != nil {
			return false, err
		}
		if active != 0 {
			return false, nil
		}
		if dead != 0 {
			return false, fmt.Errorf("%w: replay generation has dead work", harness.ErrStep)
		}
		if state != "dispatched" || replay != generation {
			return false, nil
		}
		if created != runs {
			return false, fmt.Errorf("%w: replay created %d runs, want%d", harness.ErrStep, created, runs)
		}
		fmt.Printf("Delivery replay evidence: generation=%d new-runs=%d all work settled\n", replay, created)
		return true, nil
	})
}
func (s *deliverySteps) audits(ctx context.Context, want int) error {
	var count int
	var good bool
	err := s.w.Row(ctx, `SELECT count(*),coalesce(bool_and(actor_username=$2 AND actor_run IS NULL AND event_type='UPDATE' AND affected_type='VCS_INTEGRATION' AND coalesce(remote_ip,'')<>'' AND body IS NULL),true) FROM audit_trail_entries WHERE account_id=$1 AND action='vcs_integration.replay_delivery'`, []any{harness.UUIDOf(harness.Home.Account), harness.Home.Key}, &count, &good)
	if err != nil {
		return err
	}
	if count != want || !good {
		return fmt.Errorf("%w: expected %d caller replay audits, got%d valid=%t", harness.ErrStep, want, count, good)
	}
	return nil
}
func (s *deliverySteps) large(ctx context.Context, name, integration, replica string) error {
	delivery, err := s.gitlab.fake.PushWebhook("acme/platform", "infra", "main", s.w.IDs["seed"], s.w.IDs["seed"])
	if err != nil {
		return err
	}
	delivery.ID = kernel.NewULID().UUID()
	delivery.Body = append(delivery.Body[:len(delivery.Body)-1], []byte(`,"padding":"`+strings.Repeat("p", 262145)+`"}`)...)
	s.hooks.deliveries[name] = delivery
	return s.hooks.sendDelivery(ctx, integration, replica, s.hooks.secret.Reveal(), delivery)
}
func (s *deliverySteps) expire(ctx context.Context, key string) error {
	var count int
	if err := s.w.Row(ctx, `WITH changed AS(UPDATE blob_objects SET expires_at=now()-interval '1 second' WHERE store='inbox' AND state='live' AND key=(SELECT body_key FROM vcs_deliveries WHERE id=$1) RETURNING key) SELECT count(*) FROM changed`, []any{harness.UUIDOf(s.w.IDs[key])}, &count); err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%w: no live retained inbox object to expire", harness.ErrStep)
	}
	return nil
}
func (s *deliverySteps) generation(ctx context.Context, key string, want int) error {
	var got int
	if err := s.w.Row(ctx, `SELECT replays FROM vcs_deliveries WHERE id=$1`, []any{harness.UUIDOf(s.w.IDs[key])}, &got); err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%w: generation changed to%d, want%d", harness.ErrStep, got, want)
	}
	return nil
}
