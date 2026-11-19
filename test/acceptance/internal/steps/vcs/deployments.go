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

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type deploymentSteps struct {
	w                   *harness.World
	github              *githubSteps
	gitlab              *gitlabSteps
	provider, namespace string
	integration         string
}

func (s *deploymentSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a "(GitHub|GitLab)" deployment stack "([^"]+)" using "(terraform|opentofu)" with labels '([^']*)'$`, s.stack)
	sc.Step(`^the "(GitHub|GitLab)" deployment API loses its next successful create response$`, s.loseCreate)
	sc.Step(`^the deployment repository changes its resource input to "([^"]+)" as commit "(\w+)"$`, s.change)
	sc.Step(`^deployment events for run "(\w+)" have settled$`, s.settled)
	sc.Step(`^a deployment report for run "(\w+)" succeeded after retry$`, s.retried)
	sc.Step(`^the "(GitHub|GitLab)" repository has exactly (\d+) native deployments and (\d+) environments$`, s.counts)
	sc.Step(`^the "(GitHub|GitLab)" deployment for run "(\w+)" on commit "(\w+)" in environment "([^"]+)" has statuses "([^"]+)"$`, s.statuses)
}

func deploymentFiles(value string) map[string]string {
	return map[string]string{"main.tf": "resource \"terraform_data\" \"this\" {\n  input = " + strconv.Quote(value) + "\n}\n"}
}

func (s *deploymentSteps) create(ctx context.Context, path string, body any) (string, error) {
	r, err := s.w.Do(ctx, "A", "h", http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	var result struct {
		ID string `json:"id"`
	}
	if r.Status != http.StatusCreated || json.Unmarshal(r.Body, &result) != nil || result.ID == "" {
		return "", fmt.Errorf("%w: fixture create %s answered %d without an ID", harness.ErrStep, path, r.Status)
	}
	return result.ID, nil
}

func (s *deploymentSteps) stack(ctx context.Context, provider, slug, vendor, rawLabels string) error {
	var labels []string
	if json.Unmarshal([]byte(rawLabels), &labels) != nil || s.provider != "" && s.provider != provider {
		return fmt.Errorf("%w: invalid deployment fixture labels or provider", harness.ErrStep)
	}
	if s.provider == "" {
		s.provider = provider
		var body map[string]any
		path := "/api/v1/gitlab-integrations"
		if provider == "GitHub" {
			if s.github.fake == nil {
				return fmt.Errorf("%w: deployment fixture needs GitHub", harness.ErrStep)
			}
			app := s.github.fake.AddApp("deployment", "acme")
			s.github.fake.Install(app.ID, "acme", "Organization", "infra")
			s.namespace = "acme"
			s.w.IDs["seed"] = s.github.fake.Repo(s.namespace, "infra").Commit("main", "deployment seed", deploymentFiles("one"))
			path = "/api/v1/github-enterprise-integrations"
			body = map[string]any{"apiHost": s.github.srv.URL, "appID": strconv.FormatInt(app.ID, 10), "privateKey": string(app.PrivateKey)}
		} else {
			if s.gitlab.fake == nil {
				return fmt.Errorf("%w: deployment fixture needs GitLab", harness.ErrStep)
			}
			s.namespace = "acme/platform"
			token := s.gitlab.fake.AddToken("deployment", "api")
			s.gitlab.fake.AddMember(s.namespace, "infra", "deployment", gitlabfake.Maintainer)
			s.w.IDs["seed"] = s.gitlab.fake.Repo(s.namespace, "infra").Commit("main", "deployment seed", deploymentFiles("one"))
			body = map[string]any{"apiHost": s.gitlab.srv.URL, "privateToken": token}
		}
		body["customInput"] = map[string]any{"name": "Deployments", "spaceID": "acme", "isDefault": true}
		var err error
		if s.integration, err = s.create(ctx, path, body); err != nil {
			return err
		}
	}
	providerKind := "GITLAB"
	switch provider {
	case "GitHub":
		providerKind = "GITHUB_ENTERPRISE"
	case "Bitbucket Cloud":
		providerKind = "BITBUCKET_CLOUD"
	case "Bitbucket DC":
		providerKind = "BITBUCKET_DATACENTER"
	case "Azure DevOps":
		providerKind = "AZURE_DEVOPS"
	}
	_, err := s.create(ctx, "/api/v1/stacks", map[string]any{
		"input": map[string]any{
			"name": slug, "namespace": s.namespace, "repository": "infra", "branch": "main", "provider": providerKind,
			"space": "acme", "workerPool": s.w.IDs["pool"], "labels": labels, "vcsIntegrationId": s.integration,
			"vendorConfig": map[string]any{vendor: map[string]any{}},
		},
		"manageState": true,
	})
	if err != nil {
		return err
	}
	return deps.Eventually(ctx, "deployment stack source commit", func(ctx context.Context) (bool, error) {
		r, err := s.w.Do(ctx, "B", "h", http.MethodGet, "/api/v1/stacks/"+slug, nil)
		if err != nil {
			return false, err
		}
		var stack struct {
			TrackedCommit *struct {
				Hash string `json:"hash"`
			} `json:"trackedCommit"`
		}
		return r.Status == http.StatusOK && json.Unmarshal(r.Body, &stack) == nil &&
			stack.TrackedCommit != nil && stack.TrackedCommit.Hash == s.w.IDs["seed"], nil
	})
}

func (s *deploymentSteps) loseCreate(provider string) error {
	if provider != s.provider {
		return fmt.Errorf("%w: provider differs from deployment fixture", harness.ErrStep)
	}
	if provider == "GitHub" {
		s.github.fake.FailNextDeploymentCreate()
	} else {
		s.gitlab.fake.FailNextDeploymentCreate()
	}
	return nil
}

func (s *deploymentSteps) change(value, name string) error {
	switch s.provider {
	case "GitHub":
		s.w.IDs[name] = s.github.fake.Repo(s.namespace, "infra").Commit("main", name, deploymentFiles(value))
	case "GitLab":
		s.w.IDs[name] = s.gitlab.fake.Repo(s.namespace, "infra").Commit("main", name, deploymentFiles(value))
	default:
		return fmt.Errorf("%w: no deployment repository", harness.ErrStep)
	}
	return nil
}

// settled makes negative assertions wait for every historical event to be
// fanned out and all sinks/report jobs to finish, including events with no target.
func (s *deploymentSteps) settled(ctx context.Context, run string) error {
	return deps.Eventually(ctx, "deployment event processing for "+run, func(ctx context.Context) (bool, error) {
		var count, pending int
		var fanned bool
		err := s.w.Row(ctx, `SELECT count(*), coalesce(bool_and(e.fanned_out_at IS NOT NULL AND EXISTS (
SELECT 1 FROM event_consumptions c WHERE c.account_id=e.account_id AND c.event_id=e.id AND c.consumer='events.sinks')),false),
(SELECT count(*) FROM jobs j WHERE j.account_id=$1 AND j.state<>'succeeded' AND
((j.kind='events.consume' AND j.payload->>'consumer'='events.sinks') OR j.kind='vcs.report_deployment'))
FROM events e WHERE e.account_id=$1 AND e.kind='run_updated' AND e.subject_kind='run' AND e.subject_id=$2`,
			[]any{harness.UUIDOf(harness.Home.Account), s.w.IDs[run]}, &count, &fanned, &pending)
		return count > 0 && fanned && pending == 0, err
	})
}

func (s *deploymentSteps) retried(ctx context.Context, run string) error {
	return deps.Eventually(ctx, "recovered deployment report for "+run, func(ctx context.Context) (bool, error) {
		var count int
		err := s.w.Row(ctx, `SELECT count(*) FROM jobs WHERE account_id=$1
AND kind='vcs.report_deployment' AND payload->'deployment'->'run'->>'id'=$2
AND state='succeeded' AND attempt>=2`, []any{harness.UUIDOf(harness.Home.Account), s.w.IDs[run]}, &count)
		return count > 0, err
	})
}

func (s *deploymentSteps) counts(provider string, deployments, environments int) error {
	if provider != s.provider {
		return fmt.Errorf("%w: provider differs from deployment fixture", harness.ErrStep)
	}
	count, envs := 0, 0
	if provider == "GitHub" {
		names := map[string]bool{}
		for _, record := range s.github.fake.Deployments(s.namespace, "infra") {
			count++
			names[record.Environment] = true
		}
		envs = len(names)
	} else {
		count = len(s.gitlab.fake.Deployments(s.namespace, "infra"))
		envs = len(s.gitlab.fake.Environments(s.namespace, "infra"))
	}
	if count != deployments || envs != environments {
		return fmt.Errorf("%w: native deployments=%d environments=%d, want %d/%d", harness.ErrStep, count, envs, deployments, environments)
	}
	return nil
}

func (s *deploymentSteps) statuses(ctx context.Context, provider, run, commit, environment, states string) error {
	return deps.Eventually(ctx, "native deployment states for "+run, func(context.Context) (bool, error) {
		want := strings.Split(states, ", ")
		if provider == "GitHub" {
			for _, record := range s.github.fake.Deployments(s.namespace, "infra") {
				var payload struct {
					Run string `json:"runId"`
				}
				if json.Unmarshal(record.Payload, &payload) != nil || payload.Run != s.w.IDs[run] {
					continue
				}
				got := make([]string, 0, len(record.Statuses))
				for _, status := range record.Statuses {
					got = append(got, status.State)
				}
				valid := record.SHA == s.w.IDs[commit] && record.Ref == s.w.IDs[commit] &&
					record.Environment == environment && record.Task == "infraweave apply" &&
					record.AutoMerge != nil && !*record.AutoMerge && record.RequiredContexts != nil && len(record.RequiredContexts) == 0 &&
					record.ProductionEnvironment != nil && *record.ProductionEnvironment
				if valid && slices.Equal(got, want) {
					return true, nil
				}
				return false, fmt.Errorf("%w: native metadata valid=%t statuses=%v, want %v", harness.ErrStep, valid, got, want)
			}
		} else {
			for _, record := range s.gitlab.fake.Deployments(s.namespace, "infra") {
				if record.SHA == s.w.IDs[commit] && record.Ref == "main" && !record.Tag && record.Environment.Name == environment {
					if slices.Equal(record.States, want) {
						return true, nil
					}
					return false, fmt.Errorf("%w: native statuses=%v, want %v", harness.ErrStep, record.States, want)
				}
			}
		}
		return false, fmt.Errorf("%w: no native deployment matches run %s, commit %s and environment %s", harness.ErrStep, run, commit, environment)
	})
}
