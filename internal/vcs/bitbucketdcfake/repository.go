package bitbucketdcfake

import "net/http"

func (f *Fake) repositoryMetadata(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member binds it
	answer(w, http.StatusOK, map[string]any{"id": repo.id, "slug": repo.slug, "project": map[string]string{"key": repo.project}})
}
