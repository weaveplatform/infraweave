package bitbucketcloudfake

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// ErrRequest refuses a non-object native request document or invalid identity.
var ErrRequest = errors.New("bitbucketcloudfake: invalid native request fixture")

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

func (f *Fake) nativeRequest(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member binds the repository
	number, err := strconv.Atoi(chi.URLParam(r, "request"))
	f.mu.Lock()
	body, ok := f.nativeRequests[repo.workspace+"/"+repo.slug][number]
	body = slices.Clone(body)
	f.mu.Unlock()
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
