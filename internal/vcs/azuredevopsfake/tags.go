package azuredevopsfake

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (f *Fake) annotatedTag(w http.ResponseWriter, r *http.Request) {
	repo := repoOf(r)
	tag, err := repo.repo.TagObject(chi.URLParam(r, "object"))
	if err != nil {
		answer(w, http.StatusNotFound, map[string]string{"message": "Tag object not found"})
		return
	}
	answer(
		w,
		http.StatusOK,
		map[string]any{
			"name":         tag.Name,
			"message":      tag.Message,
			"objectId":     tag.Hash.String(),
			"taggedObject": map[string]string{"objectId": tag.Target.String(), "objectType": tag.TargetType.String()},
		},
	)
}
