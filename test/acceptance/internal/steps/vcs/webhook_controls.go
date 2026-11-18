package vcs

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type webhookControlSteps struct {
	w      *harness.World
	gitlab *gitlabSteps
	cloud  *bitbucketCloudSteps
	dc     *bitbucketDCSteps
	azure  *azureDevOpsSteps
	github *githubSteps
}

func (h *webhookControlSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's webhook secret is kept as "(\w+)"$`, h.keepSecret)
	sc.Step(`^the answer's webhook secret differs from "(\w+)"$`, h.differentSecret)
	sc.Step(`^the webhook endpoint in the answer is "([^"]+)"$`, h.endpoint)
	sc.Step(`^token "(\w+)" regenerates the webhook secret at "([^"]+)" with idempotency key "([^"]+)" through ([AB])$`, h.rotate)
	sc.Step(`^integration "(\w+)" has (\d+) secret rotation audit entries and never logs or audits secrets "([^"]+)"$`, h.audit)
	sc.Step(`^the (GitLab|Bitbucket Cloud|Bitbucket Data Center|Azure DevOps) repository "([^"]+)" has a webhook to "([^"]+)" for events "([^"]*)"$`, h.hook)
	sc.Step(`^the (GitLab|Bitbucket Cloud|Bitbucket Data Center|Azure DevOps) repository "([^"]+)" has no webhook$`, h.noHook)
	sc.Step(`^the answer's ([A-Z_]+) check is (SUCCESS|FAIL)$`, h.check)
	sc.Step(`^the failed webhook check explains "([^"]+)" against "([^"]+)"$`, h.hookDiagnostic)
	sc.Step(`^the GitHub App "(\w+)" lacks required (event|permission) "([^"]+)"$`, h.missingAppRequirement)
	sc.Step(`^the failed GitHub App check names missing (event|permission) "([^"]+)"$`, h.appDiagnostic)
}

func (h *webhookControlSteps) secret() (string, error) {
	var view struct {
		Secret   string `json:"webhookSecret"`
		Password string `json:"webhookPassword"`
	}
	r := h.w.Last()
	if json.Unmarshal(r.Body, &view) != nil || (view.Secret == "") == (view.Password == "") {
		return "", fmt.Errorf("%w: answer %d has no webhook secret", harness.ErrStep, r.Status)
	}
	if view.Password != "" {
		return view.Password, nil
	}
	return view.Secret, nil
}

func (h *webhookControlSteps) endpoint(want string) error {
	var got string
	r := h.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &got) != nil || got != h.w.Resolve(want) {
		return fmt.Errorf("%w: answer is not the integration's expected webhook URL", harness.ErrStep)
	}
	return nil
}

func (h *webhookControlSteps) keepSecret(key string) error {
	secret, err := h.secret()
	if err != nil {
		return err
	}
	h.w.IDs[key] = secret
	return nil
}

func (h *webhookControlSteps) differentSecret(key string) error {
	secret, err := h.secret()
	if err != nil {
		return err
	}
	decoded, decodeErr := hex.DecodeString(secret)
	if h.w.IDs[key] == "" || secret == h.w.IDs[key] || decodeErr != nil || len(decoded) != 32 {
		return fmt.Errorf("%w: secret did not change from %s to 32 fresh bytes of entropy", harness.ErrStep, key)
	}
	return nil
}

func (h *webhookControlSteps) rotate(ctx context.Context, token, path, key, replica string) error {
	r, err := h.w.Do(ctx, replica, token, http.MethodPost, h.w.Resolve(path), nil,
		sdk.Header{Name: "Idempotency-Key", Value: key})
	if err != nil && r.Status == 0 {
		return err
	}
	return nil
}

// audit counts committed rotations, including their caller, and checks
// complete audit/event rows and server output without printing a secret.
func (h *webhookControlSteps) audit(ctx context.Context, integration string, want int, keys string) error {
	var n int
	var caller bool
	account, id := harness.UUIDOf(harness.Home.Account), h.w.IDs[integration]
	if id == "" {
		return fmt.Errorf("%w: no integration kept as %s", harness.ErrStep, integration)
	}
	if err := h.w.Row(ctx, `SELECT count(*), coalesce(bool_and(actor_username = $3
AND actor_run IS NULL AND coalesce(remote_ip, '') <> ''), true) FROM audit_trail_entries
WHERE account_id = $1 AND action = 'vcs_integration.rotate_webhook_secret'
AND event_type = 'UPDATE' AND affected_type = 'VCS_INTEGRATION' AND affected_id = $2
`, []any{account, id, harness.Home.Key}, &n, &caller); err != nil {
		return err
	}
	if n != want || !caller {
		return fmt.Errorf("%w: %d caller-attributed rotation audit entries, want %d", harness.ErrStep, n, want)
	}
	for _, key := range strings.Split(keys, ", ") {
		secret := h.w.IDs[key]
		if secret == "" {
			return fmt.Errorf("%w: no secret kept as %s", harness.ErrStep, key)
		}
		for i, srv := range h.w.Servers {
			if bytes.Contains(srv.Stdout(), []byte(secret)) {
				return fmt.Errorf("%w: server %d logs the secret kept as %s", harness.ErrStep, i+1, key)
			}
		}
		if err := h.w.Row(ctx, `SELECT
 (SELECT count(*) FROM audit_trail_entries e WHERE account_id = $1 AND strpos(e::text, $2) > 0)
+ (SELECT count(*) FROM events e WHERE account_id = $1 AND strpos(e::text, $2) > 0)`,
			[]any{account, secret}, &n); err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("%w: audit/event rows contain the secret kept as %s", harness.ErrStep, key)
		}
	}
	return nil
}

func (h *webhookControlSteps) noHook(provider, path string) error {
	return h.hook(provider, path, "", "")
}

func (h *webhookControlSteps) hook(provider, path, endpoint, csv string) error {
	i := strings.LastIndex(path, "/")
	if i <= 0 || i == len(path)-1 {
		return fmt.Errorf("%w: invalid repository path %q", harness.ErrStep, path)
	}
	namespace, name := path[:i], path[i+1:]
	events := []string{}
	if csv != "" {
		events = strings.Split(csv, ", ")
	}
	endpoint = h.w.Resolve(endpoint)
	switch {
	case provider == "GitLab" && h.gitlab.fake != nil:
		var hooks []gitlabfake.Hook
		if endpoint != "" {
			hooks = []gitlabfake.Hook{{URL: endpoint, Events: events, Active: true}}
		}
		h.gitlab.fake.SetHooks(namespace, name, hooks)
	case provider == "Bitbucket Cloud" && h.cloud.fake != nil:
		var hooks []bitbucketcloudfake.Hook
		if endpoint != "" {
			hooks = []bitbucketcloudfake.Hook{{URL: endpoint, Events: events, Active: true}}
		}
		h.cloud.fake.SetHooks(namespace, name, hooks)
	case provider == "Bitbucket Data Center" && h.dc.fake != nil:
		var hooks []bitbucketdcfake.Hook
		if endpoint != "" {
			hooks = []bitbucketdcfake.Hook{{URL: endpoint, Events: events, Active: true}}
		}
		h.dc.fake.SetHooks(namespace, name, hooks)
	case provider == "Azure DevOps" && h.azure.fake != nil:
		var hooks []azuredevopsfake.Hook
		if endpoint != "" {
			hooks = []azuredevopsfake.Hook{{URL: endpoint, Events: events, Active: true}}
		}
		h.azure.fake.SetHooks(namespace, name, hooks)
	default:
		return fmt.Errorf("%w: %s has no fake in this scenario", harness.ErrStep, provider)
	}
	return nil
}

type controlCheck struct {
	Type     string `json:"type"`
	Status   string `json:"status"`
	Metadata struct {
		WebHooks struct {
			Expected string `json:"expected"`
			Actual   string `json:"actual"`
		} `json:"webHooks"`
		Permissions []string `json:"permissions"`
		Events      []string `json:"githubAppEvents"`
	} `json:"metadata"`
}

func (h *webhookControlSteps) result(kind string) (controlCheck, error) {
	var result struct {
		Checks []controlCheck `json:"checks"`
	}
	if json.Unmarshal(h.w.Last().Body, &result) == nil {
		for _, check := range result.Checks {
			if check.Type == kind {
				return check, nil
			}
		}
	}
	return controlCheck{}, fmt.Errorf("%w: answer has no %s check", harness.ErrStep, kind)
}

func (h *webhookControlSteps) check(kind, want string) error {
	check, err := h.result(kind)
	if err != nil {
		return err
	}
	if check.Status != want {
		return fmt.Errorf("%w: %s check is %s, want %s", harness.ErrStep, kind, check.Status, want)
	}
	return nil
}

func (h *webhookControlSteps) hookDiagnostic(repository, endpoint string) error {
	check, err := h.result("WEBHOOK")
	if err != nil {
		return err
	}
	if check.Status != "FAIL" || !strings.Contains(check.Metadata.WebHooks.Expected, h.w.Resolve(endpoint)) ||
		!strings.Contains(check.Metadata.WebHooks.Actual, repository) {
		return fmt.Errorf("%w: failed WEBHOOK diagnostic omits the expected URL or failing repository", harness.ErrStep)
	}
	return nil
}

func (h *webhookControlSteps) missingAppRequirement(key, kind, name string) error {
	id, err := strconv.ParseInt(h.w.IDs[key+"ID"], 10, 64)
	if err != nil || h.github.fake == nil {
		return fmt.Errorf("%w: no GitHub App kept as %s", harness.ErrStep, key)
	}
	permissions, events := maps.Clone(githubfake.Permissions), slices.Clone(githubfake.Events)
	if kind == "event" {
		if !slices.Contains(events, name) {
			return fmt.Errorf("%w: %s is not in the complete App event fixture", harness.ErrStep, name)
		}
		events = slices.DeleteFunc(events, func(v string) bool { return v == name })
	} else {
		if permissions[name] == "" {
			return fmt.Errorf("%w: %s is not in the complete App permission fixture", harness.ErrStep, name)
		}
		delete(permissions, name)
	}
	h.github.fake.SetApp(id, permissions, events)
	return nil
}

func (h *webhookControlSteps) appDiagnostic(kind, name string) error {
	check, err := h.result("GITHUB_APP_EVENTS")
	if err != nil {
		return err
	}
	missing := check.Metadata.Events
	if kind == "permission" {
		missing = check.Metadata.Permissions
	}
	if check.Status != "FAIL" || !slices.Contains(missing, name) {
		return fmt.Errorf("%w: failed App check does not name missing %s %s", harness.ErrStep, kind, name)
	}
	return nil
}
