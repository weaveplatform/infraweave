package vcslab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/go-github/v93/github"
)

var parkedHook = HookConfig{URL: "https://parked.example.com/hook", ContentType: "form", InsecureSSL: "0"}

const temporaryHook = "https://temporary.example.com/webhooks/vcs/github_enterprise/integration"

type hookFixture struct {
	current     HookConfig
	patches     []HookConfig
	lose        bool
	failRestore bool
	getErr      bool
	fresh       bool
}

func (f *hookFixture) Get(context.Context) (HookConfig, error) {
	if f.getErr {
		return HookConfig{}, errors.New("private-native-response")
	}
	return f.current, nil
}

func (f *hookFixture) Patch(ctx context.Context, v HookConfig) error {
	f.fresh = ctx.Err() == nil
	f.patches = append(f.patches, v)
	if f.failRestore && v == parkedHook {
		return errors.New("private-restore-response")
	}
	f.current = v
	if f.lose {
		f.lose = false
		return errors.New("private-lost-response")
	}
	return nil
}

func readyKey() AppKey {
	return AppKey{secret: "known-strong-signing-key-never-patched", Readiness: "v1:ready"}
}

func TestUnitAppHookLeaseNeverOverwritesUnknownSetup(t *testing.T) {
	for _, ready := range []string{"", "wrong"} {
		f := &hookFixture{current: parkedHook}
		_, err := LeaseHook(t.Context(), f, readyKey(), ready, temporaryHook, filepath.Join(t.TempDir(), "journal"))
		if !errors.Is(err, ErrHook) || len(f.patches) != 0 {
			t.Fatal("unready App configuration was mutated")
		}
	}
}

func TestUnitAppHookUncertainEffectRestoresAndKeepsFailedCleanupJournal(t *testing.T) {
	for _, fail := range []bool{false, true} {
		f := &hookFixture{current: parkedHook, lose: true, failRestore: fail}
		path := filepath.Join(t.TempDir(), "journal")
		_, err := LeaseHook(t.Context(), f, readyKey(), readyKey().Readiness, temporaryHook, path)
		if !errors.Is(err, ErrHook) || len(f.patches) != 2 {
			t.Fatal("uncertain PATCH was not restored")
		}
		_, statErr := os.Stat(path)
		if fail {
			if statErr != nil {
				t.Fatal("failed restore discarded cleanup journal")
			}
			f.failRestore = false
			if err := RestoreHook(t.Context(), f, path); err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			t.Fatal("successful restore retained journal")
		}
		if f.current != parkedHook {
			t.Fatal("original App configuration was not restored")
		}
	}
}

func TestUnitAppHookCanceledCleanupAndStaleOwner(t *testing.T) {
	f := &hookFixture{current: parkedHook}
	path := filepath.Join(t.TempDir(), "journal")
	l, err := LeaseHook(t.Context(), f, readyKey(), readyKey().Readiness, temporaryHook, path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("cleanup journal is not private")
	}
	f.current.URL = "https://new-owner.example.com/hook"
	if err := l.Restore(t.Context()); !errors.Is(err, ErrHook) || len(f.patches) != 1 {
		t.Fatal("stale cleanup replaced another owner's hook")
	}
	f.current.URL = temporaryHook
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := l.Restore(ctx); err != nil || !f.fresh || f.current != parkedHook {
		t.Fatal("cancellation prevented independent cleanup")
	}
}

func TestUnitAppHookJournalRefusesReplacement(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "journal")
		if symlink {
			if err := os.Symlink(filepath.Join(dir, "elsewhere"), path); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, []byte("owned"), 0o600); err != nil {
			t.Fatal(err)
		}
		f := &hookFixture{current: parkedHook}
		_, err := LeaseHook(t.Context(), f, readyKey(), readyKey().Readiness, temporaryHook, path)
		if !errors.Is(err, ErrHook) || len(f.patches) != 0 {
			t.Fatal("existing cleanup journal was replaced")
		}
	}
}

func TestUnitAppHookClientOmitsSecretOnRedirectAndRestore(t *testing.T) {
	stored := parkedHook
	secret := "established-secret-remains-unchanged"
	var bodies []map[string]json.RawMessage
	r := chi.NewRouter()
	r.Get("/app/hook/config", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": stored.URL, "content_type": stored.ContentType, "insecure_ssl": stored.InsecureSSL, "secret": "********"})
	})
	r.Patch("/app/hook/config", func(w http.ResponseWriter, req *http.Request) {
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(req.Body).Decode(&raw); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		bodies = append(bodies, raw)
		for name, value := range raw {
			switch name {
			case "url":
				_ = json.Unmarshal(value, &stored.URL)
			case "content_type":
				_ = json.Unmarshal(value, &stored.ContentType)
			case "insecure_ssl":
				_ = json.Unmarshal(value, &stored.InsecureSSL)
			case "secret":
				_ = json.Unmarshal(value, &secret)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stored)
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	base := srv.URL + "/"
	client, err := github.NewClient(github.WithHTTPClient(srv.Client()), github.WithURLs(&base, &base))
	if err != nil {
		t.Fatal(err)
	}
	l, err := LeaseHook(t.Context(), &appClient{client: client}, readyKey(), readyKey().Readiness, temporaryHook, filepath.Join(t.TempDir(), "journal"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || secret != "established-secret-remains-unchanged" || stored != parkedHook {
		t.Fatal("App hook lifecycle changed its signing secret or failed restoration")
	}
	for _, body := range bodies {
		if len(body) != 3 || body["secret"] != nil {
			t.Fatal("PATCH includes a secret, mask, null or extra field")
		}
		for _, name := range []string{"url", "content_type", "insecure_ssl"} {
			if _, ok := body[name]; !ok {
				t.Fatal("PATCH omitted a restorable field")
			}
		}
	}
	if reflect.DeepEqual(bodies[0], bodies[1]) {
		t.Fatal("redirect and restore did not exercise distinct configurations")
	}
}
