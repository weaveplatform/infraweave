package bitbucketcloudfake

import "net/http"

func (f *Fake) repositoryMetadata(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member binds the repository
	answer(
		w,
		http.StatusOK,
		map[string]any{
			"uuid":       repo.id,
			"slug":       repo.slug,
			"full_name":  repo.workspace + "/" + repo.slug,
			"name":       repo.slug,
			"mainbranch": map[string]string{"name": repo.repo.DefaultBranch()},
		},
	)
}
