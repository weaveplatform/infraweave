package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type pullRequestSteps struct {
	w         *harness.World
	gitlab    *gitlabSteps
	hooks     *gitlabWebhookSteps
	snapshots map[string]api.PullRequest
	created   map[string]time.Time
	clock     time.Time
}

func (s *pullRequestSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^GitLab sends request snapshots for integration "(\w+)" through ([AB]):$`, s.snapshotsSent)
	sc.Step(`^stack "([\w-]+)" lists requests "([^"]*)" through ([AB]) with token "(\w+)"$`, s.listed)
	sc.Step(`^request "(\d+)" on stack "([\w-]+)" links runs "([^"]*)" through ([AB])$`, s.runs)
	sc.Step(`^stack "([\w-]+)" has the same requests through direct, list and search with token "(\w+)"$`, s.allViews)
	sc.Step(`^GitLab opens (\d+) requests on "([^"]+)" at commit "(\w+)" for integration "(\w+)" through ([AB])$`, s.many)
	sc.Step(`^stack "([\w-]+)" lists the newest (\d+) of (\d+) eligible requests through ([AB])$`, s.cap)
}

func (s *pullRequestSteps) snapshotsSent(ctx context.Context, integration, replica string, table *godog.Table) error {
	for _, row := range table.Rows[1:] {
		if len(row.Cells) != 7 {
			return fmt.Errorf("%w: request table needs id/action/project/head/base/commit/draft", harness.ErrStep)
		}
		id, err := strconv.Atoi(row.Cells[0].Value)
		if err != nil {
			return fmt.Errorf("request ID: %w", err)
		}
		draft, err := strconv.ParseBool(row.Cells[6].Value)
		if err != nil {
			return fmt.Errorf("request draft: %w", err)
		}
		if err := s.send(ctx, integration, replica, id, row.Cells[1].Value, row.Cells[2].Value, row.Cells[3].Value, row.Cells[4].Value, row.Cells[5].Value, draft); err != nil {
			return err
		}
	}
	return nil
}

func (s *pullRequestSteps) send(ctx context.Context, integration, replica string, id int, action, project, branch, base, commit string, draft bool) error {
	if s.snapshots == nil {
		s.snapshots = map[string]api.PullRequest{}
		s.created = map[string]time.Time{}
		s.clock = time.Now().UTC().Truncate(time.Second)
	}
	namespace, name, err := s.gitlab.split(project)
	if err != nil {
		return err
	}
	head := s.w.IDs[commit]
	if len(head) != 40 {
		return fmt.Errorf("%w: request has no commit %s", harness.ErrStep, commit)
	}
	key := project + "/" + strconv.Itoa(id)
	created, known := s.created[key]
	if !known {
		created = s.clock
		s.created[key] = created
	}
	s.clock = s.clock.Add(time.Second)
	title := fmt.Sprintf("Request %d %s", id, action)
	delivery, err := s.gitlab.fake.MergeRequestWebhook(
		namespace,
		name,
		gitlabfake.WebhookMergeRequest{
			IID:          id,
			Action:       action,
			SourceBranch: branch,
			TargetBranch: base,
			Head:         head,
			Author:       "creator",
			Actor:        "reviewer",
			Title:        title,
			Draft:        draft,
			CreatedAt:    created,
			UpdatedAt:    s.clock,
		},
	)
	if err != nil {
		return fmt.Errorf("request webhook fixture: %w", err)
	}
	delivery.ID = kernel.NewULID().UUID()
	s.hooks.deliveries[delivery.ID] = delivery
	if err := s.hooks.sendDelivery(ctx, integration, replica, s.hooks.secret.Reveal(), delivery); err != nil {
		return err
	}
	if s.w.Last().Status != http.StatusAccepted {
		return fmt.Errorf("%w: request delivery status %d", harness.ErrStep, s.w.Last().Status)
	}
	if err := s.hooks.dispatched(ctx, delivery.ID, integration); err != nil {
		return err
	}
	// The expected creator is deliberately different from the webhook actor.
	s.snapshots[strconv.Itoa(id)] = api.PullRequest{
		Id:            strconv.Itoa(id),
		AuthorLogin:   "creator",
		Title:         title,
		Url:           fmt.Sprintf("https://gitlab.example.com/%s/-/merge_requests/%d", project, id),
		HeadBranch:    branch,
		HeadBranchURL: "https://gitlab.example.com/" + project + "/-/tree/" + url.PathEscape(branch),
		HeadSHA:       head,
		Draft:         draft,
		CreatedAt:     int32(created.Unix()),
		UpdatedAt:     int32(s.clock.Unix()),
	} //nolint:gosec // fixture timestamps are within the API's int32 Unix range
	return nil
}

func (s *pullRequestSteps) read(ctx context.Context, stack, replica, token string) ([]api.PullRequest, error) {
	if err := s.w.SendAs(ctx, `token "`+token+`"`, http.MethodGet, "/api/v1/stacks/"+stack, "", replica); err != nil {
		return nil, err
	}
	var view api.Stack
	if s.w.Last().Status != http.StatusOK || json.Unmarshal(s.w.Last().Body, &view) != nil {
		return nil, fmt.Errorf("%w: stack request read: %d %s", harness.ErrStep, s.w.Last().Status, s.w.Last().Body)
	}
	return view.PullRequests, nil
}

func (s *pullRequestSteps) listed(ctx context.Context, stack, ids, replica, token string) error {
	requests, err := s.read(ctx, stack, replica, token)
	if err != nil {
		return err
	}
	var got []string
	for _, r := range requests {
		got = append(got, r.Id)
		expected, ok := s.snapshots[r.Id]
		if !ok {
			return fmt.Errorf("%w: unexpected request %s", harness.ErrStep, r.Id)
		}
		r.Runs = nil
		if !reflect.DeepEqual(r, expected) {
			return fmt.Errorf("%w: metadata lost creator/native time/URL/head: got %+v want %+v", harness.ErrStep, r, expected)
		}
	}
	if strings.Join(got, ", ") != ids {
		return fmt.Errorf("%w: requests got %v want %s", harness.ErrStep, got, ids)
	}
	fmt.Printf("Request evidence: stack=%s creator preserved; metadata/head/draft/native times match; ids=%v\n", stack, got)
	return nil
}

func (s *pullRequestSteps) runs(ctx context.Context, id, stack, names, replica string) error {
	requests, err := s.read(ctx, stack, replica, "h")
	if err != nil {
		return err
	}
	want := []api.RunRef{}
	if names != "" {
		for name := range strings.SplitSeq(names, ", ") {
			value, ok := s.w.IDs[name]
			if !ok {
				return fmt.Errorf("%w: unknown run %s", harness.ErrStep, name)
			}
			want = append(want, api.RunRef{Id: value})
		}
	}
	for _, r := range requests {
		if r.Id == id {
			if !reflect.DeepEqual(r.Runs, want) {
				return fmt.Errorf("%w: request %s links %+v want %+v", harness.ErrStep, id, r.Runs, want)
			}
			fmt.Printf("Request run evidence: request=%s current-head-first, historical/shared membership=%d\n", id, len(want))
			return nil
		}
	}
	return fmt.Errorf("%w: request %s missing", harness.ErrStep, id)
}

func (s *pullRequestSteps) allViews(ctx context.Context, stack, token string) error {
	want, err := s.read(ctx, stack, "A", token)
	if err != nil {
		return err
	}
	if err := s.w.SendAs(ctx, `token "`+token+`"`, http.MethodGet, "/api/v1/stacks?limit=100", "", "B"); err != nil {
		return err
	}
	var page api.StackPage
	if s.w.Last().Status != http.StatusOK || json.Unmarshal(s.w.Last().Body, &page) != nil {
		return fmt.Errorf("%w: stack list read failed", harness.ErrStep)
	}
	found := false
	for _, view := range page.Items {
		if view.Id == stack {
			found = true
			if !reflect.DeepEqual(view.PullRequests, want) {
				return fmt.Errorf("%w: list request projection differs", harness.ErrStep)
			}
		}
	}
	if !found {
		return fmt.Errorf("%w: stack absent from list", harness.ErrStep)
	}
	if err := s.w.SendAs(ctx, `token "`+token+`"`, http.MethodPost, "/api/v1/stacks/search", `{"first":100}`, "A"); err != nil {
		return err
	}
	var search api.SearchStacksOutput
	if s.w.Last().Status != http.StatusOK || json.Unmarshal(s.w.Last().Body, &search) != nil {
		return fmt.Errorf("%w: stack search read failed", harness.ErrStep)
	}
	for _, edge := range search.Edges {
		if edge.Node.Id == stack {
			if !reflect.DeepEqual(edge.Node.PullRequests, want) {
				return fmt.Errorf("%w: search request projection differs", harness.ErrStep)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: stack absent from search", harness.ErrStep)
}

func (s *pullRequestSteps) many(ctx context.Context, count int, project, commit, integration, replica string) error {
	for n := 1; n <= count; n++ {
		if err := s.send(ctx, integration, replica, n, "open", project, "feature", "main", commit, n%2 == 0); err != nil {
			return err
		}
	}
	return nil
}

func (s *pullRequestSteps) cap(ctx context.Context, stack string, limit, total int, replica string) error {
	var want []string
	for n := total; n > total-limit; n-- {
		want = append(want, strconv.Itoa(n))
	}
	return s.listed(ctx, stack, strings.Join(want, ", "), replica, "h")
}
