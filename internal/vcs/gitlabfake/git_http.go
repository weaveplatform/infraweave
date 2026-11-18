package gitlabfake

import (
	"net/http"
	"slices"
	"strings"
	"time"
)

// gitHTTP serves the same project graph as the REST API, admitting only an
// authenticated repository reader. A minted project token cannot cross into
// another project, even if a fixture adds its bot as a member there.
func (f *Fake) gitHTTP(w http.ResponseWriter, r *http.Request) {
	projectPath := ""
	for _, suffix := range []string{".git/info/refs", ".git/git-upload-pack"} {
		if value, ok := strings.CutSuffix(r.URL.Path, suffix); ok {
			projectPath = strings.TrimPrefix(value, "/")
			break
		}
	}
	if projectPath == "" {
		http.NotFound(w, r)
		return
	}
	username, password, basic := r.BasicAuth()
	f.mu.Lock()
	tok := f.tokens[password]
	if !basic || username == "" || tok == nil || tok.mint != nil && tok.mint.Revoked {
		f.mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="GitLab"`)
		http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
		return
	}
	if !slices.Contains(tok.scopes, "read_repository") && !slices.Contains(tok.scopes, "api") {
		f.mu.Unlock()
		http.Error(w, "insufficient_scope", http.StatusForbidden)
		return
	}
	project := f.projects[projectPath]
	allowed := project != nil
	if allowed {
		_, allowed = project.members[tok.user.id]
	}
	if tok.mint != nil && (tok.mint.Project != projectPath ||
		tok.mint.ExpiresAt <= time.Now().UTC().Format(time.DateOnly)) {
		allowed = false
	}
	f.mu.Unlock()
	if !allowed {
		http.NotFound(w, r)
		return
	}
	project.repo.ServeUploadPack(w, r)
}
