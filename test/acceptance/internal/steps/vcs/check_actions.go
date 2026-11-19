package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type checkActionSteps struct {
	w           *harness.World
	github      *githubSteps
	checks      *checkSteps
	sender      *githubfake.Sender
	check       githubfake.CheckRecord
	pointers    string
	deliveries  []string
	bodies      map[string][]byte
	createdRuns map[string]string
	before      int
	public      bool
}

func (s *checkActionSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^a recorded GitHub action check on "(public|enterprise)" using "(terraform|opentofu)"$`, s.setup)
	sc.Step(`^the action sender has repository permission "(read|write)"$`, s.permission)
	sc.Step(`^the recorded check sends action "(preview|deploy)" as delivery "(\w+)" through ([AB])$`, s.press)
	sc.Step(`^action delivery "(\w+)" is duplicated through ([AB])$`, s.duplicate)
	sc.Step(`^action delivery "(\w+)" creates one fresh "(PROPOSED|TRACKED)" run kept as "(\w+)"$`, s.created)
	sc.Step(`^all action deliveries settle without changing the stack's tracked pointers$`, s.settled)
	sc.Step(`^the action source fetched through the configured native GitHub host$`, s.sourceHost)
	sc.Step(`^the action deliveries create no run and log refusal "([a-z_]+)" exactly (\d+) times$`, s.refused)
	sc.Step(`^the recorded check sends forged "(foreign account|other repository|other integration|stale external ID)" action through ([AB])$`, s.forged)
}

func (s *checkActionSteps) setup(ctx context.Context, host, vendor string) error {
	s.public = host == "public"
	if (s.github.proxy != nil) != s.public {
		return fmt.Errorf("%w: public verification tunnel does not match host", harness.ErrStep)
	}
	if err := s.checks.seed("GitHub"); err != nil {
		return err
	}
	s.checks.body["webhookSecret"] = s.checks.githubApp.WebhookSecret
	if s.public {
		s.checks.body["apiHost"] = "https://api.github.com"
		s.checks.body["userFacingHost"] = "https://github.com"
	}
	if err := s.checks.stack(ctx, "GitHub", "actions", vendor, "INDIVIDUAL", "{}"); err != nil {
		return err
	}
	if err := s.checks.promotion(ctx, "actions", "true"); err != nil {
		return err
	}
	response, err := s.w.Do(ctx, "B", "h", http.MethodGet, s.checks.path+"/"+s.checks.integration, nil)
	if err != nil {
		return err
	}
	var hosts struct {
		APIHost        string `json:"apiHost"`
		UserFacingHost string `json:"userFacingHost"`
	}
	if response.Status != 200 || json.Unmarshal(response.Body, &hosts) != nil || hosts.APIHost != s.checks.body["apiHost"] ||
		(s.public && hosts.UserFacingHost != "https://github.com") {
		return fmt.Errorf("%w: stored GitHub API or Git host differs from native fixture", harness.ErrStep)
	}
	// This commit is deliberately newer than the recorded tracked commit. There
	// is no push: a button must neither promote nor overwrite source pointers.
	s.w.IDs["actionCommit"] = s.checks.repo.Commit("main", "requested action commit", deploymentFiles("action"))
	response, err = s.w.Do(ctx, "A", "h", http.MethodPost, "/api/v1/stacks/actions/runs", map[string]any{"runType": "PROPOSED", "commitSha": s.w.IDs["actionCommit"]})
	if err != nil {
		return err
	}
	var created struct {
		ID string `json:"id"`
	}
	if response.Status != 200 || json.Unmarshal(response.Body, &created) != nil || created.ID == "" {
		return fmt.Errorf("%w: seed run trigger answered %d", harness.ErrStep, response.Status)
	}
	run := created.ID
	s.w.IDs["actionSeed"] = run
	if err := deps.Eventually(ctx, "real seed plan and recorded action check", func(ctx context.Context) (bool, error) {
		response, err := s.w.Do(ctx, "B", "h", http.MethodGet, "/api/v1/runs/"+run, nil)
		if err != nil {
			return false, err
		}
		var view api.Run
		if response.Status != 200 || json.Unmarshal(response.Body, &view) != nil || string(view.State) != "FINISHED" {
			return false, nil
		}
		for _, check := range s.github.fake.Checks(s.checks.namespace, "infra") {
			if strings.HasSuffix(check.URL, "/runs/"+run) && check.ExternalID == s.w.IDs["checkStack:actions"] && check.SHA == s.w.IDs["actionCommit"] && len(check.Actions) == 2 {
				s.check = check
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return err
	}
	if err := s.checks.settled(ctx, "actionSeed"); err != nil {
		return err
	}
	s.pointers, err = s.readPointers(ctx)
	if err != nil {
		return err
	}
	if !strings.Contains(s.pointers, s.w.IDs["checkCommit"]) || strings.Contains(s.pointers, s.w.IDs["actionCommit"]) {
		return fmt.Errorf("%w: action fixture lacks distinct tracked commit", harness.ErrStep)
	}
	s.sender, err = s.github.fake.Sender(s.checks.githubApp.ID)
	if err != nil {
		return err
	}
	s.bodies = map[string][]byte{}
	s.createdRuns = map[string]string{}
	return s.w.Row(ctx, `SELECT count(*) FROM runs`, nil, &s.before)
}

func (s *checkActionSteps) permission(role string) {
	s.github.fake.SetCollaboratorPermission(s.checks.namespace, "infra", "developer", role)
}

func (s *checkActionSteps) send(ctx context.Context, delivery, replica string, body []byte) error {
	if _, exists := s.bodies[delivery]; !exists {
		s.deliveries = append(s.deliveries, delivery)
		s.bodies[delivery] = body
	}
	status, err := s.sender.Send(ctx, s.w.Base(replica)+"/webhooks/vcs/github_enterprise/"+s.checks.integration, delivery, "check_run", body)
	s.w.Sent = append(s.w.Sent, harness.Response{Status: status})
	if err != nil {
		return fmt.Errorf("action delivery: %w", err)
	}
	return nil
}

func (s *checkActionSteps) press(ctx context.Context, action, delivery, replica string) error {
	body, err := s.github.fake.CheckActionPayload(s.checks.namespace, "infra", s.check.ID, action, "developer")
	if err != nil {
		return err
	}
	return s.send(ctx, delivery, replica, body)
}

func (s *checkActionSteps) duplicate(ctx context.Context, delivery, replica string) error {
	body, ok := s.bodies[delivery]
	if !ok {
		return fmt.Errorf("%w: original action missing", harness.ErrStep)
	}
	return s.send(ctx, delivery, replica, body)
}

func (s *checkActionSteps) readPointers(ctx context.Context) (string, error) {
	response, err := s.w.Do(ctx, "A", "h", http.MethodGet, "/api/v1/stacks/actions", nil)
	if err != nil {
		return "", err
	}
	var stack api.Stack
	if response.Status != 200 || json.Unmarshal(response.Body, &stack) != nil || stack.TrackedCommit == nil || stack.TrackedBranchHead == nil {
		return "", fmt.Errorf("%w: missing tracked pointer view", harness.ErrStep)
	}
	data, err := json.Marshal([]any{stack.TrackedCommit, stack.TrackedBranchHead, stack.TrackedCommitSetBy})
	return string(data), err
}

func (s *checkActionSteps) settled(ctx context.Context) error {
	return deps.Eventually(ctx, "all action parent and child jobs settle with unchanged pointers", func(ctx context.Context) (bool, error) {
		for _, delivery := range s.deliveries {
			var state string
			var id kernel.ULID
			err := s.w.Row(
				ctx,
				`SELECT id,state FROM vcs_deliveries WHERE account_id=$1 AND integration_id=$2 AND delivery_id=$3`,
				[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.checks.integration), delivery},
				&id,
				&state,
			)
			if err != nil {
				return false, err
			}
			var active, dead int
			err = s.w.Row(
				ctx,
				`SELECT count(*) FILTER(WHERE state IN ('pending','running')),count(*) FILTER(WHERE state='dead') FROM jobs WHERE account_id=$1 AND kind IN ('vcs.process_delivery','vcs.evaluate_event') AND (unique_key=$2 OR starts_with(unique_key,$2||':'))`,
				[]any{harness.UUIDOf(harness.Home.Account), id.String()},
				&active,
				&dead,
			)
			if err != nil {
				return false, err
			}
			if dead > 0 {
				return false, fmt.Errorf("%w: action worker exhausted", harness.ErrStep)
			}
			if state != "dispatched" || active > 0 {
				return false, nil
			}
		}
		current, err := s.readPointers(ctx)
		if err != nil {
			return false, err
		}
		if current != s.pointers {
			return false, fmt.Errorf("%w: action changed tracked commit/head/setter", harness.ErrStep)
		}
		return true, nil
	})
}

func (s *checkActionSteps) created(ctx context.Context, delivery, kind, key string) error {
	if err := s.settled(ctx); err != nil {
		return err
	}
	var id kernel.ULID
	var count, total int
	var actualKind, sha, actor string
	if err := s.w.Row(
		ctx,
		`SELECT runs_created FROM vcs_deliveries WHERE account_id=$1 AND integration_id=$2 AND delivery_id=$3`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.checks.integration), delivery},
		&count,
	); err != nil {
		return err
	}
	err := s.w.Row(
		ctx,
		`SELECT id,type,commit->>'hash',triggered_by,(SELECT count(*) FROM runs WHERE account_id=$1 AND triggered_by='github/developer') FROM runs WHERE account_id=$1 AND stack_id=$2 AND triggered_by='github/developer' ORDER BY id DESC LIMIT 1`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.w.IDs["checkStack:actions"])},
		&id,
		&actualKind,
		&sha,
		&actor,
		&total,
	)
	if err != nil {
		return err
	}
	if count != 1 || total != len(s.createdRuns)+1 || actualKind != kind || sha != s.check.SHA || actor != "github/developer" || id.String() == s.w.IDs["actionSeed"] {
		return fmt.Errorf("%w: action lost fresh identity/type/commit/attribution", harness.ErrStep)
	}
	for _, value := range s.createdRuns {
		if value == id.String() {
			return fmt.Errorf("%w: new press reused an earlier run", harness.ErrStep)
		}
	}
	s.createdRuns[delivery] = id.String()
	s.w.IDs[key] = id.String()
	return nil
}

func (s *checkActionSteps) refused(ctx context.Context, reason string, want int) error {
	if err := s.settled(ctx); err != nil {
		return err
	}
	var count int
	if err := s.w.Row(ctx, `SELECT count(*) FROM runs`, nil, &count); err != nil {
		return err
	}
	if count != s.before {
		return fmt.Errorf("%w: refused action created runs", harness.ErrStep)
	}
	return deps.Eventually(ctx, "bounded after-commit action refusal logs", func(context.Context) (bool, error) {
		found := 0
		for _, server := range s.w.Servers {
			for _, line := range strings.Split(string(server.Stdout()), "\n") {
				var record struct {
					Msg    string `json:"msg"`
					Reason string `json:"reason"`
				}
				if json.Unmarshal([]byte(line), &record) == nil && record.Msg == "VCS check action refused" && record.Reason == reason {
					found++
				}
				if strings.Contains(line, s.checks.githubApp.WebhookSecret) || strings.Contains(line, string(s.checks.githubApp.PrivateKey)) {
					return false, fmt.Errorf("%w: action secret reached server log", deps.ErrPermanent)
				}
			}
		}
		if found > want {
			return false, fmt.Errorf("%w: action refusal logged more than once", deps.ErrPermanent)
		}
		return found == want, nil
	})
}

func (s *checkActionSteps) sourceHost() error {
	if !s.public {
		return nil
	}
	hosts := s.github.proxy.Hosts()
	if !slices.Contains(hosts, "api.github.com:443") || !slices.Contains(hosts, "github.com:443") {
		return fmt.Errorf("%w: public native API and authenticated Git did not both use true hosts", harness.ErrStep)
	}
	return nil
}

func (s *checkActionSteps) forged(ctx context.Context, kind, replica string) error {
	body, err := s.github.fake.CheckActionPayload(s.checks.namespace, "infra", s.check.ID, "preview", "developer")
	if err != nil {
		return err
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return harness.ErrStep
	}
	check := payload["check_run"].(map[string]any) //nolint:forcetypeassert // CheckActionPayload owns the shape.
	id := kernel.NewULID().String()
	if kind != "stale external ID" {
		token, space, provider, repository, integration := "h", "acme", "GITHUB_ENTERPRISE", "infra", s.checks.integration
		if kind == "foreign account" {
			db, err := s.w.Querier(ctx)
			if err != nil {
				return err
			}
			if err := harness.Sibling.Seed(ctx, db); err != nil {
				return err
			}
			anon, err := s.w.Client("A", "")
			if err != nil {
				return err
			}
			tokenValue, err := harness.Sibling.Exchange(ctx, anon)
			if err != nil {
				return err
			}
			s.w.Tokens["actionSibling"] = tokenValue
			token, space = "actionSibling", "acme-dev"
			siblingBody := map[string]any{}
			for k, v := range s.checks.body {
				siblingBody[k] = v
			}
			siblingBody["customInput"] = map[string]any{"name": "Sibling actions", "spaceID": "acme-dev", "isDefault": true}
			response, err := s.w.Do(ctx, "B", token, http.MethodPost, s.checks.path, siblingBody)
			if err != nil {
				return err
			}
			var created struct {
				ID string `json:"id"`
			}
			if response.Status != 201 || json.Unmarshal(response.Body, &created) != nil || created.ID == "" {
				return fmt.Errorf("%w: sibling App fixture answered %d", harness.ErrStep, response.Status)
			}
			integration = created.ID
		}
		if kind == "other repository" {
			repository = "web"
		}
		if kind == "other integration" {
			other := map[string]any{}
			for k, v := range s.checks.body {
				other[k] = v
			}
			other["customInput"] = map[string]any{"name": "Other actions", "spaceID": "acme"}
			integration, err = s.checks.create(ctx, s.checks.path, other)
			if err != nil {
				return err
			}
		}
		input := map[string]any{"name": "Forged target", "space": space, "provider": provider, "namespace": "acme", "repository": repository, "branch": "main"}
		if integration != "" {
			input["vcsIntegrationId"] = integration
		}
		response, err := s.w.Do(ctx, "A", token, http.MethodPost, "/api/v1/stacks", map[string]any{"input": input, "manageState": false})
		if err != nil {
			return err
		}
		if response.Status != 201 {
			return fmt.Errorf("%w: forged target fixture answered %d", harness.ErrStep, response.Status)
		}
		account := harness.Home.Account
		if kind == "foreign account" {
			account = harness.Sibling.Account
		}
		var immutable kernel.ULID
		if err := s.w.Row(ctx, `SELECT id FROM stacks WHERE account_id=$1 AND slug='forged-target'`, []any{harness.UUIDOf(account)}, &immutable); err != nil {
			return err
		}
		id = immutable.String()
	}
	check["external_id"] = id
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.send(ctx, "forged", replica, encoded)
}
