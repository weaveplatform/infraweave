package app

import (
	"net/http"
)

// webhookAnswers preserves statuses and shared rate-limit headers, but every
// webhook answer is an empty provider-protocol response, including guard faults.
// It runs inside AccessLog and outside recovery so both observe the real status
// and no problem body or authentication challenge reaches a provider.
func webhookAnswers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if under(r.URL.Path, "/webhooks/vcs") {
			w = &webhookWriter{ResponseWriter: w}
		}
		next.ServeHTTP(w, r)
	})
}

type webhookWriter struct {
	http.ResponseWriter
	written bool
}

func (w *webhookWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	w.written = true
	w.Header().Del("Content-Type")
	w.Header().Del("Content-Length")
	w.Header().Del("WWW-Authenticate")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.ResponseWriter.WriteHeader(code)
}

func (w *webhookWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return len(b), nil
}

// Unwrap keeps response-controller deadlines on the shared listener connection.
func (w *webhookWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
