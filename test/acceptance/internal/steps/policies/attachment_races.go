package policies

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"golang.org/x/sync/errgroup"

	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

const oncePolicy = "package infraweave\nwarn contains \"selected once\" if { print(\"attachment-evaluated\") }\n"

func registerAttachmentRaces(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^explicit and automatic attachments race with label writes on both replicas$`, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		a, err := w.Client("A", w.Tokens["h"])
		if err != nil {
			return err
		}
		b, err := w.Client("B", w.Tokens["h"])
		if err != nil {
			return err
		}
		policyPath := w.Resolve("/api/v1/policies/{policy}")
		attachPath := w.Resolve("/api/v1/stacks/infra/policies/{policy}")
		update := func(labels []string) map[string]any {
			return map[string]any{"name": "Plan guard", "body": oncePolicy, "labels": labels}
		}
		out, err := raceRequests(ctx, []raceRequest{{a, "PUT", policyPath, update([]string{"autoattach:*"})}, {b, "PUT", attachPath, nil}})
		if err != nil {
			return err
		}
		var attachment struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(out[1].Body, &attachment); err != nil || attachment.ID == "" {
			return fmt.Errorf("%w: race attachment identity: %s %w", harness.ErrStep, out[1].Body, err)
		}
		w.IDs["attachment"] = attachment.ID
		for round := range 4 {
			labels := []string{"prod"}
			if round%2 == 0 {
				labels = []string{}
			}
			out, err := raceRequests(ctx, []raceRequest{
				{a, "PUT", policyPath, update([]string{"autoattach:prod"})},
				{b, "PUT", "/api/v1/stacks/infra", map[string]any{"name": "Infra", "repository": "infra", "branch": "main", "provider": "GIT", "space": "acme", "labels": labels}},
				{a, "PUT", attachPath, nil},
			})
			if err != nil {
				return err
			}
			var kept struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(out[2].Body, &kept) != nil || kept.ID != attachment.ID {
				return fmt.Errorf("%w: duplicate attach replaced race identity: %s", harness.ErrStep, out[2].Body)
			}
		}
		if _, err := raceRequests(ctx, []raceRequest{{a, "PUT", policyPath, update(nil)}}); err != nil {
			return err
		}
		r, err := w.Do(ctx, "B", "h", "GET", "/api/v1/stacks/infra", nil)
		if err != nil {
			return err
		}
		var target struct {
			Policies []struct {
				ID        string `json:"id"`
				Automatic bool   `json:"isAutoattached"`
			} `json:"attachedPolicies"`
		}
		if r.Status != 200 || json.Unmarshal(r.Body, &target) != nil || len(target.Policies) != 1 || target.Policies[0].ID != attachment.ID || target.Policies[0].Automatic {
			return fmt.Errorf("%w: race did not converge to one explicit relation: %d %s", harness.ErrStep, r.Status, r.Body)
		}
		return nil
	})
	sc.Step(`^the selected policy collection evaluates exactly once$`, func(ctx context.Context) error {
		var target struct {
			Policies []struct {
				Policy string    `json:"policyId"`
				Body   string    `json:"policyBody"`
				Type   rego.Type `json:"policyType"`
			} `json:"attachedPolicies"`
		}
		if err := json.Unmarshal(w.Last().Body, &target); err != nil || len(target.Policies) != 1 || target.Policies[0].Policy != w.IDs["policy"] {
			return fmt.Errorf("%w: selected policy collection: %s %w", harness.ErrStep, w.Last().Body, err)
		}
		e, err := rego.New(rego.Defaults())
		if err != nil {
			return fmt.Errorf("construct selected-policy evaluator: %w", err)
		}
		var requests []rego.Request
		for _, policy := range target.Policies {
			requests = append(requests, rego.Request{Policy: rego.Policy{Body: policy.Body, Type: policy.Type, Engine: rego.V1}, Input: json.RawMessage(`{}`)})
		}
		out := e.EvaluateAll(ctx, requests)
		if len(out) != 1 || out[0].Err != nil || strings.Count(out[0].Result.Print, "attachment-evaluated") != 1 {
			return fmt.Errorf("%w: selected policies were not evaluated once: %+v", harness.ErrStep, out)
		}
		return nil
	})
}

type raceRequest struct {
	client       *sdk.Client
	method, path string
	body         any
}

// Requests own their clients/results and never mutate World concurrently. Every
// goroutine ends before the step reads responses or updates remembered IDs.
func raceRequests(ctx context.Context, calls []raceRequest) ([]*sdk.Response, error) {
	out := make([]*sdk.Response, len(calls))
	g, ctx := errgroup.WithContext(ctx)
	start := make(chan struct{})
	for i, call := range calls {
		g.Go(func() error {
			select {
			case <-start:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
			response, err := call.client.Do(ctx, call.method, call.path, call.body)
			if err != nil {
				return fmt.Errorf("attachment race %s %s: %w", call.method, call.path, err)
			}
			if response == nil || response.Status != 200 {
				return fmt.Errorf("%w: attachment race %s %s: %+v", harness.ErrStep, call.method, call.path, response)
			}
			out[i] = response
			return nil
		})
	}
	close(start)
	return out, g.Wait()
}
