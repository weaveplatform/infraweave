package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
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
		commit, err := s.repository().Resolve(hash)
		if err != nil {
			return fmt.Errorf("native commit fixture: %w", err)
		}
		native := nativeEventCommit{hash, commit.Author.Name, commit.Message, commit.Author.When}
		s.nativeCommits = append(s.nativeCommits, native)
		c.w.IDs[key] = hash
	}
	return nil
}

func (s *eventSteps) decorateNativeCommits(body map[string]any) {
	s.decorateNativeIdentity(body)
	commits := s.nativeCommits
	provider := s.comments.fixture.provider
	if provider == "Azure DevOps" {
		resource := body["resource"].(map[string]any) //nolint:forcetypeassert // the native fake creates this envelope
		if s.nativeMode == "missing" {
			delete(resource, "commits")
			return
		}
		items := make([]any, 0, len(commits))
		for _, commit := range commits {
			items = append(items, map[string]any{
				"commitId": commit.hash, "comment": commit.message,
				"author": map[string]any{"name": commit.author, "date": commit.at.Format(time.RFC3339Nano)},
			})
		}
		resource["commits"] = items
		if s.nativeMode == "multi-ref" {
			refs := resource["refUpdates"].([]any) //nolint:forcetypeassert // JSON-decoded native fake ref array
			resource["refUpdates"] = append(refs, map[string]any{"name": "refs/heads/other", "oldObjectId": s.comments.w.IDs["seed"], "newObjectId": s.nativeOther})
		}
		return
	}
	if provider == "Bitbucket Cloud" {
		push := body["push"].(map[string]any)                 //nolint:forcetypeassert // sendBitbucket creates this exact native fixture
		change := push["changes"].([]any)[0].(map[string]any) //nolint:forcetypeassert // one native ref change
		if s.nativeMode == "missing" {
			delete(change, "commits")
			return
		}
		if len(commits) > 5 {
			commits = commits[len(commits)-5:]
		}
		items := make([]any, 0, len(commits))
		for _, commit := range commits {
			items = append(items, map[string]any{
				"hash": commit.hash, "message": commit.message,
				"author": map[string]any{"raw": commit.author}, "date": commit.at.Format(time.RFC3339Nano),
			})
		}
		change["commits"], change["truncated"] = items, len(s.nativeCommits) > 5
		return
	}
	if provider == "Bitbucket DC" {
		return
	}
	if s.nativeMode == "missing" {
		delete(body, "commits")
		delete(body, "total_commits_count")
		return
	}
	if provider == "GitHub" && len(commits) > 2048 {
		commits = commits[:2048]
	}
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

func (s *eventSteps) nativeMetadataMode(mode string) error {
	s.nativeMode = mode
	if mode != "multi-ref" {
		return nil
	}
	if s.comments.fixture.provider != "Azure DevOps" {
		return fmt.Errorf("%w: multi-ref native array needs Azure", harness.ErrStep)
	}
	s.repository().Branch("other", s.comments.w.IDs["seed"])
	s.nativeOther = s.repository().Commit("other", "other ref only", deploymentFiles("other-ref"))
	commit, err := s.repository().Resolve(s.nativeOther)
	if err != nil {
		return fmt.Errorf("native other-ref fixture: %w", err)
	}
	s.nativeCommits = append(s.nativeCommits, nativeEventCommit{s.nativeOther, commit.Author.Name, commit.Message, commit.Author.When})
	return nil
}

func (s *eventSteps) recoveredRangeEvidence(ctx context.Context, name, branch string, count int, paths, omission string) error {
	var raw json.RawMessage
	err := s.comments.w.Row(ctx, `SELECT p.event FROM vcs_prepared_events p JOIN vcs_deliveries d ON d.account_id=p.account_id AND d.id=p.delivery_id
		WHERE d.account_id=$1 AND d.integration_id=$2 AND d.delivery_id=$3 AND p.event#>>'{event,branch}'=$4`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(s.comments.fixture.integration), s.deliveries[name], branch}, &raw)
	if err != nil {
		return err
	}
	var got struct {
		Files               []string                 `json:"files"`
		AffectedStacksCount int                      `json:"affectedStacksCount"`
		Recovered           *vcsapi.CommitCollection `json:"recoveredCommits"`
		Event               struct {
			Commits *vcsapi.CommitCollection `json:"commits"`
		} `json:"event"`
	}
	var want []string
	if json.Unmarshal(raw, &got) != nil || json.Unmarshal([]byte(paths), &want) != nil || got.Recovered == nil || got.Recovered.Omission != "" || got.Recovered.Source == "" ||
		len(got.Recovered.Items) != count ||
		got.AffectedStacksCount != 1 ||
		!slices.Equal(got.Files, want) {
		return fmt.Errorf("%w: recovered range facts are incomplete or misattributed", harness.ErrStep)
	}
	if omission == "absent" {
		if got.Event.Commits != nil {
			return fmt.Errorf("%w: recovery overwrote absent signed native provenance", harness.ErrStep)
		}
	} else if got.Event.Commits == nil || got.Event.Commits.Omission != omission {
		return fmt.Errorf("%w: recovery overwrote partial signed native provenance", harness.ErrStep)
	}
	for _, commit := range got.Recovered.Items {
		if commit.Files == nil || commit.At.IsZero() || commit.Hash == "" || commit.Hash == s.nativeOther {
			return fmt.Errorf("%w: recovered range lost per-commit facts or crossed refs", harness.ErrStep)
		}
	}
	return nil
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
