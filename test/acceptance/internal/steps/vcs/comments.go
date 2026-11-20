package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type commentSteps struct {
	w                   *harness.World
	github              *githubSteps
	gitlab              *gitlabSteps
	fixture             *deploymentSteps
	peers               *checkSteps
	sender              *githubfake.Sender
	secret              crypt.Value
	clock               time.Time
	created             map[int]time.Time
	appID, installation int64
	lastDelivery        string
	sourceNamespace     string
	sourceRepository    string
}

func (c *commentSteps) register(sc *godog.ScenarioContext) {
	c.fixture = &deploymentSteps{w: c.w, github: c.github, gitlab: c.gitlab}
	c.created = map[int]time.Time{}
	sc.Step(`^a "(GitHub|GitLab|Bitbucket Cloud|Bitbucket DC|Azure DevOps)" plan-comment stack "([^"]+)" using "(terraform|opentofu)" with labels '([^']*)'$`, c.stack)
	sc.Step(`^the comment fixture sends shared, moved and closed requests through ([AB])$`, c.requests)
	sc.Step(`^the comment fixture opens request (\d+) at the current head through ([AB])$`, c.open)
	sc.Step(`^comment request (\d+) moves to another real commit through ([AB])$`, c.move)
	sc.Step(`^the comment API loses its next successful create response$`, c.loseCreate)
	sc.Step(`^a real "(PROPOSED|TRACKED)" plan of comment stack "([^"]+)" is run as "(\w+)" with "(masked|oversized|plain)" output through ([AB])$`, c.run)
	sc.Step(`^comment events for run "(\w+)" have settled$`, c.settled)
	sc.Step(`^a comment report for run "(\w+)" recovered after a lost create$`, c.retried)
	sc.Step(`^run "(\w+)" has one masked native plan comment on each request "([^"]+)"$`, c.masked)
	sc.Step(`^run "(\w+)" has a native plan comment result "(FINISHED|UNCONFIRMED)" on request (\d+)$`, c.native)
	sc.Step(`^comment request (\d+) has (\d+) comments and its previous plan is "(outdated|unchanged)"$`, c.previous)
	sc.Step(`^run "(\w+)" has no native comment on request (\d+)$`, c.noRunComment)
	sc.Step(`^comment requests "([^"]+)" have no native comments$`, c.absent)
	sc.Step(`^unlabelled run "(\w+)" has real stored planning output and no native comment$`, c.silent)
	sc.Step(`^run "(\w+)" has an oversized native comment tail on request (\d+)$`, c.oversized)
}

func (c *commentSteps) stack(ctx context.Context, provider, slug, vendor, labels string) error {
	if c.fixture.provider == "" {
		c.fixture.provider = provider
		c.clock = time.Now().UTC().Truncate(time.Second)
		body := map[string]any{"customInput": map[string]any{"name": "Plan comments", "spaceID": "acme", "isDefault": true}}
		path := "/api/v1/gitlab-integrations"
		switch provider {
		case "GitHub":
			c.fixture.namespace = "acme"
			app := c.github.fake.AddApp("plan-comments", "acme")
			c.appID = app.ID
			c.installation = c.github.fake.Install(app.ID, "acme", "Organization", "infra")
			var err error
			c.sender, err = c.github.fake.Sender(app.ID)
			if err != nil {
				return fmt.Errorf("comment sender: %w", err)
			}
			body["apiHost"], body["appID"], body["privateKey"], body["webhookSecret"] = c.github.srv.URL, strconv.FormatInt(app.ID, 10), string(app.PrivateKey), app.WebhookSecret
			if c.github.proxy != nil {
				body["apiHost"], body["userFacingHost"] = "https://api.github.com", "https://github.com"
			}
			path = "/api/v1/github-enterprise-integrations"
			repo := c.github.fake.Repo(c.fixture.namespace, "infra")
			c.w.IDs["commentBase"] = repo.Commit("main", "comment base", deploymentFiles("base"))
			c.w.IDs["seed"] = repo.Commit("main", "comment seed", deploymentFiles("one"))
			for _, branch := range []string{"feature", "other", "target-12", "target-13", "target-14", "target-15"} {
				head := c.w.IDs["commentBase"]
				if branch == "feature" || branch == "other" {
					head = c.w.IDs["seed"]
				}
				repo.Branch(branch, head)
			}
			c.w.IDs["commentMoved"] = repo.Commit("other", "different request head", deploymentFiles("two"))
		case "Bitbucket Cloud", "Bitbucket DC", "Azure DevOps":
			var err error
			path, body, err = c.seedPeer(provider)
			if err != nil {
				return err
			}
		case "GitLab":
			c.fixture.namespace = "acme/platform"
			token := c.gitlab.fake.AddToken("comments", "api")
			c.gitlab.fake.AddMember(c.fixture.namespace, "infra", "comments", gitlabfake.Maintainer)
			c.gitlab.fake.AddMember(c.fixture.namespace, "infra", "creator", gitlabfake.Developer)
			c.gitlab.fake.AddMember(c.fixture.namespace, "infra", "reviewer", gitlabfake.Developer)
			body["apiHost"], body["privateToken"] = c.gitlab.srv.URL, token
			repo := c.gitlab.fake.Repo(c.fixture.namespace, "infra")
			c.w.IDs["commentBase"] = repo.Commit("main", "comment base", deploymentFiles("base"))
			c.w.IDs["seed"] = repo.Commit("main", "comment seed", deploymentFiles("one"))
			for _, branch := range []string{"feature", "other", "target-12", "target-13", "target-14", "target-15"} {
				head := c.w.IDs["commentBase"]
				if branch == "feature" || branch == "other" {
					head = c.w.IDs["seed"]
				}
				repo.Branch(branch, head)
			}
			c.w.IDs["commentMoved"] = repo.Commit("other", "different request head", deploymentFiles("two"))
		}
		response, err := c.w.Do(ctx, "A", "h", http.MethodPost, path, body)
		if err != nil {
			return err
		}
		var answer struct {
			ID       string `json:"id"`
			Secret   string `json:"webhookSecret"`
			Password string `json:"webhookPassword"`
		}
		if response.Status != http.StatusCreated || json.Unmarshal(response.Body, &answer) != nil || answer.ID == "" {
			return fmt.Errorf("%w: comment integration create answered %d without identity", harness.ErrStep, response.Status)
		}
		c.fixture.integration = answer.ID
		if provider != "GitHub" {
			if provider == "Azure DevOps" {
				answer.Secret = answer.Password
			}
			if answer.Secret == "" {
				return fmt.Errorf("%w: GitLab integration has no webhook token", harness.ErrStep)
			}
			c.secret = crypt.NewValue([]byte(answer.Secret))
		}
	}
	return c.fixture.stack(ctx, provider, slug, vendor, labels)
}

func (c *commentSteps) requests(ctx context.Context, replica string) error {
	for _, id := range []int{12, 13, 14, 15} {
		if err := c.sendRequest(ctx, id, "open", "seed", replica); err != nil {
			return err
		}
	}
	if err := c.sendRequest(ctx, 14, "move", "commentMoved", replica); err != nil {
		return err
	}
	if err := c.sendRequest(ctx, 15, "close", "seed", replica); err != nil {
		return err
	}
	return c.eligible(ctx, []string{"12", "13"}, replica)
}

func (c *commentSteps) open(ctx context.Context, id int, replica string) error {
	if err := c.sendRequest(ctx, id, "open", "seed", replica); err != nil {
		return err
	}
	return c.eligible(ctx, []string{strconv.Itoa(id)}, replica)
}

func (c *commentSteps) move(ctx context.Context, id int, replica string) error {
	if err := c.sendRequest(ctx, id, "move", "commentMoved", replica); err != nil {
		return err
	}
	return c.eligible(ctx, []string{"13"}, replica)
}

func (c *commentSteps) sendRequest(ctx context.Context, id int, action, commit, replica string) error {
	created, known := c.created[id]
	if !known {
		created = c.clock
		c.created[id] = created
	}
	c.clock = c.clock.Add(time.Second)
	deliveryID := kernel.NewULID().UUID()
	c.lastDelivery = deliveryID
	branch := "feature"
	if action == "move" {
		branch = "other"
	}
	var status int
	var err error
	switch c.fixture.provider {
	case "GitHub":
		nativeAction := "opened"
		switch action {
		case "move":
			nativeAction = "synchronize"
		case "close":
			nativeAction = "closed"
		}
		raw, buildErr := githubfake.Payload("pull_request", nativeAction)
		if buildErr != nil {
			return fmt.Errorf("comment request payload: %w", buildErr)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			return fmt.Errorf("comment request JSON: %w", err)
		}
		repository, repoErr := c.github.fake.RepositoryPayload(c.fixture.namespace, "infra")
		if repoErr != nil {
			return fmt.Errorf("comment native repository: %w", repoErr)
		}
		installation, installErr := c.github.fake.InstallationPayload(c.installation)
		if installErr != nil {
			return fmt.Errorf("comment native installation: %w", installErr)
		}
		var nativeInstallation map[string]any
		if err := json.Unmarshal(installation, &nativeInstallation); err != nil {
			return fmt.Errorf("comment installation JSON: %w", err)
		}
		nativeInstallation["app_id"] = c.appID
		body["repository"], body["installation"] = repository, nativeInstallation
		request, ok := body["pull_request"].(map[string]any)
		if !ok {
			return fmt.Errorf("%w: no native pull request fixture", harness.ErrStep)
		}
		request["number"], body["number"] = id, id
		request["created_at"], request["updated_at"] = created.Format(time.RFC3339), c.clock.Format(time.RFC3339)
		source := repository
		if c.sourceNamespace != "" {
			source, repoErr = c.github.fake.RepositoryPayload(c.sourceNamespace, c.sourceRepository)
			if repoErr != nil {
				return fmt.Errorf("comment native source repository: %w", repoErr)
			}
		}
		request["head"] = map[string]any{"ref": branch, "sha": c.w.IDs[commit], "repo": source}
		request["base"] = map[string]any{"ref": "target-" + strconv.Itoa(id), "sha": c.w.IDs["commentBase"], "repo": body["repository"]}
		request["html_url"] = fmt.Sprintf("https://github.example.com/acme/infra/pull/%d", id)
		native, nativeErr := json.Marshal(request)
		if nativeErr != nil {
			return fmt.Errorf("native GitHub request encode: %w", nativeErr)
		}
		if err := c.github.fake.SetPullRequest(c.fixture.namespace, "infra", id, native); err != nil {
			return fmt.Errorf("native GitHub request fixture: %w", err)
		}
		encoded, encodeErr := json.Marshal(body)
		if encodeErr != nil {
			return fmt.Errorf("comment request encode: %w", encodeErr)
		}
		status, err = c.sender.Send(ctx, c.w.Base(replica)+"/webhooks/vcs/github_enterprise/"+c.fixture.integration, deliveryID, "pull_request", encoded)
	case "Bitbucket Cloud", "Bitbucket DC", "Azure DevOps":
		status, err = c.sendPeerRequest(ctx, deliveryID, id, action, branch, commit, created, replica)
	case "GitLab":
		nativeAction := "open"
		switch action {
		case "move":
			nativeAction = "update"
		case "close":
			nativeAction = "close"
		}
		delivery, buildErr := c.gitlab.fake.MergeRequestWebhook(c.fixture.namespace, "infra", gitlabfake.WebhookMergeRequest{
			IID:              id,
			Action:           nativeAction,
			SourceBranch:     branch,
			TargetBranch:     "target-" + strconv.Itoa(id),
			Head:             c.w.IDs[commit],
			Author:           "creator",
			Actor:            "reviewer",
			Title:            fmt.Sprintf("Comment request %d", id),
			CreatedAt:        created,
			UpdatedAt:        c.clock,
			SourceNamespace:  c.sourceNamespace,
			SourceRepository: c.sourceRepository,
		})
		if buildErr != nil {
			return fmt.Errorf("comment merge request fixture: %w", buildErr)
		}
		delivery.ID = deliveryID
		sender := gitlabfake.NewWebhookSender(&http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
		status, err = sender.Send(ctx, c.w.Base(replica)+"/webhooks/vcs/gitlab/"+c.fixture.integration, c.secret.Reveal(), delivery)
	}
	if err != nil {
		return fmt.Errorf("comment request delivery: %w", err)
	}
	if status != http.StatusAccepted {
		return fmt.Errorf("%w: signed comment request delivery answered %d", harness.ErrStep, status)
	}
	return deps.Eventually(ctx, "comment pull request delivery completion", func(ctx context.Context) (bool, error) {
		var dispatched bool
		err := c.w.Row(
			ctx,
			`SELECT state='dispatched' FROM vcs_deliveries WHERE account_id=$1 AND integration_id=$2 AND delivery_id=$3`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(c.fixture.integration), deliveryID},
			&dispatched,
		)
		return dispatched, err
	})
}

func (c *commentSteps) eligible(ctx context.Context, want []string, _ string) error {
	return deps.Eventually(ctx, "processed current comment request heads", func(ctx context.Context) (bool, error) {
		var raw []byte
		err := c.w.Row(ctx, `SELECT coalesce(jsonb_agg(provider_id ORDER BY provider_id),'[]'::jsonb)
   FROM pull_requests WHERE account_id=$1 AND integration_id=$2 AND namespace=$3 AND repository='infra' AND state='open' AND head_sha=$4`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(c.fixture.integration), c.fixture.namespace, c.w.IDs["seed"]}, &raw)
		if err != nil {
			return false, err
		}
		var got []string
		if json.Unmarshal(raw, &got) != nil {
			return false, fmt.Errorf("%w: invalid retained comment request identities", harness.ErrStep)
		}
		return slices.Equal(got, want), nil
	})
}

func (c *commentSteps) loseCreate() {
	switch c.fixture.provider {
	case "GitHub":
		c.github.fake.FailNextCommentCreate()
	case "GitLab":
		c.gitlab.fake.FailNextCommentCreate()
	default:
		c.losePeerComment()
	}
}

const commentSecret = "private-plan-comment-secret-549"

func (c *commentSteps) run(ctx context.Context, kind, stack, name, output, replica string) error {
	hook := "printf 'comment-proof-549\\n'"
	switch output {
	case "masked":
		response, err := c.w.Do(
			ctx,
			replica,
			"h",
			http.MethodPost,
			"/api/v1/stacks/"+stack+"/configs",
			map[string]any{"id": "COMMENT_SECRET", "type": "ENVIRONMENT_VARIABLE", "value": commentSecret, "writeOnly": true},
		)
		if err != nil {
			return err
		}
		if response.Status != http.StatusCreated {
			return fmt.Errorf("%w: private planning variable create answered %d", harness.ErrStep, response.Status)
		}
		hook = `printf 'comment-mask=%s\n' "$COMMENT_SECRET"`
	case "oversized":
		hook = `printf 'comment-head-549\n'; i=0; while [ "$i" -lt 20000 ]; do printf 'comment-line-%05d-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789\n' "$i"; i=$((i+1)); done; printf 'comment-tail-549\n'`
	}
	response, err := c.w.Do(
		ctx,
		replica,
		"h",
		http.MethodPost,
		"/api/v1/stacks/"+stack+"/runs",
		map[string]any{"commitSha": c.w.IDs["seed"], "runType": kind, "runtimeConfig": map[string]any{"beforePlan": []string{hook}}},
	)
	if err != nil {
		return err
	}
	var answer struct {
		ID string `json:"id"`
	}
	if response.Status != http.StatusOK || json.Unmarshal(response.Body, &answer) != nil || answer.ID == "" {
		return fmt.Errorf("%w: real comment plan create answered %d", harness.ErrStep, response.Status)
	}
	c.w.IDs[name] = answer.ID
	return nil
}

// Negative comment assertions follow actual sink consumption and settled jobs;
// no timing delay or direct event insertion can substitute for this boundary.
func (c *commentSteps) settled(ctx context.Context, run string) error {
	return deps.Eventually(ctx, "comment event processing for "+run, func(ctx context.Context) (bool, error) {
		var events, pending int
		var consumed bool
		err := c.w.Row(ctx, `SELECT count(*),coalesce(bool_and(e.fanned_out_at IS NOT NULL AND EXISTS(
   SELECT 1 FROM event_consumptions c WHERE c.account_id=e.account_id AND c.event_id=e.id AND c.consumer='events.sinks')),false),
   (SELECT count(*) FROM jobs j WHERE j.account_id=$1 AND j.state<>'succeeded' AND
    ((j.kind='events.consume' AND j.payload->>'consumer'='events.sinks') OR j.kind LIKE 'vcs.report_%'))
   FROM events e WHERE e.account_id=$1 AND e.kind='run_updated' AND e.subject_kind='run' AND e.subject_id=$2`, []any{harness.UUIDOf(harness.Home.Account), c.w.IDs[run]}, &events, &consumed, &pending)
		return events > 0 && consumed && pending == 0, err
	})
}

func (c *commentSteps) retried(ctx context.Context, run string) error {
	var count int
	if err := c.w.Row(ctx, `SELECT count(*) FROM jobs j JOIN vcs_pr_comments c
  ON c.account_id=j.account_id AND j.payload->>'id'=c.binding->>'id'
  WHERE c.account_id=$1 AND c.run_id=$2 AND j.kind='vcs.report_comment' AND j.state='succeeded' AND j.attempt>=2
  AND c.reported_revision=c.revision AND c.reference->>'id'<>''`, []any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(c.w.IDs[run])}, &count); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("%w: no completed comment job recovered the uncertain native create", harness.ErrStep)
	}
	return nil
}

type nativePlanComment struct {
	ThreadID  int64
	ID        int64
	Body      string
	Minimized bool
}

func (c *commentSteps) records(pr string) []nativePlanComment {
	var out []nativePlanComment
	switch c.fixture.provider {
	case "GitHub":
		for _, record := range c.github.fake.Comments(c.fixture.namespace, "infra", pr) {
			out = append(out, nativePlanComment{ID: record.ID, Body: record.Body, Minimized: record.Minimized})
		}
	case "Bitbucket Cloud", "Bitbucket DC", "Azure DevOps":
		return c.peerComments(pr)
	case "GitLab":
		for _, record := range c.gitlab.fake.Comments(c.fixture.namespace, "infra", pr) {
			out = append(out, nativePlanComment{ID: record.ID, Body: record.Body, Minimized: record.Minimized})
		}
	}
	return out
}

func (c *commentSteps) comment(run, pr string) (nativePlanComment, error) {
	marker := "<!-- infraweave:plan:" + c.w.IDs[run] + " -->"
	var found []nativePlanComment
	for _, record := range c.records(pr) {
		if strings.HasSuffix(record.Body, "\n\n"+marker) {
			found = append(found, record)
		}
	}
	if len(found) != 1 || found[0].ID <= 0 || c.fixture.provider == "Azure DevOps" && found[0].ThreadID <= 0 {
		return nativePlanComment{}, fmt.Errorf("%w: request %s has %d native comments for run %s", harness.ErrStep, pr, len(found), run)
	}
	body := found[0].Body
	wantDetails := c.fixture.provider == "GitHub" || c.fixture.provider == "GitLab"
	if !strings.HasPrefix(body, "### [infra](") || !strings.Contains(body, "/api/v1/stacks/infra)") || !strings.Contains(body, "/api/v1/runs/"+c.w.IDs[run]+")") ||
		strings.Contains(body, "<details>\n<summary>Planning log</summary>") != wantDetails || !strings.Contains(body, "```text\n") ||
		!strings.Contains(body, "Plan:") {
		return nativePlanComment{}, fmt.Errorf("%w: native plan comment lacks heading, links, provider-supported stored tool plan or footer", harness.ErrStep)
	}
	return found[0], nil
}

func (c *commentSteps) masked(ctx context.Context, run, requests string) error {
	text, err := c.log(ctx, run)
	if err != nil {
		return err
	}
	masked := "comment-mask=*****"
	if !strings.Contains(text, masked) || strings.Contains(text, commentSecret) {
		return fmt.Errorf("%w: real stored planning log did not preserve worker masking", harness.ErrStep)
	}
	for _, request := range strings.Split(requests, ", ") {
		comment, err := c.comment(run, request)
		if err != nil {
			return err
		}
		if !strings.Contains(comment.Body, masked) || strings.Contains(comment.Body, commentSecret) {
			return fmt.Errorf("%w: native comment did not preserve worker-masked text", harness.ErrStep)
		}
	}
	for _, server := range c.w.Servers {
		if strings.Contains(string(server.Stdout()), commentSecret) {
			return fmt.Errorf("%w: planning secret reached a server log", harness.ErrStep)
		}
	}
	return nil
}

func (c *commentSteps) native(run, state string, pr int) error {
	record, err := c.comment(run, strconv.Itoa(pr))
	if err != nil {
		return err
	}
	if !strings.Contains(record.Body, ": "+state+"\n\n") {
		return fmt.Errorf("%w: native heading lost the exact plan result", harness.ErrStep)
	}
	return nil
}

func (c *commentSteps) previous(pr, count int, state string) error {
	records := c.records(strconv.Itoa(pr))
	wantMinimized := state == "outdated"
	if len(records) != count || count < 2 || (records[0].ID == records[1].ID && records[0].ThreadID == records[1].ThreadID) || records[0].Minimized != wantMinimized ||
		records[count-1].Minimized {
		return fmt.Errorf("%w: request %d new-comment count or previous native minimization differs", harness.ErrStep, pr)
	}
	if c.fixture.provider == "GitHub" && wantMinimized && len(c.github.fake.CommentMinimizeAttempts(c.fixture.namespace, "infra", strconv.Itoa(pr))) != 1 {
		return fmt.Errorf("%w: previous comment minimization was not one native mutation", harness.ErrStep)
	}
	return nil
}

func (c *commentSteps) noRunComment(run string, pr int) error {
	records := c.records(strconv.Itoa(pr))
	marker := "<!-- infraweave:plan:" + c.w.IDs[run] + " -->"
	if len(records) != 1 {
		return fmt.Errorf("%w: moved request did not retain only its first comment", harness.ErrStep)
	}
	for _, record := range records {
		if strings.HasSuffix(record.Body, marker) {
			return fmt.Errorf("%w: moved request received the later run's comment", harness.ErrStep)
		}
	}
	return nil
}

func (c *commentSteps) absent(requests string) error {
	for _, request := range strings.Split(requests, ", ") {
		if len(c.records(request)) != 0 {
			return fmt.Errorf("%w: moved/closed request %s received a comment", harness.ErrStep, request)
		}
	}
	return nil
}

func (c *commentSteps) silent(ctx context.Context, run string) error {
	text, err := c.log(ctx, run)
	if err != nil {
		return err
	}
	if !strings.Contains(text, "Plan:") || !strings.Contains(text, "comment-proof-549") {
		return fmt.Errorf("%w: unlabelled run has no stored real plan", harness.ErrStep)
	}
	marker := "<!-- infraweave:plan:" + c.w.IDs[run] + " -->"
	for _, request := range []string{"12", "13", "14", "15"} {
		for _, comment := range c.records(request) {
			if strings.HasSuffix(comment.Body, marker) {
				return fmt.Errorf("%w: unlabelled run posted a comment", harness.ErrStep)
			}
		}
	}
	var count int
	if err := c.w.Row(
		ctx,
		`SELECT count(*) FROM vcs_pr_comments WHERE account_id=$1 AND run_id=$2`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(c.w.IDs[run])},
		&count,
	); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("%w: unlabelled run reserved comment metadata", harness.ErrStep)
	}
	return nil
}

func (c *commentSteps) log(ctx context.Context, run string) (string, error) {
	var text strings.Builder
	next := ""
	for range 64 {
		path := "/api/v1/runs/" + c.w.IDs[run] + "/logs?state=PLANNING&sanitize=false"
		if next != "" {
			path += "&token=" + url.QueryEscape(next)
		}
		response, err := c.w.Do(ctx, "B", "h", http.MethodGet, path, nil)
		if err != nil {
			return "", err
		}
		var page api.LogsResponse
		if response.Status != http.StatusOK || json.Unmarshal(response.Body, &page) != nil || !page.Exists || page.Expired || page.Deleted {
			return "", fmt.Errorf("%w: stored planning log unavailable", harness.ErrStep)
		}
		for _, message := range page.Messages {
			text.WriteString(message.Message)
			text.WriteByte('\n')
		}
		if page.Finished {
			return text.String(), nil
		}
		if page.NextToken == nil || *page.NextToken == "" {
			return "", fmt.Errorf("%w: planning page has no continuation", harness.ErrStep)
		}
		next = *page.NextToken
	}
	return "", fmt.Errorf("%w: stored planning log exceeded page bound", harness.ErrStep)
}

var omittedCommentLines = regexp.MustCompile(`(?m)^([0-9]+) lines omitted(?:; first shown line shortened)?\. \[Full planning log\]\(`)

// rawLog reads the served compacted object because paged messages reconstruct
// newlines rather than preserving every original chunk byte.
func (c *commentSteps) rawLog(ctx context.Context, run string) (string, error) {
	var signed string
	if err := deps.Eventually(ctx, "the exact planning log download", func(ctx context.Context) (bool, error) {
		response, err := c.w.Do(ctx, "B", "h", http.MethodGet,
			"/api/v1/runs/"+c.w.IDs[run]+"/logs-download-url?state=PLANNING", nil)
		if err != nil {
			return false, err
		}
		var download api.LogsDownloadURL
		if response.Status != http.StatusOK || json.Unmarshal(response.Body, &download) != nil || download.Deleted || download.Expired {
			return false, fmt.Errorf("%w: completed planning log download unavailable", harness.ErrStep)
		}
		if download.Url == nil || *download.Url == "" {
			if download.Ongoing {
				return false, nil
			}
			return false, fmt.Errorf("%w: completed planning log has no download URL", harness.ErrStep)
		}
		signed = *download.Url
		return true, nil
	}); err != nil {
		return "", fmt.Errorf("%w: await planning log download: %w", harness.ErrStep, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	if err != nil {
		return "", fmt.Errorf("%w: construct planning log download request", harness.ErrStep)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: download planning log request failed", harness.ErrStep)
	}
	defer response.Body.Close()
	const bound = 8 << 20
	text, err := io.ReadAll(io.LimitReader(response.Body, bound+1))
	if err != nil {
		return "", fmt.Errorf("%w: read planning log download failed", harness.ErrStep)
	}
	if response.StatusCode != http.StatusOK || len(text) > bound {
		return "", fmt.Errorf("%w: planning log download answered %d, bytes %d", harness.ErrStep, response.StatusCode, len(text))
	}
	return string(text), nil
}

func (c *commentSteps) oversized(ctx context.Context, run string, pr int) error {
	record, err := c.comment(run, strconv.Itoa(pr))
	if err != nil {
		return err
	}
	stored, err := c.rawLog(ctx, run)
	if err != nil {
		return err
	}
	// The native GitLab API documents one million characters; GitHub's adapter
	// deliberately declares a conservative client budget of 60,000 UTF-8 bytes because
	// its REST comment schema does not specify a numeric native bound.
	fits := len(record.Body) <= 60_000
	tooLong := len(stored) > 60_000
	if c.fixture.provider == "GitLab" {
		fits = utf8.RuneCountInString(record.Body) <= 1_000_000
		tooLong = utf8.RuneCountInString(stored) > 1_000_000
	}
	matches := omittedCommentLines.FindStringSubmatch(record.Body)
	if !fits || !tooLong || len(matches) != 2 || strings.Contains(record.Body, "comment-head-549") || !strings.Contains(record.Body, "comment-tail-549") ||
		!strings.Contains(record.Body, "/logs-download-url?state=PLANNING&stateVersion=") {
		return fmt.Errorf("%w: oversized native body lacks bounded tail, exact footer, omission notice or full-log link", harness.ErrStep)
	}
	count, err := strconv.Atoi(matches[1])
	if err != nil || count <= 0 {
		return fmt.Errorf("%w: oversized body reports no cut lines", harness.ErrStep)
	}
	start := strings.Index(record.Body, "\n\n```text\n")
	if start < 0 {
		return fmt.Errorf("%w: collapsed planning log has no fenced text", harness.ErrStep)
	}
	start += len("\n\n```text\n")
	end := strings.LastIndex(record.Body, "\n```\n\n</details>")
	if end < start {
		return fmt.Errorf("%w: collapsed planning log has no closing fence", harness.ErrStep)
	}
	shown := record.Body[start:end]
	if !strings.HasSuffix(stored, shown) || strings.Count(stored[:len(stored)-len(shown)], "\n") != count {
		return fmt.Errorf("%w: comment tail or omitted-line count differs from the served stored log", harness.ErrStep)
	}
	return nil
}
