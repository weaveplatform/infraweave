package bitbucketcloudfake

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (f *Fake) gitRoutes(r chi.Router) {
	r.Get("/{workspace}/{slug}.git/info/refs", f.gitHTTP)
	r.Post("/{workspace}/{slug}.git/git-upload-pack", f.gitHTTP)
}

// gitHTTP authenticates the API-token Git username separately from REST's
// email username, then serves only upload-pack from the same seeded graph.
func (f *Fake) gitHTTP(w http.ResponseWriter, r *http.Request) {
	username, password, basic := r.BasicAuth()
	f.mu.Lock()
	account := f.tokens[password]
	if !basic || username != "x-bitbucket-api-token-auth" || account == nil {
		f.mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="Bitbucket"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	repo := f.repos[chi.URLParam(r, "workspace")+"/"+chi.URLParam(r, "slug")]
	allowed := repo != nil && repo.roles[account.email] >= Read
	f.mu.Unlock()
	if !allowed {
		http.NotFound(w, r)
		return
	}
	repo.repo.ServeUploadPack(w, r)
}
