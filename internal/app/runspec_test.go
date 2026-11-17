package app

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	workerapi "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/test/deps"
)

// The three specification tests share a real Git server. Claim-only tests
// need no repository; TestMain closes this lazy dependency after the suite.
var runSpecDependencies deps.Dependencies

func newRunSpecWorld(t *testing.T) *claimWorld {
	t.Helper()
	if err := deps.DockerReachable(t.Context()); err != nil {
		if os.Getenv(sqltest.RequireDocker) != "1" {
			t.Skipf("Gitea: %v (set %s=1 to fail instead)", err, sqltest.RequireDocker)
		}
		t.Fatalf("Gitea: %v", err)
	}
	g, err := runSpecDependencies.Gitea(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.VCS.CAFile = g.CAFile
	w := newClaimWorldConfig(t, c)
	name := "run-spec-" + strconv.FormatInt(runSpecDependencies.Serial(), 10)
	commit, err := g.CreateRepository(t.Context(), name, map[string]string{"main.tf": "terraform {}\n"})
	if err != nil {
		t.Fatal(err)
	}
	w.commit = commit
	for _, stack := range w.stacks {
		w.exec(t, `UPDATE stacks SET provider = 'GIT', repository = $3, repository_url = $4
			WHERE account_id = $1 AND id = $2`, w.acct, stack, name, g.CloneURL(name))
	}
	// A missing config is a successful source read. In particular, the sealing
	// rollback test must reach its broken certificate rather than fail in VCS.
	if _, found, err := w.d.VCS.RunSources().File(t.Context(), w.acct, w.stacks["one"], commit, ".infraweave/config.yml"); err != nil || found {
		t.Fatalf("the real commit's absent runtime configuration: found=%t, %v", found, err)
	}
	return w
}

// TestAccRunSpec checks the run specification as the server wires the
// runs, workers, configuration and access domains. A worker's claim of a
// run on a stack with variables answers a run token and the run's state
// version, with which the worker API answers the run's specification: its
// templates rendered against the stack's variables and the stack, a
// write-only variable and one whose template reads it by name alone, and
// their values, the in-run API token and the artifact key sealed to the
// pool's certificate's key for that state version, the same key on every
// fetch, and the state backend and catalogue release fixed at the first
// handover. Later changes to the stack's constraint or managed-state flag
// leave that configuration unchanged, even when no release now matches.
// Another run's specification answers the token 401, and the run once it
// leaves its worker 409.
func TestAccRunSpec(t *testing.T) {
	w := newRunSpecWorld(t)
	ctx := t.Context()
	pool, key := w.certified(t)
	for _, v := range []api.ConfigInput{
		{Id: "REGION", Value: "eu"},
		{Id: "TOKEN", Value: "s3cret", WriteOnly: true},
		{Id: "URL", Value: "https://${{ env.REGION }}.example.com/${{ stack.id }}"},
		{Id: "AUTH", Value: "Bearer ${{ env.TOKEN }}"},
	} {
		v.Type = api.ENVIRONMENTVARIABLE
		if _, err := w.d.Configuration.API().
			StackConfigAdd(w.admin, api.StackConfigAddRequestObject{
				StackId: "one", Body: &v,
			}); err != nil {
			t.Fatal(err)
		}
	}
	run := w.run(t, w.acct, pool, runSpec{kernel.RunTypeTracked, kernel.RunPriorityNormal, false},
		time.Now())
	p := w.poll(ctx, 0, w.worker(t, pool, false), false, 5)
	if claimed(t, p) != run {
		t.Fatalf("the claim took %+v, want %s", p.a, run)
	}
	stack := w.stacks["one"]
	w.exec(t, `UPDATE stacks SET manages_state = true WHERE id = $1`, stack)

	c := config.Defaults().Server
	h := API(discard(), c, memory(c), w.d, Platform{}, Blobs{})
	get := func(id kernel.ULID) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet,
			WorkerBaseURL+"/runs/"+id.String()+"/spec", nil)
		req.Header.Set("Authorization", "Bearer "+p.a.RunToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	priv, err := key.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	open := func() (workerapi.RunSpec, workerapi.RunSpecSecrets) {
		t.Helper()
		rec := get(run)
		var spec workerapi.RunSpec
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &spec) != nil {
			t.Fatalf("spec = %d %s", rec.Code, rec.Body)
		}
		plain, err := seal.OpenEnvelope(priv, run.String(), p.a.StateVersion, spec.Sealed)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(plain))
		dec.DisallowUnknownFields()
		var secrets workerapi.RunSpecSecrets
		if err := dec.Decode(&secrets); err != nil {
			t.Fatal(err)
		}
		return spec, secrets
	}
	spec, secrets := open()
	if spec.Run.Id != run.String() || spec.Run.StateVersion != p.a.StateVersion ||
		spec.Stack.Id != "one" || spec.Stack.Space != "kept" {
		t.Errorf("run %+v of stack %+v", spec.Run, spec.Stack)
	}
	plain, hidden := map[string]string{}, map[string]bool{}
	for _, v := range spec.Environment {
		if v.WriteOnly {
			hidden[v.Name] = v.Value == nil
			continue
		}
		plain[v.Name] = *v.Value
	}
	if plain["REGION"] != "eu" || plain["URL"] != "https://eu.example.com/one" ||
		plain["TF_VAR_infraweave_run_id"] != run.String() || !hidden["TOKEN"] || !hidden["AUTH"] {
		t.Errorf("variables %v, write-only %v", plain, hidden)
	}
	if secrets.Environment["TOKEN"] != "s3cret" || secrets.Environment["AUTH"] != "Bearer s3cret" ||
		len(secrets.Environment) != 2 || secrets.ApiToken == "" ||
		secrets.ApiTokenExpiresAt <= time.Now().Unix() || len(secrets.ArtifactKey) != 32 {
		t.Errorf("secrets %+v", secrets)
	}
	if _, again := open(); !bytes.Equal(again.ArtifactKey, secrets.ArtifactKey) {
		t.Error("a second fetch made another artifact key")
	}
	if spec.Tool == nil || *spec.Tool != (workerapi.RunSpecTool{
		Name: workerapi.Terraform, Version: "1.14.2", Download: true,
	}) || spec.Source.Kind != workerapi.Git {
		t.Errorf("tool %+v, source %+v", spec.Tool, spec.Source)
	}
	w.exec(t, `UPDATE stacks SET terraform_version = '~> 1.9.0' WHERE id = $1`, stack)
	if spec, _ := open(); spec.Tool == nil || spec.Tool.Version != "1.14.2" {
		t.Errorf("a changed stack constraint replaced the recorded tool %+v", spec.Tool)
	}
	w.exec(t, `UPDATE stacks SET terraform_version = '~> 0.1.0' WHERE id = $1`, stack)
	if spec, _ := open(); spec.Tool == nil || spec.Tool.Version != "1.14.2" {
		t.Errorf("an unmatched current constraint replaced the recorded tool %+v", spec.Tool)
	}
	w.exec(t, `UPDATE stacks SET terraform_version = NULL WHERE id = $1`, stack)
	w.exec(t, `UPDATE stacks SET manages_state = false WHERE id = $1`, stack)
	if spec, _ := open(); spec.StateBackend == nil ||
		spec.StateBackend.Path != "/state/v1/stacks/"+stack.String() {
		t.Errorf("a changed stack setting replaced the recorded backend %+v", spec.StateBackend)
	}
	w.exec(t, `UPDATE stacks SET manages_state = true WHERE id = $1`, stack)
	if spec, _ := open(); spec.StateBackend == nil ||
		spec.StateBackend.Path != "/state/v1/stacks/"+stack.String() {
		t.Errorf("a stack with managed state has the backend %+v", spec.StateBackend)
	}

	wantProblem(t, get(kernel.NewULID()), http.StatusUnauthorized, "credential is refused")
	w.exec(t, `UPDATE runs SET state = 'FINISHED' WHERE id = $1`, run)
	wantProblem(t, get(run), http.StatusConflict, "not on a worker")
}

// certified inserts a pool of the world's account whose certificate
// certifies a P-256 key, and answers the pool and the key.
func (w *claimWorld) certified(t *testing.T) (kernel.ULID, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	pool := w.pool(t, w.acct, nil)
	w.exec(t, `UPDATE worker_pools SET certificate = $1 WHERE id = $2`, der, pool)
	return pool, key
}
