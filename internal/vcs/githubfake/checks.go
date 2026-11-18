package githubfake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// CheckAction is a native check-run requested action.
type (
	CheckAction struct {
		Label       string `json:"label"`
		Description string `json:"description"`
		Identifier  string `json:"identifier"`
	}
	// CheckOutput is GitHub's check-run output, including the recovery marker.
	CheckOutput struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
		Text    string `json:"text"`
	}
)

// CheckRecord preserves every accepted version through CheckWrites.
//
//nolint:tagliatelle // GitHub owns the native JSON spellings.
type CheckRecord struct {
	ID         int64         `json:"id"`
	Name       string        `json:"name"`
	SHA        string        `json:"head_sha"`
	URL        string        `json:"details_url"`
	ExternalID string        `json:"external_id"`
	Status     string        `json:"status"`
	Conclusion string        `json:"conclusion,omitempty"`
	Output     CheckOutput   `json:"output"`
	Actions    []CheckAction `json:"actions"`
}
type checkStore struct {
	records                  map[string][]CheckRecord
	writes                   map[string][]CheckRecord
	failCreate, rejectReopen bool
}

func cloneCheck(r CheckRecord) CheckRecord { r.Actions = slices.Clone(r.Actions); return r }

// Checks returns independent snapshots of native check-run identities.
func (f *Fake) Checks(owner, repo string) []CheckRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.checks.records[owner+"/"+repo])
	for i := range out {
		out[i] = cloneCheck(out[i])
	}
	return out
}

// CheckWrites returns accepted creates and updates in native order.
func (f *Fake) CheckWrites(owner, repo string) []CheckRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.checks.writes[owner+"/"+repo])
	for i := range out {
		out[i] = cloneCheck(out[i])
	}
	return out
}

// FailNextCheckCreate loses a successful create response exactly once.
func (f *Fake) FailNextCheckCreate() { f.mu.Lock(); defer f.mu.Unlock(); f.checks.failCreate = true }

// RejectCheckReopen models installations that refuse completed-to-active PATCH.
func (f *Fake) RejectCheckReopen(reject bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks.rejectReopen = reject
}

func (f *Fake) checkRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(f.checkPermission)
		r.Get("/commits/{sha}/check-runs", f.listChecks)
		r.Post("/check-runs", f.createCheck)
		r.Patch("/check-runs/{check}", f.updateCheck)
	})
}

func (f *Fake) checkPermission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Context().Value(tokenKey).(token)          //nolint:forcetypeassert // asInstallation sets it
		in := r.Context().Value(installKey).(*Installation) //nolint:forcetypeassert // asInstallation sets it
		f.mu.Lock()
		perms := tok.perms
		if perms == nil {
			perms = f.apps[in.AppID].Permissions
		}
		level := perms["checks"]
		f.mu.Unlock()
		if level != "write" && (r.Method != http.MethodGet || level != "read") {
			answer(w, http.StatusForbidden, message("Resource not accessible by integration"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (f *Fake) listChecks(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	for _, v := range f.checks.records[deploymentRepo(r)] {
		if v.SHA == chi.URLParam(r, "sha") && (r.URL.Query().Get("check_name") == "" || r.URL.Query().Get("check_name") == v.Name) {
			items = append(items, checkJSON(v))
		}
	}
	paged(w, r, items, func(page []map[string]any) any { return map[string]any{"total_count": len(items), "check_runs": page} })
}

func (f *Fake) createCheck(w http.ResponseWriter, r *http.Request) {
	var body CheckRecord
	if json.NewDecoder(r.Body).Decode(&body) != nil || !validCheck(body) {
		answer(w, 422, message("Validation Failed"))
		return
	}
	repo := r.Context().Value(repoKey).(*Repo) //nolint:forcetypeassert // reachable sets it
	commit, err := repo.Resolve(body.SHA)
	if err != nil || commit.Hash.String() != body.SHA {
		answer(w, 422, message("No commit found"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checks.records == nil {
		f.checks.records = map[string][]CheckRecord{}
		f.checks.writes = map[string][]CheckRecord{}
	}
	f.nextID++
	body.ID = f.nextID
	key := deploymentRepo(r)
	f.checks.records[key] = append(f.checks.records[key], cloneCheck(body))
	f.checks.writes[key] = append(f.checks.writes[key], cloneCheck(body))
	if f.checks.failCreate {
		f.checks.failCreate = false
		answer(w, 502, message("Response lost"))
		return
	}
	answer(w, http.StatusCreated, body)
}

func (f *Fake) updateCheck(w http.ResponseWriter, r *http.Request) {
	var body CheckRecord
	if json.NewDecoder(r.Body).Decode(&body) != nil || !validCheck(body) {
		answer(w, 422, message("Validation Failed"))
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "check"), 10, 64)
	if err != nil {
		answer(w, 404, message("Not Found"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := deploymentRepo(r)
	for i, old := range f.checks.records[key] {
		if old.ID == id {
			if f.checks.rejectReopen && old.Status == "completed" && body.Status != "completed" {
				answer(w, 422, message("Completed check cannot reopen"))
				return
			}
			body.ID = id
			body.SHA = old.SHA
			f.checks.records[key][i] = cloneCheck(body)
			f.checks.writes[key] = append(f.checks.writes[key], cloneCheck(body))
			answer(w, 200, body)
			return
		}
	}
	answer(w, 404, message("Not Found"))
}

func validCheck(r CheckRecord) bool {
	if r.Name == "" || r.URL == "" || r.Output.Title == "" || r.Output.Summary == "" || len(r.Actions) > 3 {
		return false
	}
	switch r.Status {
	case "queued", "in_progress":
		return r.Conclusion == ""
	case "completed":
		return slices.Contains([]string{"success", "failure", "neutral", "cancelled", "skipped"}, r.Conclusion)
	}
	return false
}

func checkJSON(v CheckRecord) map[string]any {
	return map[string]any{
		"id":          v.ID,
		"name":        v.Name,
		"head_sha":    v.SHA,
		"details_url": v.URL,
		"external_id": v.ExternalID,
		"status":      v.Status,
		"conclusion":  v.Conclusion,
		"output":      v.Output,
		"actions":     v.Actions,
	}
}
