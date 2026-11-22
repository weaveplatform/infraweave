package policies

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerProjectionAttachments(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(
		`^token "(\w+)" reads stack "([^"]+)" and attachment "([^"]+)" for policy "([^"]+)" with committed history through ([AB])$`,
		func(ctx context.Context, token, stack, attachment, policy, replica string) error {
			got, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/policies/"+w.IDs[policy], nil)
			if err != nil {
				return err
			}
			var parent api.Policy
			if got.Status != 200 || json.Unmarshal(got.Body, &parent) != nil || len(parent.EvaluationRecords) == 0 {
				return fmt.Errorf("%w: no public policy history", harness.ErrStep)
			}
			got, err = w.Do(ctx, replica, token, http.MethodGet, "/api/v1/stacks/"+stack+"/policy-attachment?id="+w.IDs[attachment], nil)
			if err != nil {
				return err
			}
			var link api.StackPolicyAttachment
			if got.Status != 200 || json.Unmarshal(got.Body, &link) != nil {
				return fmt.Errorf("%w: attachment returned %d", harness.ErrStep, got.Status)
			}
			if link.PolicyId != parent.Id || link.PolicyBody != parent.Body || link.PolicyName != parent.Name ||
				!reflect.DeepEqual(link.EvaluationRecords, parent.EvaluationRecords) {
				return fmt.Errorf("%w: attachment identity/body/name/history differs from policy", harness.ErrStep)
			}
			got, err = w.Do(ctx, replica, token, http.MethodGet, "/api/v1/stacks/"+stack, nil)
			if err != nil {
				return err
			}
			var target api.Stack
			if got.Status != 200 || json.Unmarshal(got.Body, &target) != nil || len(target.AttachedPolicies) != 1 {
				return fmt.Errorf("%w: stack view returned %d or wrong attachment count", harness.ErrStep, got.Status)
			}
			if !reflect.DeepEqual(target.AttachedPolicies[0], link) {
				return fmt.Errorf("%w: stack and attachment history differ", harness.ErrStep)
			}
			return nil
		},
	)
	sc.Step(`^token "(\w+)" cannot open the raw sample of policy "([^"]+)" through ([AB])$`, func(ctx context.Context, token, policy, replica string) error {
		got, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/policies/"+w.IDs[policy], nil)
		if err != nil {
			return err
		}
		var view api.Policy
		if got.Status != 200 || json.Unmarshal(got.Body, &view) != nil || len(view.EvaluationRecords) == 0 {
			return fmt.Errorf("%w: unreadable policy metadata", harness.ErrStep)
		}
		got, err = w.Do(ctx, replica, token, http.MethodGet, "/api/v1/policies/"+view.Id+"/evaluation-sample?key="+view.EvaluationRecords[0].Key, nil)
		if err != nil {
			return err
		}
		return harness.IsProblem(got, "child reader raw policy sample", "403", "forbidden")
	})
}
