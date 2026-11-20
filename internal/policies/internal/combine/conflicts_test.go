package combine

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

func TestUnitCombinationPushConflictsAndDirectives(t *testing.T) {
	e, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	for _, bodies := range [][]string{
		{"track := true\npriority := \"high\"", "priority := \"low\""},
		{"track := true\nlock := \"a\"", "lock := \"b\""},
		{"track := true\npriority := \"invalid\""},
		{"track := true\npriority := \"high\"\nprioritize := true"},
	} {
		got := Push(nativeReceipts(t, e, policyapi.Push, bodies...))
		if got.Outcome != policyapi.Ignore || len(got.Failures) != 1 || !got.Push.FailedCheck || got.Push.Track || got.Push.Propose {
			t.Fatalf("push conflict opened run gate: %+v", got)
		}
	}
	got := Push(nativeReceipts(t, e, policyapi.Push, `track := true
prioritize := true
lock := "owner"
unlock := "old-owner"
module_version := "1.2.3"
notify := true
fail := true
allow_fork := true
cancel contains "run-a" if true
message contains "status" if true
external_dependency contains "build" if true`))
	if got.Outcome != policyapi.PushRun || got.Push.Priority != "high" || got.Push.Lock != "owner" || got.Push.Unlock != "old-owner" || got.Push.ModuleVersion != "1.2.3" ||
		!got.Push.Notify ||
		!got.Push.Fail ||
		!got.Push.AllowFork ||
		len(got.Push.Cancel) != 1 ||
		len(got.Push.Messages) != 1 ||
		len(got.Push.ExternalDependencies) != 1 {
		t.Fatalf("directive lost: %+v", got)
	}
	got = Push(nativeReceipts(t, e, policyapi.Push, "track := true\nnotrigger := true"))
	if got.Outcome != policyapi.Ignore || !got.Push.Track || !got.Push.NoTrigger {
		t.Fatalf("head-only directive: %+v", got)
	}
}

func TestUnitCombinationTriggerPriorityFallback(t *testing.T) {
	e, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	got := Trigger(nativeReceipts(t, e, policyapi.Trigger, `trigger contains "a" if true
priority := "high"`, `trigger contains "b" if true
priority := "low"`))
	if got.Outcome != policyapi.TriggerRuns || got.Trigger.Priority != "normal" || !reflect.DeepEqual(got.Trigger.Targets, []string{"a", "b"}) || len(got.Failures) != 1 ||
		got.Failures[0].Code != "PRIORITY_CONFLICT" {
		t.Fatalf("nonfatal priority fallback: %+v", got)
	}
	got = Trigger(nativeReceipts(t, e, policyapi.Trigger, `trigger contains "a" if true
priority := "invalid"`))
	if got.Outcome != policyapi.Ignore || len(got.Trigger.Targets) != 0 || len(got.Failures) != 1 {
		t.Fatalf("invalid priority triggered work: %+v", got)
	}
}

func TestUnitCombinationLoginMessagesGrantsAndSampling(t *testing.T) {
	e, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	receipts := nativeReceipts(t, e, policyapi.Login, `allow := true
roles["root"] := ["role-b","role-a"]
team contains "rewritten" if true
space_admin contains "admin" if true
space_write contains "write" if true
space_read contains "read" if true
deny_message contains "z" if true`, `deny := true
roles["root"]["role-c"] := true
roles["root"]["ignored"] := false
deny_message contains "a" if true
deny_message contains "z" if true
sample := false`)
	got := Login(receipts)
	if got.Outcome != policyapi.Deny || got.Login.Message != "a;z" || got.Login.Allow || got.Login.Admin ||
		!reflect.DeepEqual(got.Login.Roles["root"], []string{"role-a", "role-b", "role-c"}) ||
		!reflect.DeepEqual(got.Login.Teams, []string{"rewritten"}) ||
		len(got.Login.SpaceRead) != 1 ||
		len(got.Login.SpaceWrite) != 1 ||
		len(got.Login.SpaceAdmin) != 1 ||
		!got.Receipts[0].Sample ||
		got.Receipts[1].Sample {
		t.Fatalf("login grants/message/sampling: %+v", got)
	}
	raw, err := json.Marshal(map[string]any{"deny_message": []string{strings.Repeat("界", 1050)}})
	if err != nil {
		t.Fatal(err)
	}
	got = Login([]policyapi.Receipt{{Policy: kernel.NewULID(), Type: policyapi.Login, Decision: raw}})
	if utf8.RuneCountInString(got.Login.Message) != 1024 || !strings.HasSuffix(got.Login.Message, "...") || !utf8.ValidString(got.Login.Message) {
		t.Fatalf("Unicode denial truncation: %d", utf8.RuneCountInString(got.Login.Message))
	}
}

func TestUnitCombinationNotificationTypedRoutes(t *testing.T) {
	e, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	got := Notification(nativeReceipts(t, e, policyapi.Notification, `inbox contains {"title":"t","body":"b","severity":"INFO"} if true
slack contains {"channel_id":"c","message":"m","mention_users":["u"],"mention_groups":["g"],"thread_key":"thread"} if true
webhook contains {"endpoint_id":"w","headers":{"x-test":"v"},"payload":null,"method":"POST"} if true
pull_request contains {"id":0,"commit":"sha","branch":"main","body":"comment","deduplication_key":"key","truncate_body":false} if true`))
	n := got.Notification
	if got.Outcome != policyapi.RouteNotification || len(n.Inbox) != 1 || len(n.Slack) != 1 || len(n.Webhook) != 1 || len(n.PullRequest) != 1 || n.Slack[0].ChannelID != "c" ||
		string(n.Webhook[0].Payload) != "null" ||
		n.PullRequest[0].ID == nil ||
		*n.PullRequest[0].ID != 0 ||
		n.PullRequest[0].TruncateBody == nil ||
		*n.PullRequest[0].TruncateBody {
		t.Fatalf("typed routes: %+v", got)
	}
	for _, raw := range []string{`{"inbox":[{"title":null}]}`, `{"slack":[{}]}`, `{"slack":[{"channel_id":"x","message":null}]}`, `{"webhook":[{}]}`, `{"webhook":[{"endpoint_id":"x","method":null}]}`, `{"pull_request":[{"id":null}]}`} {
		got = Notification([]policyapi.Receipt{{Policy: kernel.NewULID(), Type: policyapi.Notification, Decision: json.RawMessage(raw)}})
		if got.Outcome != policyapi.Ignore || len(got.Failures) != 1 || routeCount(*got.Notification) != 0 {
			t.Fatalf("malformed route delivered: %+v", got)
		}
	}
}

func TestUnitCombinationEveryOtherTypeFailsClosed(t *testing.T) {
	for _, test := range []struct {
		kind    policyapi.Type
		combine func([]policyapi.Receipt) policyapi.Evaluation
		want    policyapi.Outcome
	}{
		{policyapi.Push, Push, policyapi.Ignore},
		{policyapi.Trigger, Trigger, policyapi.Ignore},
		{policyapi.Login, Login, policyapi.Deny},
		{policyapi.Notification, Notification, policyapi.Ignore},
		{policyapi.Task, func(r []policyapi.Receipt) policyapi.Evaluation { return Negative(policyapi.Task, r) }, policyapi.Deny},
		{policyapi.Initialization, func(r []policyapi.Receipt) policyapi.Evaluation { return Negative(policyapi.Initialization, r) }, policyapi.Deny},
		{policyapi.Intent, func(r []policyapi.Receipt) policyapi.Evaluation { return Negative(policyapi.Intent, r) }, policyapi.Deny},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			for _, r := range []policyapi.Receipt{
				{Policy: kernel.NewULID(), Type: test.kind, Failure: &policyapi.Failure{Code: "TIMEOUT", Message: "Policy evaluation timed out."}},
				{Policy: kernel.NewULID(), Type: test.kind, Decision: json.RawMessage(`{"flag":null}`)},
				{Policy: kernel.NewULID(), Type: policyapi.Type("ACCESS"), Decision: json.RawMessage(`{}`)},
			} {
				got := test.combine([]policyapi.Receipt{r})
				if got.Outcome != test.want || len(got.Failures) != 1 || len(got.Receipts[0].Flags) != 0 || len(got.Receipts[0].ResetFlags) != 0 {
					t.Fatalf("fail-closed error outcome: %+v", got)
				}
			}
		})
	}
}
