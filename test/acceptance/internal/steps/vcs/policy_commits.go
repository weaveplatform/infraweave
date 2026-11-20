package vcs

import (
	"context"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type nativeEventCommit struct {
	hash, author, message string
	at                    time.Time
}

func (s *eventSteps) policyNativeCommits(count int, branch, key string) error {
	c := s.comments
	for i := range count {
		value := fmt.Sprintf("native-%d", i)
		if s.nativeRevert && i == count-1 {
			value = "one"
		}
		hash := s.repository().Commit(branch, fmt.Sprintf("native commit %d", i), deploymentFiles(value))
		var native nativeEventCommit
		switch c.fixture.provider {
		case "GitHub":
			commit, err := c.github.fake.Repo(c.fixture.namespace, "infra").Resolve(hash)
			if err != nil {
				return fmt.Errorf("native commit fixture: %w", err)
			}
			native = nativeEventCommit{hash, commit.Author.Name, commit.Message, commit.Author.When}
		case "GitLab":
			commit, err := c.gitlab.fake.Repo(c.fixture.namespace, "infra").Resolve(hash)
			if err != nil {
				return fmt.Errorf("native commit fixture: %w", err)
			}
			native = nativeEventCommit{hash, commit.Author.Name, commit.Message, commit.Author.When}
		default:
			return fmt.Errorf("%w: native complete-commit fixture needs GitHub or GitLab", harness.ErrStep)
		}
		s.nativeCommits = append(s.nativeCommits, native)
		c.w.IDs[key] = hash
	}
	return nil
}

func (s *eventSteps) decorateNativeCommits(body map[string]any) {
	commits := s.nativeCommits
	if s.comments.fixture.provider == "GitLab" && len(commits) > 20 {
		commits = commits[len(commits)-20:]
	}
	items := make([]any, 0, len(commits))
	for _, commit := range commits {
		items = append(
			items,
			map[string]any{
				"id":        commit.hash,
				"message":   commit.message,
				"timestamp": commit.at.Format(time.RFC3339Nano),
				"author":    map[string]string{"name": commit.author},
				"modified":  []string{"main.tf"},
				"added":     []string{},
				"removed":   []string{},
			},
		)
	}
	body["commits"] = items
	if s.comments.fixture.provider == "GitLab" {
		body["total_commits_count"] = len(s.nativeCommits)
	}
}

func (s *eventSteps) policyNativeCommitEvidence(ctx context.Context, name string, count int, omission string) error {
	var retained bool
	err := s.comments.w.Row(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM vcs_prepared_events p JOIN vcs_deliveries d ON d.account_id=p.account_id AND d.id=p.delivery_id WHERE d.account_id=$1 AND d.integration_id=$2 AND d.delivery_id=$3 AND jsonb_array_length(p.event#>'{event,commits,items}')=$4 AND coalesce(p.event#>>'{event,commits,omission}','')=$5 AND p.event#>>'{event,commits,source}'='commits')`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.comments.fixture.integration), s.deliveries[name], count, omission},
		&retained,
	)
	if err != nil {
		return err
	}
	if !retained {
		return fmt.Errorf("%w: native collection completeness/provenance was lost", harness.ErrStep)
	}
	return nil
}
