package source

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type metadataOnlySource struct{ port.RepositorySource }

type pushHistorySource struct {
	port.Source
	commit                          vcsapi.Commit
	result                          port.PushRange
	resolveErr, historyErr, diffErr error
	metadataErr                     error
	metadataID                      string
	historyCalls, diffCalls         int
	reached                         port.Repository
	base                            string
}

func (s *pushHistorySource) ResolveRepositoryRef(ctx context.Context, repo port.Repository, ref string) (port.Resolution, error) {
	if s.metadataErr != nil {
		return port.Resolution{}, s.metadataErr
	}
	id := repo.ExpectedID
	if s.metadataID != "" {
		id = s.metadataID
	}
	identity := vcsapi.Repository{ID: id, Namespace: repo.Namespace, Name: repo.Name}
	if err := port.MatchRepository(repo, identity); err != nil {
		return port.Resolution{}, err
	}
	commit, err := s.ResolveRef(ctx, repo, ref)
	return port.Resolution{Commit: commit, Repository: identity}, err
}

func (s *pushHistorySource) ResolveRef(_ context.Context, repo port.Repository, _ string) (vcsapi.Commit, error) {
	s.reached = repo
	return s.commit, s.resolveErr
}

func (s *pushHistorySource) RecoverPush(_ context.Context, repo port.Repository, _, _ string) (port.PushRange, error) {
	s.historyCalls++
	s.reached = repo
	return s.result, s.historyErr
}

func (s *pushHistorySource) AffectedFiles(_ context.Context, repo port.Repository, base, _ string, _ ...port.DiffMode) ([]string, error) {
	s.diffCalls++
	s.reached, s.base = repo, base
	return []string{"endpoint.tf"}, s.diffErr
}

func TestUnitPreparePushUsesCompleteUnionAndOriginalIntegration(t *testing.T) {
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovered", true: "complete native"}[native], func(t *testing.T) {
			item := vcsapi.PushCommit{Hash: head, At: time.Now(), Files: new([]string{"reverted.tf"})}
			rangeFacts, err := port.RecoveredRange(base, "native REST", []vcsapi.PushCommit{item})
			if err != nil {
				t.Fatal(err)
			}
			src := &pushHistorySource{commit: vcsapi.Commit{Hash: head}, result: rangeFacts}
			f := newCredentialFixture(t, func(*credentialFixture) port.Source { return src })
			var selected *kernel.ULID
			f.svc.integrations = integrations{in: f.in, found: true, asked: &selected}
			event := vcsapi.Event{Before: strings.Repeat("0", 40), Head: head, Branch: "topic", Repository: vcsapi.Repository{ID: "native-id", Namespace: "original", Name: "repo"}}
			if native {
				event.Before, event.Commits = base, rangeFacts.Commits
			} else {
				event.Commits = port.NativeCommits("signed native array", []vcsapi.PushCommit{item}, "provider-commit-limit")
			}
			got, err := f.svc.PreparePush(t.Context(), f.acct, f.in.ID, "GITLAB", event)
			if err != nil || got.Commit.Hash != head || got.Commit.Branch != "topic" || !slices.Equal(got.Files, []string{"endpoint.tf", "reverted.tf"}) {
				t.Fatal(got, err)
			}
			if selected == nil || *selected != f.in.ID || src.reached.Integration.ID != f.in.ID || src.reached.Namespace != "original" || src.reached.Name != "repo" ||
				src.base != base ||
				src.diffCalls != 1 {
				t.Fatal("recovery changed original binding or frozen base", selected, src)
			}
			if native && (src.historyCalls != 0 || got.RecoveredCommits != nil) ||
				!native && (src.historyCalls != 1 || got.RecoveredCommits != rangeFacts.Commits || event.Commits.Omission != "provider-commit-limit") {
				t.Fatal("signed provenance was overwritten or complete native data was crawled", got, src)
			}
		})
	}
}

func TestUnitPreparePushRefusesIncompleteOrFailedRecovery(t *testing.T) {
	cause := errors.New("provider failed")
	for _, mode := range []string{"unknown provider", "integration failure", "foreign integration", "missing signed identity", "metadata failure", "identity mismatch", "resolve failure", "history failure", "incomplete history", "invalid base", "diff failure", "no identity capability", "no history capability"} {
		t.Run(mode, func(t *testing.T) {
			base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
			src := &pushHistorySource{commit: vcsapi.Commit{Hash: head}, result: port.PushRange{Base: base, Commits: port.NativeCommits("REST", nil, "")}}
			f := newCredentialFixture(t, func(*credentialFixture) port.Source { return src })
			want, provider := error(cause), "GITLAB"
			event := vcsapi.Event{Before: base, Head: head, Repository: vcsapi.Repository{ID: "native-id", Namespace: "original", Name: "repo"}}
			switch mode {
			case "unknown provider":
				provider, want = "absent", kernel.ErrUnavailable
			case "integration failure":
				f.svc.integrations = integrations{err: cause}
			case "foreign integration":
				f.svc.integrations, want = integrations{}, port.ErrNoIntegration
			case "missing signed identity":
				event.Repository.ID, want = "", kernel.ErrUnavailable
			case "metadata failure":
				src.metadataErr = cause
			case "identity mismatch":
				src.metadataID, want = "other-native-id", port.ErrRepositoryIdentity
			case "resolve failure":
				src.resolveErr = cause
			case "history failure":
				src.historyErr = cause
			case "incomplete history":
				src.result.Commits.Omission, want = "provider limit", kernel.ErrUnavailable
			case "invalid base":
				src.result.Base, want = "main", kernel.ErrUnavailable
			case "diff failure":
				src.diffErr = cause
			case "no history capability":
				f.svc.providers[provider], want = metadataOnlySource{src}, kernel.ErrUnavailable
			case "no identity capability":
				var reached port.Repository
				f.svc.providers[provider], want = source{repo: &reached, commit: src.commit}, kernel.ErrUnavailable
			}
			got, err := f.svc.PreparePush(t.Context(), f.acct, f.in.ID, provider, event)
			if !errors.Is(err, want) || got.Commit.Hash != "" || got.RecoveredCommits != nil {
				t.Fatal("failed provider read became usable preparation", got, err)
			}
			if mode != "diff failure" && src.diffCalls != 0 {
				t.Fatal("incomplete history proceeded to diff", src.diffCalls)
			}
			if (mode == "missing signed identity" || mode == "identity mismatch" || mode == "metadata failure") && (src.historyCalls != 0 || src.reached.Integration != nil) {
				t.Fatal("unvalidated identity reached source objects or history", src)
			}
		})
	}
}
