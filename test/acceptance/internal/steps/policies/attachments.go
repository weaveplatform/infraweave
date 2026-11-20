package policies

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerAttachments(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^policy reconciliation storage (fails|recovers)$`, func(ctx context.Context, state string) error {
		q, err := w.Querier(ctx)
		if err != nil {
			return err
		}
		statement := "DROP TRIGGER refuse_policy_reconcile ON jobs"
		if state == "fails" {
			statement = `CREATE FUNCTION refuse_policy_reconcile() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='policies.reconcile' THEN RAISE EXCEPTION 'injected reconciliation queue failure' USING ERRCODE='08006'; END IF; RETURN NEW; END $$;
CREATE TRIGGER refuse_policy_reconcile BEFORE INSERT ON jobs FOR EACH ROW EXECUTE FUNCTION refuse_policy_reconcile()`
		}
		if _, err := q.Query(ctx, statement); err != nil {
			return fmt.Errorf("reconciliation storage injection: %w", err)
		}
		return nil
	})
	sc.Step(`^the stack's attached policy IDs are "([^"]*)"$`, func(want string) error {
		var out struct {
			Policies []struct {
				Policy string `json:"policyId"`
			} `json:"attachedPolicies"`
		}
		if err := json.Unmarshal(w.Last().Body, &out); err != nil {
			return fmt.Errorf("decode attached policies: %w", err)
		}
		got := []string{}
		for _, policy := range out.Policies {
			got = append(got, policy.Policy)
		}
		expected := []string{}
		if want != "" {
			expected = strings.Split(w.Resolve(want), ", ")
		}
		if !reflect.DeepEqual(got, expected) {
			return fmt.Errorf("%w: attached policy IDs %v, want %v", harness.ErrStep, got, expected)
		}
		return nil
	})
	sc.Step(`^policy "([^"]*)" eventually has (\d+) stored attachments$`, func(ctx context.Context, policy, count string) error {
		q, err := w.Querier(ctx)
		if err != nil {
			return err
		}
		id, err := kernel.ParseULID(w.Resolve(policy))
		if err != nil {
			return fmt.Errorf("attachment policy identity: %w", err)
		}
		return deps.Eventually(ctx, "stored policy attachment count "+count, func(ctx context.Context) (bool, error) {
			got, err := q.Query(ctx, fmt.Sprintf("SELECT count(*) FROM policy_stack_attachments WHERE policy_id='%s'::uuid", id.UUID()))
			if err != nil {
				return false, fmt.Errorf("read stored attachments: %w", err)
			}
			return strings.TrimSpace(got) == count, nil
		})
	})
	sc.Step(`^policy "([^"]*)" through ([AB]) with token "([^"]*)" eventually has attachments '(.*)'$`, func(ctx context.Context, policy, replica, token, want string) error {
		path := w.Resolve("/api/v1/policies/" + policy)
		var expected map[string]any
		if err := json.Unmarshal([]byte(w.Resolve(want)), &expected); err != nil {
			return fmt.Errorf("decode expected attachment fields: %w", err)
		}
		return deps.Eventually(ctx, "policy attachments to match "+want, func(ctx context.Context) (bool, error) {
			r, err := w.Do(ctx, replica, token, "GET", path, nil)
			if err != nil {
				return false, err
			}
			var fields map[string]any
			if r.Status != 200 || json.Unmarshal(r.Body, &fields) != nil {
				return false, fmt.Errorf("%w: policy read answered %d: %s", harness.ErrStep, r.Status, r.Body)
			}
			for field, value := range expected {
				if !reflect.DeepEqual(fields[field], value) {
					return false, fmt.Errorf("%w: policy field %s is %v, want %v", harness.ErrStep, field, fields[field], value)
				}
			}
			return true, nil
		})
	})
	sc.Step(`^the policy's attachment to "([^"]*)" is automatic and kept as "([^"]*)"$`, func(stack, name string) error {
		var out struct {
			Attached []struct {
				ID        string `json:"id"`
				Stack     string `json:"stackId"`
				Automatic bool   `json:"isAutoattached"`
			} `json:"attachedStacks"`
		}
		if err := json.Unmarshal(w.Last().Body, &out); err != nil {
			return fmt.Errorf("decode policy attachments: %w", err)
		}
		for _, a := range out.Attached {
			if a.Stack == w.Resolve(stack) && a.Automatic && a.ID != "" {
				w.IDs[name] = a.ID
				return nil
			}
		}
		return fmt.Errorf("%w: no automatic attachment to %s: %s", harness.ErrStep, stack, w.Last().Body)
	})
	sc.Step(`^the policy and attachment have distinct ULIDs$`, func() error {
		policy, err := kernel.ParseULID(w.IDs["policy"])
		if err != nil {
			return fmt.Errorf("policy identity: %w", err)
		}
		attachment, err := kernel.ParseULID(w.IDs["attachment"])
		if err != nil {
			return fmt.Errorf("attachment identity: %w", err)
		}
		if policy == attachment {
			return fmt.Errorf("%w: attachment reused policy identity", harness.ErrStep)
		}
		return nil
	})
}
