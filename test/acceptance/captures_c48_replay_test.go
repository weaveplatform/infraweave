package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

const (
	c48Notification = "/exchanges/18/response/errors/0/message"
	c48Stacks       = "/exchanges/23/response/data/stacks"
	c48Policies     = "/exchanges/23/response/data/policies"
	c48Workflow     = "/exchanges/20/response/data/stack/run/state"
)

var captureSubsetReplayers = map[string]func(*testing.T, *sdk.Client) []string{
	"policy-free-tier-management":    replayFreeTierManagement,
	"worker-pool-public-projections": replayPublicPoolProjections,
}

func validateCaptureSubsetReplayers(captures []harness.Capture, registry map[string]func(*testing.T, *sdk.Client) []string) error {
	for _, capture := range captures {
		if capture.ReplaySubset != "" && registry[capture.ReplaySubset] == nil {
			return fmt.Errorf("%w: capture %s names unknown REST subset replayer %q", harness.ErrCapture, capture.ID, capture.ReplaySubset)
		}
	}
	return nil
}

type c48PolicyInput struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Body   string `json:"body"`
	Engine string `json:"engineType"`
	Space  string `json:"space"`
}

type c48PolicyView struct {
	c48PolicyInput
	ID          string `json:"id"`
	HasAttached bool   `json:"hasAttachedStacks"`
}

type c48Replay struct {
	t      *testing.T
	client *sdk.Client
}

func (r c48Replay) call(method, path string, body any, status int) []byte {
	r.t.Helper()
	response, err := r.client.Do(r.t.Context(), method, path, body)
	if response == nil || response.Status != status || status < 400 && err != nil {
		r.t.Fatalf("C48 %s %s: %+v %v, want %d", method, path, response, err, status)
	}
	return response.Body
}

func (r c48Replay) keep(path string) {
	r.t.Helper()
	r.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.t.Context()), 10*time.Second)
		defer cancel()
		response, err := r.client.Do(ctx, "DELETE", path, nil)
		if response == nil || response.Status != 200 && response.Status != 404 {
			r.t.Errorf("C48 fallback cleanup %s: %+v %v", path, response, err)
		}
	})
}

func (r c48Replay) inventory(path string) []string {
	r.t.Helper()
	ids := []string{}
	seen := map[string]bool{}
	cursor := ""
	for {
		var page struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Next *string `json:"nextCursor"`
		}
		if err := json.Unmarshal(r.call("GET", path+"?limit=100&cursor="+url.QueryEscape(cursor), nil, 200), &page); err != nil || page.Items == nil {
			r.t.Fatalf("C48 inventory %s: %+v %v", path, page, err)
		}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		if page.Next == nil {
			break
		}
		if *page.Next == "" || seen[*page.Next] {
			r.t.Fatalf("C48 inventory %s repeated cursor", path)
		}
		cursor = *page.Next
		seen[cursor] = true
	}
	slices.Sort(ids)
	return ids
}

// The package name, resource name and root space are the only translations.
// Compilation and persistence use the captured sources and per-policy dialects;
// no policy is evaluated and no historical billing response is fabricated.
func replayFreeTierManagement(t *testing.T, client *sdk.Client) []string {
	t.Helper()
	r := c48Replay{t: t, client: client}
	beforeStacks, beforePolicies := r.inventory("/api/v1/stacks"), r.inventory("/api/v1/policies")
	var stack struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(r.call("POST", "/api/v1/stacks", map[string]any{
		"input": map[string]any{"name": "C48 management", "provider": "GIT", "repository": "infra", "branch": "main", "space": "acme"}, "manageState": false,
	}, 201), &stack); err != nil || stack.ID == "" {
		t.Fatalf("C48 stack identity: %+v %v", stack, err)
	}
	stackPath := "/api/v1/stacks/" + stack.ID
	r.keep(stackPath)
	var captured struct {
		Exchanges []struct {
			Request struct {
				Query     string `json:"query"`
				Variables struct {
					Input *c48PolicyInput `json:"input"`
				} `json:"variables"`
			} `json:"request"`
		} `json:"exchanges"`
	}
	captureJSON(t, "C48", harness.ResponseFile, &captured)
	policies := []string{}
	createdTypes, attachedTypes := map[string]bool{}, map[string]bool{}
	attachments := 0
	notificationRefused := false
	for _, exchange := range captured.Exchanges {
		if !strings.Contains(exchange.Request.Query, "policyCreatev2") {
			continue
		}
		if exchange.Request.Variables.Input == nil {
			t.Fatal("C48 policy creation lacks its captured input")
		}
		input := *exchange.Request.Variables.Input
		if !strings.HasPrefix(input.Body, "package spacelift\n") {
			t.Fatal("C48 source lacks its captured namespace")
		}
		input.Name = "C48 " + input.Name
		input.Space = "acme"
		input.Body = strings.Replace(input.Body, "package spacelift\n", "package infraweave\n", 1)
		var policy c48PolicyView
		if err := json.Unmarshal(r.call("POST", "/api/v1/policies", input, 201), &policy); err != nil || policy.ID == "" {
			t.Fatalf("C48 policy create: %+v %v", policy, err)
		}
		path := "/api/v1/policies/" + policy.ID
		r.keep(path + "?force=true")
		policies = append(policies, path)
		var read c48PolicyView
		if err := json.Unmarshal(r.call("GET", path, nil, 200), &read); err != nil || read.ID != policy.ID || read.c48PolicyInput != input || read.HasAttached {
			t.Fatalf("C48 persisted policy differs: %+v %v", read, err)
		}
		createdTypes[input.Type] = true
		if input.Type == "INTENT" {
			continue
		}
		if input.Type == "NOTIFICATION" {
			var problem struct {
				Type   string `json:"type"`
				Errors []struct {
					Pointer string `json:"pointer"`
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(r.call("PUT", stackPath+"/policies/"+policy.ID, nil, 422), &problem); err != nil ||
				problem.Type != "urn:infraweave:problem:validation-failed" || len(problem.Errors) != 1 ||
				problem.Errors[0].Pointer != "/policyId" || problem.Errors[0].Reason != "rule" || !strings.Contains(problem.Errors[0].Message, "NOTIFICATION") {
				t.Fatalf("C48 wrong typed attachment refusal: %+v %v", problem, err)
			}
			if err := json.Unmarshal(r.call("GET", path, nil, 200), &read); err != nil || read.HasAttached {
				t.Fatalf("C48 refused notification attached: %+v %v", read, err)
			}
			notificationRefused = true
			continue
		}
		var attachment struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(r.call("PUT", stackPath+"/policies/"+policy.ID, nil, 200), &attachment); err != nil || attachment.ID == "" || attachment.ID == policy.ID {
			t.Fatalf("C48 attachment identity: %+v %v", attachment, err)
		}
		r.call("GET", stackPath+"/policy-attachment?id="+attachment.ID, nil, 200)
		if err := json.Unmarshal(r.call("GET", path, nil, 200), &read); err != nil || !read.HasAttached {
			t.Fatalf("C48 missing policy attachment: %+v %v", read, err)
		}
		attachedTypes[input.Type] = true
		attachments++
	}
	if len(policies) != 10 || len(createdTypes) != 8 || len(attachedTypes) != 6 || attachments != 8 || !notificationRefused {
		t.Fatalf(
			"C48 incomplete management replay: %d policies, created %v, attached %v/%d, notification refused %t",
			len(policies),
			createdTypes,
			attachedTypes,
			attachments,
			notificationRefused,
		)
	}
	r.call("DELETE", stackPath, nil, 200)
	r.call("GET", stackPath, nil, 404)
	for _, path := range policies {
		r.call("DELETE", path, nil, 200)
		r.call("GET", path, nil, 404)
	}
	if got := r.inventory("/api/v1/stacks"); !slices.Equal(got, beforeStacks) {
		t.Fatalf("C48 stacks left after cleanup: %v, want %v", got, beforeStacks)
	}
	if got := r.inventory("/api/v1/policies"); !slices.Equal(got, beforePolicies) {
		t.Fatalf("C48 policies left after cleanup: %v, want %v", got, beforePolicies)
	}
	t.Log(
		"C48 REST subset:10 captured policy sources persisted across8 types;8 attachments across6 supported stack types; NOTIFICATION refused; stack and all policies deleted with404 and restored inventories. Workflow/evidence handoffs remain.",
	)
	return []string{c48Notification, c48Stacks, c48Policies}
}

// C291 isolates the same approval/warning programs recorded in C48. Compare
// their complete captured inputs before using that real workflow as C48 proof.
func verifyApprovalCapturePrograms(t *testing.T) {
	t.Helper()
	programs := map[string]map[string]c48PolicyInput{}
	for _, id := range []string{"C48", "C291"} {
		var capture struct {
			Exchanges []struct {
				Request struct {
					Variables struct {
						Input *c48PolicyInput `json:"input"`
					} `json:"variables"`
				} `json:"request"`
			} `json:"exchanges"`
		}
		captureJSON(t, id, harness.ResponseFile, &capture)
		programs[id] = map[string]c48PolicyInput{}
		for _, exchange := range capture.Exchanges {
			if input := exchange.Request.Variables.Input; input != nil {
				programs[id][input.Name] = *input
			}
		}
	}
	for _, name := range []string{"policy-approval", "policy-plan-warn", "policy-always"} {
		input, ok := programs["C48"][name]
		if !ok || input != programs["C291"][name] {
			t.Fatalf("C48 and C291 no longer share the captured %s program", name)
		}
	}
}
