package gitlabfake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// EnvironmentRecord is one native environment belonging to a project.
type EnvironmentRecord struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// DeploymentRecord retains native deployment identity and accepted state changes.
// No invented per-run payload or credential enters the native response.
type DeploymentRecord struct {
	ID          int64
	Environment EnvironmentRecord
	Ref, SHA    string
	Tag         bool
	Status      string
	States      []string
}

// DeploymentWrite records one accepted create/update in chronological order.
type DeploymentWrite struct {
	ID    int64
	State string
}

type deploymentStore struct {
	environments map[string][]EnvironmentRecord
	records      map[string][]DeploymentRecord
	writes       map[string][]DeploymentWrite
	failCreate   bool
}

// Environments returns an independent snapshot in creation order.
func (f *Fake) Environments(namespace, name string) []EnvironmentRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.deployments.environments[namespace+"/"+name])
}

// Deployments returns independent snapshots in creation order.
func (f *Fake) Deployments(namespace, name string) []DeploymentRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.deployments.records[namespace+"/"+name])
	for i := range out {
		out[i].States = slices.Clone(out[i].States)
	}
	return out
}

// DeploymentWrites returns all accepted deployment mutations in order.
func (f *Fake) DeploymentWrites(namespace, name string) []DeploymentWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.deployments.writes[namespace+"/"+name])
}

// FailNextDeploymentCreate returns one 502 after the native row is created.
// Retry tests recover it by listing the documented environment/ref/SHA fields.
func (f *Fake) FailNextDeploymentCreate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deployments.failCreate = true
}

func (f *Fake) deploymentRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(f.deploymentPermission)
		r.Get("/environments", f.listEnvironments)
		r.Post("/environments", f.createEnvironment)
		r.Get("/deployments", f.listDeployments)
		r.Post("/deployments", f.createDeployment)
		r.Put("/deployments/{deployment}", f.updateDeployment)
	})
}

func (f *Fake) deploymentPermission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
		tok := r.Context().Value(tokenKey).(*token)   //nolint:forcetypeassert // authenticated sets it
		f.mu.Lock()
		level := p.members[tok.user.id]
		f.mu.Unlock()
		if r.Method != http.MethodGet && (!slices.Contains(tok.scopes, "api") || level < Developer) {
			answer(w, http.StatusForbidden, message("403 Forbidden"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (f *Fake) listEnvironments(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	for _, environment := range f.deployments.environments[p.fullPath()] {
		if name := r.URL.Query().Get("name"); name != "" && environment.Name != name {
			continue
		}
		items = append(items, map[string]any{"id": environment.ID, "name": environment.Name, "state": "available"})
	}
	paged(w, r, items)
}

func (f *Fake) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Name == "" {
		answer(w, http.StatusBadRequest, message("name is required"))
		return
	}
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	f.mu.Lock()
	defer f.mu.Unlock()
	key := p.fullPath()
	if slices.ContainsFunc(f.deployments.environments[key], func(e EnvironmentRecord) bool { return e.Name == body.Name }) {
		answer(w, http.StatusBadRequest, message("Name has already been taken"))
		return
	}
	if f.deployments.environments == nil {
		f.deployments.environments = map[string][]EnvironmentRecord{}
	}
	f.nextID++
	environment := EnvironmentRecord{ID: f.nextID, Name: body.Name}
	f.deployments.environments[key] = append(f.deployments.environments[key], environment)
	answer(w, http.StatusCreated, map[string]any{"id": environment.ID, "name": environment.Name, "state": "available"})
}

func deploymentJSON(d DeploymentRecord) map[string]any {
	return map[string]any{"id": d.ID, "environment": d.Environment, "ref": d.Ref, "sha": d.SHA, "tag": d.Tag, "status": d.Status}
}

func (f *Fake) listDeployments(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	list := f.deployments.records[p.fullPath()]
	for i := len(list) - 1; i >= 0; i-- {
		d := list[i]
		if environment := r.URL.Query().Get("environment"); environment != "" && d.Environment.Name != environment {
			continue
		}
		if status := r.URL.Query().Get("status"); status != "" && d.Status != status {
			continue
		}
		// Native deployments list has no ref or SHA filters. Recovery must
		// compare those returned fields itself, across every page.
		items = append(items, deploymentJSON(d))
	}
	if r.URL.Query().Get("sort") != "desc" {
		slices.Reverse(items)
	}
	paged(w, r, items)
}

func deploymentStatus(status string) bool {
	return slices.Contains([]string{"running", "success", "failed", "canceled"}, status)
}

func (f *Fake) createDeployment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Environment string `json:"environment"`
		SHA         string `json:"sha"`
		Ref         string `json:"ref"`
		Tag         *bool  `json:"tag"`
		Status      string `json:"status"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Environment == "" || body.SHA == "" || body.Ref == "" || body.Tag == nil || !deploymentStatus(body.Status) {
		answer(w, http.StatusBadRequest, message("environment, sha, ref, tag and valid status are required"))
		return
	}
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	f.mu.Lock()
	defer f.mu.Unlock()
	key := p.fullPath()
	environments := f.deployments.environments[key]
	index := slices.IndexFunc(environments, func(e EnvironmentRecord) bool { return e.Name == body.Environment })
	if index < 0 {
		// The native create endpoint creates the environment when absent.
		if f.deployments.environments == nil {
			f.deployments.environments = map[string][]EnvironmentRecord{}
		}
		f.nextID++
		environments = append(environments, EnvironmentRecord{ID: f.nextID, Name: body.Environment})
		f.deployments.environments[key] = environments
		index = len(environments) - 1
	}
	f.nextID++
	d := DeploymentRecord{ID: f.nextID, Environment: environments[index], Ref: body.Ref, SHA: body.SHA, Tag: *body.Tag, Status: body.Status, States: []string{body.Status}}
	if f.deployments.records == nil {
		f.deployments.records = map[string][]DeploymentRecord{}
	}
	f.deployments.records[key] = append(f.deployments.records[key], d)
	f.recordDeploymentWrite(key, d.ID, body.Status)
	if f.deployments.failCreate {
		f.deployments.failCreate = false
		answer(w, http.StatusBadGateway, message("Upstream response lost"))
		return
	}
	answer(w, http.StatusCreated, deploymentJSON(d))
}

func (f *Fake) recordDeploymentWrite(key string, id int64, state string) {
	if f.deployments.writes == nil {
		f.deployments.writes = map[string][]DeploymentWrite{}
	}
	f.deployments.writes[key] = append(f.deployments.writes[key], DeploymentWrite{ID: id, State: state})
}

func (f *Fake) updateDeployment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || !deploymentStatus(body.Status) {
		answer(w, http.StatusBadRequest, message("valid status is required"))
		return
	}
	p := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member sets it
	id, err := strconv.ParseInt(chi.URLParam(r, "deployment"), 10, 64)
	f.mu.Lock()
	defer f.mu.Unlock()
	key := p.fullPath()
	index := slices.IndexFunc(f.deployments.records[key], func(d DeploymentRecord) bool { return d.ID == id })
	if err != nil || index < 0 {
		answer(w, http.StatusNotFound, message("404 Deployment Not Found"))
		return
	}
	d := &f.deployments.records[key][index]
	d.Status, d.States = body.Status, append(d.States, body.Status)
	f.recordDeploymentWrite(key, d.ID, body.Status)
	answer(w, http.StatusOK, deploymentJSON(*d))
}
