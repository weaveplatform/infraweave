package githubfake

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// DeploymentStatus records one native status in creation order.
type DeploymentStatus struct {
	ID    int64
	State string
}

// DeploymentRecord retains the native create request and its resulting identity.
// Pointer booleans and nil versus empty contexts preserve explicit request fields.
// Payload is the caller's native JSON payload, never an authentication header.
type DeploymentRecord struct {
	ID                    int64
	Ref, SHA, Task        string
	Environment           string
	Payload               json.RawMessage
	AutoMerge             *bool
	RequiredContexts      []string
	ProductionEnvironment *bool
	Statuses              []DeploymentStatus
}

// DeploymentWrite is one status mutation across all deployments of a repository.
type DeploymentWrite struct {
	ID    int64
	State string
}

type deploymentStore struct {
	records    map[string][]DeploymentRecord
	writes     map[string][]DeploymentWrite
	failCreate bool
}

func cloneDeployment(in DeploymentRecord) DeploymentRecord {
	in.Payload = bytes.Clone(in.Payload)
	in.RequiredContexts = slices.Clone(in.RequiredContexts)
	in.Statuses = slices.Clone(in.Statuses)
	if in.AutoMerge != nil {
		in.AutoMerge = new(*in.AutoMerge)
	}
	if in.ProductionEnvironment != nil {
		in.ProductionEnvironment = new(*in.ProductionEnvironment)
	}
	return in
}

// Deployments returns independent create/status snapshots in creation order.
func (f *Fake) Deployments(owner, repo string) []DeploymentRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.deployments.records[owner+"/"+repo])
	for i := range out {
		out[i] = cloneDeployment(out[i])
	}
	return out
}

// DeploymentWrites returns status mutations in the order the server accepted them.
func (f *Fake) DeploymentWrites(owner, repo string) []DeploymentWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.deployments.writes[owner+"/"+repo])
}

// FailNextDeploymentCreate loses one successful create's response after persisting
// it. A client must recover that native deployment before attempting another create.
func (f *Fake) FailNextDeploymentCreate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deployments.failCreate = true
}

func (f *Fake) deploymentRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(f.deploymentPermission)
		r.Get("/deployments", f.listDeployments)
		r.Post("/deployments", f.createDeployment)
		r.Get("/deployments/{deployment}/statuses", f.listDeploymentStatuses)
		r.Post("/deployments/{deployment}/statuses", f.createDeploymentStatus)
	})
}

func (f *Fake) deploymentPermission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := r.Context().Value(tokenKey).(token)            //nolint:forcetypeassert // asInstallation sets it
		in := r.Context().Value(installKey).(*Installation) //nolint:forcetypeassert // asInstallation sets it
		f.mu.Lock()
		perms := t.perms
		if perms == nil {
			perms = f.apps[in.AppID].Permissions
		}
		level := perms["deployments"]
		f.mu.Unlock()
		if level != "write" && (r.Method != http.MethodGet || level != "read") {
			answer(w, http.StatusForbidden, message("Resource not accessible by integration"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func deploymentRepo(r *http.Request) string {
	return chi.URLParam(r, "owner") + "/" + chi.URLParam(r, "repo")
}

func deploymentJSON(d DeploymentRecord) map[string]any {
	production := d.Environment == "production"
	if d.ProductionEnvironment != nil {
		production = *d.ProductionEnvironment
	}
	return map[string]any{
		"id": d.ID, "ref": d.Ref, "sha": d.SHA, "task": d.Task,
		"environment": d.Environment, "payload": d.Payload, "production_environment": production,
	}
}

func (f *Fake) listDeployments(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	list := f.deployments.records[deploymentRepo(r)]
	q := r.URL.Query()
	for i := len(list) - 1; i >= 0; i-- {
		d := list[i]
		if q.Get("sha") != "" && q.Get("sha") != d.SHA || q.Get("ref") != "" && q.Get("ref") != d.Ref ||
			q.Get("task") != "" && q.Get("task") != d.Task || q.Get("environment") != "" && q.Get("environment") != d.Environment {
			continue
		}
		items = append(items, deploymentJSON(d))
	}
	paged(w, r, items, func(page []map[string]any) any { return page })
}

func (f *Fake) createDeployment(w http.ResponseWriter, r *http.Request) {
	//nolint:tagliatelle // GitHub owns the native request field names.
	var body struct {
		Ref                   string          `json:"ref"`
		Task                  string          `json:"task"`
		Environment           string          `json:"environment"`
		Payload               json.RawMessage `json:"payload"`
		AutoMerge             *bool           `json:"auto_merge"`
		RequiredContexts      []string        `json:"required_contexts"`
		ProductionEnvironment *bool           `json:"production_environment"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Ref == "" {
		answer(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	repo := r.Context().Value(repoKey).(*Repo) //nolint:forcetypeassert // reachable sets it
	commit, err := repo.Resolve(body.Ref)
	if err != nil {
		answer(w, http.StatusUnprocessableEntity, message("No ref found"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deployments.records == nil {
		f.deployments.records = map[string][]DeploymentRecord{}
	}
	f.nextID++
	d := DeploymentRecord{
		ID: f.nextID, Ref: body.Ref, SHA: commit.Hash.String(), Task: orDeployment(body.Task, "deploy"),
		Environment: orDeployment(body.Environment, "production"), Payload: body.Payload,
		AutoMerge: body.AutoMerge, RequiredContexts: body.RequiredContexts, ProductionEnvironment: body.ProductionEnvironment,
	}
	if len(d.Payload) == 0 {
		d.Payload = json.RawMessage(`{}`)
	}
	key := deploymentRepo(r)
	f.deployments.records[key] = append(f.deployments.records[key], d)
	if f.deployments.failCreate {
		f.deployments.failCreate = false
		answer(w, http.StatusBadGateway, message("Upstream response lost"))
		return
	}
	answer(w, http.StatusCreated, deploymentJSON(d))
}

func orDeployment(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func (f *Fake) deploymentIndex(r *http.Request) (string, int) {
	key := deploymentRepo(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "deployment"), 10, 64)
	if err != nil {
		return key, -1
	}
	return key, slices.IndexFunc(f.deployments.records[key], func(d DeploymentRecord) bool { return d.ID == id })
}

func (f *Fake) listDeploymentStatuses(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, index := f.deploymentIndex(r)
	if index < 0 {
		answer(w, http.StatusNotFound, message("Not Found"))
		return
	}
	items := []map[string]any{}
	list := f.deployments.records[key][index].Statuses
	for i := len(list) - 1; i >= 0; i-- {
		items = append(items, map[string]any{"id": list[i].ID, "state": list[i].State})
	}
	paged(w, r, items, func(page []map[string]any) any { return page })
}

func (f *Fake) createDeploymentStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State string `json:"state"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || !slices.Contains([]string{"error", "failure", "inactive", "in_progress", "queued", "pending", "success"}, body.State) {
		answer(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key, index := f.deploymentIndex(r)
	if index < 0 {
		answer(w, http.StatusNotFound, message("Not Found"))
		return
	}
	f.nextID++
	status := DeploymentStatus{ID: f.nextID, State: body.State}
	f.deployments.records[key][index].Statuses = append(f.deployments.records[key][index].Statuses, status)
	if f.deployments.writes == nil {
		f.deployments.writes = map[string][]DeploymentWrite{}
	}
	f.deployments.writes[key] = append(f.deployments.writes[key], DeploymentWrite{ID: f.deployments.records[key][index].ID, State: body.State})
	answer(w, http.StatusCreated, map[string]any{"id": status.ID, "state": status.State})
}
