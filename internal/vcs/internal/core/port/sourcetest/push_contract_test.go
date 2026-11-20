package sourcetest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitRecoveredRangeOracleRejectsBrokenProviderFacts(t *testing.T) {
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, mode := range []string{"valid", "provider failure", "missing collection", "partial collection", "wrong base", "missing provenance", "wrong count", "wrong path with correct count", "unknown files", "missing author", "missing message", "missing URL", "before included"} {
		t.Run(mode, func(t *testing.T) {
			item := vcsapi.PushCommit{
				Hash:    head,
				At:      time.Unix(1, 0),
				Author:  "author",
				Message: "change",
				URL:     "https://example.test/commit/" + head,
				Files:   new([]string{"main.tf"}),
			}
			got := port.PushRange{Base: base, Commits: port.NativeCommits("native REST", []vcsapi.PushCommit{item}, "")}
			var cause error
			switch mode {
			case "provider failure":
				cause = errOffline
			case "missing collection":
				got.Commits = nil
			case "partial collection":
				got.Commits.Omission = "provider limit"
			case "wrong base":
				got.Base = head
			case "missing provenance":
				got.Commits.Source = ""
			case "wrong count":
				got.Commits.Items = append(got.Commits.Items, item)
			case "wrong path with correct count":
				got.Commits.Items[0].Files = new([]string{"substituted.tf"})
			case "unknown files":
				got.Commits.Items[0].Files = nil
			case "missing author":
				got.Commits.Items[0].Author = ""
			case "missing message":
				got.Commits.Items[0].Message = ""
			case "missing URL":
				got.Commits.Items[0].URL = ""
			case "before included":
				got.Commits.Items[0].Hash = base
			}
			err := checkRecoveredRange(got, cause, rangeExpectation{before: base, base: base, commits: 1, files: []string{"main.tf"}})
			if (mode == "valid") != (err == nil) || err != nil && !errors.Is(err, ErrContract) {
				t.Fatal("broken native facts escaped the shared oracle", mode, err)
			}
			if cause != nil && !errors.Is(err, cause) {
				t.Fatal("provider failure cause lost", err)
			}
		})
	}
}

type identitySource struct {
	port.Source
	mode  string
	calls int
}

func (s *identitySource) ResolveRepositoryRef(_ context.Context, r port.Repository, head string) (port.Resolution, error) {
	s.calls++
	if s.mode == "initial failure" && s.calls == 1 || s.mode == "bound failure" && s.calls == 2 {
		return port.Resolution{}, errOffline
	}
	if s.calls == 3 {
		if s.mode == "unfenced lookup" {
			return port.Resolution{}, nil
		}
		return port.Resolution{}, port.ErrRepositoryIdentity
	}
	out := port.Resolution{Repository: vcsapi.Repository{ID: "native", Namespace: "acme", Name: "infra"}, Commit: vcsapi.Commit{Hash: head}}
	if s.mode == "missing identity" {
		out.Repository.ID = ""
	}
	if s.calls == 2 {
		if s.mode == "changed coordinates" {
			out.Repository.Name = "another"
		}
		if s.mode == "changed head" {
			out.Commit.Hash = strings.Repeat("f", 40)
		}
	}
	return out, nil
}

func TestUnitBoundRepositoryOracleRejectsBrokenIdentityFences(t *testing.T) {
	for _, mode := range []string{"valid", "initial failure", "missing identity", "bound failure", "changed coordinates", "changed head", "unfenced lookup"} {
		t.Run(mode, func(t *testing.T) {
			src := &identitySource{mode: mode}
			err := checkBoundRepository(t.Context(), src, Repository{Repo: port.Repository{Namespace: "acme", Name: "infra"}, Head: strings.Repeat("a", 40)})
			if (mode == "valid") != (err == nil) || err != nil && !errors.Is(err, ErrContract) {
				t.Fatal("broken identity fence escaped shared oracle", mode, err)
			}
		})
	}
}
