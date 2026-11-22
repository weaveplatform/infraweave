package policies

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerProjections(sc *godog.ScenarioContext, w *harness.World) {
	registerProjectionAttachments(sc, w)
	registerCapturedPage(sc, w)
	registerDefaultLoginSample(sc, w)
	sc.Step(
		`^token "(\w+)" eventually reads policy "([^"]+)" with (\d+) visible attachments and empty evidence through ([AB])$`,
		func(ctx context.Context, token, name string, attachments int, replica string) error {
			return deps.Eventually(ctx, "current empty policy projection", func(ctx context.Context) (bool, error) {
				response, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/policies/"+w.IDs[name], nil)
				if err != nil {
					return false, err
				}
				var policy api.Policy
				if response.Status != 200 || json.Unmarshal(response.Body, &policy) != nil {
					return false, fmt.Errorf("%w: empty policy projection returned %d", harness.ErrStep, response.Status)
				}
				return len(policy.AttachedStacks) == attachments && policy.HasAttachedStacks == (attachments > 0) && policy.SampleCount == 0 &&
					len(policy.EvaluationRecords) == 0 &&
					policy.NotificationCount == 0, nil
			})
		},
	)
	sc.Step(
		`^token "(\w+)" eventually reads policy "([^"]+)" with "(retained|expired)" evidence projections and (\d+) visible attachments through ([AB])$`,
		func(ctx context.Context, token, name, state string, attachments int, replica string) error {
			return deps.Eventually(ctx, "public policy metadata and retained sample count", func(ctx context.Context) (bool, error) {
				var records, retained int
				if err := w.Row(
					ctx,
					`SELECT count(*),count(*) FILTER(WHERE i.published_at IS NOT NULL AND i.state='live' AND i.expires_at>statement_timestamp() AND b.state='live' AND (b.expires_at IS NULL OR b.expires_at>statement_timestamp()))
 FROM policy_evaluations e LEFT JOIN policy_input_objects i ON i.account_id=e.account_id AND i.id=e.sample_input_id
 LEFT JOIN blob_objects b ON b.store='policy-inputs' AND b.key=i.object_key WHERE e.account_id=$1 AND e.policy_id=$2`,
					[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[name])},
					&records,
					&retained,
				); err != nil {
					return false, err
				}
				if records == 0 || state == "retained" && retained == 0 || state == "expired" && retained != 0 {
					return false, nil
				}
				response, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/policies/"+w.IDs[name], nil)
				if err != nil {
					return false, err
				}
				var policy api.Policy
				if response.Status != 200 || json.Unmarshal(response.Body, &policy) != nil {
					return false, fmt.Errorf("%w: policy projection returned %d", harness.ErrStep, response.Status)
				}
				if int(policy.SampleCount) != retained || len(policy.EvaluationRecords) != min(records, 100) {
					return false, nil
				}
				if policy.Id != w.IDs[name] || policy.HasAttachedStacks != (attachments > 0) || len(policy.AttachedStacks) != attachments {
					return false, fmt.Errorf("%w: policy identity/visible attachment projection differs", harness.ErrStep)
				}
				for i, record := range policy.EvaluationRecords {
					if i > 0 && policy.EvaluationRecords[i-1].Timestamp < record.Timestamp {
						return false, fmt.Errorf("%w: policy records are not newest first", harness.ErrStep)
					}
				}
				return true, nil
			})
		},
	)
}
