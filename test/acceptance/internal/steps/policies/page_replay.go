package policies

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerCapturedPage(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(
		`^token "(\w+)" reads safe PLAN capture metadata in space "([^"]+)" but cannot open its input through ([AB])$`,
		func(ctx context.Context, token, space, replica string) error {
			response, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/policy-input-captures?space="+url.QueryEscape(space)+"&type=PLAN", nil)
			if err != nil {
				return err
			}
			var page api.PolicyInputCaptureSummaryPage
			if response.Status != 200 || json.Unmarshal(response.Body, &page) != nil || len(page.Items) == 0 || bytes.Contains(response.Body, []byte(`"input"`)) {
				return fmt.Errorf("%w: safe capture list leaked input or lost visible metadata", harness.ErrStep)
			}
			response, err = w.Do(ctx, replica, token, http.MethodGet, "/api/v1/policy-input-captures/"+page.Items[0].Id, nil)
			if err != nil {
				return err
			}
			return harness.IsProblem(response, "non-admin captured input", "403", "forbidden")
		},
	)

	sc.Step(
		`^token "(\w+)" reads an? (PLAN|APPROVAL) input capture in space "([^"]+)" masking "([^"]*)" through ([AB])$`,
		func(ctx context.Context, token, kind, space, secret, replica string) error {
			return deps.Eventually(ctx, "sanitized PLAN capture without depending on a sample", func(ctx context.Context) (bool, error) {
				var page api.PolicyInputCaptureSummaryPage
				if err := readReplayJSON(ctx, w, replica, token, "/api/v1/policy-input-captures?space="+url.QueryEscape(space)+"&type="+kind, &page); err != nil {
					return false, err
				}
				if len(page.Items) == 0 {
					return false, nil
				}
				var capture api.PolicyInputCapture
				if err := readReplayJSON(ctx, w, replica, token, "/api/v1/policy-input-captures/"+page.Items[0].Id, &capture); err != nil {
					return false, err
				}
				if capture.Input == nil {
					return false, nil
				}
				if capture.Id != page.Items[0].Id || string(capture.Kind) != kind || !json.Valid([]byte(*capture.Input)) {
					return false, fmt.Errorf("%w: capture identity/kind/input changed", harness.ErrStep)
				}
				// Captures blank marked leaves; samples retain the exact sanitized
				// Terraform document, including its last-eight hash.
				if secret != "" && (bytes.Contains([]byte(*capture.Input), []byte(secret)) || !bytes.Contains([]byte(*capture.Input), []byte("[redacted]"))) {
					return false, fmt.Errorf("%w: capture retained a marked value or lost its privacy placeholder", harness.ErrStep)
				}
				return true, nil
			})
		},
	)
	sc.Step(
		`^token "(\w+)" reads the PLAN page of policy "([^"]+)" with (\d+) records and (\d+) samples, masking "([^"]*)", through ([AB])$`,
		func(ctx context.Context, token, policy string, records, samples int, secret, replica string) error {
			return deps.Eventually(ctx, "real PLAN page and retained input", func(ctx context.Context) (bool, error) {
				var parent api.Policy
				if err := readReplayJSON(ctx, w, replica, token, "/api/v1/policies/"+w.IDs[policy], &parent); err != nil {
					return false, err
				}
				if parent.SampleCount != int32(samples) || len(parent.EvaluationRecords) != records {
					return false, nil
				}
				if parent.Id != w.IDs[policy] || parent.Type != api.PolicyTypePLAN || parent.EngineType != api.REGOV1 || !parent.HasAttachedStacks ||
					len(parent.AttachedStacks) != 1 {
					return false, fmt.Errorf("%w: PLAN page identity/engine/attachments differ", harness.ErrStep)
				}
				var page api.SearchPolicyEvaluationRecordOutput
				if err := readReplayJSON(ctx, w, replica, token, "/api/v1/policies/"+parent.Id+"/search-evaluation-records?input="+url.QueryEscape(`{}`), &page); err != nil {
					return false, err
				}
				if len(page.Edges) != records || page.PageInfo.HasNextPage {
					return false, fmt.Errorf("%w: PLAN metadata search count differs", harness.ErrStep)
				}
				for i, edge := range page.Edges {
					if !reflect.DeepEqual(edge.Node, parent.EvaluationRecords[i]) || edge.Node.Outcome != "allow" || i > 0 && page.Edges[i-1].Node.Timestamp < edge.Node.Timestamp {
						return false, fmt.Errorf("%w: PLAN preview and search disagree", harness.ErrStep)
					}
				}
				var suggestions api.SearchSuggestionsOutput
				if err := readReplayJSON(
					ctx,
					w,
					replica,
					token,
					"/api/v1/policies/"+parent.Id+"/search-evaluation-record-suggestions?input="+url.QueryEscape(`{}`),
					&suggestions,
				); err != nil {
					return false, err
				}
				names := make([]string, len(suggestions.Fields))
				for i, field := range suggestions.Fields {
					names[i] = field.Name
				}
				slices.Sort(names)
				want := []string{"createdAt", "outcome", "runDriftDetection", "runType", "stackName", "stackRepository"}
				if suggestions.FilteredCount != int32(records) || !slices.Equal(names, want) {
					return false, fmt.Errorf("%w: PLAN fields/count differ from C273's captured schema", harness.ErrStep)
				}
				if records == 0 {
					return true, nil
				}
				response, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/policies/"+parent.Id+"/evaluation-sample?key="+url.QueryEscape(page.Edges[0].Node.Key), nil)
				if err != nil {
					return false, err
				}
				if samples == 0 {
					return response.Status == http.StatusNotFound, nil
				}
				var sample api.PolicyEvaluationSample
				if response.Status != 200 || json.Unmarshal(response.Body, &sample) != nil || !json.Valid([]byte(sample.Input)) || !json.Valid([]byte(sample.Results)) ||
					sample.Outcome != "allow" ||
					sample.Body != parent.Body ||
					sample.EngineType != parent.EngineType {
					return false, fmt.Errorf("%w: PLAN sample lost its actual evaluated source/input/results", harness.ErrStep)
				}
				if secret != "" {
					digest := sha256.Sum256([]byte(secret))
					masked := hex.EncodeToString(digest[len(digest)-4:])
					if bytes.Contains([]byte(sample.Input), []byte(secret)) || !bytes.Contains([]byte(sample.Input), []byte(masked)) {
						return false, fmt.Errorf("%w: sensitive Terraform input was not masked with the captured hash contract", harness.ErrStep)
					}
				}
				return true, nil
			})
		},
	)
}

func readReplayJSON(ctx context.Context, w *harness.World, replica, token, path string, out any) error {
	response, err := w.Do(ctx, replica, token, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if response.Status != http.StatusOK {
		return fmt.Errorf("%w: replay read %s returned %d", harness.ErrStep, path, response.Status)
	}
	if err := json.Unmarshal(response.Body, out); err != nil {
		return fmt.Errorf("decode replay response: %w", err)
	}
	return nil
}
