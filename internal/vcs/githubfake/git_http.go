package githubfake

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// gitRoutes exposes the same seeded graph to the real source fetcher. Only
// upload-pack is served; installation and repository restrictions remain the
// existing API middleware's, with contents permission required for Git reads.
func (f *Fake) gitRoutes(r chi.Router) {
	r.Get("/{owner}/{repo}.git/info/refs", f.gitHTTP)
	r.Post("/{owner}/{repo}.git/git-upload-pack", f.gitHTTP)
}

func (f *Fake) gitHTTP(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok || username == "" || password == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	request := r.Clone(r.Context())
	request.Header.Set("Authorization", "Bearer "+password)
	f.asInstallation(f.reachable(http.HandlerFunc(f.gitUpload))).ServeHTTP(w, request)
}

func (f *Fake) gitUpload(w http.ResponseWriter, r *http.Request) {
	tok := r.Context().Value(tokenKey).(token)          //nolint:forcetypeassert // asInstallation sets it
	in := r.Context().Value(installKey).(*Installation) //nolint:forcetypeassert // asInstallation sets it
	f.mu.Lock()
	permissions := tok.perms
	if permissions == nil && f.apps[in.AppID] != nil {
		permissions = f.apps[in.AppID].Permissions
	}
	contents := permissions["contents"]
	f.mu.Unlock()
	if contents != "read" && contents != "write" {
		http.Error(w, "contents permission required", http.StatusForbidden)
		return
	}
	repo := r.Context().Value(repoKey).(*Repo) //nolint:forcetypeassert // reachable sets it
	repo.ServeUploadPack(w, r)
}
