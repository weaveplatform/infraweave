package bitbucketdcfake

import (
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (f *Fake) gitRoutes(r chi.Router) {
	r.Get("/scm/{project}/{slug}.git/info/refs", f.gitHTTP)
	r.Post("/scm/{project}/{slug}.git/git-upload-pack", f.gitHTTP)
}

// gitHTTP preserves project-key case insensitivity and repository-token scope.
// Even a fixture granting a minted bot another membership cannot widen it.
func (f *Fake) gitHTTP(w http.ResponseWriter, r *http.Request) {
	username, password, basic := r.BasicAuth()
	f.mu.Lock()
	user := f.tokens[password]
	if !basic || user == nil || username == "" {
		f.mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="Bitbucket"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var repo *repository
	for _, candidate := range f.repos {
		if strings.EqualFold(candidate.project, chi.URLParam(r, "project")) && candidate.slug == chi.URLParam(r, "slug") {
			repo = candidate
			break
		}
	}
	allowed := repo != nil && repo.roles[user.name] >= Read
	if id, minted := strings.CutPrefix(user.name, "access-token-"); minted {
		index := slices.IndexFunc(f.minted, func(m *Minted) bool { return m.ID == id })
		allowed = allowed && username == "x-token-auth" && index >= 0
		if allowed {
			m := f.minted[index]
			allowed = !m.Revoked && m.Project == repo.project && m.Slug == repo.slug && slices.Contains(m.Permissions, "REPO_READ")
		}
	} else {
		allowed = allowed && username == user.name
	}
	f.mu.Unlock()
	if !allowed {
		http.NotFound(w, r)
		return
	}
	repo.repo.ServeUploadPack(w, r)
}
