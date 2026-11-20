package bitbucketcloudfake

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (f *Fake) commits(w http.ResponseWriter, r *http.Request) {
	f.recordSourceRead()
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member binds it
	head := r.URL.Query().Get("include")
	if head == "" {
		head = repo.repo.DefaultBranch()
	}
	commits, err := repo.repo.Range(r.Context(), r.URL.Query().Get("exclude"), head)
	if err != nil {
		answer(w, http.StatusNotFound, problem("Commit not found"))
		return
	}
	items := make([]map[string]any, 0, len(commits))
	for _, commit := range commits {
		items = append(items, f.commitJSON(r, repo, commit))
	}
	paged(w, r, items)
}

func (f *Fake) mergeBase(w http.ResponseWriter, r *http.Request) {
	f.recordSourceRead()
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member binds it
	head, base, ok := strings.Cut(chi.URLParam(r, "spec"), "..")
	sha, err := repo.repo.MergeBase(base, head)
	if !ok || err != nil {
		answer(w, http.StatusNotFound, problem("Merge base not found"))
		return
	}
	answer(w, http.StatusOK, map[string]string{"hash": sha})
}
