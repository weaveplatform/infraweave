package githubfake

import (
	"net/http"
	"path"

	"github.com/go-chi/chi/v5"
)

// TruncateRecursiveTrees models GitHub's recursive tree response limit. Single
// level trees stay complete so the provider can recover every changed path.
func (f *Fake) TruncateRecursiveTrees(limit int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.treeLimit = limit
}

func (f *Fake) repositoryMetadata(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*Repo) //nolint:forcetypeassert // reachable binds the repository
	answer(w, http.StatusOK, map[string]any{"name": chi.URLParam(r, "repo"), "default_branch": repo.DefaultBranch()})
}

func (f *Fake) gitTree(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*Repo) //nolint:forcetypeassert // reachable binds the repository
	sha, entries, err := repo.Tree(chi.URLParam(r, "tree"))
	if err != nil {
		answer(w, http.StatusNotFound, message("Not Found"))
		return
	}
	recursive := r.URL.Query().Get("recursive") != ""
	type entry struct {
		Path string `json:"path"`
		SHA  string `json:"sha"`
		Mode string `json:"mode"`
		Type string `json:"type"`
	}
	out := make([]entry, 0, len(entries))
	for _, value := range entries {
		out = append(out, entry{value.Path, value.SHA, value.Mode, value.Type})
	}
	if recursive {
		for index := 0; index < len(out); index++ {
			current := out[index]
			if current.Type != "tree" {
				continue
			}
			_, nested, err := repo.Tree(current.SHA)
			if err != nil {
				answer(w, http.StatusNotFound, message("Not Found"))
				return
			}
			for _, value := range nested {
				out = append(out, entry{path.Join(current.Path, value.Path), value.SHA, value.Mode, value.Type})
			}
		}
	}
	f.mu.Lock()
	limit := f.treeLimit
	f.mu.Unlock()
	truncated := recursive && limit > 0 && len(out) > limit
	if truncated {
		out = out[:limit]
	}
	answer(w, http.StatusOK, map[string]any{"sha": sha, "tree": out, "truncated": truncated})
}
