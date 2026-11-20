package gitlabfake

import (
	"net/http"
	"path"
	"strings"
)

func (f *Fake) commits(w http.ResponseWriter, r *http.Request) {
	f.recordSourceRead()
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member binds it
	base, head, rangeQuery := strings.Cut(r.URL.Query().Get("ref_name"), "..")
	if !rangeQuery {
		head, base = base, ""
	}
	if head == "" {
		head = p.repo.DefaultBranch()
	}
	commits, err := p.repo.Range(r.Context(), base, head)
	if err != nil {
		answer(w, http.StatusNotFound, message("404 Ref Not Found"))
		return
	}
	items := make([]map[string]any, 0, len(commits))
	for _, commit := range commits {
		items = append(items, commitJSON(r, p, commit))
	}
	paged(w, r, items)
}

func (f *Fake) mergeBase(w http.ResponseWriter, r *http.Request) {
	f.recordSourceRead()
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member binds it
	refs := r.URL.Query()["refs[]"]
	if len(refs) != 2 {
		answer(w, http.StatusBadRequest, message("Two refs required"))
		return
	}
	sha, err := p.repo.MergeBase(refs[0], refs[1])
	if err != nil {
		answer(w, http.StatusNotFound, message("404 Merge Base Not Found"))
		return
	}
	commit, err := p.repo.Resolve(sha)
	if err != nil {
		answer(w, http.StatusNotFound, message("404 Commit Not Found"))
		return
	}
	answer(w, http.StatusOK, commitJSON(r, p, commit))
}

func (f *Fake) tree(w http.ResponseWriter, r *http.Request) {
	f.recordSourceRead()
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member binds it
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = p.repo.DefaultBranch()
	}
	entries, err := p.repo.FullTree(r.Context(), ref)
	if err != nil {
		answer(w, http.StatusNotFound, message("404 Tree Not Found"))
		return
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if r.URL.Query().Get("recursive") != "true" && strings.Contains(entry.Path, "/") {
			continue
		}
		items = append(items, map[string]any{"id": entry.SHA, "name": path.Base(entry.Path), "path": entry.Path, "type": entry.Type, "mode": entry.Mode})
	}
	paged(w, r, items)
}
