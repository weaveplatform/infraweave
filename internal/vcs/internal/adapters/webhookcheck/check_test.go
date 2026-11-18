package webhookcheck

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitCoverage(t *testing.T) {
	expected := port.WebhookInput{URL: "https://iw.example/webhooks/vcs/gitlab/id", Repositories: []port.Listed{{Namespace: "acme", Name: "infra"}}}
	for _, tc := range []struct {
		name  string
		hooks []Hook
		err   error
		pass  bool
	}{
		{"all events", []Hook{{URL: expected.URL, Active: true, Events: []string{"push", "pr"}}}, nil, true},
		{"per-event subscriptions", []Hook{{URL: expected.URL, Active: true, Events: []string{"push"}}, {URL: expected.URL, Active: true, Events: []string{"pr"}}}, nil, true},
		{"no hooks", nil, nil, false},
		{"inactive", []Hook{{URL: expected.URL, Events: []string{"push", "pr"}}}, nil, false},
		{"wrong URL", []Hook{{URL: "https://elsewhere.example", Active: true, Events: []string{"push", "pr"}}}, nil, false},
		{"missing event", []Hook{{URL: expected.URL, Active: true, Events: []string{"push"}}}, nil, false},
		{"provider failure", nil, errors.New("upstream-private-secret"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Check(expected, []string{"push", "pr"}, func(repo port.Listed) ([]Hook, error) {
				if repo != expected.Repositories[0] {
					t.Fatal(repo)
				}
				return tc.hooks, tc.err
			})
			if got.Type != port.CheckWebhook || got.Passed != tc.pass {
				t.Fatalf("%+v", got)
			}
			if !tc.pass && (got.WebHooks == nil || !strings.Contains(got.WebHooks.Expected, expected.URL) ||
				!strings.Contains(got.WebHooks.Actual, "acme/infra") || strings.Contains(got.WebHooks.Actual, "upstream-private-secret")) {
				t.Fatalf("unbounded or missing metadata: %+v", got)
			}
		})
	}
	if got := Check(port.WebhookInput{}, []string{"push"}, func(port.Listed) ([]Hook, error) {
		t.Fatal("no stack uses the integration")
		return nil, nil
	}); !got.Passed {
		t.Fatal(got)
	}
}

func TestUnitBoundedDiagnostics(t *testing.T) {
	expected := port.WebhookInput{URL: "https://iw.example/hook"}
	for i := range 12 {
		expected.Repositories = append(expected.Repositories, port.Listed{Namespace: "acme", Name: fmt.Sprintf("repo-%02d", i)})
	}
	calls := 0
	got := Check(expected, []string{"push"}, func(port.Listed) ([]Hook, error) { calls++; return nil, nil })
	if got.Passed || calls != 12 || !strings.Contains(got.WebHooks.Actual, "repo-09") ||
		strings.Contains(got.WebHooks.Actual, "repo-10") || !strings.Contains(got.WebHooks.Actual, "and 2 more repositories") {
		t.Fatalf("repository bound: %+v, reads=%d", got, calls)
	}
	expected.Repositories = expected.Repositories[:1]
	hooks := make([]Hook, 12)
	for i := range hooks {
		hooks[i] = Hook{URL: "https://user:password@elsewhere.example/hook?token=query-secret#fragment-secret", Active: true, Events: []string{"push"}}
	}
	hooks[0].URL = "%invalid"
	got = Check(expected, []string{"push"}, func(port.Listed) ([]Hook, error) { return hooks, nil })
	if got.Passed || !strings.Contains(got.WebHooks.Actual, "invalid URL") || !strings.Contains(got.WebHooks.Actual, "and 2 more hooks") {
		t.Fatalf("hook bound: %+v", got)
	}
	for _, secret := range []string{"password", "query-secret", "fragment-secret"} {
		if strings.Contains(got.WebHooks.Actual, secret) {
			t.Fatal("secret in diagnostic")
		}
	}
}
