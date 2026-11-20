package combine

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

func TestUnitCombinationOtherTypesUseRealOPA(t *testing.T) {
	e, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		kind    policyapi.Type
		bodies  []string
		combine func([]policyapi.Receipt) policyapi.Evaluation
		want    policyapi.Outcome
	}{
		{"push-default", policyapi.Push, []string{""}, Push, policyapi.Ignore},
		{"push-track", policyapi.Push, []string{"track := true"}, Push, policyapi.PushRun},
		{"push-ignore-wins", policyapi.Push, []string{"track := true", "ignore := true"}, Push, policyapi.Ignore},
		{"push-drop-track", policyapi.Push, []string{"track := true", "ignore_track := true"}, Push, policyapi.Ignore},
		{"push-propose-survives-drop-track", policyapi.Push, []string{"track := true\npropose := true", "ignore_track := true"}, Push, policyapi.PushRun},
		{"trigger-default", policyapi.Trigger, []string{""}, Trigger, policyapi.Ignore},
		{"trigger-target", policyapi.Trigger, []string{`trigger contains "target" if true`}, Trigger, policyapi.TriggerRuns},
		{"login-default-denies", policyapi.Login, []string{""}, Login, policyapi.Deny},
		{"login-admin-implies-allow", policyapi.Login, []string{"admin := true"}, Login, policyapi.Allow},
		{"login-deny-wins", policyapi.Login, []string{"admin := true", "deny := true"}, Login, policyapi.Deny},
		{"login-deny-admin-keeps-login", policyapi.Login, []string{"admin := true", "deny_admin := true"}, Login, policyapi.Allow},
		{"task-default-allows", policyapi.Task, []string{""}, func(r []policyapi.Receipt) policyapi.Evaluation { return Negative(policyapi.Task, r) }, policyapi.Allow},
		{"task-deny-wins", policyapi.Task, []string{"", `deny contains "blocked" if true`}, func(r []policyapi.Receipt) policyapi.Evaluation { return Negative(policyapi.Task, r) }, policyapi.Deny},
		{"initialization-deny", policyapi.Initialization, []string{`deny contains "blocked" if true`}, func(r []policyapi.Receipt) policyapi.Evaluation { return Negative(policyapi.Initialization, r) }, policyapi.Deny},
		{"intent-default-waits", policyapi.Intent, []string{""}, func(r []policyapi.Receipt) policyapi.Evaluation { return Negative(policyapi.Intent, r) }, policyapi.Undecided},
		{"intent-allow", policyapi.Intent, []string{`allow contains "approved" if true`}, func(r []policyapi.Receipt) policyapi.Evaluation { return Negative(policyapi.Intent, r) }, policyapi.Allow},
		{"intent-deny-wins", policyapi.Intent, []string{`allow contains "approved" if true`, `deny contains "blocked" if true`}, func(r []policyapi.Receipt) policyapi.Evaluation { return Negative(policyapi.Intent, r) }, policyapi.Deny},
		{"notification-default", policyapi.Notification, []string{""}, Notification, policyapi.Ignore},
		{"notification-route", policyapi.Notification, []string{`inbox contains {"title":"test"} if true`}, Notification, policyapi.RouteNotification},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.combine(nativeReceipts(t, e, test.kind, test.bodies...)); got.Outcome != test.want {
				t.Fatalf("got %+v, want %s", got, test.want)
			}
			if got := test.combine(nil); got.Outcome != policyapi.NoPolicies || got.HasPolicies {
				t.Fatalf("policy-free default lost: %+v", got)
			}
		})
	}
}
