package githubfake

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// ErrRequest refuses a non-object native request document or invalid identity.
var ErrRequest = errors.New("githubfake: invalid native request fixture")

// SetPullRequest freezes the documented native GET response independently of
// delivery transport. This lets tests move or omit webhook facts without
// silently changing the server's authoritative request snapshot.
func (f *Fake) SetPullRequest(owner, repository string, number int, body []byte) error {
	var object map[string]json.RawMessage
	if owner == "" || repository == "" || number < 1 || json.Unmarshal(body, &object) != nil || object == nil {
		return ErrRequest
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nativeRequests == nil {
		f.nativeRequests = map[string]map[int]json.RawMessage{}
	}
	key := owner + "/" + repository
	if f.nativeRequests[key] == nil {
		f.nativeRequests[key] = map[int]json.RawMessage{}
	}
	f.nativeRequests[key][number] = slices.Clone(body)
	return nil
}

// SetPublicRepository permits native reads without an installation on this
// repository. Writes still require that installation's own repository reach.
func (f *Fake) SetPublicRepository(owner, name string, public bool) {
	repo := f.Repo(owner, name)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publicRepos == nil {
		f.publicRepos = map[*Repo]bool{}
	}
	f.publicRepos[repo] = public
}

func (f *Fake) nativeRequest(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(chi.URLParam(r, "request"))
	f.mu.Lock()
	tok := r.Context().Value(tokenKey).(token)          //nolint:forcetypeassert // asInstallation binds it
	in := r.Context().Value(installKey).(*Installation) //nolint:forcetypeassert // asInstallation binds it
	perms := tok.perms
	if perms == nil {
		perms = f.apps[in.AppID].Permissions
	}
	level := perms["pull_requests"]
	body, ok := f.nativeRequests[chi.URLParam(r, "owner")+"/"+chi.URLParam(r, "repo")][number]
	body = slices.Clone(body)
	f.mu.Unlock()
	if level != "read" && level != "write" {
		answer(w, http.StatusForbidden, message("Resource not accessible by integration"))
		return
	}
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
