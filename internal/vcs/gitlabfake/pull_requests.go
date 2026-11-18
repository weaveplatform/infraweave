package gitlabfake

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (f *Fake) requestAuthor(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "user"), 10, 64)
	if err == nil {
		f.mu.Lock()
		for _, user := range f.users {
			if user.id == id {
				body := map[string]any{"id": user.id, "username": user.username, "name": user.username}
				f.mu.Unlock()
				answer(w, http.StatusOK, body)
				return
			}
		}
		f.mu.Unlock()
	}
	answer(w, http.StatusNotFound, message("404 User Not Found"))
}

func (f *Fake) requestUser(username string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.userOf(username)
	return map[string]any{"id": u.id, "username": u.username, "name": "Example Author", "email": username + "@example.com"}
}
