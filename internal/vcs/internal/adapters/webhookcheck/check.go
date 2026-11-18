package webhookcheck

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Hook is the common, non-secret projection of a provider's hook listing.
type Hook struct {
	URL    string
	Events []string
	Active bool
}

const named = 10

// Check requires complete event coverage by active hooks to the exact URL.
// Coverage may span hooks: Azure DevOps subscriptions each name one event.
func Check(expected port.WebhookInput, events []string, read func(port.Listed) ([]Hook, error)) port.Check {
	check := port.Check{Type: port.CheckWebhook, Passed: true}
	var failures []string
	count := 0
	for _, repo := range expected.Repositories {
		hooks, err := read(repo)
		covered := map[string]bool{}
		for _, hook := range hooks {
			if hook.Active && hook.URL == expected.URL {
				for _, event := range hook.Events {
					covered[event] = true
				}
			}
		}
		if err == nil && !slices.ContainsFunc(events, func(event string) bool { return !covered[event] }) {
			continue
		}
		count++
		if len(failures) == named {
			continue
		}
		actual := "no matching active hook"
		if err != nil {
			actual = "hook listing unavailable"
		} else if len(hooks) > 0 {
			var values []string
			for _, hook := range hooks[:min(len(hooks), named)] {
				values = append(values, fmt.Sprintf("%s active=%t [%s]", safeURL(hook.URL), hook.Active, strings.Join(hook.Events, ", ")))
			}
			actual = strings.Join(values, "; ")
			if len(hooks) > named {
				actual += fmt.Sprintf("; and %d more hooks", len(hooks)-named)
			}
		}
		failures = append(failures, repo.Namespace+"/"+repo.Name+": "+actual)
	}
	if count == 0 {
		return check
	}
	check.Passed, check.Reason = false, "The repositories' webhooks do not cover the required URL and events."
	actual := strings.Join(failures, "; ")
	if count > named {
		actual += fmt.Sprintf("; and %d more repositories", count-named)
	}
	check.WebHooks = &port.WebhookCheck{Expected: expected.URL + " [" + strings.Join(events, ", ") + "]", Actual: actual}
	return check
}

// Hook URLs can carry credentials or query tokens. Matching uses the
// original URL, but diagnostics never retain those secret-bearing parts.
func safeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid URL"
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}
