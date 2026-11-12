package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	gen "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// Server is the worker API's strict server. The worker API's
// authenticator, in front of every route but the registration, puts the
// worker session token's or, on a run's calls, the run token's
// accessapi.Bearer on each request's context.
type Server struct {
	svc *worker.Service
	log *slog.Logger
}

var _ gen.StrictServerInterface = Server{}

// New is the worker API's strict server over svc, which logs each refused
// credential through log at info.
//
// ref: decision 0008 § "The worker API"
func New(svc *worker.Service, log *slog.Logger) Server { return Server{svc: svc, log: log} }

// bearer is the request's worker session token or run token, or a 401
// problem for a request the authenticator did not admit, which the route
// table never sends.
func bearer(ctx context.Context) (accessapi.Bearer, error) {
	b, ok := accessapi.BearerFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return b, problem.Unauthenticated.Error("The request carries no valid token.")
	}
	return b, nil
}

// refusals are the runs domain's refusals of a claimed run's calls and
// the problems that answer them: 409 invalid-state for a run off its
// worker, a state version it moved on from, or a report its state cannot
// take; 409 already-exists for an upload name taken; 404 for a run with
// no workspace; and 400 for a report whose delta is missing or misplaced.
var refusals = []struct {
	err    error
	typ    problem.Type
	detail string
}{
	{workersapi.ErrNotOnWorker, problem.InvalidState, "The run is not on a worker."},
	{workersapi.ErrMovedOn, problem.InvalidState, "The run moved on from that state version."},
	{workersapi.ErrReportRefused, problem.InvalidState, "The run's state cannot take this report."},
	{
		workersapi.ErrUploadExists, problem.AlreadyExists,
		"The run's state version already declared or recorded an upload of that name.",
	},
	{workersapi.ErrNoWorkspace, problem.NotFound, "The run has no saved workspace."},
	{
		workersapi.ErrDeltaRefused, problem.InvalidRequest,
		"A succeeded report of PLANNING carries the plan's delta, and no other report does.",
	},
}

// answer maps the service's refusals to problems: a refused credential
// to 401, which the log records with its reason, the private-workers
// limit to 403 limit-reached, and the runs domain's refusals as refusals
// lists. A *kernel.Invalid is answered as it is.
func (s Server) answer(ctx context.Context, err error) error {
	for _, r := range refusals {
		if errors.Is(err, r.err) {
			return r.typ.Error(r.detail) //nolint:wrapcheck // a problem, answered as it is
		}
	}
	if errors.Is(err, worker.ErrUnauthenticated) {
		s.log.InfoContext(ctx, "worker credential refused", "reason", err.Error())
		//nolint:wrapcheck // a problem, answered as it is
		return problem.Unauthenticated.Error("The worker's credential is refused.")
	}
	if le, ok := errors.AsType[*featuresapi.LimitError](err); ok {
		return &problem.Error{
			Problem: problem.NewLimitReached(le.Limit, string(le.Scope), le.Value),
		}
	}
	return err
}

// WorkerRegister registers the worker the signed registration describes.
func (s Server) WorkerRegister(
	ctx context.Context,
	req gen.WorkerRegisterRequestObject,
) (gen.WorkerRegisterResponseObject, error) {
	reg, err := s.svc.Register(ctx, req.Body.Registration)
	if err != nil {
		return nil, s.answer(ctx, err)
	}
	return gen.WorkerRegister200JSONResponse{Body: gen.Registration{
		Worker: reg.Worker.String(), SessionToken: reg.Token.Raw,
		SessionExpiresAt: reg.Token.Expires.Unix(),
	}}, nil
}

// WorkerPoll reports the session's worker's state and answers what the
// server has to say to it, holding the request while it has nothing.
func (s Server) WorkerPoll(
	ctx context.Context,
	req gen.WorkerPollRequestObject,
) (gen.WorkerPollResponseObject, error) {
	b, err := bearer(ctx)
	if err != nil {
		return nil, err
	}
	in := worker.PollInput{Busy: req.Body.Busy}
	if req.Body.Drained != nil {
		in.Drained = *req.Body.Drained
	}
	if req.Body.Wait != nil {
		in.Wait = time.Duration(*req.Body.Wait) * time.Second
	}
	if req.Body.Run != nil {
		// A malformed ID parses as the zero ULID, which no worker holds, so
		// the poll fails no run for it.
		run, _ := kernel.ParseULID(*req.Body.Run)
		in.Run = &run
	}
	if e := req.Body.Exit; e != nil {
		in.Exit = &workersapi.Exit{Code: int(e.Code), Reason: workersapi.ExitReason(e.Reason)}
	}
	got, err := s.svc.Poll(ctx, b, in)
	if err != nil {
		return nil, s.answer(ctx, err)
	}
	out := gen.PollResult{}
	if a := got.Assignment; a != nil {
		out.Assignment = &gen.Assignment{
			Run: a.Run.String(), State: gen.AssignmentState(a.State), StateVersion: a.StateVersion,
			RunToken: a.Token.Raw, RunTokenExpiresAt: a.Token.Expires.Unix(),
		}
	}
	if len(got.Signals) > 0 {
		signals := make([]gen.Signal, 0, len(got.Signals))
		for _, sig := range got.Signals {
			signals = append(signals, gen.Signal(sig))
		}
		out.Signals = &signals
	}
	if got.Drained {
		out.Drained = &got.Drained
	}
	if got.Token != nil {
		exp := got.Token.Expires.Unix()
		out.SessionToken, out.SessionExpiresAt = &got.Token.Raw, &exp
	}
	return gen.WorkerPoll200JSONResponse{Body: out}, nil
}

// WorkerDeregister removes the session's worker.
func (s Server) WorkerDeregister(
	ctx context.Context,
	_ gen.WorkerDeregisterRequestObject,
) (gen.WorkerDeregisterResponseObject, error) {
	b, err := bearer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.svc.Deregister(ctx, b); err != nil {
		return nil, s.answer(ctx, err)
	}
	return gen.WorkerDeregister204Response{}, nil
}

// WorkerRunSpec answers the specification of the run the run token names,
// its secrets sealed to the run's pool's key.
//
// ref: decision 0008 § "The run specification and its secrets"
func (s Server) WorkerRunSpec(
	ctx context.Context,
	req gen.WorkerRunSpecRequestObject,
) (gen.WorkerRunSpecResponseObject, error) {
	b, err := bearer(ctx)
	if err != nil {
		return nil, err
	}
	// A malformed ID parses as the zero ULID, which no run token names,
	// so the service refuses it as another run's.
	run, _ := kernel.ParseULID(req.Run)
	spec, err := s.svc.Spec(ctx, b, run)
	if err != nil {
		return nil, s.answer(ctx, err)
	}
	return gen.WorkerRunSpec200JSONResponse{Body: runSpec(spec)}, nil
}

// runSpec is spec as the worker API answers it.
func runSpec(spec worker.RunSpec) gen.RunSpec {
	r, st, c := spec.Run, spec.Stack, spec.Commit
	out := gen.RunSpec{
		Run: gen.RunSpecRun{
			Id: r.ID.String(), Type: gen.RunSpecRunType(r.Type), State: string(r.State),
			StateVersion: r.StateVersion, TriggeredBy: r.TriggeredBy,
			Priority: gen.RunSpecRunPriority(r.Priority), DriftDetection: r.DriftDetection,
			CreatedAt: r.CreatedAt.Unix(),
		},
		Stack: gen.RunSpecStack{
			Id: st.Slug, Name: st.Name, Space: st.Space, Labels: nonNil(st.Labels),
			Repository: st.Repository, Branch: st.Branch, ProjectRoot: st.ProjectRoot,
			Vendor: st.Vendor, VendorConfig: object(st.VendorConfig),
		},
		Commit: gen.RunSpecCommit{
			Hash: c.Hash, Tag: optional(c.Tag), Url: optional(c.URL), Message: c.Message,
			AuthorName: c.AuthorName, AuthorLogin: c.AuthorLogin, Timestamp: c.Timestamp.Unix(),
		},
		RunnerImage: spec.RunnerImage,
		Hooks:       spec.Hooks,
		Environment: make([]gen.RunSpecVariable, 0, len(spec.Environment)),
		Files:       make([]gen.RunSpecFile, 0, len(spec.Files)),
		Sealed:      spec.Sealed,
	}
	if out.Hooks == nil {
		out.Hooks = map[string][]string{}
	}
	if spec.RuntimeConfig != nil {
		rc := object(spec.RuntimeConfig)
		out.RuntimeConfig = &rc
	}
	for _, v := range spec.Environment {
		out.Environment = append(out.Environment,
			gen.RunSpecVariable{Name: v.Name, Value: v.Value, WriteOnly: v.WriteOnly})
	}
	for _, f := range spec.Files {
		file := gen.RunSpecFile{
			Path: f.Path, FileMode: f.Mode, Checksum: f.Checksum, WriteOnly: f.WriteOnly,
		}
		if f.Content != nil {
			file.Content = &f.Content
		}
		out.Files = append(out.Files, file)
	}
	return out
}

// object is raw, a JSON object, as a map, empty when raw holds none.
func object(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

// optional is s, nil when empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nonNil is s, an empty slice when nil, so the answer carries [].
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
