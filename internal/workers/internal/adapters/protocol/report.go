package protocol

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	gen "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// Each call below parses its run ID as WorkerRunSpec does: a malformed
// one is the zero ULID, which no run token names.

// WorkerRunReport reports a phase of the run the run token names and
// answers what the worker does next.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
func (s Server) WorkerRunReport(
	ctx context.Context,
	req gen.WorkerRunReportRequestObject,
) (gen.WorkerRunReportResponseObject, error) {
	b, err := bearer(ctx)
	if err != nil {
		return nil, err
	}
	run, _ := kernel.ParseULID(req.Run)
	r := workersapi.Report{
		StateVersion: req.Body.StateVersion, State: kernel.RunState(req.Body.State),
		Event: workersapi.ReportEvent(req.Body.Event),
	}
	if req.Body.Details != nil {
		r.Details = *req.Body.Details
	}
	if d := req.Body.Delta; d != nil {
		r.Delta = &workersapi.Delta{
			Add: int(d.Add), Change: int(d.Change), Delete: int(d.Delete), Import: int(d.Import),
			Outputs: d.Outputs,
		}
	}
	next, err := s.svc.Report(ctx, b, run, r)
	if err != nil {
		return nil, s.answer(ctx, err)
	}
	return gen.WorkerRunReport200JSONResponse{Body: gen.NextStep{
		Action: gen.NextStepAction(next.Action), State: string(next.State),
		StateVersion: next.StateVersion,
	}}, nil
}

// WorkerRunUploadURLs signs an upload URL for each object the worker
// declares.
//
// ref: decision 0008 § "Logs and artifacts"
func (s Server) WorkerRunUploadURLs(
	ctx context.Context,
	req gen.WorkerRunUploadURLsRequestObject,
) (gen.WorkerRunUploadURLsResponseObject, error) {
	b, err := bearer(ctx)
	if err != nil {
		return nil, err
	}
	run, _ := kernel.ParseULID(req.Run)
	ups := make([]workersapi.Upload, 0, len(req.Body.Uploads))
	for _, u := range req.Body.Uploads {
		// The schema's pattern admits hex alone; anything else declares
		// no SHA-256, which the runs domain refuses by its field.
		sum, _ := hex.DecodeString(u.Sha256)
		ups = append(ups, workersapi.Upload{
			Kind: workersapi.UploadKind(u.Kind), Name: u.Name, Size: u.Size, SHA256: sum,
		})
	}
	signed, err := s.svc.Uploads(ctx, b, run, req.Body.StateVersion, ups)
	if err != nil {
		return nil, s.answer(ctx, err)
	}
	out := gen.UploadURLs{Uploads: make([]gen.SignedUpload, 0, len(signed))}
	for _, u := range signed {
		out.Uploads = append(out.Uploads, gen.SignedUpload{
			Kind: gen.UploadKind(u.Kind), Name: u.Name, Key: u.Key, Url: u.URL, Headers: u.Header,
		})
	}
	return gen.WorkerRunUploadURLs200JSONResponse{Body: out}, nil
}

// WorkerRunLogChunks indexes uploaded log chunks.
func (s Server) WorkerRunLogChunks(
	ctx context.Context,
	req gen.WorkerRunLogChunksRequestObject,
) (gen.WorkerRunLogChunksResponseObject, error) {
	b, err := bearer(ctx)
	if err != nil {
		return nil, err
	}
	run, _ := kernel.ParseULID(req.Run)
	chunks := make([]workersapi.LogChunk, 0, len(req.Body.Chunks))
	for _, c := range req.Body.Chunks {
		chunks = append(chunks, workersapi.LogChunk{
			Seq:     c.Seq,
			FirstAt: time.Unix(c.FirstAt, 0).UTC(),
			LastAt:  time.Unix(c.LastAt, 0).UTC(),
		})
	}
	if err := s.svc.LogChunks(ctx, b, run, req.Body.StateVersion, chunks); err != nil {
		return nil, s.answer(ctx, err)
	}
	return gen.WorkerRunLogChunks204Response{}, nil
}

// WorkerRunArtifacts records uploaded artifacts, workspaces and
// observability artifacts.
func (s Server) WorkerRunArtifacts(
	ctx context.Context,
	req gen.WorkerRunArtifactsRequestObject,
) (gen.WorkerRunArtifactsResponseObject, error) {
	b, err := bearer(ctx)
	if err != nil {
		return nil, err
	}
	run, _ := kernel.ParseULID(req.Run)
	recs := make([]workersapi.Recorded, 0, len(req.Body.Artifacts))
	for _, a := range req.Body.Artifacts {
		recs = append(recs, workersapi.Recorded{Kind: workersapi.UploadKind(a.Kind), Name: a.Name})
	}
	if err := s.svc.Artifacts(ctx, b, run, req.Body.StateVersion, recs); err != nil {
		return nil, s.answer(ctx, err)
	}
	return gen.WorkerRunArtifacts204Response{}, nil
}

// WorkerRunWorkspace redirects to a pre-signed GET of the run's saved
// workspace.
func (s Server) WorkerRunWorkspace(
	ctx context.Context,
	req gen.WorkerRunWorkspaceRequestObject,
) (gen.WorkerRunWorkspaceResponseObject, error) {
	b, err := bearer(ctx)
	if err != nil {
		return nil, err
	}
	run, _ := kernel.ParseULID(req.Run)
	url, err := s.svc.Workspace(ctx, b, run)
	if err != nil {
		return nil, s.answer(ctx, err)
	}
	return gen.WorkerRunWorkspace307Response{
		Headers: gen.WorkerRunWorkspace307ResponseHeaders{Location: &url},
	}, nil
}
