package gitlabfake

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
)

// CheckRecord is one native external commit status.
//
//nolint:tagliatelle // GitLab owns the native JSON spellings.
type CheckRecord struct {
	ID          int64  `json:"id"`
	SHA         string `json:"sha"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Description string `json:"description"`
	URL         string `json:"target_url"`
}
type checkStore struct {
	records    map[string][]CheckRecord
	writes     map[string][]CheckRecord
	failCreate bool
}

// Checks returns independent native status snapshots, including terminal history.
func (f *Fake) Checks(namespace, repo string) []CheckRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.checks.records[namespace+"/"+repo])
}

// CheckWrites returns accepted statuses in native order.
func (f *Fake) CheckWrites(namespace, repo string) []CheckRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.checks.writes[namespace+"/"+repo])
}

// FailNextCheckCreate loses one successful status response.
func (f *Fake) FailNextCheckCreate() { f.mu.Lock(); defer f.mu.Unlock(); f.checks.failCreate = true }

func (f *Fake) checkRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(f.deploymentPermission)
		r.Get("/repository/commits/{sha}/statuses", f.listChecks)
		r.Post("/statuses/{sha}", f.setCheck)
	})
}

func (f *Fake) listChecks(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	seen := map[string]bool{}
	list := f.checks.records[p.fullPath()]
	for i := len(list) - 1; i >= 0; i-- {
		v := list[i]
		if v.SHA != chi.URLParam(r, "sha") || r.URL.Query().Get("name") != "" && v.Name != r.URL.Query().Get("name") {
			continue
		}
		if r.URL.Query().Get("all") != "true" && seen[v.Name] {
			continue
		}
		seen[v.Name] = true
		items = append(items, map[string]any{"id": v.ID, "sha": v.SHA, "name": v.Name, "status": v.Status, "description": v.Description, "target_url": v.URL})
	}
	paged(w, r, items)
}

func (f *Fake) setCheck(w http.ResponseWriter, r *http.Request) {
	//nolint:tagliatelle // GitLab owns the native JSON spellings.
	var body struct {
		State       string `json:"state"`
		Name        string `json:"name"`
		Description string `json:"description"`
		URL         string `json:"target_url"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Name == "" || !slices.Contains([]string{"pending", "running", "success", "failed", "canceled"}, body.State) {
		answer(w, 400, message("Invalid commit status"))
		return
	}
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	commit, err := p.repo.Resolve(chi.URLParam(r, "sha"))
	if err != nil {
		answer(w, 404, message("Commit not found"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := p.fullPath()
	if f.checks.records == nil {
		f.checks.records = map[string][]CheckRecord{}
		f.checks.writes = map[string][]CheckRecord{}
	}
	next := CheckRecord{SHA: commit.Hash.String(), Name: body.Name, Status: body.State, Description: body.Description, URL: body.URL}
	index := -1
	for i := len(f.checks.records[key]) - 1; i >= 0; i-- {
		old := f.checks.records[key][i]
		if old.SHA == next.SHA && old.Name == next.Name {
			if old.Status == "pending" || old.Status == "running" {
				index = i
				next.ID = old.ID
			}
			break
		}
	}
	if index < 0 {
		f.nextID++
		next.ID = f.nextID
		f.checks.records[key] = append(f.checks.records[key], next)
	} else {
		f.checks.records[key][index] = next
	}
	f.checks.writes[key] = append(f.checks.writes[key], next)
	if f.checks.failCreate {
		f.checks.failCreate = false
		answer(w, 502, message("Response lost"))
		return
	}
	answer(w, 201, next)
}
