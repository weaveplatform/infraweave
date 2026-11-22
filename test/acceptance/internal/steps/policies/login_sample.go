package policies

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerDefaultLoginSample(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(
		`^token "(\w+)" eventually reads the default "(REGO_V0|REGO_V1)" LOGIN sample of policy "([^"]+)" through ([AB])$`,
		func(ctx context.Context, token, engine, policy, replica string) error {
			return deps.Eventually(ctx, "actual default LOGIN sample", func(ctx context.Context) (bool, error) {
				var view api.Policy
				if err := readReplayJSON(ctx, w, replica, token, "/api/v1/policies/"+w.IDs[policy], &view); err != nil {
					return false, err
				}
				if view.SampleCount != 1 || len(view.EvaluationRecords) != 1 {
					return false, nil
				}
				var sample api.PolicyEvaluationSample
				if err := readReplayJSON(
					ctx,
					w,
					replica,
					token,
					"/api/v1/policies/"+view.Id+"/evaluation-sample?key="+url.QueryEscape(view.EvaluationRecords[0].Key),
					&sample,
				); err != nil {
					return false, err
				}
				var input struct {
					Session struct {
						Login string `json:"login"`
					} `json:"session"`
				}
				var decision struct {
					Allow  bool     `json:"allow"`
					Sample bool     `json:"sample"`
					Flag   []string `json:"flag"`
					Team   []string `json:"team"`
				}
				if json.Unmarshal([]byte(sample.Input), &input) != nil || json.Unmarshal([]byte(sample.Results), &decision) != nil ||
					!decision.Allow || !decision.Sample || !slices.Equal(decision.Flag, []string{"login-no-run"}) ||
					len(decision.Team) != 2 || !slices.Contains(decision.Team, "ci") || !slices.Contains(decision.Team, "ops") ||
					input.Session.Login != harness.Member.Key ||
					string(sample.EngineType) != engine ||
					sample.Outcome != "allow" ||
					sample.Error != "" ||
					sample.Body != view.Body {
					return false, fmt.Errorf("%w: default LOGIN sample lost its actor, dialect, source or computed helper/comprehension/flag result", harness.ErrStep)
				}
				return true, nil
			})
		},
	)
}
