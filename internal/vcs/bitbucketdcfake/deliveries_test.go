package bitbucketdcfake_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
)

func TestUnitWebhookFixtures(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Post("/", func(w http.ResponseWriter, req *http.Request) {
		b, err := io.ReadAll(req.Body)
		if err != nil || !json.Valid(b) || req.Header.Get("X-Request-Id") != "delivery" || req.Header.Get("X-Event-Key") != "repo:refs_changed" ||
			!strings.HasPrefix(req.Header.Get("X-Hub-Signature"), "sha256=") {
			t.Error("sender did not post documented signed request")
		}
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	s := bitbucketdcfake.NewSender([]byte("bitbucket-hook-secret-32-bytes-long"), srv.Client())
	b, err := bitbucketdcfake.Payload("repo:refs_changed")
	if err != nil {
		t.Fatal(err)
	}
	if status, err := s.Send(t.Context(), srv.URL, "delivery", "repo:refs_changed", b); err != nil || status != 202 {
		t.Fatalf("send=%d %v", status, err)
	}
	if status, err := s.Redeliver(t.Context(), 0); err != nil || status != 202 {
		t.Fatalf("redeliver=%d %v", status, err)
	}
	if attempts := s.Attempts(); len(attempts) != 2 || attempts[0].Status != 202 || attempts[1].DeliveryID != "delivery" {
		t.Fatalf("attempts=%+v", attempts)
	}
	if b, err := bitbucketdcfake.Payload("diagnostics:ping"); err != nil || !json.Valid(b) {
		t.Fatal("filter fixture invalid")
	}
	if _, err := bitbucketdcfake.Payload("unknown"); !errors.Is(err, bitbucketdcfake.ErrEvent) {
		t.Fatal("unknown builder event accepted")
	}
}
