package github_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitCheckActionCollaboratorPermission(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	for _, tc := range []struct {
		name, permission string
		want             bool
	}{
		{"write", "write", true}, {"admin", "admin", true}, {"read", "read", false}, {"triage", "read", false}, {"none", "none", false}, {"unknown", "custom-role", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l.fake.SetCollaboratorPermission("acme", "infra", "developer", tc.permission)
			got, err := l.gh.CanWrite(t.Context(), l.repo.Repo, "developer")
			if err != nil || got != tc.want {
				t.Fatal("native collaborator permission", got, err)
			}
			if len(l.fake.CheckWrites("acme", "infra")) != 0 {
				t.Fatal("permission read mutated checks")
			}
		})
	}
	if ok, err := l.gh.CanWrite(t.Context(), l.repo.Repo, ""); ok || !errors.Is(err, port.ErrCheckActionTarget) {
		t.Fatal("empty sender", ok, err)
	}
	l.in.Credential = []byte("invalid private key")
	if ok, err := l.gh.CanWrite(t.Context(), l.repo.Repo, "developer"); ok || !errors.Is(err, port.ErrCredential) {
		t.Fatal("provider denial authorized sender", ok, err)
	}
}

func TestUnitRecordedCheckPressKeepsNativeIdentityAndSignature(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	external := kernel.NewULID().String()
	id, err := l.gh.Set(
		t.Context(),
		l.repo.Repo,
		l.repo.Head,
		port.CheckReport{
			Name:       "infraweave/actions",
			State:      port.CheckSuccess,
			Title:      "Plan",
			Summary:    "Ready",
			URL:        "https://infra.example/run",
			ExternalID: external,
			RecoveryID: "infraweave/run:original",
			Actions:    []port.CheckAction{{Label: "Preview", Description: "Propose", Identifier: "preview"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	native, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var events int
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		if _, err := l.gh.Verify(r.Header, body, [][]byte{[]byte(l.app.WebhookSecret)}); err != nil {
			w.WriteHeader(401)
			return
		}
		parsed, err := l.gh.Parse(r.Header, body)
		if err != nil || len(parsed) != 1 || parsed[0].CheckAction == nil || parsed[0].CheckAction.ExternalID != external || parsed[0].Head != l.repo.Head ||
			parsed[0].Author != "developer" ||
			parsed[0].CheckAction.Action != "preview" {
			w.WriteHeader(422)
			return
		}
		mu.Lock()
		events++
		mu.Unlock()
		w.WriteHeader(202)
	}))
	defer hook.Close()
	if status, err := l.fake.PressCheck(t.Context(), hook.URL, "new-press", "acme", "infra", native, "preview", "developer"); err != nil || status != 202 {
		t.Fatal("recorded signed press", status, err)
	}
	if _, err := l.fake.CheckActionPayload("acme", "other", native, "preview", "developer"); err == nil {
		t.Fatal("another repository adopted check identity")
	}
	mu.Lock()
	count := events
	mu.Unlock()
	if count != 1 || len(l.fake.CheckWrites("acme", "infra")) != 1 {
		t.Fatal("press retried or mutated check", count)
	}
}
