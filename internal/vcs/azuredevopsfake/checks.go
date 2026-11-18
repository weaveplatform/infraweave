package azuredevopsfake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// ScopeCodeStatus reads and writes commit and pull-request statuses.
const ScopeCodeStatus = "vso.code_status"

// CheckContext is the native named status identity.
type (
	CheckContext struct {
		Name  string `json:"name"`
		Genre string `json:"genre"`
	}
	// CheckRecord records a native commit or iteration status.
	CheckRecord struct {
		ID          int          `json:"id"`
		SHA         string       `json:"-"`
		PullRequest int          `json:"-"`
		Iteration   int          `json:"iterationId,omitempty"`
		State       string       `json:"state"`
		Description string       `json:"description"`
		Context     CheckContext `json:"context"`
		URL         string       `json:"targetUrl"`
	}
	// CheckIteration is an immutable source head of a native PR iteration.
	CheckIteration struct {
		ID     int `json:"id"`
		Source struct {
			SHA string `json:"commitId"`
		} `json:"sourceRefCommit"`
	}
	checkStore struct {
		records    map[string][]CheckRecord
		writes     map[string][]CheckRecord
		iterations map[string]map[int][]CheckIteration
		failCreate bool
	}
)

// Checks returns independent current native contexts, including PR iterations.
func (f *Fake) Checks(project, repo string) []CheckRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.checks.records[project+"/"+repo])
}

// CheckWrites returns accepted native status mutations in order.
func (f *Fake) CheckWrites(project, repo string) []CheckRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.checks.writes[project+"/"+repo])
}

// FailNextCheckCreate loses one accepted status response.
func (f *Fake) FailNextCheckCreate() { f.mu.Lock(); defer f.mu.Unlock(); f.checks.failCreate = true }

// SetCheckIterations replaces a PR's iteration response with independent values.
func (f *Fake) SetCheckIterations(project, repo string, number int, items []CheckIteration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checks.iterations == nil {
		f.checks.iterations = map[string]map[int][]CheckIteration{}
	}
	key := project + "/" + repo
	if f.checks.iterations[key] == nil {
		f.checks.iterations[key] = map[int][]CheckIteration{}
	}
	f.checks.iterations[key][number] = slices.Clone(items)
}

func (f *Fake) checkRoutes(r chi.Router) {
	r.With(f.scoped(ScopeCodeStatus)).Get("/commits/{commit}/statuses", f.listChecks)
	r.With(f.scoped(ScopeCodeStatus)).Post("/commits/{commit}/statuses", f.setCheck)
	r.Get("/pullRequests/{request}/iterations", f.checkIterations)
	r.With(f.scoped(ScopeCodeStatus)).Get("/pullRequests/{request}/iterations/{iteration}/statuses", f.listChecks)
	r.With(f.scoped(ScopeCodeStatus)).Post("/pullRequests/{request}/iterations/{iteration}/statuses", f.setCheck)
}
func checkRepo(r *http.Request) string { return param(r, "project") + "/" + param(r, "repository") }
func checkRequest(r *http.Request) (int, int) {
	number, _ := strconv.Atoi(chi.URLParam(r, "request"))
	iteration, _ := strconv.Atoi(chi.URLParam(r, "iteration"))
	return number, iteration
}

func (f *Fake) checkIterations(w http.ResponseWriter, r *http.Request) {
	number, _ := checkRequest(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	items, ok := f.checks.iterations[checkRepo(r)][number]
	if !ok {
		answer(w, 404, problem("Pull request not found"))
		return
	}
	answer(w, 200, map[string]any{"count": len(items), "value": slices.Clone(items)})
}

func (f *Fake) listChecks(w http.ResponseWriter, r *http.Request) {
	number, iteration := checkRequest(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []CheckRecord{}
	for _, v := range f.checks.records[checkRepo(r)] {
		if v.PullRequest != number || v.Iteration != iteration || number == 0 && v.SHA != chi.URLParam(r, "commit") {
			continue
		}
		items = append(items, v)
	}
	if number == 0 {
		skip, _ := strconv.Atoi(r.URL.Query().Get("skip"))
		size, _ := strconv.Atoi(r.URL.Query().Get("top"))
		if size <= 0 {
			size = 1000
		}
		skip = min(max(skip, 0), len(items))
		items = items[skip:min(skip+size, len(items))]
	}
	answer(w, 200, map[string]any{"count": len(items), "value": items})
}

func (f *Fake) setCheck(w http.ResponseWriter, r *http.Request) {
	var body CheckRecord
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Context.Name == "" || !slices.Contains([]string{"pending", "succeeded", "failed", "notApplicable"}, body.State) {
		answer(w, 400, problem("Invalid status"))
		return
	}
	number, iteration := checkRequest(r)
	repo := repoOf(r)
	if number == 0 {
		commit, err := repo.repo.Resolve(chi.URLParam(r, "commit"))
		if err != nil {
			answer(w, 404, problem("Commit not found"))
			return
		}
		body.SHA = commit.Hash.String()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := checkRepo(r)
	if number > 0 {
		if body.Iteration != 0 && body.Iteration != iteration {
			answer(w, 400, problem("Iteration mismatch"))
			return
		}
		body.Iteration = iteration
		found := false
		for _, v := range f.checks.iterations[key][number] {
			if v.ID == iteration {
				body.SHA = v.Source.SHA
				found = true
			}
		}
		if !found {
			answer(w, 404, problem("Iteration not found"))
			return
		}
		body.PullRequest = number
	}
	if f.checks.records == nil {
		f.checks.records = map[string][]CheckRecord{}
		f.checks.writes = map[string][]CheckRecord{}
	}
	f.nextID++
	body.ID = f.nextID
	replaced := false
	for i, v := range f.checks.records[key] {
		if v.SHA == body.SHA && v.PullRequest == number && v.Iteration == iteration && v.Context == body.Context {
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
