package deliveryfake_test

import (
	"crypto/fips140"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/deliveryfake"
)

func TestUnitWebhookSenderRetries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		statuses []int
		limit    int
	}{
		{"cloud retries twice", []int{500, 503, 202}, 3},
		{"cloud bounded", []int{599, 599, 599}, 3},
		{"dc one attempt", []int{500}, 1},
		{"unauthorized not retried", []int{401}, 3},
		{"rate limit not retried", []int{429}, 3},
		{"invalid retry bound defaults to one", []int{500}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			secret := []byte(strings.Repeat("s", 32))
			body := []byte(`{"event":"immutable"}`)
			calls := 0
			hook := ""
			r := chi.NewRouter()
			r.Post("/hook", func(w http.ResponseWriter, req *http.Request) {
				calls++
				b, err := io.ReadAll(req.Body)
				if err != nil {
					t.Error(err)
				}
				m := hmac.New(sha256.New, secret)
				_, _ = m.Write(body)
				if string(b) != string(body) || req.Header.Get("X-Hub-Signature") != "sha256="+hex.EncodeToString(m.Sum(nil)) {
					t.Error("body or exact HMAC changed across attempts")
				}
				if req.Header.Get("X-Request-UUID") != "same-id" || req.Header.Get("X-Event-Key") != "repo:push" || req.Header.Get("X-Attempt-Number") != strconv.Itoa(calls) {
					t.Error("delivery/event identity or attempt header changed")
				}
				if calls == 1 {
					hook = req.Header.Get("X-Hook-UUID")
				}
				if hook == "" || hook != req.Header.Get("X-Hook-UUID") {
					t.Error("unstable hook identity")
				}
				w.WriteHeader(tc.statuses[calls-1])
			})
			srv := httptest.NewServer(r)
			t.Cleanup(srv.Close)
			s := deliveryfake.New(
				secret,
				nil,
				deliveryfake.Config{DeliveryHeader: "X-Request-UUID", AttemptHeader: "X-Attempt-Number", HookHeader: "X-Hook-UUID", Attempts: tc.limit},
			)
			status, err := s.Send(t.Context(), srv.URL+"/hook", "same-id", "repo:push", body)
			if err != nil || status != tc.statuses[len(tc.statuses)-1] || calls != len(tc.statuses) {
				t.Fatalf("status=%d calls=%d err=%v", status, calls, err)
			}
			for i, a := range s.Attempts() {
				if a.DeliveryID != "same-id" || a.Event != "repo:push" || a.Number != i+1 || a.Status != tc.statuses[i] {
					t.Fatalf("attempt=%+v", a)
				}
			}
		})
	}
}

func TestUnitWebhookSenderCopiesAndRedelivery(t *testing.T) {
	t.Parallel()
	body, secret := []byte("original"), []byte(strings.Repeat("s", 32))
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write(body)
	want := "original/sha256=" + hex.EncodeToString(m.Sum(nil))
	var seen []string
	r := chi.NewRouter()
	r.Post("/hook", func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		seen = append(seen, string(b)+"/"+req.Header.Get("X-Hub-Signature"))
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	s := deliveryfake.New(secret, srv.Client(), deliveryfake.Config{DeliveryHeader: "X-Request-Id", Attempts: 1})
	secret[0] = 'x'
	if status, err := s.Send(t.Context(), srv.URL+"/hook", "delivery", "pr:opened", body); err != nil || status != 202 {
		t.Fatalf("send=%d %v", status, err)
	}
	body[0] = 'x'
	attempts := s.Attempts()
	attempts[0].Event = "mutated"
	if status, err := s.Redeliver(t.Context(), 0); err != nil || status != 202 {
		t.Fatalf("redeliver=%d %v", status, err)
	}
	if len(seen) != 2 || seen[0] != want || seen[1] != want || s.Attempts()[0].Event != "pr:opened" {
		t.Fatalf("snapshots not independent: %v", seen)
	}
	for _, index := range []int{-1, 2} {
		if _, err := s.Redeliver(t.Context(), index); !errors.Is(err, deliveryfake.ErrDelivery) {
			t.Fatal("invalid redelivery accepted")
		}
	}
}

func TestUnitWebhookSenderRefusals(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Post("/unsigned", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Hub-Signature") != "" {
			t.Error("nil secret must omit signature")
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	r.Post("/redirect", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/unsigned", http.StatusTemporaryRedirect)
	})
	srv := httptest.NewServer(r)
	client := srv.Client()
	client.Timeout = time.Second
	t.Cleanup(srv.Close)
	s := deliveryfake.New(nil, client, deliveryfake.Config{DeliveryHeader: "X-Request-Id", Attempts: 3})
	for _, path := range []string{"/unsigned", "/redirect"} {
		want := 401
		if path == "/redirect" {
			want = 307
		}
		if status, err := s.Send(t.Context(), srv.URL+path, "id", "event", nil); err != nil || status != want {
			t.Fatalf("send %s=%d %v", path, status, err)
		}
	}
	if client.CheckRedirect != nil || client.Timeout != time.Second {
		t.Fatal("caller client mutated")
	}
	if len(s.Attempts()) != 2 {
		t.Fatal("redirect or non5xx retried")
	}
	for _, meta := range [][2]string{{"", "event"}, {"id", ""}} {
		if _, err := s.Send(t.Context(), srv.URL, meta[0], meta[1], nil); !errors.Is(err, deliveryfake.ErrDelivery) {
			t.Fatal("empty metadata accepted")
		}
	}
	if _, err := s.Send(t.Context(), "://bad", "id", "event", nil); err == nil {
		t.Fatal("bad URL accepted")
	}
	srv.Close()
	if _, err := s.Send(t.Context(), srv.URL, "id", "event", nil); err == nil {
		t.Fatal("transport failure accepted")
	}
	if attempts := s.Attempts(); len(attempts) != 3 || attempts[2].Status != 0 {
		t.Fatal("transport failure retried or not recorded")
	}
}

func TestUnitWebhookSenderShortKey(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Post("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(202) })
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	s := deliveryfake.New([]byte("hook"), nil, deliveryfake.Config{DeliveryHeader: "X-Request-Id"})
	status, err := s.Send(t.Context(), srv.URL, "id", "event", nil)
	if fips140.Enforced() {
		if status != 0 || !errors.Is(err, deliveryfake.ErrDelivery) || len(s.Attempts()) != 0 {
			t.Fatal("policy-ineligible key reached HTTP")
		}
	} else if err != nil || status != 202 {
		t.Fatalf("ordinary HMAC refused: %d %v", status, err)
	}
}
