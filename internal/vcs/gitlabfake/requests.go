package gitlabfake

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (f *Fake) rememberRequest(namespace, repository string, in WebhookMergeRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nativeRequests == nil {
		f.nativeRequests = map[string]map[int]WebhookMergeRequest{}
	}
	key := namespace + "/" + repository
	if f.nativeRequests[key] == nil {
		f.nativeRequests[key] = map[int]WebhookMergeRequest{}
	}
	in.Labels = slices.Clone(in.Labels)
	f.nativeRequests[key][in.IID] = in
}

func (f *Fake) nativeRequest(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member binds it
	number, err := strconv.Atoi(chi.URLParam(r, "pr"))
	if err != nil {
		answer(w, http.StatusNotFound, message("404 Merge Request Not Found"))
		return
	}
	f.mu.Lock()
	in, ok := f.nativeRequests[p.fullPath()][number]
	f.mu.Unlock()
	if !ok {
		answer(w, http.StatusNotFound, message("404 Merge Request Not Found"))
		return
	}
	base, err := p.repo.Resolve(in.TargetBranch)
	if err != nil {
		answer(w, http.StatusNotFound, message("404 Request Base Not Found"))
		return
	}
	body := f.webhookRequest(p.namespace, p.path, in)
	author := in.Author
	if author == "" {
		author = "author"
	}
	body["author"] = f.requestUser(author)
	body["labels"] = slices.Clone(in.Labels)
	body["project_id"] = p.id
	body["web_url"] = body["url"]
	body["sha"] = in.Head
	body["diff_refs"] = map[string]any{"base_sha": base.Hash.String(), "start_sha": base.Hash.String(), "head_sha": in.Head}
	body["diverged_commits_count"] = 0
	answer(w, http.StatusOK, body)
}

func (f *Fake) nativeRequestApprovals(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member binds it
	number, err := strconv.Atoi(chi.URLParam(r, "pr"))
	f.mu.Lock()
	_, ok := f.nativeRequests[p.fullPath()][number]
	f.mu.Unlock()
	if err != nil || !ok {
		answer(w, http.StatusNotFound, message("404 Merge Request Not Found"))
		return
	}
	answer(w, http.StatusOK, map[string]any{"id": 9001, "iid": number, "project_id": p.id, "approved": false, "approvals_required": 0, "approvals_left": 0})
}
