package vcs

import (
	"context"
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

type ignoredSteps struct {
	w      *harness.World
	gitlab *gitlabSteps
	hooks  *gitlabWebhookSteps
	stacks map[string]string
}

func (s *ignoredSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^GitLab tags commit "(\w+)" as "([^"]+)" in delivery "(\w+)" for integration "(\w+)" through ([AB])$`, s.tag)
	sc.Step(`^token "(\w+)" searches ignored triggers of stack "([^"]+)" with '([^']*)' through ([AB])$`, s.search)
	sc.Step(`^the ignored-trigger answer has (\d+) records for commit "(\w+)" with reason "([A-Z_]+)" and tag "([^"]*)"$`, s.answer)
	sc.Step(`^the ignored-trigger answer has (\d+) records and keeps cursor "(\w+)"$`, s.page)
	sc.Step(`^stack "([\w-]+)" has exactly (\d+) stored ignored triggers$`, s.stored)
	sc.Step(`^the deleted stack "([\w-]+)" has no ignored triggers$`, s.deleted)
	sc.Step(`^the home account has no retained ignored triggers$`, s.purged)
	sc.Step(`^the only capped tracked stack is kept as "(\w+)"$`, s.capped)
}

func (s *ignoredSteps) tag(ctx context.Context, commit, tag, name, integration, replica string) error {
	head := s.w.IDs[commit]
	if len(head) != 40 {
		return fmt.Errorf("%w: tag has no immutable commit", harness.ErrStep)
	}
	delivery, err := s.gitlab.fake.TagWebhook("acme/platform", "infra", tag, strings.Repeat("0", 40), head)
	if err != nil {
		return fmt.Errorf("tag fixture: %w", err)
	}
	delivery.ID = kernel.NewULID().UUID()
	s.hooks.deliveries[name] = delivery
	return s.hooks.sendDelivery(ctx, integration, replica, s.hooks.secret.Reveal(), delivery)
}

func (s *ignoredSteps) search(ctx context.Context, token, stack, input, replica string) error {
	input = s.w.Resolve(input)
	if !json.Valid([]byte(input)) {
		return fmt.Errorf("%w: invalid ignored-trigger search fixture", harness.ErrStep)
	}
	response, err := s.w.Do(ctx, replica, token, http.MethodGet, "/api/v1/stacks/"+s.w.Resolve(stack)+"/search-run-ignored-triggers?input="+url.QueryEscape(input), nil)
	if err != nil && response.Status == 0 {
		return err
	}
	return nil
}

type ignoredAnswer struct {
	Edges []struct {
		Cursor string `json:"cursor"`
		Node   struct {
			ID        string            `json:"id"`
			CreatedAt int64             `json:"createdAt"`
			Reason    string            `json:"reasonType"`
			Policies  []json.RawMessage `json:"policyAttachments"`
			Commit    struct {
				Hash       string `json:"hash"`
				Tag        string `json:"tag"`
				URL        string `json:"url"`
				Message    string `json:"message"`
				AuthorName string `json:"authorName"`
				Timestamp  int64  `json:"timestamp"`
			} `json:"commit"`
		} `json:"node"`
	} `json:"edges"`
}

func (s *ignoredSteps) answer(want int, commit, reason, tag string) error {
	var page ignoredAnswer
	response := s.w.Last()
	if response.Status != http.StatusOK || json.Unmarshal(response.Body, &page) != nil || len(page.Edges) != want {
		return fmt.Errorf("%w: ignored history count/status: %d %s", harness.ErrStep, response.Status, response.Body)
	}
	native, err := s.gitlab.fake.Repo("acme/platform", "infra").Resolve(s.w.IDs[commit])
	if err != nil {
		return fmt.Errorf("native ignored commit: %w", err)
	}
	subject, _, _ := strings.Cut(native.Message, "\n")
	for _, edge := range page.Edges {
		r := edge.Node
		c := r.Commit
		if r.ID == "" || r.CreatedAt == 0 || r.Reason != reason || r.Policies == nil || len(r.Policies) != 0 || c.Hash != s.w.IDs[commit] || c.Tag != tag || c.URL == "" ||
			!strings.Contains(c.URL, c.Hash) ||
			c.Message != subject ||
			c.AuthorName != native.Author.Name ||
			c.Timestamp != native.Author.When.Unix() {
			return fmt.Errorf("%w: ignored commit metadata/reason/policy projection mismatch: %s", harness.ErrStep, response.Body)
		}
	}
	fmt.Printf("Ignored-trigger evidence: records=%d reason=%s commit=%s tag=%q empty-policy-attachments=true\n", want, reason, s.w.IDs[commit], tag)
	return nil
}

func (s *ignoredSteps) page(want int, key string) error {
	var page ignoredAnswer
	if s.w.Last().Status != http.StatusOK || json.Unmarshal(s.w.Last().Body, &page) != nil || len(page.Edges) != want {
		return fmt.Errorf("%w: ignored-trigger page %s", harness.ErrStep, s.w.Last().Body)
	}
	if len(page.Edges) > 0 {
		s.w.IDs[key] = page.Edges[len(page.Edges)-1].Cursor
	}
	return nil
}

func (s *ignoredSteps) stored(ctx context.Context, stack string, want int) error {
	var id string
	if err := s.w.Row(ctx, `SELECT id::text FROM stacks WHERE account_id=$1 AND slug=$2`, []any{harness.UUIDOf(harness.Home.Account), s.w.Resolve(stack)}, &id); err != nil {
		return err
	}
	if s.stacks == nil {
		s.stacks = map[string]string{}
	}
	s.stacks[stack] = id
	var count int
	if err := s.w.Row(ctx, `SELECT count(*) FROM vcs_ignored_triggers WHERE account_id=$1 AND stack_id=$2`, []any{harness.UUIDOf(harness.Home.Account), id}, &count); err != nil {
		return err
	}
	if count != want {
		return fmt.Errorf("%w: stack %s has %d stored ignored triggers, want %d", harness.ErrStep, stack, count, want)
	}
	return nil
}

func (s *ignoredSteps) deleted(ctx context.Context, stack string) error {
	id := s.stacks[stack]
	if id == "" {
		return fmt.Errorf("%w: deleted stack identity was not observed", harness.ErrStep)
	}
	return deps.Eventually(ctx, "real stack deletion removes ignored history", func(ctx context.Context) (bool, error) {
		var rows, stacks int
		err := s.w.Row(
			ctx,
			`SELECT (SELECT count(*) FROM vcs_ignored_triggers WHERE account_id=$1 AND stack_id=$2),(SELECT count(*) FROM stacks WHERE account_id=$1 AND id=$2)`,
			[]any{harness.UUIDOf(harness.Home.Account), id},
			&rows,
			&stacks,
		)
		return rows == 0 && stacks == 0, err
	})
}

func (s *ignoredSteps) purged(ctx context.Context) error {
	return deps.Eventually(ctx, "account purge removes ignored history", func(ctx context.Context) (bool, error) {
		var count int
		err := s.w.Row(ctx, `SELECT count(*) FROM vcs_ignored_triggers WHERE account_id=$1`, []any{harness.UUIDOf(harness.Home.Account)}, &count)
		return count == 0, err
	})
}

func (s *ignoredSteps) capped(ctx context.Context, key string) error {
	var slug string
	if err := s.w.Row(
		ctx,
		`SELECT s.slug FROM vcs_ignored_triggers i JOIN stacks s ON s.account_id=i.account_id AND s.id=i.stack_id WHERE i.account_id=$1 AND i.reason='RUN_LIMIT_REACHED'`,
		[]any{harness.UUIDOf(harness.Home.Account)},
		&slug,
	); err != nil {
		return err
	}
	s.w.IDs[key] = slug
	return nil
}
