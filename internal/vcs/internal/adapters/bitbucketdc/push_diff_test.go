package bitbucketdc_test

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketdc"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
)

func TestUnitPushDiff(t *testing.T) {
	l := newLab(t)
	sourcetest.Push(t, l.bb, l.repo.Repo, l.fake.Repo("ACME", "infra"))
}

func TestUnitPushFileDiffEvidence(t *testing.T) {
	for _, check := range []struct {
		name, response string
		changed, fails bool
	}{
		{"binary", `{"diffs":[{"source":{"toString":"main.tf"},"binary":true,"hunks":[]}]}`, true, false},
		{"mode only", `{"diffs":[{"source":{"toString":"main.tf"},"hunks":[],"properties":{"executable":true}}]}`, true, false},
		{"truncated content keeps identity", `{"diffs":[{"destination":{"toString":"main.tf"},"truncated":true}],"truncated":true}`, true, false},
		{"unchanged", `{"diffs":[],"truncated":false}`, false, false},
		{"truncated without identity", `{"diffs":[],"truncated":true}`, false, true},
		{"wrong file", `{"diffs":[{"source":{"toString":"other.tf"}}]}`, false, true},
		{"malformed JSON", `{`, false, true},
	} {
		t.Run(check.name, func(t *testing.T) {
			l := newLab(t)
			base := l.fake.Repo("ACME", "infra").Commit("main", "base", map[string]string{"main.tf": "before"})
			head := l.fake.Repo("ACME", "infra").Commit("main", "head", map[string]string{"main.tf": "after"})
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/diff/") {
					if strings.HasSuffix(r.URL.Path, "/main.tf") {
						_, _ = w.Write([]byte(check.response))
					} else {
						_, _ = w.Write([]byte(`{"diffs":[]}`))
					}
					return
				}
				l.fake.Handler().ServeHTTP(w, r)
			})
			provider := bitbucketdc.NewOver(memory{handler})
			got, err := provider.AffectedFiles(t.Context(), l.repo.Repo, base, head, port.PushDiff)
			if check.fails {
				if !errors.Is(err, kernel.ErrUnavailable) {
					t.Fatal("incomplete comparison was accepted", got, err)
				}
				return
			}
			if err != nil || slices.Contains(got, "main.tf") != check.changed {
				t.Fatal("file diff lost content-independent change evidence", got, err)
			}
		})
	}
}

func TestUnitPushCompletePathPages(t *testing.T) {
	l := newLab(t)
	r := l.fake.Repo("ACME", "infra")
	files := map[string]string{}
	for i := range 25 {
		files[fmt.Sprintf("large/file%02d.bin", i)] = "before\x00"
	}
	before := r.Commit("main", "many original paths", files)
	for name := range files {
		files[name] = "after\x00"
	}
	head := r.Commit("main", "all binary files change", files)
	got, err := l.bb.AffectedFiles(t.Context(), l.repo.Repo, before, head, port.PushDiff)
	if err != nil || len(got) != len(files) || !slices.Contains(got, "large/file24.bin") {
		t.Fatal("complete push comparison stopped at an API page", len(got), err)
	}
}

func TestUnitPushMissingMergeBase(t *testing.T) {
	l := newLab(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/merge-base") {
			http.NotFound(w, r)
			return
		}
		l.fake.Handler().ServeHTTP(w, r)
	})
	provider := bitbucketdc.NewOver(memory{handler})
	if _, err := provider.AffectedFiles(t.Context(), l.repo.Repo, strings.Repeat("0", 40), l.repo.Head, port.PushDiff); !errors.Is(err, port.ErrNotFound) {
		t.Fatal("unsupported merge-base response silently fell back", err)
	}
}
