package acceptance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// C9's ordinary-policy deletion invariant is replayed with a REST-created
// relation. Plugin-managed policy and context deletion remain unobserved.
func replayPolicyForcedDeletion(t *testing.T, client *sdk.Client) {
	t.Helper()
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		resp, err := client.Do(t.Context(), method, path, body)
		if resp == nil || resp.Status != want || want < 400 && err != nil {
			t.Fatalf("C9 %s %s: %+v %v, want %d", method, path, resp, err, want)
		}
		return resp.Body
	}
	keep := func(path string) {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
			defer cancel()
			resp, err := client.Do(ctx, "DELETE", path, nil)
			if resp == nil || resp.Status != 200 && resp.Status != 404 {
				t.Errorf("C9 cleanup %s: %+v %v", path, resp, err)
			}
		})
	}
	identity := func(body []byte) string {
		t.Helper()
		var row struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &row); err != nil || row.ID == "" {
			t.Fatalf("C9 identity: %s %v", body, err)
		}
		return row.ID
	}
	stack := identity(call("POST", "/api/v1/stacks", map[string]any{
		"input": map[string]any{"name": "C9 force deletion", "provider": "GIT", "repository": "infra", "branch": "main", "space": "acme"}, "manageState": false,
	}, 201))
	stackPath := "/api/v1/stacks/" + stack
	keep(stackPath)
	policy := identity(call("POST", "/api/v1/policies", map[string]any{"name": "C9 guard", "type": "PLAN", "body": "package infraweave\n"}, 201))
	policyPath := "/api/v1/policies/" + policy
	keep(policyPath + "?force=true")
	attachment := identity(call("PUT", stackPath+"/policies/"+policy, nil, 200))
	if attachment == policy {
		t.Fatal("C9 attachment reused its policy identity")
	}
	call("DELETE", policyPath, nil, 422)
	var preserved struct {
		ID          string `json:"id"`
		HasAttached bool   `json:"hasAttachedStacks"`
		Attachments []struct {
			ID string `json:"id"`
		} `json:"attachedStacks"`
	}
	if err := json.Unmarshal(
		call("GET", policyPath, nil, 200),
		&preserved,
	); err != nil || preserved.ID != policy || !preserved.HasAttached || len(preserved.Attachments) != 1 ||
		preserved.Attachments[0].ID != attachment {
		t.Fatalf("C9 refusal changed the relation: %+v %v", preserved, err)
	}
	call("GET", stackPath+"/policy-attachment?id="+attachment, nil, 200)
	call("DELETE", policyPath+"?force=true", nil, 200)
	call("GET", policyPath, nil, 404)
	call("GET", stackPath+"/policy-attachment?id="+attachment, nil, 404)
	var target struct {
		Policies []json.RawMessage `json:"attachedPolicies"`
	}
	if err := json.Unmarshal(call("GET", stackPath, nil, 200), &target); err != nil || target.Policies == nil || len(target.Policies) != 0 {
		t.Fatalf("C9 forced deletion left a projection: %+v %v", target, err)
	}
	call("DELETE", stackPath, nil, 200)
	call("GET", stackPath, nil, 404)
}
