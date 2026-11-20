package azuredevopsfake

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (f *Fake) commits(w http.ResponseWriter, r *http.Request) {
	repo := repoOf(r)
	q := r.URL.Query()
	ref := q.Get("searchCriteria.itemVersion.version")
	if q.Get("searchCriteria.itemVersion.versionType") != "commit" {
		answer(w, http.StatusBadRequest, problem("An immutable commit version is required."))
		return
	}
	commits, err := repo.repo.Range(r.Context(), "", ref)
	if err != nil {
		answer(w, http.StatusNotFound, problem("The commit could not be resolved."))
		return
	}
	items := make([]map[string]any, 0, len(commits))
	for _, commit := range commits {
		items = append(items, commitJSON(r, repo, commit))
	}
	start := min(intOf(q.Get("searchCriteria.$skip"), 0), len(items))
	end := min(start+intOf(q.Get("searchCriteria.$top"), 100), len(items))
	answer(w, http.StatusOK, map[string]any{"count": end - start, "value": items[start:end]})
}

func (f *Fake) commitChanges(w http.ResponseWriter, r *http.Request) {
	repo := repoOf(r)
	commit, err := repo.repo.Resolve(chi.URLParam(r, "commit"))
	if err != nil {
		answer(w, http.StatusNotFound, problem("The commit could not be resolved."))
		return
	}
	base := ""
	if len(commit.ParentHashes) > 0 {
		base = commit.ParentHashes[0].String()
	}
	changes, err := repo.repo.Changes(r.Context(), base, commit.Hash.String())
	if err != nil {
		answer(w, http.StatusNotFound, problem("The commit changes could not be resolved."))
		return
	}
	items := make([]map[string]any, 0, len(changes))
	counts := map[string]int{}
	for i, change := range changes {
		name, kind := change.To, "Edit"
		switch {
		case change.From == "":
			kind = "Add"
		case change.To == "":
			name, kind = change.From, "Delete"
		case change.From != change.To:
			kind = "Rename"
		}
		counts[kind]++
		item := map[string]any{"changeId": i + 1, "changeType": kind, "item": map[string]any{"path": "/" + name, "gitObjectType": "blob"}}
		if kind == "Rename" {
			item["originalPath"] = "/" + change.From
		}
		items = append(items, item)
	}
	q := r.URL.Query()
	start := min(intOf(q.Get("skip"), 0), len(items))
	end := min(start+intOf(q.Get("top"), 100), len(items))
	answer(w, http.StatusOK, map[string]any{"changeCounts": counts, "changes": items[start:end]})
}
