package policies

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerEvidenceReaders(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^public evidence handles for run "([^"]+)" and policy "([^"]+)" are kept through ([AB])$`, func(ctx context.Context, run, policy, replica string) error {
		return deps.Eventually(ctx, "public retained evidence handles", func(ctx context.Context) (bool, error) {
			response, err := w.Do(ctx, replica, "h", http.MethodGet, "/api/v1/runs/"+w.IDs[run], nil)
			if err != nil {
				return false, err
			}
			var view api.Run
			if response.Status != 200 || json.Unmarshal(response.Body, &view) != nil {
				return false, nil
			}
			found := false
			for _, receipt := range view.PolicyReceipts {
				if receipt.PolicySlug != nil && *receipt.PolicySlug == w.IDs[policy] && receipt.Sampled {
					w.IDs["evidence-receipt"] = receipt.Id
					found = true
					break
				}
			}
			if !found {
				return false, nil
			}
			response, err = w.Do(ctx, replica, "h", http.MethodGet, "/api/v1/policies/"+w.IDs[policy]+"/search-evaluation-records?input="+url.QueryEscape(`{}`), nil)
			if err != nil {
				return false, err
			}
			var records api.SearchPolicyEvaluationRecordOutput
			if response.Status != 200 || json.Unmarshal(response.Body, &records) != nil || len(records.Edges) == 0 {
				return false, nil
			}
			w.IDs["evidence-key"] = records.Edges[0].Node.Key
			response, err = w.Do(ctx, replica, "h", http.MethodGet, "/api/v1/policy-input-captures?space=legacy&type=APPROVAL", nil)
			if err != nil {
				return false, err
			}
			var captures api.PolicyInputCaptureSummaryPage
			if response.Status != 200 || json.Unmarshal(response.Body, &captures) != nil || len(captures.Items) == 0 {
				return false, nil
			}
			w.IDs["evidence-capture"] = captures.Items[0].Id
			return true, nil
		})
	})
	sc.Step(
		`^token "(\w+)" reads all six evidence operations for run "([^"]+)" and policy "([^"]+)" as "(root|unbound|child-admin|foreign|expired|feature-disabled)" through ([AB])$`,
		func(ctx context.Context, token, run, policy, mode, replica string) error {
			paths := evidencePaths(w, run, policy)
			for i, path := range paths {
				want := http.StatusOK
				switch mode {
				case "feature-disabled":
					want = http.StatusForbidden
				case "unbound":
					want = http.StatusNotFound
				case "child-admin":
					if i == 0 || i == 4 || i == 5 {
						want = http.StatusForbidden
					}
				case "foreign":
					if i != 3 {
						want = http.StatusNotFound
					}
				case "expired":
					if i == 0 || i == 4 || i == 5 {
						want = http.StatusNotFound
					}
				}
				response, err := w.Do(ctx, replica, token, http.MethodGet, path, nil)
				if err != nil {
					return err
				}
				if response.Status != want {
					return fmt.Errorf("%w: evidence operation %d (%s) status %d, want %d", harness.ErrStep, i, mode, response.Status, want)
				}
				if want != 200 {
					if mode == "feature-disabled" && !bytes.Contains(response.Body, []byte("urn:infraweave:problem:feature-disabled")) {
						return fmt.Errorf("%w: owned evidence route %d bypassed its feature control", harness.ErrStep, i)
					}
					if response.Header.Get("Content-Type") != "application/problem+json" {
						return fmt.Errorf("%w: evidence refusal is not a problem", harness.ErrStep)
					}
					continue
				}
				if i == 0 || i == 5 {
					var sample api.PolicyEvaluationSample
					if json.Unmarshal(response.Body, &sample) != nil || !json.Valid([]byte(sample.Input)) || !json.Valid([]byte(sample.Results)) ||
						!strings.Contains(sample.Print, "evidence-private-print") ||
						!strings.Contains(sample.Body, "evidence-private-source") ||
						sample.EngineType != api.REGOV1 {
						return fmt.Errorf("%w: real retained input/source/result/print lost in operation %d", harness.ErrStep, i)
					}
				} else {
					if bytes.Contains(response.Body, []byte("evidence-private-print")) || bytes.Contains(response.Body, []byte("evidence-private-source")) {
						return fmt.Errorf("%w: private source/print leaked into evidence metadata", harness.ErrStep)
					}
					if i == 3 && (mode == "foreign" || mode == "expired") {
						var list api.PolicyInputCaptureSummaryPage
						if json.Unmarshal(response.Body, &list) != nil || len(list.Items) != 0 {
							return fmt.Errorf("%w: foreign/expired capture listed", harness.ErrStep)
						}
					}
					if i == 4 {
						var capture api.PolicyInputCapture
						if json.Unmarshal(response.Body, &capture) != nil || capture.Input == nil || !json.Valid([]byte(*capture.Input)) {
							return fmt.Errorf("%w: current capture lost sanitized JSON", harness.ErrStep)
						}
					}
				}
			}
			return nil
		},
	)
	sc.Step(`^the evidence replicas restart with policies disabled$`, func(ctx context.Context) error {
		var err error
		for _, server := range w.Servers[:2] {
			err = errors.Join(err, server.Stop(ctx))
		}
		if err != nil {
			return err
		}
		w.Settings["features"] = map[string]any{"disabled": []string{"policies"}}
		for range 2 {
			if _, err := w.StartServer(ctx, nil); err != nil {
				return err
			}
		}
		w.Servers[0], w.Servers[2] = w.Servers[2], w.Servers[0]
		w.Servers[1], w.Servers[3] = w.Servers[3], w.Servers[1]
		return nil
	})
	sc.Step(`^all retained public policy evidence expires$`, func(ctx context.Context) error {
		_, pool, err := w.Keyring(ctx)
		if err != nil {
			return err
		}
		defer pool.Close()
		tag, err := pool.Exec(
			ctx,
			`UPDATE policy_input_objects SET expires_at=statement_timestamp()-interval '1 minute' WHERE account_id=$1 AND published_at IS NOT NULL`,
			harness.UUIDOf(harness.Home.Account),
		)
		if err != nil {
			return fmt.Errorf("expire real evidence: %w", err)
		}
		if tag.RowsAffected() < 2 {
			return fmt.Errorf("%w: no real sample and capture to expire", harness.ErrStep)
		}
		return nil
	})
	sc.Step(`^metadata evidence operations reject invalid typed filters through ([AB])$`, func(ctx context.Context, replica string) error {
		bad := url.QueryEscape(`{"predicates":[{"field":"privateInput","constraint":{"stringEquals":["secret"]}}]}`)
		for _, suffix := range []string{"search-evaluation-records", "search-evaluation-record-suggestions"} {
			response, err := w.Do(ctx, replica, "h", http.MethodGet, "/api/v1/policies/"+w.IDs["guard"]+"/"+suffix+"?input="+bad, nil)
			if err != nil {
				return err
			}
			if response.Status != http.StatusUnprocessableEntity {
				return fmt.Errorf("%w: unsafe/unknown metadata filter %s returned %d", harness.ErrStep, suffix, response.Status)
			}
		}
		response, err := w.Do(ctx, replica, "h", http.MethodGet, "/api/v1/policy-input-captures?space=legacy&type=APPROVAL&cursor=invalid", nil)
		if err != nil {
			return err
		}
		if response.Status != http.StatusBadRequest {
			return fmt.Errorf("%w: invalid capture cursor returned %d", harness.ErrStep, response.Status)
		}
		return nil
	})
}

func evidencePaths(w *harness.World, run, policy string) []string {
	base := "/api/v1/policies/" + w.IDs[policy]
	return []string{
		base + "/evaluation-sample?key=" + w.IDs["evidence-key"],
		base + "/search-evaluation-records?input=" + url.QueryEscape(`{}`),
		base + "/search-evaluation-record-suggestions?input=" + url.QueryEscape(`{}`),
		"/api/v1/policy-input-captures?space=legacy&type=APPROVAL",
		"/api/v1/policy-input-captures/" + w.IDs["evidence-capture"],
		"/api/v1/runs/" + w.IDs[run] + "/policy-receipt-sample?id=" + w.IDs["evidence-receipt"],
	}
}
