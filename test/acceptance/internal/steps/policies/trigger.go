package policies

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerTrigger(sc *godog.ScenarioContext, w *harness.World) {
	registerTriggerRecovery(sc, w)
	sc.Step(`^the answer's commit hash is "([^"]+)"$`, func(want string) error {
		var run api.Run
		if err := json.Unmarshal(w.Last().Body, &run); err != nil {
			return fmt.Errorf("decode triggered run commit: %w", err)
		}
		if run.Commit.Hash != w.Resolve(want) {
			return fmt.Errorf("%w: triggered run used a different commit", harness.ErrStep)
		}
		return nil
	})
	sc.Step(`^token "(\w+)" eventually reads the single policy-triggered run of stack "([^"]+)" from stack "([^"]+)" run "(\w+)" as "(\w+)" through ([AB])$`,
		func(ctx context.Context, token, target, sourceStack, source, child, replica string) error {
			parent := "policy/" + sourceStack + "/" + w.IDs[source]
			return deps.Eventually(ctx, "single durable policy-triggered run of "+target, func(ctx context.Context) (bool, error) {
				response, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/stacks/"+url.PathEscape(target)+"/runs", nil)
				if err != nil {
					return false, err
				}
				if response.Status != http.StatusOK {
					return false, fmt.Errorf("%w: target run listing answered %d", harness.ErrStep, response.Status)
				}
				var page api.RunPage
				if err := json.Unmarshal(response.Body, &page); err != nil {
					return false, fmt.Errorf("decode policy target runs: %w", err)
				}
				matches := []api.Run{}
				for _, run := range page.Items {
					if run.TriggeredBy != nil && *run.TriggeredBy == parent {
						matches = append(matches, run)
					}
				}
				if len(matches) > 1 {
					return false, fmt.Errorf("%w: source created %d target runs", harness.ErrStep, len(matches))
				}
				if len(matches) == 0 {
					return false, nil
				}
				w.IDs[child] = matches[0].Id
				return true, nil
			})
		})
	sc.Step(`^the answer's TRIGGER receipt outcomes are "([^"]*)"$`, func(want string) error {
		var run api.Run
		if err := json.Unmarshal(w.Last().Body, &run); err != nil {
			return fmt.Errorf("decode trigger receipts: %w", err)
		}
		outcomes := []string{}
		for _, receipt := range run.PolicyReceipts {
			if receipt.PolicyType == api.PolicyTypeTRIGGER {
				outcomes = append(outcomes, receipt.Outcome)
			}
		}
		if strings.Join(outcomes, ", ") != want {
			return fmt.Errorf("%w: TRIGGER outcomes %v, want %s", harness.ErrStep, outcomes, want)
		}
		return nil
	})
	sc.Step(`^token "(\w+)" eventually reads TRIGGER outcomes "([^"]*)" for run "(\w+)" through ([AB])$`,
		func(ctx context.Context, token, want, run, replica string) error {
			return deps.Eventually(ctx, "committed terminal TRIGGER receipts", func(ctx context.Context) (bool, error) {
				response, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/runs/"+url.PathEscape(w.IDs[run]), nil)
				if err != nil {
					return false, err
				}
				if response.Status != http.StatusOK {
					return false, fmt.Errorf("%w: source run read answered %d", harness.ErrStep, response.Status)
				}
				var source api.Run
				if err := json.Unmarshal(response.Body, &source); err != nil {
					return false, fmt.Errorf("decode terminal source receipts: %w", err)
				}
				outcomes := []string{}
				for _, receipt := range source.PolicyReceipts {
					if receipt.PolicyType == api.PolicyTypeTRIGGER {
						outcomes = append(outcomes, receipt.Outcome)
					}
				}
				return strings.Join(outcomes, ", ") == want, nil
			})
		})
	sc.Step(`^the answer contains a TRIGGER error receipt "([A-Z_]+)"$`, func(code string) error {
		var run api.Run
		if err := json.Unmarshal(w.Last().Body, &run); err != nil {
			return fmt.Errorf("decode trigger error receipt: %w", err)
		}
		for _, receipt := range run.PolicyReceipts {
			if receipt.PolicyType == api.PolicyTypeTRIGGER && receipt.Error != nil && receipt.Error.Code == code {
				return nil
			}
		}
		return fmt.Errorf("%w: no safe TRIGGER %s error receipt", harness.ErrStep, code)
	})
	sc.Step(`^token "(\w+)" reads no policy-triggered run of stack "([^"]+)" from stack "([^"]+)" run "(\w+)" through ([AB])$`,
		func(ctx context.Context, token, target, sourceStack, source, replica string) error {
			response, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/stacks/"+url.PathEscape(target)+"/runs", nil)
			if err != nil {
				return err
			}
			if response.Status != http.StatusOK {
				return fmt.Errorf("%w: target run listing answered %d", harness.ErrStep, response.Status)
			}
			var page api.RunPage
			if err := json.Unmarshal(response.Body, &page); err != nil {
				return fmt.Errorf("decode refused target runs: %w", err)
			}
			for _, run := range page.Items {
				if run.TriggeredBy != nil && *run.TriggeredBy == "policy/"+sourceStack+"/"+w.IDs[source] {
					return fmt.Errorf("%w: refused target has a policy-triggered run", harness.ErrStep)
				}
			}
			return nil
		})
}
