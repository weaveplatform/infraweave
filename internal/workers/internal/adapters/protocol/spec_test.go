package protocol_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// get sends GET path with token as its bearer, when set, and answers the
// status and the decoded body.
func (f *fixture) get(t *testing.T, path, token string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	out := map[string]any{}
	if raw, _ := io.ReadAll(w.Body); len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s answered %q", path, raw)
		}
	}
	return w.Code, out
}

// TestUnitProtocolRunSpec checks that a run's specification answers each
// part of the handover, its state backend among them, a write-only
// variable and file by name alone, and sealed, which the pool's private
// key opens to the handover's secrets for the run's state version.
func TestUnitProtocolRunSpec(t *testing.T) {
	f := newFixture(t)
	f.store.pool.Certificate = f.cert.Raw
	run := kernel.NewULID()
	at := time.Unix(1_800_000_000, 0)
	root, image, login, value, mode := "infra", "ghcr.io/acme/runner", "alice", "eu", "600"
	doc := []byte(`{"environment":{"TOKEN":"s3cret"}}`)
	f.svc.BindScheduler(&scheduler{handover: workersapi.Handover{
		Account: f.store.pool.Account, Pool: f.store.pool.ID, Secrets: crypt.NewValue(doc),
		Spec: workersapi.Spec{
			Run: workersapi.SpecRun{
				Account:      f.store.pool.Account,
				ID:           run,
				Type:         kernel.RunTypeTracked,
				State:        kernel.RunStatePreparing,
				StateVersion: 3,
				TriggeredBy:  "alice",
				Priority:     kernel.RunPriorityNormal,
				CreatedAt:    at,
			},
			Stack: workersapi.SpecStack{
				Slug: "infra", Name: "Infra", Space: "legacy", Repository: "infra",
				Branch: "main", ProjectRoot: &root, Vendor: "terraform",
				VendorConfig:                 json.RawMessage(`{"version":"1.9"}`),
				EnableWellKnownSecretMasking: true,
			},
			Commit: workersapi.SpecCommit{
				Hash: "abc", URL: "https://example.com/abc", Message: "Fix", AuthorName: "Alice",
				AuthorLogin: &login, Timestamp: at,
			},
			RuntimeConfig: json.RawMessage(`{"projectRoot":"infra"}`),
			RunnerImage:   &image,
			StateBackend:  &workersapi.StateBackend{Path: "/state/v1/stacks/" + run.String()},
			Source: workersapi.SpecSource{
				Kind: workersapi.SourceGit, URL: "https://git.example.com/a.git", Narrowed: true,
			},
			Tool:  &workersapi.SpecTool{Name: "terraform", Version: "1.14.2", Download: true},
			Hooks: map[string][]string{"beforeInit": {"echo hi"}},
			Environment: []workersapi.Variable{
				{Name: "REGION", Value: &value}, {Name: "TOKEN", WriteOnly: true},
			},
			Files: []workersapi.File{
				{Path: "a.tf", Checksum: "x", Content: []byte("terraform {}")},
				{Path: "key.pem", Mode: &mode, Checksum: "y", WriteOnly: true},
			},
		},
	}})
	code, body := f.get(t, "/runs/"+run.String()+"/spec", "run."+run.String())
	if code != http.StatusOK {
		t.Fatalf("spec = %d %v", code, body)
	}
	raw, _ := body["sealed"].(string)
	sealed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := f.key.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := seal.OpenEnvelope(priv, run.String(), 3, sealed); err != nil ||
		string(plain) != string(doc) {
		t.Errorf("opened %q, %v", plain, err)
	}
	delete(body, "sealed")
	want := map[string]any{
		"run": map[string]any{
			"account":        f.store.pool.Account.String(),
			"id":             run.String(),
			"type":           "TRACKED",
			"state":          "PREPARING",
			"stateVersion":   3.0,
			"triggeredBy":    "alice",
			"priority":       "normal",
			"driftDetection": false,
			"createdAt":      1.8e9,
		},
		"stack": map[string]any{
			"id": "infra", "name": "Infra", "space": "legacy", "labels": []any{},
			"repository": "infra", "branch": "main", "projectRoot": "infra",
			"vendor": "terraform", "vendorConfig": map[string]any{"version": "1.9"},
			"enableWellKnownSecretMasking": true,
		},
		"commit": map[string]any{
			"hash": "abc", "url": "https://example.com/abc", "message": "Fix",
			"authorName": "Alice", "authorLogin": "alice", "timestamp": 1.8e9,
		},
		"runtimeConfig": map[string]any{"projectRoot": "infra"},
		"runnerImage":   "ghcr.io/acme/runner",
		"stateBackend":  map[string]any{"path": "/state/v1/stacks/" + run.String()},
		"source": map[string]any{
			"kind": "git", "url": "https://git.example.com/a.git", "narrowed": true,
		},
		"tool":  map[string]any{"name": "terraform", "version": "1.14.2", "download": true},
		"hooks": map[string]any{"beforeInit": []any{"echo hi"}},
		"environment": []any{
			map[string]any{"name": "REGION", "value": "eu", "writeOnly": false},
			map[string]any{"name": "TOKEN", "writeOnly": true},
		},
		"files": []any{
			map[string]any{
				"path": "a.tf", "checksum": "x", "writeOnly": false,
				"content": base64.StdEncoding.EncodeToString([]byte("terraform {}")),
			},
			map[string]any{
				"path":      "key.pem",
				"fileMode":  "600",
				"checksum":  "y",
				"writeOnly": true,
			},
		},
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("spec = %v\nwant %v", body, want)
	}
}

// TestUnitProtocolRunSpecRefused checks that the specification refuses
// no token, another run's token and a malformed run ID with 401, a run
// not on a worker with 409 invalid-state, and a template that fails to
// render with 422; and that a handover with nothing optional answers
// empty hooks, environment and files rather than null, and no state
// backend.
func TestUnitProtocolRunSpecRefused(t *testing.T) {
	f := newFixture(t)
	f.store.pool.Certificate = f.cert.Raw
	run := kernel.NewULID()
	sch := &scheduler{handover: workersapi.Handover{
		Pool: f.store.pool.ID, Secrets: crypt.NewValue([]byte(`{}`)),
		Spec: workersapi.Spec{Run: workersapi.SpecRun{ID: run}},
	}}
	f.svc.BindScheduler(sch)
	path := "/runs/" + run.String() + "/spec"
	for name, tc := range map[string]struct{ path, token string }{
		"no token":          {path, ""},
		"another run's":     {path, "run." + kernel.NewULID().String()},
		"a malformed run":   {"/runs/nope/spec", "run." + run.String()},
		"a session token's": {path, "session." + kernel.NewULID().String()},
	} {
		if code, body := f.get(t, tc.path, tc.token); code != http.StatusUnauthorized ||
			body["type"] != "urn:infraweave:problem:unauthenticated" {
			t.Errorf("%s = %d %v", name, code, body)
		}
	}
	code, body := f.get(t, path, "run."+run.String())
	if code != http.StatusOK || !reflect.DeepEqual(body["hooks"], map[string]any{}) ||
		!reflect.DeepEqual(body["environment"], []any{}) ||
		!reflect.DeepEqual(body["files"], []any{}) || body["runtimeConfig"] != nil ||
		body["stateBackend"] != nil {
		t.Errorf("a bare handover = %d %v", code, body)
	}
	sch.err = workersapi.ErrNotOnWorker
	if code, body := f.get(t, path, "run."+run.String()); code != http.StatusConflict ||
		body["type"] != "urn:infraweave:problem:invalid-state" {
		t.Errorf("not on a worker = %d %v", code, body)
	}
	sch.err = &kernel.Invalid{Detail: "The variable URL's template does not render."}
	if code, body := f.get(t, path, "run."+run.String()); code != http.StatusUnprocessableEntity ||
		body["type"] != "urn:infraweave:problem:validation-failed" {
		t.Errorf("a template that fails = %d %v", code, body)
	}
}
