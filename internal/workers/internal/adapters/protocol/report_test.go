package protocol_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// reports answers each call with its fields, keeping what it was given,
// and fails with err.
type reports struct {
	report  workersapi.Report
	version int64
	uploads []workersapi.Upload
	chunks  []workersapi.LogChunk
	recs    []workersapi.Recorded
	err     error
}

func (r *reports) Report(_ context.Context, _ kernel.ULID,
	rep workersapi.Report,
) (workersapi.Step, error) {
	r.report = rep
	return workersapi.Step{
		Action: workersapi.ActionExecute, State: kernel.RunStatePlanning,
		StateVersion: rep.StateVersion + 1,
	}, r.err
}

func (r *reports) Uploads(_ context.Context, _ kernel.ULID, v int64,
	u []workersapi.Upload,
) ([]workersapi.SignedUpload, error) {
	r.version, r.uploads = v, u
	return []workersapi.SignedUpload{{
		Kind: u[0].Kind, Name: u[0].Name, Key: "a/r/PLANNING-4/0", URL: "https://store.test/put",
		Header: map[string]string{"X-Amz-Checksum-Sha256": "sum"},
	}}, r.err
}

func (r *reports) LogChunks(_ context.Context, _ kernel.ULID, v int64,
	c []workersapi.LogChunk,
) error {
	r.version, r.chunks = v, c
	return r.err
}

func (r *reports) Artifacts(_ context.Context, _ kernel.ULID, v int64,
	rec []workersapi.Recorded,
) error {
	r.version, r.recs = v, rec
	return r.err
}

func (r *reports) Workspace(context.Context, kernel.ULID) (string, error) {
	return "https://store.test/get", r.err
}

// TestUnitProtocolRunReports checks a claimed run's reports and uploads
// as the worker API carries them: each request reaches the runs domain
// as it was sent, each answer is the runs domain's, and each of its
// refusals is answered with its problem.
func TestUnitProtocolRunReports(t *testing.T) {
	f := newFixture(t)
	r := &reports{}
	f.svc.BindReports(r)
	run := kernel.NewULID()
	token, path := "run."+run.String(), "/runs/"+run.String()
	sum := strings.Repeat("ab", 32)

	code, step := f.call(t, path+"/reports", token, map[string]any{
		"stateVersion": 4, "state": "PLANNING", "event": "failed", "details": "exit code 1",
	})
	if code != http.StatusOK || step["action"] != "execute" || step["state"] != "PLANNING" ||
		step["stateVersion"] != float64(5) {
		t.Errorf("report = %d %v", code, step)
	}
	if r.report != (workersapi.Report{
		StateVersion: 4, State: kernel.RunStatePlanning,
		Event: workersapi.ReportFailed, Details: "exit code 1",
	}) {
		t.Errorf("reported %+v", r.report)
	}
	code, _ = f.call(t, path+"/reports", token, map[string]any{
		"stateVersion": 4, "state": "PLANNING", "event": "succeeded",
		"delta": map[string]any{
			"add": 1, "change": 2, "delete": 3, "import": 4, "outputs": true,
		},
	})
	if code != http.StatusOK || r.report.Delta == nil || *r.report.Delta != (workersapi.Delta{
		Add: 1, Change: 2, Delete: 3, Import: 4, Outputs: true,
	}) {
		t.Errorf("a report with a delta = %d, reported %+v", code, r.report)
	}
	code, _ = f.call(t, path+"/reports", token, map[string]any{
		"stateVersion": 4, "state": "PLANNING", "event": "succeeded",
		"delta": map[string]any{
			"add": 0, "change": 0, "delete": 0, "import": 0, "outputs": false,
			"move": 5, "forget": 6, "resources": 7,
		},
	})
	if code != http.StatusOK || r.report.Delta == nil || *r.report.Delta != (workersapi.Delta{
		Move: 5, Forget: 6, Resources: 7,
	}) {
		t.Errorf("a delta that moves and forgets = %d, reported %+v", code, r.report)
	}

	code, signed := f.call(t, path+"/upload-urls", token, map[string]any{
		"stateVersion": 4,
		"uploads": []map[string]any{
			{"kind": "log-chunk", "name": "0", "size": 9, "sha256": sum},
		},
	})
	ups, _ := signed["uploads"].([]any)
	if code != http.StatusOK || len(ups) != 1 || r.version != 4 || len(r.uploads) != 1 ||
		len(r.uploads[0].SHA256) != 32 || r.uploads[0].SHA256[0] != 0xab {
		t.Fatalf("upload-urls = %d %v, given %+v", code, signed, r.uploads)
	}
	if u := ups[0].(map[string]any); u["url"] != "https://store.test/put" ||
		u["key"] != "a/r/PLANNING-4/0" || u["kind"] != "log-chunk" ||
		u["headers"].(map[string]any)["X-Amz-Checksum-Sha256"] != "sum" {
		t.Errorf("signed %v", u)
	}

	code, _ = f.call(t, path+"/log-chunks", token, map[string]any{
		"stateVersion": 4,
		"chunks": []map[string]any{
			{"seq": 0, "firstAt": 1_800_000_000, "lastAt": 1_800_000_002},
		},
	})
	if code != http.StatusNoContent || len(r.chunks) != 1 ||
		!r.chunks[0].LastAt.Equal(time.Unix(1_800_000_002, 0)) {
		t.Errorf("log-chunks = %d, given %+v", code, r.chunks)
	}
	code, _ = f.call(t, path+"/artifacts", token, map[string]any{
		"stateVersion": 4, "artifacts": []map[string]any{{"kind": "workspace", "name": "ws"}},
	})
	if code != http.StatusNoContent || len(r.recs) != 1 ||
		r.recs[0] != (workersapi.Recorded{Kind: workersapi.UploadWorkspace, Name: "ws"}) {
		t.Errorf("artifacts = %d, given %+v", code, r.recs)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, base+path+"/workspace", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	if w.Code != http.StatusTemporaryRedirect ||
		w.Header().Get("Location") != "https://store.test/get" {
		t.Errorf("workspace = %d to %q", w.Code, w.Header().Get("Location"))
	}

	report := map[string]any{"stateVersion": 4, "state": "PLANNING", "event": "succeeded"}
	for err, want := range map[error]struct {
		code int
		typ  string
	}{
		workersapi.ErrNotOnWorker:   {http.StatusConflict, "invalid-state"},
		workersapi.ErrMovedOn:       {http.StatusConflict, "invalid-state"},
		workersapi.ErrReportRefused: {http.StatusConflict, "invalid-state"},
		workersapi.ErrUploadExists:  {http.StatusConflict, "already-exists"},
		workersapi.ErrNoWorkspace:   {http.StatusNotFound, "not-found"},
		workersapi.ErrDeltaRefused:  {http.StatusBadRequest, "invalid-request"},
	} {
		r.err = err
		code, p := f.call(t, path+"/reports", token, report)
		if code != want.code || p["type"] != "urn:infraweave:problem:"+want.typ {
			t.Errorf("%v answers %d %v", err, code, p)
		}
	}
	r.err = nil
	other := kernel.NewULID().String()
	for _, p := range []string{"/reports", "/upload-urls", "/log-chunks", "/artifacts"} {
		if code, _ := f.call(t, "/runs/"+other+p, token, map[string]any{}); code !=
			http.StatusUnauthorized {
			t.Errorf("another run's %s = %d", p, code)
		}
	}
	if code, _ := f.get(t, "/runs/"+other+"/workspace", token); code != http.StatusUnauthorized {
		t.Errorf("another run's workspace = %d", code)
	}
}
