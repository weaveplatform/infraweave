package http

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

type webhookReceiver interface {
	Receive(context.Context, string, string, http.Header, []byte) (int, error)
}

// WebhookPattern is the provider protocol route, outside the served JSON API.
const WebhookPattern = "/webhooks/vcs/{provider}/{integrationId}"

// Webhook is the hand-written intake adapter. Shared controls run before this
// handler; it bounds the body before looking up any integration.
func Webhook(s webhookReceiver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > intake.MaxBody {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, intake.MaxBody))
		if err != nil {
			code := http.StatusBadRequest
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				code = http.StatusRequestEntityTooLarge
			}
			w.WriteHeader(code)
			return
		}
		if s == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		code, err := s.Receive(r.Context(), chi.URLParam(r, "provider"), chi.URLParam(r, "integrationId"), r.Header, body)
		switch {
		case errors.Is(err, intake.ErrNotFound):
			code = http.StatusNotFound
		case errors.Is(err, intake.ErrVerification):
			code = http.StatusUnauthorized
		case err != nil:
			code = http.StatusServiceUnavailable
		}
		w.WriteHeader(code)
	})
}
