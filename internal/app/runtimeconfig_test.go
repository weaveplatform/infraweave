package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	workerapi "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
)

// TestAccRunConfigurationCommitsWithSpec fails after the runs domain has
// prepared its snapshot and artifact key, while the worker domain reads
// the pool certificate to seal the answer. Neither write survives; a
// retry records the settings it resolves then, with its sealed answer.
func TestAccRunConfigurationCommitsWithSpec(t *testing.T) {
	w := newRunSpecWorld(t)
	pool, _ := w.certified(t)
	run := w.run(t, w.acct, pool,
		runSpec{kernel.RunTypeTracked, kernel.RunPriorityNormal, false}, time.Now())
	p := w.poll(t.Context(), 0, w.worker(t, pool, false), false, 5)
	if claimed(t, p) != run {
		t.Fatalf("the claim took %+v, want %s", p.a, run)
	}
	var certificate []byte
	if err := w.q.QueryRow(t.Context(), `SELECT certificate FROM worker_pools WHERE id = $1`,
		pool).Scan(&certificate); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults().Server
	h := API(discard(), c, memory(c), w.d, Platform{}, Blobs{})
	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			WorkerBaseURL+"/runs/"+run.String()+"/spec", nil)
		req.Header.Set("Authorization", "Bearer "+p.a.RunToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	stored := func() (record, key bool) {
		t.Helper()
		if err := w.q.QueryRow(t.Context(), `SELECT resolved_config IS NOT NULL,
			artifact_key_sealed IS NOT NULL FROM runs WHERE account_id = $1 AND id = $2`,
			w.acct, run).Scan(&record, &key); err != nil {
			t.Fatal(err)
		}
		return record, key
	}
	w.exec(t, `UPDATE worker_pools SET certificate = $1 WHERE id = $2`, []byte{0}, pool)
	if rec := get(); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a pool that cannot seal the answer = %d %s", rec.Code, rec.Body)
	}
	if record, key := stored(); record || key {
		t.Fatalf("the failed answer committed its record=%t or artifact key=%t", record, key)
	}
	w.exec(t, `UPDATE worker_pools SET certificate = $1 WHERE id = $2`, certificate, pool)
	w.exec(t, `UPDATE stacks SET terraform_version = '~> 1.9.0' WHERE id = $1`, w.stacks["one"])
	rec := get()
	var spec workerapi.RunSpec
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &spec) != nil ||
		spec.Tool == nil || spec.Tool.Version != "1.9.8" || len(spec.Sealed) == 0 {
		t.Fatalf("the successful retry did not resolve and seal its settings: %d %s", rec.Code, rec.Body)
	}
	if record, key := stored(); !record || !key {
		t.Fatalf("the successful answer lacks its record=%t or artifact key=%t", record, key)
	}
}
