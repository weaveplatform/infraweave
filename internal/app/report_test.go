package app

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	workerapi "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// runCalls sends a claimed run's calls through the API listener's
// handler with the run's token.
type runCalls struct {
	h     http.Handler
	token string
	path  string
}

func (c runCalls) send(t *testing.T, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, target, r)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

func (c runCalls) post(t *testing.T, call string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return c.send(t, http.MethodPost, c.path+"/"+call, body)
}

// report reports ev of state at version v and checks the answer.
func (c runCalls) report(t *testing.T, v int64, state, ev string, action workerapi.NextStepAction,
	next string, nextVersion int64,
) {
	t.Helper()
	rec := c.post(t, "reports", map[string]any{"stateVersion": v, "state": state, "event": ev})
	var step workerapi.NextStep
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &step) != nil ||
		step != (workerapi.NextStep{Action: action, State: next, StateVersion: nextVersion}) {
		t.Fatalf("%s %s at %d = %d %s", state, ev, v, rec.Code, rec.Body)
	}
}

// upload declares body as kind name at version v, PUTs it to the signed
// URL through the fs driver's routes, and answers its key.
func (c runCalls) upload(t *testing.T, v int64, kind, name string, body []byte) string {
	t.Helper()
	sum := sha256.Sum256(body)
	rec := c.post(t, "upload-urls", map[string]any{"stateVersion": v, "uploads": []map[string]any{{
		"kind": kind, "name": name, "size": len(body), "sha256": hex.EncodeToString(sum[:]),
	}}})
	var signed workerapi.UploadURLs
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &signed) != nil ||
		len(signed.Uploads) != 1 {
		t.Fatalf("upload-urls %s %s = %d %s", kind, name, rec.Code, rec.Body)
	}
	u := signed.Uploads[0]
	target, err := url.Parse(u.Url)
	if err != nil {
		t.Fatal(err)
	}
	put := httptest.NewRequestWithContext(t.Context(), http.MethodPut, target.RequestURI(),
		bytes.NewReader(body))
	for k, v := range u.Headers {
		put.Header.Set(k, v)
	}
	put.ContentLength = int64(len(body))
	putRec := httptest.NewRecorder()
	c.h.ServeHTTP(putRec, put)
	if putRec.Code/100 != 2 {
		t.Fatalf("PUT %s = %d %s", u.Key, putRec.Code, putRec.Body)
	}
	return u.Key
}

// TestAccRunReports checks a claimed run's reports and uploads as the
// server wires the runs and workers domains over PostgreSQL and the fs
// object store. The first started report ends the handover and its
// repeat answers alike; upload URLs write under the run's prefix, for the
// declared size; a log chunk is indexed once; an artifact the runner
// seals with the content key derived from the specification's artifact
// key opens on the server; the saved workspace answers 307 to its bytes;
// a stale report answers 409; a planning report without its delta, or
// another with one, answers 400; with autodeploy on, the plan decision
// moves the run to APPLYING, keeps its delta and answers its repeat
// alike, and the apply finishes it; another run's token answers 401. The account's purge deletes the chunks and
// records and marks their objects for deletion.
func TestAccRunReports(t *testing.T) {
	w := newClaimWorld(t)
	ctx := t.Context()
	pool, key := w.certified(t)
	run := w.run(t, w.acct, pool, runSpec{kernel.RunTypeTracked, kernel.RunPriorityNormal, false},
		time.Now())
	p := w.poll(ctx, 0, w.worker(t, pool, false), false, 5)
	if claimed(t, p) != run {
		t.Fatalf("the claim took %+v, want %s", p.a, run)
	}
	c := config.Defaults().Server
	calls := runCalls{
		h:     API(discard(), c, memory(c), w.d, Platform{}, w.blobs),
		token: p.a.RunToken, path: WorkerBaseURL + "/runs/" + run.String(),
	}
	artifactKey := openArtifactKey(t, calls, key, run.String(), p.a.StateVersion)

	v := p.a.StateVersion
	calls.report(t, v, "INITIALIZING", "started", workerapi.Execute, "INITIALIZING", v+1)
	calls.report(t, v, "INITIALIZING", "started", workerapi.Execute, "INITIALIZING", v+1)
	v++

	chunk := []byte("Initializing the backend...\n")
	chunkKey := calls.upload(t, v, "log-chunk", "0", chunk)
	prefix := w.acct.String() + "/" + run.String() + "/INITIALIZING-" + strconv.FormatInt(
		v,
		10,
	) + "/"
	if chunkKey != prefix+"0" {
		t.Errorf("the chunk's key is %s, want it under %s", chunkKey, prefix)
	}
	for range 2 {
		if rec := calls.post(t, "log-chunks", map[string]any{
			"stateVersion": v,
			"chunks":       []map[string]any{{"seq": 0, "firstAt": 1, "lastAt": 2}},
		}); rec.Code !=
			http.StatusNoContent {
			t.Fatalf("log-chunks = %d %s", rec.Code, rec.Body)
		}
	}
	var chunks int
	if err := w.q.QueryRow(ctx, `SELECT count(*) FROM run_log_chunks WHERE run_id = $1 AND
		bytes = $2`, run, len(chunk)).Scan(&chunks); err != nil || chunks != 1 {
		t.Errorf("%d chunks indexed, %v", chunks, err)
	}
	rec := calls.post(t, "log-chunks", map[string]any{
		"stateVersion": v,
		"chunks":       []map[string]any{{"seq": 1, "firstAt": 1, "lastAt": 2}},
	})
	var invalid problem.Problem
	if rec.Code != http.StatusUnprocessableEntity ||
		json.Unmarshal(rec.Body.Bytes(), &invalid) != nil ||
		len(invalid.Errors) != 1 ||
		invalid.Errors[0].Pointer != "/chunks/0" {
		t.Errorf("an undeclared chunk = %d %s", rec.Code, rec.Body)
	}

	plan := []byte(`{"resource_changes":[]}`)
	planKey := prefix + "plan.json"
	content, err := seal.ArtifactKey(artifactKey, planKey)
	if err != nil {
		t.Fatal(err)
	}
	var sealed bytes.Buffer
	sw, err := seal.NewWriter(&sealed, content)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sw.Write(plan); err != nil || sw.Close() != nil {
		t.Fatal(err)
	}
	if got := calls.upload(t, v, "artifact", "plan.json", sealed.Bytes()); got != planKey {
		t.Errorf("the artifact's key is %s", got)
	}
	workspace := []byte("an encrypted workspace")
	calls.upload(t, v, "workspace", "ws", workspace)
	if rec := calls.post(t, "artifacts", map[string]any{
		"stateVersion": v,
		"artifacts": []map[string]any{
			{"kind": "artifact", "name": "plan.json"},
			{"kind": "workspace", "name": "ws"},
		},
	}); rec.Code != http.StatusNoContent {
		t.Fatalf("artifacts = %d %s", rec.Code, rec.Body)
	}
	opened, err := w.blobs.Objects.Open(ctx, objectstore.RunArtifacts, planKey)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(opened)
	_ = opened.Close()
	if !bytes.Equal(got, plan) {
		t.Errorf("the server opened the artifact to %q", got)
	}
	rec = calls.send(t, http.MethodGet, calls.path+"/workspace", nil)
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("workspace = %d %s", rec.Code, rec.Body)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	fetched := calls.send(t, http.MethodGet, loc.RequestURI(), nil)
	if !bytes.Equal(fetched.Body.Bytes(), workspace) {
		t.Errorf("the workspace's URL answers %d %q", fetched.Code, fetched.Body)
	}

	calls.report(t, v, "INITIALIZING", "succeeded", workerapi.Execute, "PLANNING", v+1)
	rec = calls.post(t, "reports", map[string]any{
		"stateVersion": v - 1,
		"state":        "INITIALIZING", "event": "failed",
	})
	wantProblem(t, rec, http.StatusConflict, "moved on")
	v++
	delta := map[string]any{"add": 1, "change": 0, "delete": 0, "import": 0, "outputs": false}
	wantProblem(t, calls.post(t, "reports", map[string]any{
		"stateVersion": v, "state": "PLANNING", "event": "succeeded",
	}), http.StatusBadRequest, "delta")
	wantProblem(t, calls.post(t, "reports", map[string]any{
		"stateVersion": v, "state": "PLANNING", "event": "failed", "delta": delta,
	}), http.StatusBadRequest, "delta")
	if _, err := w.q.Exec(ctx, `UPDATE stacks SET autodeploy = true WHERE id = $1`,
		w.stacks["one"]); err != nil {
		t.Fatal(err)
	}
	planned := map[string]any{
		"stateVersion": v, "state": "PLANNING", "event": "succeeded", "delta": delta,
	}
	for range 2 {
		rec = calls.post(t, "reports", planned)
		var step workerapi.NextStep
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &step) != nil ||
			step != (workerapi.NextStep{
				Action: workerapi.Execute, State: "APPLYING", StateVersion: v + 1,
			}) {
			t.Fatalf("the plan decision = %d %s", rec.Code, rec.Body)
		}
	}
	var kept string
	if err := w.q.QueryRow(ctx, `SELECT delta->>'add' FROM runs WHERE id = $1`, run).
		Scan(&kept); err != nil || kept != "1" {
		t.Errorf("the run kept %q of its delta, %v", kept, err)
	}
	v++
	other := runCalls{
		h: calls.h, token: calls.token,
		path: WorkerBaseURL + "/runs/" + kernel.NewULID().String(),
	}
	wantProblem(t, other.post(t, "reports", map[string]any{
		"stateVersion": v,
		"state":        "APPLYING", "event": "failed",
	}), http.StatusUnauthorized, "")
	calls.report(t, v, "APPLYING", "succeeded", workerapi.Exit, "FINISHED", v+1)
	wantProblem(t, calls.post(t, "reports", map[string]any{
		"stateVersion": v + 1,
		"state":        "APPLYING", "event": "failed",
	}), http.StatusConflict, "not on a worker")

	if err := w.d.Runs.Purge(ctx, w.acct); err != nil {
		t.Fatal(err)
	}
	var rows, deleting int
	if err := w.q.QueryRow(ctx, `SELECT (SELECT count(*) FROM run_log_chunks) +
		(SELECT count(*) FROM run_artifacts), (SELECT count(*) FROM blob_objects
		WHERE state = 'deleting' AND key = ANY($1))`,
		[]string{chunkKey, planKey, prefix + "ws"}).Scan(&rows, &deleting); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || deleting != 3 {
		t.Errorf("after the purge %d rows are left and %d objects are deleting", rows, deleting)
	}
}

// openArtifactKey fetches the run's specification and opens its sealed
// part with the pool's private key, as a launcher does, for the artifact
// key.
func openArtifactKey(t *testing.T, c runCalls, key *ecdsa.PrivateKey, run string,
	version int64,
) []byte {
	t.Helper()
	rec := c.send(t, http.MethodGet, c.path+"/spec", nil)
	var spec workerapi.RunSpec
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &spec) != nil {
		t.Fatalf("spec = %d %s", rec.Code, rec.Body)
	}
	priv, err := key.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := seal.OpenEnvelope(priv, run, version, spec.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	var secrets workerapi.RunSpecSecrets
	if err := json.Unmarshal(plain, &secrets); err != nil {
		t.Fatal(err)
	}
	return secrets.ArtifactKey
}
