package bitbucketdcfake

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (f *Fake) defaultBranch(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member binds the repository
	name := repo.repo.DefaultBranch()
	commit, err := repo.repo.Resolve(name)
	if err != nil {
		answer(w, http.StatusNotFound, problem("A default branch does not exist in repository."))
		return
	}
	answer(w, http.StatusOK, map[string]string{"id": "refs/heads/" + name, "displayId": name, "latestCommit": commit.Hash.String()})
}

func (f *Fake) mergeBase(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member binds the repository
	sha, err := repo.repo.MergeBase(chi.URLParam(r, "commit"), r.URL.Query().Get("otherCommitId"))
	if err != nil {
		answer(w, http.StatusNotFound, problem("A common ancestor does not exist."))
		return
	}
	answer(w, http.StatusOK, map[string]string{"id": sha})
}

func (f *Fake) files(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member binds the repository
	type directory struct{ ref, prefix string }
	work := []directory{{ref: r.URL.Query().Get("at")}}
	files := []string{}
	for len(work) > 0 {
		item := work[len(work)-1]
		work = work[:len(work)-1]
		_, entries, err := repo.repo.Tree(item.ref)
		if err != nil {
			answer(w, http.StatusNotFound, problem("The tree does not exist."))
			return
		}
		for _, entry := range entries {
			name := item.prefix + entry.Path
			if entry.Type == "tree" {
				work = append(work, directory{entry.SHA, name + "/"})
			} else {
				files = append(files, name)
			}
		}
	}
	// Small pages force callers to honor nextPageStart rather than assuming
	// that a provider returns the requested limit.
	start, _ := strconv.Atoi(r.URL.Query().Get("start"))
	start = min(max(0, start), len(files))
	end := min(start+3, len(files))
	answer(w, http.StatusOK, map[string]any{"values": files[start:end], "isLastPage": end == len(files), "nextPageStart": end})
}

func (f *Fake) fileDiff(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member binds the repository
	q, name := r.URL.Query(), rest(r)
	changes, err := repo.repo.Changes(r.Context(), q.Get("since"), q.Get("until"))
	if err != nil {
		answer(w, http.StatusNotFound, problem("The revisions do not exist."))
		return
	}
	diffs := []map[string]any{}
	for _, change := range changes {
		if change.From != name && change.To != name {
			continue
		}
		body, _ := repo.repo.File(q.Get("until"), name)
		diffs = append(diffs, map[string]any{
			"source": map[string]string{"toString": change.From}, "destination": map[string]string{"toString": change.To},
			"binary": strings.ContainsRune(string(body), '\x00'), "hunks": []any{}, "truncated": false,
		})
	}
	answer(w, http.StatusOK, map[string]any{"diffs": diffs, "truncated": false})
}
