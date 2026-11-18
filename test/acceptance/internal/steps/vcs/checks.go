package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type checkRepository interface {
	Commit(string, string, map[string]string) string
	HoldUploadPacks()
	HeldUploadPacks() int
	ReleaseUploadPacks()
}
type checkSteps struct {
	pushRun                                      string
	originalETag                                 string
	w                                            *harness.World
	github                                       *githubSteps
	gitlab                                       *gitlabSteps
	cloud                                        *bitbucketCloudSteps
	dc                                           *bitbucketDCSteps
	azure                                        *azureDevOpsSteps
	provider, namespace, kind, path, integration string
	body                                         map[string]any
	repo                                         checkRepository
}
type nativeCheck struct {
	ID, Name, SHA, URL, State, Summary, ExternalID string
	Actions                                        []string
}

func (s *checkSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^GitLab pushes a changed commit to the check repository$`, s.push)
	sc.Step(`^the check push's tracked run is kept as "(\w+)"$`, s.keepPush)
	sc.Step(`^the check push records (\d+) native cap failures naming limit (\d+)$`, func(ctx context.Context, count, limit int) error { return s.capFailures(ctx, limit, count) })

	sc.Before(func(ctx context.Context, scenario *godog.Scenario) (context.Context, error) {
		if strings.HasPrefix(scenario.Name, "Operator-disabled aggregate") {
			s.w.Settings["features"] = map[string]any{"disabled": []string{"aggregated-vcs-checks"}}
		}
		return ctx, nil
	})
	sc.Step(`^a "(GitHub|GitLab|Bitbucket Cloud|Bitbucket DC|Azure DevOps)" check integration using individual mode$`, s.integrationFixture)
	sc.Step(`^the check integration requests "(POST|PUT|PATCH)" with mode "(AGGREGATED|ALL)"$`, s.requestMode)
	sc.Step(`^the refused check mode leaves the integration unchanged$`, s.unchangedIntegration)

	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.repo != nil {
			s.repo.ReleaseUploadPacks()
		}
		return ctx, err
	})

	sc.Step(
		`^a "(GitHub|GitLab|Bitbucket Cloud|Bitbucket DC|Azure DevOps)" check stack "([^"]+)" using "(terraform|opentofu)" with mode "(INDIVIDUAL|AGGREGATED|ALL)" and options '([^']*)'$`,
		s.stack,
	)
	sc.Step(`^the check repository holds authenticated uploads$`, s.hold)
	sc.Step(`^the check repository has a held authenticated upload$`, s.held)
	sc.Step(`^the check repository releases authenticated uploads$`, s.release)
	sc.Step(`^the check "([^"]+)" for run "(\w+)" and stack "([^"]+)" has summary containing "([^"]+)"$`, s.metadata)
	sc.Step(`^no native check belongs to run "(\w+)"$`, s.noRun)
	sc.Step(`^a check report "([^"]+)" for run "(\w+)" succeeded after retry$`, s.retried)
	sc.Step(`^the check stack "([^"]+)" sets GitHub promotion to "(true|false)"$`, s.promotion)
	sc.Step(`^the GitHub check "([^"]+)" for run "(\w+)" has actions "([^"]*)"$`, s.actions)
	sc.Step(`^check events for run "(\w+)" have settled$`, s.settled)
	sc.Step(`^the "(GitHub|GitLab|Bitbucket Cloud|Bitbucket DC|Azure DevOps)" check "([^"]+)" for run "(\w+)" on commit "(\w+)" has states "([^"]+)"$`, s.states)
	sc.Step(`^the check repository contains no check named "([^"]+)"$`, s.absent)
	sc.Step(`^the check repository contains a "([^"]+)" check in native state "([^"]+)" with summary containing "([^"]+)"$`, s.aggregate)
	sc.Step(`^the check repository loses its next successful check response$`, s.lose)
	sc.Step(`^the check repository changes to a failing configuration as commit "(\w+)"$`, s.failCommit)
	sc.Step(`^the check stack "([^"]+)" fails before planning$`, s.failStack)
	sc.Step(`^the check integration uses mode "(INDIVIDUAL|AGGREGATED|ALL)" and options '([^']*)'$`, s.policy)
}

func (s *checkSteps) create(ctx context.Context, path string, body any) (string, error) {
	r, err := s.w.Do(ctx, "A", "h", http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	var result struct {
		ID string `json:"id"`
	}
	if r.Status != 201 || json.Unmarshal(r.Body, &result) != nil || result.ID == "" {
		return "", fmt.Errorf("%w: check fixture create %s answered %d", harness.ErrStep, path, r.Status)
	}
	return result.ID, nil
}

func (s *checkSteps) seed(provider string) error {
	if s.provider != "" {
		if s.provider != provider {
			return fmt.Errorf("%w: check provider changed", harness.ErrStep)
		}
		return nil
	}
	s.provider = provider
	switch provider {
	case "GitHub":
		if s.github.fake == nil {
			return fmt.Errorf("%w: missing GitHub fake", harness.ErrStep)
		}
		app := s.github.fake.AddApp("checks", "acme")
		s.github.fake.Install(app.ID, "acme", "Organization", "infra")
		s.namespace = "acme"
		s.kind = "GITHUB_ENTERPRISE"
		s.path = "/api/v1/github-enterprise-integrations"
		s.repo = s.github.fake.Repo(s.namespace, "infra")
		s.body = map[string]any{"apiHost": s.github.srv.URL, "appID": strconv.FormatInt(app.ID, 10), "privateKey": string(app.PrivateKey)}
	case "GitLab":
		if s.gitlab.fake == nil {
			return fmt.Errorf("%w: missing GitLab fake", harness.ErrStep)
		}
		token := s.gitlab.fake.AddToken("checks", "api")
		s.namespace = "acme/platform"
		s.gitlab.fake.AddMember(s.namespace, "infra", "checks", gitlabfake.Maintainer)
		s.kind = "GITLAB"
		s.path = "/api/v1/gitlab-integrations"
		s.repo = s.gitlab.fake.Repo(s.namespace, "infra")
		s.body = map[string]any{"apiHost": s.gitlab.srv.URL, "privateToken": token}
	case "Bitbucket Cloud":
		if s.cloud.fake == nil {
			return fmt.Errorf("%w: missing Cloud fake", harness.ErrStep)
		}
		token := s.cloud.fake.AddToken("checks@example.com")
		s.namespace = "acme"
		s.cloud.fake.Grant(s.namespace, "infra", "checks@example.com", bitbucketcloudfake.Admin)
		s.kind = "BITBUCKET_CLOUD"
		s.path = "/api/v1/bitbucket-cloud-integrations"
		s.repo = s.cloud.fake.Repo(s.namespace, "infra")
		s.body = map[string]any{"email": "checks@example.com", "apiToken": token}
	case "Bitbucket DC":
		if s.dc.fake == nil {
			return fmt.Errorf("%w: missing DC fake", harness.ErrStep)
		}
		token := s.dc.fake.AddToken("checks")
		s.namespace = "ACME"
		s.dc.fake.Grant(s.namespace, "infra", "checks", bitbucketdcfake.Admin)
		s.kind = "BITBUCKET_DATACENTER"
		s.path = "/api/v1/bitbucket-datacenter-integrations"
		s.repo = s.dc.fake.Repo(s.namespace, "infra")
		s.body = map[string]any{"apiHost": s.dc.srv.URL, "userFacingHost": s.dc.srv.URL, "username": "checks", "accessToken": token}
	case "Azure DevOps":
		if s.azure.fake == nil {
			return fmt.Errorf("%w: missing Azure fake", harness.ErrStep)
		}
		token := s.azure.fake.AddToken("checks")
		s.namespace = "Platform"
		s.azure.fake.Grant(s.namespace, "checks", azuredevopsfake.Administrator)
		s.kind = "AZURE_DEVOPS"
		s.path = "/api/v1/azure-dev-ops-repo-integrations"
		s.repo = s.azure.fake.Repo(s.namespace, "infra")
		s.body = map[string]any{"organizationURL": s.azure.srv.OrganizationURL, "userFacingHost": s.azure.srv.OrganizationURL, "personalAccessToken": token}
	default:
		return fmt.Errorf("%w: unknown check provider", harness.ErrStep)
	}
	s.w.IDs["checkCommit"] = s.repo.Commit("main", "commit check seed", deploymentFiles("one"))
	return nil
}

func (s *checkSteps) stack(ctx context.Context, provider, slug, vendor, mode, raw string) error {
	var options map[string]any
	if json.Unmarshal([]byte(raw), &options) != nil {
		return fmt.Errorf("%w: invalid check options", harness.ErrStep)
	}
	if err := s.seed(provider); err != nil {
		return err
	}
	if s.integration == "" {
		s.body["customInput"] = map[string]any{"name": "Checks", "spaceID": "acme", "isDefault": true, "vcsChecks": mode, "vcsCheckOptions": options}
		id, err := s.create(ctx, s.path, s.body)
		if err != nil {
			return err
		}
		s.integration = id
		s.w.IDs["checkIntegration"] = id
	}
	version := deps.Terraform.Version
	if vendor == "opentofu" {
		version = deps.OpenTofu.Version
	}
	_, err := s.create(
		ctx,
		"/api/v1/stacks",
		map[string]any{
			"input": map[string]any{
				"name":             slug,
				"namespace":        s.namespace,
				"repository":       "infra",
				"branch":           "main",
				"provider":         s.kind,
				"space":            "acme",
				"workerPool":       s.w.IDs["pool"],
				"vcsIntegrationId": s.integration,
				"labels":           []string{"ghenv:-"},
				"vendorConfig":     map[string]any{vendor: map[string]any{"version": version}},
			},
			"manageState": true,
		},
	)
	if err != nil {
		return err
	}
	var stackID kernel.ULID
	if err := s.w.Row(ctx, `SELECT id FROM stacks WHERE account_id=$1 AND slug=$2`, []any{harness.UUIDOf(harness.Home.Account), slug}, &stackID); err != nil {
		return err
	}
	s.w.IDs["checkStack:"+slug] = stackID.String()
	return deps.Eventually(ctx, "check stack immutable source", func(ctx context.Context) (bool, error) {
		r, err := s.w.Do(ctx, "B", "h", http.MethodGet, "/api/v1/stacks/"+slug, nil)
		if err != nil {
			return false, err
		}
		var stack struct {
			TrackedCommit *struct {
				Hash string `json:"hash"`
			} `json:"trackedCommit"`
		}
		return r.Status == 200 && json.Unmarshal(r.Body, &stack) == nil && stack.TrackedCommit != nil && stack.TrackedCommit.Hash == s.w.IDs["checkCommit"], nil
	})
}

func (s *checkSteps) hold() error {
	if s.repo == nil {
		return fmt.Errorf("%w: no check repository", harness.ErrStep)
	}
	s.repo.HoldUploadPacks()
	return nil
}

func (s *checkSteps) held(ctx context.Context) error {
	return deps.Eventually(ctx, "authenticated source fetch held", func(context.Context) (bool, error) { return s.repo.HeldUploadPacks() > 0, nil })
}
func (s *checkSteps) release() error { s.repo.ReleaseUploadPacks(); return nil }
func (s *checkSteps) failCommit(name string) error {
	s.w.IDs[name] = s.repo.Commit("main", "invalid Terraform", map[string]string{"main.tf": "this is not valid HCL !"})
	return nil
}

func (s *checkSteps) failStack(ctx context.Context, slug string) error {
	path := "/api/v1/stacks/" + slug
	before, err := s.w.Do(ctx, "A", "h", http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	etag := before.Header.Get("ETag")
	if before.Status != 200 || etag == "" {
		return fmt.Errorf("%w: failure-hook fixture lacks stack ETag", harness.ErrStep)
	}
	result, err := s.w.Do(ctx, "A", "h", http.MethodPatch, path, map[string]any{"beforePlan": []string{"exit 17"}}, sdk.Header{Name: "If-Match", Value: etag})
	if err != nil {
		return err
	}
	if result.Status != 200 {
		return fmt.Errorf("%w: failure hook update answered %d", harness.ErrStep, result.Status)
	}
	return nil
}

func (s *checkSteps) policy(ctx context.Context, mode, raw string) error {
	var options map[string]any
	if json.Unmarshal([]byte(raw), &options) != nil {
		return fmt.Errorf("%w: invalid check options", harness.ErrStep)
	}
	r, err := s.w.Do(
		ctx,
		"B",
		"h",
		http.MethodPut,
		s.path+"/"+s.integration,
		map[string]any{"customInput": map[string]any{"space": "acme", "vcsChecks": mode, "vcsCheckOptions": options}},
	)
	if err != nil {
		return err
	}
	if r.Status != 200 {
		return fmt.Errorf("%w: check policy update answered %d", harness.ErrStep, r.Status)
	}
	return nil
}

func (s *checkSteps) lose() error {
	switch s.provider {
	case "GitHub":
		s.github.fake.FailNextCheckCreate()
	case "GitLab":
		s.gitlab.fake.FailNextCheckCreate()
	case "Bitbucket Cloud":
		s.cloud.fake.FailNextCheckCreate()
	case "Bitbucket DC":
		s.dc.fake.FailNextCheckCreate()
	case "Azure DevOps":
		s.azure.fake.FailNextCheckCreate()
	default:
		return fmt.Errorf("%w: no check provider", harness.ErrStep)
	}
	return nil
}

func (s *checkSteps) snapshots(writes bool) []nativeCheck {
	out := []nativeCheck{}
	switch s.provider {
	case "GitHub":
		items := s.github.fake.Checks(s.namespace, "infra")
		if writes {
			items = s.github.fake.CheckWrites(s.namespace, "infra")
		}
		for _, r := range items {
			state := r.Status
			if state == "completed" {
				state = r.Conclusion
			}
			actions := []string{}
			for _, action := range r.Actions {
				actions = append(actions, action.Identifier)
			}
			out = append(out, nativeCheck{strconv.FormatInt(r.ID, 10), r.Name, r.SHA, r.URL, state, r.Output.Summary, r.ExternalID, actions})
		}
	case "GitLab":
		items := s.gitlab.fake.Checks(s.namespace, "infra")
		if writes {
			items = s.gitlab.fake.CheckWrites(s.namespace, "infra")
		}
		for _, r := range items {
			out = append(out, nativeCheck{ID: strconv.FormatInt(r.ID, 10), Name: r.Name, SHA: r.SHA, URL: r.URL, State: r.Status, Summary: r.Description})
		}
	case "Bitbucket Cloud":
		items := s.cloud.fake.Checks(s.namespace, "infra")
		if writes {
			items = s.cloud.fake.CheckWrites(s.namespace, "infra")
		}
		for _, r := range items {
			out = append(out, nativeCheck{ID: r.Key, Name: r.Key, SHA: r.SHA, URL: r.URL, State: r.State, Summary: r.Description})
		}
	case "Bitbucket DC":
		items := s.dc.fake.Checks(s.namespace, "infra")
		if writes {
			items = s.dc.fake.CheckWrites(s.namespace, "infra")
		}
		for _, r := range items {
			out = append(out, nativeCheck{ID: r.Key, Name: r.Key, SHA: r.SHA, URL: r.URL, State: r.State, Summary: r.Description})
		}
	case "Azure DevOps":
		items := s.azure.fake.Checks(s.namespace, "infra")
		if writes {
			items = s.azure.fake.CheckWrites(s.namespace, "infra")
		}
		for _, r := range items {
			if r.PullRequest == 0 {
				out = append(out, nativeCheck{ID: strconv.Itoa(r.ID), Name: r.Context.Name, SHA: r.SHA, URL: r.URL, State: r.State, Summary: r.Description})
			}
		}
	}
	return out
}

func (s *checkSteps) states(ctx context.Context, provider, name, run, commit, raw string) error {
	if provider != s.provider {
		return fmt.Errorf("%w: different check provider", harness.ErrStep)
	}
	name = s.w.Resolve(name)
	want := strings.Split(raw, ", ")
	return deps.Eventually(ctx, "native check states "+name+" for "+run, func(context.Context) (bool, error) {
		states := []string{}
		ids := map[string]bool{}
		for _, r := range s.snapshots(true) {
			if r.Name == name && r.SHA == s.w.IDs[commit] && strings.HasSuffix(r.URL, "/runs/"+s.w.IDs[run]) {
				ids[r.ID] = true
				if len(states) == 0 || states[len(states)-1] != r.State {
					states = append(states, r.State)
				}
			}
		}
		if s.provider == "GitHub" && len(ids) > 1 {
			return false, fmt.Errorf("%w: one run acquired multiple GitHub check IDs", harness.ErrStep)
		}
		return slices.Equal(states, want), nil
	})
}

func (s *checkSteps) settled(ctx context.Context, run string) error {
	return deps.Eventually(ctx, "all check event fanout and native jobs settled", func(ctx context.Context) (bool, error) {
		var count, pending int
		var fanned bool
		err := s.w.Row(
			ctx,
			`SELECT count(*),coalesce(bool_and(e.fanned_out_at IS NOT NULL AND EXISTS(SELECT 1 FROM event_consumptions c WHERE c.account_id=e.account_id AND c.event_id=e.id AND c.consumer='events.sinks')),false),(SELECT count(*) FROM jobs j WHERE j.account_id=$1 AND j.state<>'succeeded' AND ((j.kind='events.consume' AND j.payload->>'consumer'='events.sinks') OR j.kind IN ('vcs.report_check','vcs.report_deployment'))) FROM events e WHERE e.account_id=$1 AND e.kind='run_updated' AND e.subject_kind='run' AND e.subject_id=$2`,
			[]any{harness.UUIDOf(harness.Home.Account), s.w.IDs[run]},
			&count,
			&fanned,
			&pending,
		)
		return count > 0 && fanned && pending == 0, err
	})
}

func (s *checkSteps) absent(name string) error {
	for _, r := range s.snapshots(false) {
		if r.Name == name {
			return fmt.Errorf("%w: unexpected native check %s", harness.ErrStep, name)
		}
	}
	return nil
}

func (s *checkSteps) aggregate(ctx context.Context, name, state, text string) error {
	return deps.Eventually(ctx, "native aggregate "+name, func(context.Context) (bool, error) {
		var latest *nativeCheck
		items := s.snapshots(true)
		for _, r := range items {
			if r.Name == name && r.SHA == s.w.IDs["checkCommit"] {
				latest = new(r)
			}
		}
		return latest != nil && latest.State == state && strings.Contains(latest.Summary, text), nil
	})
}

func (s *checkSteps) metadata(ctx context.Context, name, run, slug, text string) error {
	name = s.w.Resolve(name)
	slug = s.w.Resolve(slug)
	return deps.Eventually(ctx, "native check metadata "+name, func(context.Context) (bool, error) {
		for _, r := range s.snapshots(true) {
			if r.Name == name && r.SHA == s.w.IDs["checkCommit"] && strings.HasSuffix(r.URL, "/runs/"+s.w.IDs[run]) && strings.Contains(r.Summary, text) {
				if s.provider == "GitHub" && r.ExternalID != s.w.IDs["checkStack:"+slug] {
					return false, fmt.Errorf("%w: GitHub check external ID is not the stack", harness.ErrStep)
				}
				return true, nil
			}
		}
		return false, nil
	})
}

func (s *checkSteps) noRun(run string) error {
	for _, r := range s.snapshots(true) {
		if strings.HasSuffix(r.URL, "/runs/"+s.w.IDs[run]) {
			return fmt.Errorf("%w: excluded run acquired a native check", harness.ErrStep)
		}
	}
	return nil
}

func (s *checkSteps) retried(ctx context.Context, name, run string) error {
	return deps.Eventually(ctx, "check response-loss retry", func(ctx context.Context) (bool, error) {
		var count int
		err := s.w.Row(
			ctx,
			`SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='vcs.report_check' AND payload->'binding'->>'run'=$2 AND payload->>'name'=$3 AND payload->>'aggregate'='false'
            AND state='succeeded' AND generation>=2 AND last_error='VCS commit check report failed'`,
			[]any{harness.UUIDOf(harness.Home.Account), s.w.IDs[run], name},
			&count,
		)
		if err != nil || count == 0 {
			return false, err
		}
		identities := map[string]bool{}
		for _, r := range s.snapshots(false) {
			if r.Name == name && r.SHA == s.w.IDs["checkCommit"] && strings.HasSuffix(r.URL, "/runs/"+s.w.IDs[run]) {
				identities[r.ID] = true
			}
		}
		if len(identities) > 1 {
			return false, fmt.Errorf("%w: response-loss recovery created multiple native identities", harness.ErrStep)
		}
		return len(identities) == 1, nil
	})
}

func (s *checkSteps) promotion(ctx context.Context, slug, enabled string) error {
	path := "/api/v1/stacks/" + slug
	r, err := s.w.Do(ctx, "A", "h", http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	etag := r.Header.Get("ETag")
	if r.Status != 200 || etag == "" {
		return fmt.Errorf("%w: promotion fixture lacks stack ETag", harness.ErrStep)
	}
	result, err := s.w.Do(ctx, "A", "h", http.MethodPatch, path, map[string]any{"githubActionDeploy": enabled == "true"}, sdk.Header{Name: "If-Match", Value: etag})
	if err != nil {
		return err
	}
	if result.Status != 200 {
		return fmt.Errorf("%w: promotion update answered %d", harness.ErrStep, result.Status)
	}
	return nil
}

func (s *checkSteps) actions(ctx context.Context, name, run, raw string) error {
	want := []string{}
	if raw != "" {
		want = strings.Split(raw, ", ")
	}
	return deps.Eventually(ctx, "GitHub current requested actions", func(context.Context) (bool, error) {
		for _, r := range s.snapshots(false) {
			if r.Name == name && strings.HasSuffix(r.URL, "/runs/"+s.w.IDs[run]) {
				return slices.Equal(r.Actions, want), nil
			}
		}
		return false, nil
	})
}

func (s *checkSteps) integrationFixture(ctx context.Context, provider string) error {
	if err := s.seed(provider); err != nil {
		return err
	}
	s.body["customInput"] = map[string]any{"name": "Checks", "spaceID": "acme", "isDefault": true, "vcsChecks": "INDIVIDUAL"}
	id, err := s.create(ctx, s.path, s.body)
	if err != nil {
		return err
	}
	s.integration = id
	s.w.IDs["checkIntegration"] = id
	r, err := s.w.Do(ctx, "B", "h", http.MethodGet, s.path+"/"+id, nil)
	if err != nil {
		return err
	}
	s.originalETag = r.Header.Get("ETag")
	if r.Status != 200 || s.originalETag == "" {
		return fmt.Errorf("%w: option fixture lacks integration ETag", harness.ErrStep)
	}
	return nil
}

func (s *checkSteps) requestMode(ctx context.Context, method, mode string) error {
	path := s.path
	var body map[string]any
	headers := []sdk.Header{}
	if method == http.MethodPost {
		body = map[string]any{}
		for k, v := range s.body {
			body[k] = v
		}
		body["customInput"] = map[string]any{"name": "Denied checks", "spaceID": "acme", "isDefault": true, "vcsChecks": mode}
	} else {
		path += "/" + s.integration
		body = map[string]any{"customInput": map[string]any{"space": "acme", "vcsChecks": mode}}
		if method == http.MethodPatch {
			headers = append(headers, sdk.Header{Name: "If-Match", Value: s.originalETag})
		}
	}
	r, err := s.w.Do(ctx, "B", "h", method, path, body, headers...)
	if err != nil && r.Status == 0 {
		return err
	}
	return nil
}

func (s *checkSteps) unchangedIntegration(ctx context.Context) error {
	r, err := s.w.Do(ctx, "A", "h", http.MethodGet, s.path+"/"+s.integration, nil)
	if err != nil {
		return err
	}
	var view struct {
		Mode string `json:"vcsChecks"`
	}
	if r.Status != 200 || json.Unmarshal(r.Body, &view) != nil || view.Mode != "INDIVIDUAL" || r.Header.Get("ETag") != s.originalETag {
		return fmt.Errorf("%w: refused option mutated stored integration", harness.ErrStep)
	}
	var count int
	if err := s.w.Row(ctx, `SELECT count(*) FROM vcs_integrations WHERE account_id=$1`, []any{harness.UUIDOf(harness.Home.Account)}, &count); err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%w: refused option stored another integration", harness.ErrStep)
	}
	return nil
}

func (s *checkSteps) push(ctx context.Context) error {
	if s.provider != "GitLab" {
		return fmt.Errorf("%w: push proof requires GitLab", harness.ErrStep)
	}
	r, err := s.w.Do(ctx, "A", "h", http.MethodGet, s.path+"/"+s.integration, nil)
	if err != nil {
		return err
	}
	if r.Status != 200 {
		return fmt.Errorf("%w: webhook secret read failed", harness.ErrStep)
	}
	hooks := &gitlabWebhookSteps{w: s.w, provider: s.gitlab}
	if err := hooks.keepSecret(); err != nil {
		return err
	}
	before := s.w.IDs["checkCommit"]
	head := s.repo.Commit("main", "event check changes", deploymentFiles("two"))
	s.w.IDs["checkPushCommit"] = head
	delivery, err := s.gitlab.fake.PushWebhook(s.namespace, "infra", "main", before, head)
	if err != nil {
		return err
	}
	delivery.ID = kernel.NewULID().UUID()
	if err := hooks.sendDelivery(ctx, "checkIntegration", "A", hooks.secret.Reveal(), delivery); err != nil {
		return err
	}
	if s.w.Last().Status != 202 {
		return fmt.Errorf("%w: check push intake answered %d", harness.ErrStep, s.w.Last().Status)
	}
	return nil
}

func (s *checkSteps) keepPush(ctx context.Context, key string) error {
	if err := (&pushSteps{w: s.w}).keepAny(ctx, "TRACKED", "checkPushCommit", key); err != nil {
		return err
	}
	var slug string
	if err := s.w.Row(
		ctx,
		`SELECT s.slug FROM runs r JOIN stacks s ON s.account_id=r.account_id AND s.id=r.stack_id WHERE r.account_id=$1 AND r.id=$2`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.w.IDs[key])},
		&slug,
	); err != nil {
		return err
	}
	s.w.IDs["checkPushStack"] = slug
	s.pushRun = s.w.IDs[key]
	return nil
}

func (s *checkSteps) capFailures(ctx context.Context, limit, count int) error {
	return deps.Eventually(ctx, "cap diagnostics and completed native effects", func(ctx context.Context) (bool, error) {
		var raw string
		var runs int
		err := s.w.Row(
			ctx,
			`SELECT coalesce(jsonb_agg(DISTINCT payload->'binding'->>'slug'),'[]')::text FROM jobs WHERE account_id=$1 AND kind='vcs.report_check' AND state='succeeded' AND payload->'binding'->>'commit'=$2 AND payload->'binding'->>'cap'='true'`,
			[]any{harness.UUIDOf(harness.Home.Account), s.w.IDs["checkPushCommit"]},
			&raw,
		)
		if err != nil {
			return false, err
		}
		var slugs []string
		if json.Unmarshal([]byte(raw), &slugs) != nil {
			return false, fmt.Errorf("%w: invalid cap receipts", harness.ErrStep)
		}
		if len(slugs) != count {
			return false, nil
		}
		err = s.w.Row(
			ctx,
			`SELECT count(*) FROM runs WHERE account_id=$1 AND commit->>'hash'=$2 AND triggered_by='vcs/commit'`,
			[]any{harness.UUIDOf(harness.Home.Account), s.w.IDs["checkPushCommit"]},
			&runs,
		)
		if err != nil {
			return false, err
		}
		if runs != 1 {
			return false, fmt.Errorf("%w: capped event created %d runs", harness.ErrStep, runs)
		}
		admitted := false
		for _, r := range s.snapshots(true) {
			if r.Name == "infraweave/"+s.w.IDs["checkPushStack"]+"/tracked" && r.SHA == s.w.IDs["checkPushCommit"] && strings.HasSuffix(r.URL, "/runs/"+s.pushRun) &&
				r.State == "running" &&
				strings.Contains(r.Summary, "1 added") {
				admitted = true
			}
		}
		if !admitted {
			return false, nil
		}
		for _, slug := range slugs {
			found := false
			for _, r := range s.snapshots(true) {
				if r.Name == "infraweave/"+slug+"/tracked" && r.SHA == s.w.IDs["checkPushCommit"] && r.State == "failed" &&
					strings.Contains(r.Summary, "limit of "+strconv.Itoa(limit)+" runs") {
					found = true
				}
			}
			if !found {
				return false, nil
			}
		}
		fmt.Printf("Check cap evidence: admitted=%d excluded=%d limit=%d native-failures=%s\n", runs, len(slugs), limit, strings.Join(slugs, ","))
		return true, nil
	})
}
