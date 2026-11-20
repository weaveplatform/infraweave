package github_test

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitNewBranchFreezesBaseBeforePagedRecovery(t *testing.T) {
	l := newLab(t)
	r := l.fake.Repo("acme", "infra")
	root := l.repo.Head
	r.Branch("topic", root)
	want := make(map[string][]string, 102)
	topic := root
	for i := range 100 {
		topic = r.Commit("topic", fmt.Sprintf("common %03d", i), map[string]string{"common.tf": fmt.Sprint(i)})
		want[topic] = []string{"common.tf"}
	}
	// Equal-size sibling ancestry keeps total_commits unchanged when trunk
	// moves. The substituted late commit lies beyond the first 100 results.
	r.Branch("left", root)
	left := r.Commit("left", "left branch only", map[string]string{"left.tf": "left"})
	r.Branch("right", root)
	right := r.Commit("right", "right branch only", map[string]string{"right.tf": "right"})
	head := r.Merge("combined", topic, "merge all parents", topic, left, right)
	want[right] = []string{"right.tf"}
	want[head] = []string{}
	r.Branch("trunk", left)
	r.SetDefaultBranch("trunk")

	// The native handler first serializes page one from the old branch tip,
	// then moves that name before the caller can ask for its next page.
	// There are no goroutines, sleeps, or timing assumptions in this barrier.
	mutated := false
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		l.fake.Handler().ServeHTTP(w, req)
		if !mutated && strings.Contains(req.URL.Path, "/compare/trunk...") {
			mutated = true
			r.Branch("trunk", right)
		}
	})
	provider := github.NewOver(memory{h})
	got, err := provider.RecoverPush(t.Context(), l.repo.Repo, strings.Repeat("0", 40), head)
	if err != nil {
		t.Fatalf("a frozen new-branch range must survive a later name move: %v", err)
	}
	if !mutated || got.Base != left || got.Commits == nil || got.Commits.Omission != "" {
		t.Fatalf("missing mutation or frozen base: mutated=%t base=%s commits=%+v", mutated, got.Base, got.Commits)
	}
	if len(got.Commits.Items) != len(want) {
		t.Errorf("commit count=%d want=%d", len(got.Commits.Items), len(want))
	}
	seen := make(map[string]bool, len(got.Commits.Items))
	for _, commit := range got.Commits.Items {
		paths, expected := want[commit.Hash]
		if !expected {
			t.Errorf("substituted commit %s (left/base=%s); missing right=%s would lose right.tf", commit.Hash, left, right)
			continue
		}
		if seen[commit.Hash] {
			t.Errorf("duplicate commit %s", commit.Hash)
		}
		seen[commit.Hash] = true
		if commit.Files == nil || !slices.Equal(*commit.Files, paths) {
			t.Errorf("commit %s paths=%v want=%v", commit.Hash, commit.Files, paths)
		}
	}
	for hash := range want {
		if !seen[hash] {
			t.Errorf("omitted expected commit %s", hash)
		}
	}
	paths, complete := port.NativeCommitFiles(got.Commits)
	if !complete || !slices.Equal(paths, []string{"common.tf", "right.tf"}) {
		t.Errorf("exact path union=%v complete=%t want=[common.tf right.tf]", paths, complete)
	}
}

func TestUnitNewBranchRefusesUnavailableMergeBase(t *testing.T) {
	for _, check := range []struct {
		name, body string
		status     int
	}{
		{"missing comparison", `null`, http.StatusOK},
		{"missing merge base", `{"merge_base_commit":{}}`, http.StatusOK},
		{"invalid merge base", `{"merge_base_commit":{"sha":"short"}}`, http.StatusOK},
		{"native failure", `{"message":"unavailable"}`, http.StatusInternalServerError},
	} {
		t.Run(check.name, func(t *testing.T) {
			l := newLab(t)
			h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if !strings.Contains(req.URL.Path, "/compare/") {
					l.fake.Handler().ServeHTTP(w, req)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(check.status)
				_, _ = w.Write([]byte(check.body))
			})
			got, err := github.NewOver(memory{h}).RecoverPush(t.Context(), l.repo.Repo, strings.Repeat("0", 40), l.repo.Head)
			if !errors.Is(err, kernel.ErrUnavailable) || got.Commits != nil || got.Base != "" {
				t.Fatalf("unavailable merge base became usable range: %+v, %v", got, err)
			}
		})
	}
}
