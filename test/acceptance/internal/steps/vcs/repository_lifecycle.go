package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type repositoryLifecycleSteps struct {
	w      *harness.World
	github *githubSteps
	gitlab *gitlabSteps
	hooks  *gitlabWebhookSteps
	stacks map[string]lifecycleStack
	mints  map[string]int
	tenant string
}

type lifecycleStack struct {
	account, id             kernel.ULID
	namespace, name, branch string
	etag                    string
	version                 int64
	events, audits          int
}

func (s *repositoryLifecycleSteps) register(sc *godog.ScenarioContext) {
	s.stacks = map[string]lifecycleStack{}
	s.mints = map[string]int{}
	sc.Step(`^a GitHub lifecycle App "(\w+)" is installed on "([^"]+)"$`, s.app)
	sc.Step(`^these repository lifecycle stacks are created:$`, s.createStacks)
	sc.Step(`^repository lifecycle stacks are checkpointed:$`, s.checkpoints)
	sc.Step(`^repository lifecycle stacks now have these coordinates and version advances:$`, s.coordinates)
	sc.Step(`^GitLab renames project "([^"]+)" to "([^"]+)"$`, s.renameProject)
	sc.Step(`^GitLab pushes renamed project "([^"]+)" as "(\w+)" for integration "(\w+)" from "(\w+)" to "(\w+)" on "([^"]+)" through ([AB])$`, s.push)
	sc.Step(`^GitHub renames repository "([^"]+)" to "([^"]+)" and App "(\w+)" sends delivery "(\w+)" to integration "(\w+)" through ([AB])$`, s.renameRepository)
	sc.Step(
		`^GitHub renames owner "([^"]+)" to "([^"]+)" and App "(\w+)" sends "(organization|installation_target)" delivery "(\w+)" to integration "(\w+)" through ([AB])$`,
		s.renameOwner,
	)
	sc.Step(`^the native GitHub installation of App "(\w+)" becomes "(active|suspended|deleted)"$`, s.nativeState)
	sc.Step(`^GitHub restores App "(\w+)" on "([^"]+)" using "(the same installation|a new installation)"$`, s.restore)
	sc.Step(`^GitHub App "(\w+)" minted source tokens only for its restored installation$`, s.restoredTokens)
	sc.Step(`^GitHub App "(\w+)" sends installation action "([a-z_]+)" for owner "([^"]+)" as delivery "(\w+)" to integration "(\w+)" through ([AB])$`, s.installation)
	sc.Step(`^GitHub App "(\w+)" sends repository membership "(added|removed)" for "([^"]+)" as delivery "(\w+)" to integration "(\w+)" through ([AB])$`, s.membership)
	sc.Step(`^GitHub source resolution on stack "([^"]+)" refuses "([^"]+)" without a provider call through ([AB])$`, s.refused)
	sc.Step(`^the GitHub installation check names owner "([^"]+)" and state "(suspended|deleted)"$`, s.checkFailure)
	sc.Step(`^local tenancy is checkpointed before provider lifecycle events$`, s.tenancyCheckpoint)
	sc.Step(`^provider lifecycle events leave every local account organization and space unchanged$`, s.tenancyUnchanged)
}

// The native installation ID is retained explicitly; webhook payloads use the
// fake's allocated identities rather than unrelated static protocol examples.
func (s *repositoryLifecycleSteps) app(key, owner string) error {
	if s.github.fake == nil {
		return fmt.Errorf("%w: lifecycle App needs @github", harness.ErrStep)
	}
	app := s.github.fake.AddApp(key, owner)
	id := s.github.fake.Install(app.ID, owner, "Organization", "infra", "web")
	pem, err := json.Marshal(string(app.PrivateKey))
	if err != nil {
		return fmt.Errorf("lifecycle App key: %w", err)
	}
	s.w.IDs[key+"ID"] = strconv.FormatInt(app.ID, 10)
	s.w.IDs[key+"Key"] = strings.Trim(string(pem), `"`)
	s.w.IDs[key+"WebhookSecret"] = app.WebhookSecret
	s.w.IDs[key+"Installation"] = strconv.FormatInt(id, 10)
	for _, name := range []string{"infra", "web"} {
		repo := s.github.fake.Repo(owner, name)
		head := repo.Commit("main", "lifecycle seed", deploymentFiles("base"))
		repo.Branch("feature", head)
		if name == "infra" {
			s.w.IDs["seed"] = head
		} else {
			s.w.IDs["webSeed"] = head
		}
	}
	return nil
}

func (s *repositoryLifecycleSteps) createStacks(ctx context.Context, table *godog.Table) error {
	for _, row := range table.Rows[1:] {
		if len(row.Cells) != 10 {
			return fmt.Errorf("%w: lifecycle stack needs ten columns", harness.ErrStep)
		}
		v := make([]string, len(row.Cells))
		for i, cell := range row.Cells {
			v[i] = s.w.Resolve(cell.Value)
		}
		input := map[string]any{"name": v[1], "provider": v[2], "namespace": v[3], "repository": v[4], "branch": v[5], "space": v[6]}
		if v[7] != "-" {
			input["vcsIntegrationId"] = s.w.IDs[v[7]]
		}
		if v[8] != "-" {
			input["vendorConfig"] = map[string]any{v[8]: map[string]any{}}
			input["workerPool"] = s.w.IDs["pool"]
		}
		r, err := s.w.Do(ctx, "B", v[0], http.MethodPost, "/api/v1/stacks", map[string]any{"input": input, "manageState": v[9] == "true"})
		if err != nil {
			return fmt.Errorf("create lifecycle stack %s: %w", v[1], err)
		}
		if r.Status != http.StatusCreated {
			return fmt.Errorf("%w: create lifecycle stack %s: status=%d body=%s", harness.ErrStep, v[1], r.Status, r.Body)
		}
	}
	return nil
}

func (s *repositoryLifecycleSteps) snapshot(ctx context.Context, token, slug string) (lifecycleStack, error) {
	r, err := s.w.Do(ctx, "B", token, http.MethodGet, "/api/v1/stacks/"+slug, nil)
	if err != nil {
		return lifecycleStack{}, fmt.Errorf("lifecycle stack read %s: %w", slug, err)
	}
	var view struct {
		ID string `json:"id"`
	}
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &view) != nil || view.ID != slug || r.Header.Get("ETag") == "" {
		return lifecycleStack{}, fmt.Errorf("%w: lifecycle stack read %s: status=%d", harness.ErrStep, slug, r.Status)
	}
	st := lifecycleStack{etag: r.Header.Get("ETag")}
	var claims harness.APIClaims
	if err := harness.JWTPart(s.w.Tokens[token], 1, &claims); err != nil {
		return st, err
	}
	key, err := kernel.ParseULID(strings.TrimPrefix(claims.Subject, "api::"))
	if err != nil {
		return st, fmt.Errorf("lifecycle caller identity: %w", err)
	}
	// Stack.id is the served slug. Resolve the immutable fixture identity only
	// within the account of the API key that successfully read that slug.
	if err := s.w.Row(ctx, `SELECT s.account_id,s.id FROM stacks s JOIN api_keys k ON k.account_id=s.account_id
		WHERE k.id=$1 AND s.slug=$2`, []any{key, slug}, &st.account, &st.id); err != nil {
		return st, err
	}
	err = s.w.Row(ctx, `SELECT s.account_id,s.id,s.namespace,s.repository,s.branch,s.version,
		(SELECT count(*) FROM events WHERE account_id=s.account_id AND kind='stack.changed' AND subject_id=$3),
		(SELECT count(*) FROM audit_trail_entries WHERE account_id=s.account_id AND affected_id IN ($3,$4))
		FROM stacks s WHERE s.account_id=$1 AND s.id=$2`, []any{st.account, st.id, st.id.String(), slug}, &st.account, &st.id, &st.namespace, &st.name, &st.branch, &st.version, &st.events, &st.audits)
	return st, err
}

func (s *repositoryLifecycleSteps) checkpoints(ctx context.Context, table *godog.Table) error {
	for _, row := range table.Rows[1:] {
		if len(row.Cells) != 2 {
			return fmt.Errorf("%w: lifecycle checkpoint needs token and stack", harness.ErrStep)
		}
		token, slug := row.Cells[0].Value, row.Cells[1].Value
		st, err := s.snapshot(ctx, token, slug)
		if err != nil {
			return err
		}
		// Source tracking, rather than a synthetic INSERT, must seed the map
		// before a rename can prove that stable native identity drives it.
		if err := deps.Eventually(ctx, "initial stack source identity map", func(ctx context.Context) (bool, error) {
			var mapped bool
			err := s.w.Row(ctx, `SELECT EXISTS(SELECT 1 FROM vcs_repository_map m
				JOIN vcs_integrations i ON i.account_id=m.account_id AND i.id=m.integration_id JOIN stacks s
				ON m.account_id=s.account_id AND m.namespace=s.namespace AND m.name=s.repository
				AND (s.vcs_integration_id=m.integration_id OR (s.vcs_integration_id IS NULL AND i.is_default
				AND (s.provider=i.provider OR (s.provider='GITHUB' AND i.provider='GITHUB_ENTERPRISE'))))
				WHERE s.account_id=$1 AND s.id=$2)`, []any{st.account, st.id}, &mapped)
			return mapped, err
		}); err != nil {
			return err
		}
		s.stacks[token+"/"+slug] = st
	}
	return nil
}

func (s *repositoryLifecycleSteps) coordinates(ctx context.Context, table *godog.Table) error {
	for _, row := range table.Rows[1:] {
		if len(row.Cells) != 5 {
			return fmt.Errorf("%w: lifecycle result needs token stack namespace repository advances", harness.ErrStep)
		}
		token, slug := row.Cells[0].Value, row.Cells[1].Value
		before, ok := s.stacks[token+"/"+slug]
		advances, err := strconv.ParseInt(row.Cells[4].Value, 10, 64)
		if !ok || err != nil {
			return fmt.Errorf("%w: missing lifecycle checkpoint or invalid advance for %s", harness.ErrStep, slug)
		}
		if err := deps.Eventually(ctx, "repository coordinates and canonical versioned event", func(ctx context.Context) (bool, error) {
			after, err := s.snapshot(ctx, token, slug)
			if err != nil {
				return false, err
			}
			if after.namespace != row.Cells[2].Value || after.name != row.Cells[3].Value {
				return false, nil
			}
			if after.account != before.account || after.id != before.id || after.branch != before.branch || after.version != before.version+advances ||
				after.events != before.events+int(advances) || after.audits != before.audits || (after.etag != before.etag) != (advances > 0) {
				return false, fmt.Errorf("%w: repository rename changed identity/branch, lost version/event/ETag, or wrote an audit for %s", harness.ErrStep, slug)
			}
			var canonical int64
			if err := s.w.Row(ctx, `SELECT count(*) FROM events WHERE account_id=$1 AND subject_id=$2
				AND kind='stack.changed' AND schema_version=1 AND subject_kind='stack' AND sequence>$3 AND sequence<=$4
				AND document->>'stack'=$2 AND document->>'slug'=$5 AND document->>'change'='updated'
				AND NOT coalesce((document->>'source')::boolean,false)`, []any{after.account, after.id.String(), before.version, after.version, slug}, &canonical); err != nil {
				return false, err
			}
			if canonical != advances {
				return false, fmt.Errorf("%w: stack %s has %d canonical rename events, want %d", harness.ErrStep, slug, canonical, advances)
			}
			return true, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *repositoryLifecycleSteps) renameProject(from, to string) error {
	ns, name, err := s.gitlab.split(from)
	if err != nil {
		return err
	}
	nextNS, nextName, err := s.gitlab.split(to)
	if err != nil {
		return err
	}
	return s.gitlab.fake.RenameProject(ns, name, nextNS, nextName) //nolint:wrapcheck // the fake names the refused native operation
}

func (s *repositoryLifecycleSteps) push(ctx context.Context, path, delivery, integration, before, head, branch, replica string) error {
	ns, name, err := s.gitlab.split(path)
	if err != nil {
		return err
	}
	d, err := s.gitlab.fake.PushWebhook(ns, name, branch, s.w.IDs[before], s.w.IDs[head])
	if err != nil {
		return fmt.Errorf("renamed GitLab push: %w", err)
	}
	d.ID = kernel.NewULID().UUID()
	s.hooks.deliveries[delivery] = d
	return s.hooks.sendDelivery(ctx, integration, replica, s.hooks.secret.Reveal(), d)
}

func (s *repositoryLifecycleSteps) send(ctx context.Context, app, event, delivery, integration, replica string, body any) error {
	id, err := strconv.ParseInt(s.w.IDs[app+"ID"], 10, 64)
	if err != nil {
		return fmt.Errorf("lifecycle App ID: %w", err)
	}
	sender, err := s.github.fake.Sender(id)
	if err != nil {
		return fmt.Errorf("lifecycle sender: %w", err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("lifecycle payload: %w", err)
	}
	status, err := sender.Send(ctx, s.w.Base(replica)+"/webhooks/vcs/github_enterprise/"+s.w.IDs[integration], delivery, event, raw)
	s.w.Sent = append(s.w.Sent, harness.Response{Status: status})
	if err != nil {
		return fmt.Errorf("lifecycle delivery: %w", err)
	}
	return nil
}

func (s *repositoryLifecycleSteps) renameRepository(ctx context.Context, path, next, app, delivery, integration, replica string) error {
	owner, name, ok := strings.Cut(path, "/")
	if !ok {
		return fmt.Errorf("%w: repository needs owner/name", harness.ErrStep)
	}
	if err := s.github.fake.RenameRepository(owner, name, next); err != nil {
		return fmt.Errorf("native repository rename: %w", err)
	}
	repo, err := s.github.fake.RepositoryPayload(owner, next)
	if err != nil {
		return fmt.Errorf("native repository payload: %w", err)
	}
	return s.send(ctx, app, "repository", delivery, integration, replica, map[string]any{
		"action": "renamed", "repository": repo,
		"changes": map[string]any{"repository": map[string]any{"name": map[string]any{"from": name}}},
	})
}

func (s *repositoryLifecycleSteps) installationID(app string) (int64, error) {
	id, err := strconv.ParseInt(s.w.IDs[app+"Installation"], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: missing native installation for %s", harness.ErrStep, app)
	}
	return id, nil
}

func (s *repositoryLifecycleSteps) renameOwner(ctx context.Context, from, to, app, event, delivery, integration, replica string) error {
	if err := s.github.fake.RenameOwner(from, to); err != nil {
		return fmt.Errorf("native owner rename: %w", err)
	}
	id, err := s.installationID(app)
	if err != nil {
		return err
	}
	raw, err := s.github.fake.InstallationPayload(id)
	var install struct {
		Account json.RawMessage `json:"account"`
	}
	if err != nil || json.Unmarshal(raw, &install) != nil {
		return fmt.Errorf("%w: native installation payload unavailable", harness.ErrStep)
	}
	field := "account"
	if event == "organization" {
		field = "organization"
	}
	return s.send(ctx, app, event, delivery, integration, replica, map[string]any{
		"action": "renamed", field: install.Account,
		"changes": map[string]any{"login": map[string]any{"from": from}},
	})
}

func (s *repositoryLifecycleSteps) nativeState(app, state string) error {
	id, err := s.installationID(app)
	if err != nil {
		return err
	}
	return s.github.fake.SetInstallationState(id, state) //nolint:wrapcheck // the fake names the invalid native state
}

func (s *repositoryLifecycleSteps) restore(app, owner, mode string) error {
	id, err := s.installationID(app)
	if err != nil {
		return err
	}
	s.mints[app] = len(s.github.fake.TokenRequests())
	if mode == "the same installation" {
		return s.nativeState(app, "active")
	}
	appID, err := strconv.ParseInt(s.w.IDs[app+"ID"], 10, 64)
	if err != nil {
		return fmt.Errorf("lifecycle App ID: %w", err)
	}
	next := s.github.fake.Install(appID, owner, "Organization", "infra", "web")
	if next == id {
		return fmt.Errorf("%w: reinstall reused the deleted native installation ID", harness.ErrStep)
	}
	s.w.IDs[app+"Installation"] = strconv.FormatInt(next, 10)
	return nil
}

func (s *repositoryLifecycleSteps) restoredTokens(app string) error {
	id, err := s.installationID(app)
	if err != nil {
		return err
	}
	before, kept := s.mints[app]
	requests := s.github.fake.TokenRequests()
	if !kept || len(requests) <= before {
		return fmt.Errorf("%w: restored run minted no native source token", harness.ErrStep)
	}
	for _, request := range requests[before:] {
		if request.Installation != id {
			return fmt.Errorf("%w: restored source used obsolete installation %d, want %d", harness.ErrStep, request.Installation, id)
		}
	}
	return nil
}

func (s *repositoryLifecycleSteps) installation(ctx context.Context, app, action, owner, delivery, integration, replica string) error {
	id, err := s.installationID(app)
	if err != nil {
		return err
	}
	in, err := s.github.fake.InstallationPayload(id)
	if err != nil {
		return fmt.Errorf("native installation: %w", err)
	}
	repos := []json.RawMessage{}
	for _, name := range []string{"infra", "web"} {
		r, err := s.github.fake.RepositoryPayload(owner, name)
		if err != nil {
			return fmt.Errorf("native installed repository: %w", err)
		}
		repos = append(repos, r)
	}
	return s.send(ctx, app, "installation", delivery, integration, replica, map[string]any{"action": action, "installation": in, "repositories": repos})
}

func (s *repositoryLifecycleSteps) membership(ctx context.Context, app, action, path, delivery, integration, replica string) error {
	owner, name, ok := strings.Cut(path, "/")
	if !ok {
		return fmt.Errorf("%w: repository membership needs owner/name", harness.ErrStep)
	}
	id, err := s.installationID(app)
	if err != nil {
		return err
	}
	repos := []string{"infra", "web"}
	if action == "removed" {
		repos = []string{"web"}
	}
	if err := s.github.fake.SetInstallationRepositories(id, repos...); err != nil {
		return fmt.Errorf("native membership: %w", err)
	}
	in, err := s.github.fake.InstallationPayload(id)
	if err != nil {
		return fmt.Errorf("native installation: %w", err)
	}
	repo, err := s.github.fake.RepositoryPayload(owner, name)
	if err != nil {
		return fmt.Errorf("native membership repository: %w", err)
	}
	return s.send(ctx, app, "installation_repositories", delivery, integration, replica, map[string]any{
		"action": action, "installation": in, "repositories_" + action: []json.RawMessage{repo},
	})
}

func (s *repositoryLifecycleSteps) refused(ctx context.Context, stack, reason, replica string) error {
	before := s.github.fake.Requests()
	mints := len(s.github.fake.TokenRequests())
	r, err := s.w.Do(ctx, replica, "h", http.MethodPost, "/api/v1/stacks/"+stack+"/runs", map[string]any{"commitSha": s.w.IDs["seed"]})
	if err != nil {
		return fmt.Errorf("lifecycle source request: %w", err)
	}
	var problem struct {
		Detail string `json:"detail"`
	}
	if r.Status != http.StatusUnprocessableEntity || json.Unmarshal(r.Body, &problem) != nil || !strings.Contains(problem.Detail, reason) ||
		!strings.Contains(problem.Detail, "acme") || s.github.fake.Requests() != before || len(s.github.fake.TokenRequests()) != mints {
		return fmt.Errorf("%w: source refusal lost owner/reason or called GitHub: status=%d detail=%s", harness.ErrStep, r.Status, problem.Detail)
	}
	return nil
}

func (s *repositoryLifecycleSteps) checkFailure(owner, state string) error {
	var result struct {
		Status string `json:"status"`
		Checks []struct {
			Type     string `json:"type"`
			Status   string `json:"status"`
			Metadata struct {
				Reason string `json:"reason"`
			} `json:"metadata"`
		} `json:"checks"`
	}
	r := s.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &result) != nil || result.Status != "FAIL" {
		return fmt.Errorf("%w: unavailable installation did not fail connection test", harness.ErrStep)
	}
	for _, check := range result.Checks {
		if check.Type == "PERMISSIONS" && check.Status == "FAIL" && strings.Contains(check.Metadata.Reason, owner) && strings.Contains(check.Metadata.Reason, state) {
			return nil
		}
	}
	return fmt.Errorf("%w: no failed PERMISSIONS check names %s %s", harness.ErrStep, owner, state)
}

func (s *repositoryLifecycleSteps) tenancy(ctx context.Context) (string, error) {
	var snapshot string
	err := s.w.Row(ctx, `SELECT jsonb_build_object(
		'accounts',(SELECT jsonb_agg(jsonb_build_array(id,organization_id,slug,name) ORDER BY id) FROM accounts),
		'organizations',(SELECT jsonb_agg(jsonb_build_array(id,slug,name) ORDER BY id) FROM organizations),
		'spaces',(SELECT jsonb_agg(jsonb_build_array(id,account_id,parent_id,slug,name) ORDER BY id) FROM spaces))::text`, nil, &snapshot)
	return snapshot, err
}

func (s *repositoryLifecycleSteps) tenancyCheckpoint(ctx context.Context) error {
	var err error
	s.tenant, err = s.tenancy(ctx)
	return err
}

func (s *repositoryLifecycleSteps) tenancyUnchanged(ctx context.Context) error {
	after, err := s.tenancy(ctx)
	if err != nil {
		return err
	}
	if s.tenant == "" || after != s.tenant {
		return fmt.Errorf("%w: provider lifecycle changed local account/organization/space identity or membership", harness.ErrStep)
	}
	return nil
}
