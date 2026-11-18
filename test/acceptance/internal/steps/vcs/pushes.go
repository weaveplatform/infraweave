package vcs

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

type pushSteps struct {
	w      *harness.World
	gitlab *gitlabSteps
	hooks  *gitlabWebhookSteps
}

func (s *pushSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the GitLab project "([^"]+)" commits files on "([^"]+)" as "(\w+)":$`, s.commit)
	sc.Step(`^the GitLab project "([^"]+)" branches "([^"]+)" from "(\w+)"$`, s.branch)
	sc.Step(`^GitLab pushes "(\w+)" for integration "(\w+)" from "(\w+)" to "(\w+)" on "([^"]+)" through ([AB])$`, s.send)
	sc.Step(`^GitLab push "(\w+)" for integration "(\w+)" settles with (\d+) runs and (\d+) stack jobs$`, s.settled)
	sc.Step(`^the "(TRACKED|PROPOSED)" event run on stack "([\w-]+)" for commit "(\w+)" is kept as "(\w+)"$`, s.keepRun)
	sc.Step(`^the only "(TRACKED|PROPOSED)" event run for commit "(\w+)" is kept as "(\w+)"$`, s.keepAny)
	sc.Step(`^commit "(\w+)" has exactly (\d+) event runs across all stacks$`, s.totalRuns)
	sc.Step(`^all event runs for commit "(\w+)" finish through ([AB])$`, s.finished)
	sc.Step(`^the event run "(\w+)" has a real tool plan through ([AB])$`, s.planningEvidence)
	sc.Step(`^stack "([\w-]+)" has exactly (\d+) event runs for commit "(\w+)"$`, s.runCount)
}

func (s *pushSteps) planningEvidence(ctx context.Context, run, replica string) error {
	next := ""
	found := false
	for range 100 {
		path := "/api/v1/runs/" + s.w.IDs[run] + "/logs?sanitize=true&state=PLANNING"
		if next != "" {
			path += "&token=" + url.QueryEscape(next)
		}
		if err := s.w.SendAs(ctx, `token "h"`, http.MethodGet, path, "", replica); err != nil {
			return err
		}
		var page struct {
			Messages []struct {
				Message string `json:"message"`
			} `json:"messages"`
			Next     string `json:"nextToken"`
			Finished bool   `json:"finished"`
		}
		if s.w.Last().Status != http.StatusOK || json.Unmarshal(s.w.Last().Body, &page) != nil {
			return fmt.Errorf("%w: event planning log was not readable", harness.ErrStep)
		}
		for _, message := range page.Messages {
			for line := range strings.SplitSeq(message.Message, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "Plan:") || strings.HasPrefix(line, "No changes.") {
					fmt.Printf("Event planning evidence: %s\n", line)
					found = true
				}
			}
		}
		if page.Finished {
			if !found {
				return fmt.Errorf("%w: event planning log has no tool plan", harness.ErrStep)
			}
			return nil
		}
		next = page.Next
	}
	return fmt.Errorf("%w: event planning log exceeded page bound", harness.ErrStep)
}

func (s *pushSteps) commit(path, branch, key string, table *godog.Table) error {
	namespace, name, err := s.gitlab.split(path)
	if err != nil {
		return err
	}
	files := map[string]string{}
	for _, row := range table.Rows {
		if len(row.Cells) != 2 {
			return fmt.Errorf("%w: GitLab commit needs path/content pairs", harness.ErrStep)
		}
		files[row.Cells[0].Value] = s.w.Resolve(strings.ReplaceAll(row.Cells[1].Value, `\n`, "\n"))
	}
	s.w.IDs[key] = s.gitlab.fake.Repo(namespace, name).Commit(branch, key, files)
	return nil
}

func (s *pushSteps) branch(path, branch, from string) error {
	namespace, name, err := s.gitlab.split(path)
	if err != nil {
		return err
	}
	sha, err := s.commitHash(from)
	if err != nil {
		return err
	}
	s.gitlab.fake.Repo(namespace, name).Branch(branch, sha)
	return nil
}

func (s *pushSteps) commitHash(key string) (string, error) {
	if key == "zero" {
		return strings.Repeat("0", 40), nil
	}
	value := s.w.IDs[key]
	if decoded, err := hex.DecodeString(value); err != nil || len(decoded) != 20 {
		return "", fmt.Errorf("%w: no commit named %s", harness.ErrStep, key)
	}
	return value, nil
}

func (s *pushSteps) send(ctx context.Context, name, integration, before, head, branch, replica string) error {
	base, err := s.commitHash(before)
	if err != nil {
		return err
	}
	target, err := s.commitHash(head)
	if err != nil {
		return err
	}
	delivery, err := s.gitlab.fake.PushWebhook("acme/platform", "infra", branch, base, target)
	if err != nil {
		return fmt.Errorf("GitLab push fixture: %w", err)
	}
	delivery.ID = kernel.NewULID().UUID()
	s.hooks.deliveries[name] = delivery
	return s.hooks.sendDelivery(ctx, integration, replica, s.hooks.secret.Reveal(), delivery)
}

func (s *pushSteps) settled(ctx context.Context, name, integration string, runs, children int) error {
	delivery, ok := s.hooks.deliveries[name]
	if !ok {
		return fmt.Errorf("%w: no push named %s", harness.ErrStep, name)
	}
	id, err := kernel.ParseULID(s.w.IDs[integration])
	if err != nil {
		return fmt.Errorf("%w: invalid integration", harness.ErrStep)
	}
	db, err := s.w.Querier(ctx)
	if err != nil {
		return err
	}
	return deps.Eventually(ctx, "push dispatch and every stack evaluation", func(ctx context.Context) (bool, error) {
		query := fmt.Sprintf(`SELECT coalesce(jsonb_agg(jsonb_build_object('state',d.state,'runs',d.runs_created,
			'id',d.id::text,'jobs',(SELECT coalesce(jsonb_agg(jsonb_build_object('delivery',j.payload->>'delivery','state',j.state)),'[]') FROM jobs j WHERE j.kind='vcs.evaluate_event' AND j.account_id=d.account_id))),'[]')::text
			FROM vcs_deliveries d WHERE d.integration_id='%s'::uuid AND d.delivery_id='%s'`, id.UUID(), delivery.ID)
		raw, err := db.Query(ctx, query)
		if err != nil {
			return false, err
		}
		var rows []struct {
			State string `json:"state"`
			Runs  int    `json:"runs"`
			ID    string `json:"id"`
			Jobs  []struct {
				Delivery kernel.ULID `json:"delivery"`
				State    string      `json:"state"`
			} `json:"jobs"`
		}
		if err := json.Unmarshal([]byte(raw), &rows); err != nil {
			return false, fmt.Errorf("push settlement: %w", err)
		}
		if len(rows) != 1 {
			return false, nil
		}
		got := rows[0]
		if got.State == "failed" {
			return false, fmt.Errorf("%w: push preparation failed", harness.ErrStep)
		}
		var internal kernel.ULID
		if err := internal.Scan(got.ID); err != nil {
			return false, fmt.Errorf("delivery identity: %w", err)
		}
		childCount, done := 0, 0
		for _, job := range got.Jobs {
			if job.Delivery == internal {
				childCount++
				if job.State == "succeeded" {
					done++
				}
			}
		}
		if got.State != "dispatched" || childCount != children || done != children {
			return false, nil
		}
		if got.Runs != runs {
			return false, fmt.Errorf("%w: delivery created %d runs, want %d", harness.ErrStep, got.Runs, runs)
		}
		fmt.Printf("Push evidence: refs retained, stack-jobs=%d succeeded, new-runs=%d\n", childCount, got.Runs)
		return true, nil
	})
}

func (s *pushSteps) matchingRuns(ctx context.Context, stack, commit, kind string) ([]string, error) {
	sha, err := s.commitHash(commit)
	if err != nil {
		return nil, err
	}
	account, err := kernel.ParseULID(harness.Home.Account)
	if err != nil {
		return nil, fmt.Errorf("home account: %w", err)
	}
	db, err := s.w.Querier(ctx)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT coalesce(jsonb_agg(r.id::text),'[]')::text FROM runs r JOIN stacks s ON s.account_id=r.account_id AND s.id=r.stack_id
		WHERE r.account_id='%s'::uuid AND r.commit->>'hash'='%s' AND r.triggered_by='vcs/commit'`, account.UUID(), sha)
	if stack != "" {
		query += " AND s.slug='" + strings.ReplaceAll(stack, "'", "''") + "'"
	}
	if kind != "" {
		query += " AND r.type='" + kind + "'"
	}
	raw, err := db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, fmt.Errorf("event run rows: %w", err)
	}
	return ids, nil
}

func (s *pushSteps) keepAny(ctx context.Context, kind, commit, key string) error {
	return s.keepRun(ctx, kind, "", commit, key)
}

func (s *pushSteps) totalRuns(ctx context.Context, commit string, count int) error {
	return s.runCount(ctx, "", count, commit)
}

func (s *pushSteps) finished(ctx context.Context, commit, replica string) error {
	ids, err := s.matchingRuns(ctx, "", commit, "")
	if err != nil {
		return err
	}
	for _, raw := range ids {
		var id kernel.ULID
		if err := id.Scan(raw); err != nil {
			return fmt.Errorf("event run identity: %w", err)
		}
		if err := deps.Eventually(ctx, "admitted event run to finish", func(ctx context.Context) (bool, error) {
			if err := s.w.SendAs(ctx, `token "h"`, "GET", "/api/v1/runs/"+id.String(), "", replica); err != nil {
				return false, err
			}
			var view struct {
				State string `json:"state"`
			}
			if s.w.Last().Status != 200 || json.Unmarshal(s.w.Last().Body, &view) != nil {
				return false, fmt.Errorf("%w: event run read failed", harness.ErrStep)
			}
			return view.State == "FINISHED", nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *pushSteps) keepRun(ctx context.Context, kind, stack, commit, key string) error {
	return deps.Eventually(ctx, "event run creation", func(ctx context.Context) (bool, error) {
		ids, err := s.matchingRuns(ctx, stack, commit, kind)
		if err != nil {
			return false, err
		}
		if len(ids) == 0 {
			return false, nil
		}
		if len(ids) != 1 {
			return false, fmt.Errorf("%w: event commit has %d runs", harness.ErrStep, len(ids))
		}
		var id kernel.ULID
		err = id.Scan(ids[0])
		if err != nil {
			return false, fmt.Errorf("event run ID: %w", err)
		}
		s.w.IDs[key] = id.String()
		return true, nil
	})
}

func (s *pushSteps) runCount(ctx context.Context, stack string, count int, commit string) error {
	ids, err := s.matchingRuns(ctx, stack, commit, "")
	if err != nil {
		return err
	}
	if len(ids) != count {
		return fmt.Errorf("%w: stack %s has %d event runs, want %d", harness.ErrStep, stack, len(ids), count)
	}
	return nil
}
