package bitbucketcloudfake

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"github.com/go-chi/chi/v5"
)

// CheckRecord is a native build status keyed by immutable commit and visible key.
type (
	CheckRecord struct {
		SHA         string `json:"-"`
		Key         string `json:"key"`
		Name        string `json:"name"`
		State       string `json:"state"`
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	checkStore struct {
		records    map[string][]CheckRecord
		writes     map[string][]CheckRecord
		failCreate bool
	}
)

// Checks returns an independent snapshot of current native keys.
func (f *Fake) Checks(namespace, repo string) []CheckRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.checks.records[namespace+"/"+repo])
}

// CheckWrites returns accepted native mutations in order.
func (f *Fake) CheckWrites(namespace, repo string) []CheckRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.checks.writes[namespace+"/"+repo])
}

// FailNextCheckCreate loses one accepted build status response.
func (f *Fake) FailNextCheckCreate() { f.mu.Lock(); defer f.mu.Unlock(); f.checks.failCreate = true }

func (f *Fake) checkRoutes(r chi.Router) {
	r.Get("/commit/{commit}/statuses/build/{key}", f.getCheck)
	r.Post("/commit/{commit}/statuses/build", f.setCheck)
}

func (f *Fake) getCheck(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "workspace") + "/" + chi.URLParam(r, "slug")
	want, _ := url.PathUnescape(chi.URLParam(r, "key"))
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.checks.records[key] {
		if v.SHA == chi.URLParam(r, "commit") && v.Key == want {
			answer(w, 200, v)
			return
		}
	}
	answer(w, 404, problem("Build status not found"))
}

func (f *Fake) setCheck(w http.ResponseWriter, r *http.Request) {
	var body CheckRecord
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Key == "" || body.URL == "" || !slices.Contains([]string{"INPROGRESS", "SUCCESSFUL", "FAILED", "STOPPED"}, body.State) {
		answer(w, 400, problem("Invalid build status"))
		return
	}
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	commit, err := repo.repo.Resolve(chi.URLParam(r, "commit"))
	if err != nil {
		answer(w, 404, problem("Commit not found"))
		return
	}
	body.SHA = commit.Hash.String()
	f.mu.Lock()
	defer f.mu.Unlock()
	key := chi.URLParam(r, "workspace") + "/" + chi.URLParam(r, "slug")
	if f.checks.records == nil {
		f.checks.records = map[string][]CheckRecord{}
		f.checks.writes = map[string][]CheckRecord{}
	}
	replaced := false
	for i, old := range f.checks.records[key] {
		if old.SHA == body.SHA && old.Key == body.Key {
			f.checks.records[key][i] = body
			replaced = true
			break
		}
	}
	if !replaced {
		f.checks.records[key] = append(f.checks.records[key], body)
	}
	f.checks.writes[key] = append(f.checks.writes[key], body)
	if f.checks.failCreate {
		f.checks.failCreate = false
		answer(w, 502, problem("Response lost"))
		return
	}
	answer(w, 201, body)
}
