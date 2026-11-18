package azuredevopsfake

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (f *Fake) gitRoutes(r chi.Router) {
	r.Get("/"+Organization+"/{project}/_git/{repository}/info/refs", f.gitHTTP)
	r.Post("/"+Organization+"/{project}/_git/{repository}/git-upload-pack", f.gitHTTP)
}

// gitHTTP requires the native Code scope and project-reader membership. The
// read-only Git graph is the same one the organization's REST routes expose.
func (f *Fake) gitHTTP(w http.ResponseWriter, r *http.Request) {
	username, password, basic := r.BasicAuth()
	f.mu.Lock()
	token := f.tokens[password]
	if !basic || username == "" || token == nil {
		f.mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="Azure DevOps"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if !token.holds(ScopeCode) {
		f.mu.Unlock()
		http.Error(w, "code scope required", http.StatusForbidden)
		return
	}
	project := f.projects[param(r, "project")]
	var repo *repository
	if project != nil && project.roles[token.user] >= Reader {
		repo = project.repos[param(r, "repository")]
	}
	f.mu.Unlock()
	if repo == nil {
		http.NotFound(w, r)
		return
	}
	repo.repo.ServeUploadPack(w, r)
}
