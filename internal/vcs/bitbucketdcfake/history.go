package bitbucketdcfake

import "net/http"

func (f *Fake) commits(w http.ResponseWriter, r *http.Request) {
	f.recordSourceRead()
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member binds it
	head := r.URL.Query().Get("until")
	if head == "" {
		head = repo.repo.DefaultBranch()
	}
	commits, err := repo.repo.Range(r.Context(), r.URL.Query().Get("since"), head)
	if err != nil {
		answer(w, http.StatusNotFound, problem("The revisions do not exist."))
		return
	}
	items := make([]map[string]any, 0, len(commits))
	for _, commit := range commits {
		items = append(items, f.commitJSON(commit))
	}
	paged(w, r, items)
}
