package azuredevopsfake

import "net/http"

func (f *Fake) repositoryMetadata(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository)    //nolint:forcetypeassert // repository middleware binds it
	project := r.Context().Value(projectKey).(*project) //nolint:forcetypeassert // member binds it
	answer(w, http.StatusOK, map[string]any{"project": map[string]string{"id": project.id, "name": project.name}, "id": repo.id, "name": repo.name, "defaultBranch": "refs/heads/" + repo.repo.DefaultBranch()})
}
