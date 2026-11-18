package azuredevopsfake

import "net/http"

func (f *Fake) repositoryMetadata(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // repository middleware binds it
	answer(w, http.StatusOK, map[string]string{"id": repo.id, "name": repo.name, "defaultBranch": "refs/heads/" + repo.repo.DefaultBranch()})
}
