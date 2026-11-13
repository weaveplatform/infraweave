package client_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/agenttest"
	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// doer answers every request with fn.
type doer func(*http.Request) (*http.Response, error)

func (d doer) Do(r *http.Request) (*http.Response, error) { return d(r) }

func TestUnitClientBase(t *testing.T) {
	for _, public := range []string{"https://iw.example.com", "https://iw.example.com/"} {
		if got := client.Base(public); got != "https://iw.example.com/worker/v1" {
			t.Errorf("Base(%q) = %q", public, got)
		}
	}
	c := client.New("http://[::1", http.DefaultClient)
	if err := c.Deregister(t.Context(), "s"); err == nil || client.Status(err) != 0 {
		t.Fatalf("a call to a base that is no URL: %v", err)
	}
	if _, err := c.Workspace(t.Context(), "t", "r"); err == nil || client.Status(err) != 0 {
		t.Fatalf("a workspace from a base that is no URL: %v", err)
	}
}

func TestUnitClientCalls(t *testing.T) {
	pool, err := agenttest.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := agenttest.NewServer(pool)
	c := client.New(client.Base(agenttest.Endpoint), srv.Client())
	ctx := t.Context()
	run := agenttest.Run{Assignment: wire.Assignment{
		Run: "run-1", RunToken: "token", State: wire.AssignmentStatePREPARING, StateVersion: 2,
	}}
	if err := srv.Assign(run); err != nil {
		t.Fatal(err)
	}
	spec, err := c.Spec(ctx, "token", "run-1")
	if err != nil || len(spec.Sealed) == 0 {
		t.Fatalf("Spec: %+v, %v", spec, err)
	}
	step, err := c.Report(ctx, "token", "run-1", wire.RunReport{
		StateVersion: 2, State: wire.WorkerStateINITIALIZING, Event: wire.Started,
	})
	if err != nil || step.Action != wire.Execute || step.StateVersion != 3 {
		t.Fatalf("Report: %+v, %v", step, err)
	}
	body := []byte("a line\n")
	sum := sha256.Sum256(body)
	ups, err := c.UploadURLs(ctx, "token", "run-1", wire.UploadURLsRequest{
		StateVersion: 3, Uploads: []wire.Upload{{
			Kind: wire.UploadKindLogChunk, Name: "0", Size: int64(len(body)),
			Sha256: hex.EncodeToString(sum[:]),
		}},
	})
	if err != nil || len(ups) != 1 || ups[0].Name != "0" {
		t.Fatalf("UploadURLs: %+v, %v", ups, err)
	}
	if err := c.Put(ctx, ups[0], []byte("another")); client.Status(err) != http.StatusBadRequest ||
		!strings.HasPrefix(err.Error(), "upload 0: 400 ") {
		t.Fatalf("Put of other bytes: %v", err)
	}
	if err := c.Put(ctx, ups[0], body); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.LogChunks(ctx, "token", "run-1", wire.LogChunksRequest{
		StateVersion: 3, Chunks: []wire.LogChunk{{Seq: 0, FirstAt: 1, LastAt: 2}},
	}); err != nil {
		t.Fatalf("LogChunks: %v", err)
	}
	if got := srv.Chunks(); len(got) != 1 || string(got[0].Body) != "a line\n" {
		t.Fatalf("indexed %+v", got)
	}
	if _, err := c.Workspace(ctx, "token", "run-1"); client.Status(err) != http.StatusNotFound {
		t.Fatalf("Workspace of a run that saved none: %v", err)
	}
	saved := filepath.Join(t.TempDir(), "workspace")
	if err := os.WriteFile(saved, []byte("sealed"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256([]byte("sealed"))
	ups, err = c.UploadURLs(ctx, "token", "run-1", wire.UploadURLsRequest{
		StateVersion: 3, Uploads: []wire.Upload{{
			Kind: wire.UploadKindWorkspace, Name: "workspace", Size: 6,
			Sha256: hex.EncodeToString(sum[:]),
		}},
	})
	if err != nil || len(ups) != 1 {
		t.Fatalf("UploadURLs of a workspace: %+v, %v", ups, err)
	}
	rec := wire.ArtifactsRequest{StateVersion: 3, Artifacts: []wire.ArtifactRecord{{
		Kind: wire.ArtifactRecordKindWorkspace, Name: "workspace",
	}}}
	if err := c.Artifacts(ctx, "token", "run-1", rec); client.Status(err) !=
		http.StatusUnprocessableEntity {
		t.Fatalf("Artifacts before the upload: %v", err)
	}
	if err := c.PutFile(ctx, ups[0], saved); err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if err := c.Artifacts(ctx, "token", "run-1", rec); err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	if got := srv.Artifacts(); len(got) != 1 || string(got[0].Body) != "sealed" ||
		got[0].Kind != wire.ArtifactRecordKindWorkspace || got[0].StateVersion != 3 {
		t.Fatalf("recorded %+v", got)
	}
	loc, err := c.Workspace(ctx, "token", "run-1")
	if err != nil || loc != ups[0].Url {
		t.Fatalf("Workspace = %q, %v; want %q", loc, err, ups[0].Url)
	}
	for auth, want := range map[string]int{
		"": http.StatusOK, "Bearer token": http.StatusForbidden,
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("the store's GET with %q: %d", auth, res.StatusCode)
		}
	}
	if err := c.Artifacts(ctx, "token", "run-1", rec); err != nil {
		t.Fatalf("Artifacts of a recorded workspace again: %v", err)
	}
	if _, err := c.UploadURLs(ctx, "token", "run-1", wire.UploadURLsRequest{
		StateVersion: 3, Uploads: []wire.Upload{{
			Kind: wire.UploadKindWorkspace, Name: "workspace", Size: 6,
			Sha256: hex.EncodeToString(sum[:]),
		}},
	}); client.Status(err) != http.StatusConflict {
		t.Fatalf("UploadURLs of a recorded workspace: %v", err)
	}
	if err := c.Put(ctx, wire.SignedUpload{
		Name: "x", Url: agenttest.Endpoint + "/store/missing",
		Headers: map[string]string{"Content-Type": agenttest.ContentType},
	}, nil); client.Status(err) != http.StatusForbidden {
		t.Fatalf("Put of an object never declared: %v", err)
	}
	srv.Refuse("artifacts", http.StatusServiceUnavailable, 1)
	srv.Refuse("workspace", http.StatusServiceUnavailable, 1)
	srv.Refuse("get", http.StatusServiceUnavailable, 1)
	if err := c.Artifacts(ctx, "token", "run-1", rec); client.Status(err) !=
		http.StatusServiceUnavailable {
		t.Errorf("a refused record: %v", err)
	}
	if _, err := c.Workspace(ctx, "token", "run-1"); client.Status(err) !=
		http.StatusServiceUnavailable {
		t.Errorf("a refused workspace: %v", err)
	}
	for _, c := range []struct {
		url  string
		want int
	}{
		{loc, http.StatusServiceUnavailable},
		{agenttest.Endpoint + "/store/workspace/gone", http.StatusNotFound},
	} {
		url, want := c.url, c.want
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("the store's GET of %s: %d, want %d", url, res.StatusCode, want)
		}
	}

	for op, call := range map[string]func() error{
		"register":   func() error { _, err := c.Register(ctx, "not a JWS"); return err },
		"poll":       func() error { _, err := c.Poll(ctx, "nobody", wire.PollRequest{}); return err },
		"deregister": func() error { return c.Deregister(ctx, "nobody") },
		"fetch the run's specification": func() error {
			_, err := c.Spec(ctx, "wrong", "run-1")
			return err
		},
		"report INITIALIZING started": func() error {
			_, err := c.Report(ctx, "wrong", "run-1", wire.RunReport{
				State: wire.WorkerStateINITIALIZING, Event: wire.Started,
			})
			return err
		},
		"declare uploads": func() error {
			_, err := c.UploadURLs(ctx, "wrong", "run-1", wire.UploadURLsRequest{})
			return err
		},
		"commit log chunks": func() error {
			return c.LogChunks(ctx, "wrong", "run-1", wire.LogChunksRequest{})
		},
		"record artifacts": func() error {
			return c.Artifacts(ctx, "wrong", "run-1", wire.ArtifactsRequest{})
		},
		"find the saved workspace": func() error {
			_, err := c.Workspace(ctx, "wrong", "run-1")
			return err
		},
	} {
		err := call()
		var e *client.Error
		if !errors.As(err, &e) || e.Op != op || e.Status != http.StatusUnauthorized ||
			e.Detail == "" ||
			client.Status(err) != http.StatusUnauthorized ||
			!strings.HasPrefix(err.Error(), op+": 401 ") {
			t.Errorf("%s: %v", op, err)
		}
	}
}

func TestUnitClientFailures(t *testing.T) {
	ctx := t.Context()
	down := client.New(
		"http://iw.invalid/worker/v1",
		doer(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("unreachable")
		}),
	)
	plain := client.New(
		"http://iw.invalid/worker/v1",
		doer(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     http.Header{"Content-Type": {"text/html"}},
				Body:       http.NoBody,
				Request:    r,
			}, nil
		}),
	)
	for _, c := range []*client.Client{down, plain} {
		for name, err := range map[string]error{
			"register":   func() error { _, err := c.Register(ctx, "jws"); return err }(),
			"poll":       func() error { _, err := c.Poll(ctx, "s", wire.PollRequest{}); return err }(),
			"deregister": c.Deregister(ctx, "s"),
			"spec":       func() error { _, err := c.Spec(ctx, "t", "r"); return err }(),
			"report":     func() error { _, err := c.Report(ctx, "t", "r", wire.RunReport{}); return err }(),
			"uploads": func() error {
				_, err := c.UploadURLs(ctx, "t", "r", wire.UploadURLsRequest{})
				return err
			}(),
			"put":       c.Put(ctx, wire.SignedUpload{Url: "http://store.invalid/k"}, nil),
			"bad url":   c.Put(ctx, wire.SignedUpload{Url: "http://[::1"}, nil),
			"chunks":    c.LogChunks(ctx, "t", "r", wire.LogChunksRequest{}),
			"artifacts": c.Artifacts(ctx, "t", "r", wire.ArtifactsRequest{}),
			"workspace": func() error { _, err := c.Workspace(ctx, "t", "r"); return err }(),
			"no file":   c.PutFile(ctx, wire.SignedUpload{Url: "http://store.invalid/k"}, "/gone"),
			"a file": c.PutFile(ctx, wire.SignedUpload{Url: "http://store.invalid/k"},
				"client_test.go"),
		} {
			if err == nil {
				t.Errorf("%s succeeded", name)
			}
		}
	}
	if err := plain.Put(ctx, wire.SignedUpload{
		Name: "0", Url: "http://store.invalid/k?X-Amz-Signature=secret",
	}, nil); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("a store's refusal: %v", err)
	}
	httpDown := client.New("http://iw.invalid/worker/v1", http.DefaultClient)
	if err := httpDown.Put(ctx, wire.SignedUpload{
		Name: "0", Url: "http://127.0.0.1:1/k?X-Amz-Signature=secret",
	}, nil); err == nil || strings.Contains(err.Error(), "secret") ||
		!strings.HasPrefix(err.Error(), "upload 0: ") {
		t.Errorf("an unreachable store: %v", err)
	}
	err := plain.Deregister(ctx, "s")
	if client.Status(err) != http.StatusBadGateway || err.Error() != "deregister: 502 Bad Gateway" {
		t.Fatalf("a refusal without a problem: %v", err)
	}
	if client.Status(errors.New("other")) != 0 {
		t.Fatal("Status of an error that is no refusal")
	}
}
