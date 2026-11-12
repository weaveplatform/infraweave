package middleware

import "net/http"

// Uncacheable marks every answer Cache-Control: no-store and
// X-Content-Type-Options: nosniff before calling on, so an answer of any
// handler or control behind it, a refusal, a 404 or a panic's 500
// included, carries both: no browser or shared cache keeps a credential
// or a caller's state, and no browser reads a body as a type it was not
// served as.
//
// ref: decision 0003 § "API surfaces"
func Uncacheable(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
