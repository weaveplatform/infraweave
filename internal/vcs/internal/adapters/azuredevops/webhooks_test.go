package azuredevops_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitWebhookConfiguration(t *testing.T) {
	l := newLab(t)
	events := []string{"git.push", "git.pullrequest.created", "git.pullrequest.updated", "git.pullrequest.merged", "ms.vss-code.git-pullrequest-comment-event"}
	expected := port.WebhookInput{URL: "https://iw.example/webhooks/vcs/provider/id", Repositories: []port.Listed{{Namespace: "Platform", Name: "infra"}}}
	hooks := []azuredevopsfake.Hook{{URL: expected.URL, Events: events, Active: true}}
	check := func(want bool) port.Check {
		t.Helper()
		got, err := l.az.TestWebhooks(t.Context(), l.in, expected)
		if err != nil || got.Type != port.CheckWebhook || got.Passed != want {
			t.Fatalf("hook check: %+v %v", got, err)
		}
		if !want && (got.WebHooks == nil || !strings.Contains(got.WebHooks.Expected, expected.URL) || !strings.Contains(got.WebHooks.Actual, "Platform/infra")) {
			t.Fatalf("hook diagnostic: %+v", got)
		}
		return got
	}
	check(false)
	l.fake.SetHooks("Platform", "infra", hooks)
	hooks[0].URL = "https://elsewhere.example/hook"
	hooks[0].Events[0] = "caller-mutated"
	check(true) // fixture mutation cannot change a published hook snapshot
	events[0] = "git.push"
	hooks[0].Events = events
	l.fake.SetHooks("Platform", "infra", hooks)
	check(false)
	hooks[0].URL, hooks[0].Events = expected.URL, events[:len(events)-1]
	l.fake.SetHooks("Platform", "infra", hooks)
	check(false)
	hooks[0].Events, hooks[0].Active = events, false
	l.fake.SetHooks("Platform", "infra", hooks)
	check(false)
	hooks[0].Active = true
	many := slices.Repeat(hooks, 101)
	for i := range many {
		many[i].URL = "https://elsewhere.example/hook"
	}
	many[len(many)-1].URL = expected.URL
	l.fake.SetHooks("Platform", "infra", many)
	check(true)
	l.fake.Fail(azuredevopsfake.ServerError, 0)
	check(false)
	l.fake.Fail(azuredevopsfake.None, 0)
	l.in.Credential = nil
	check(false)
}
