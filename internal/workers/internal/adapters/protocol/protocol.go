package protocol

import (
	"context"
	"errors"
	"log/slog"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	gen "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

// Server is the worker API's strict server. The worker API's
// authenticator, in front of every route but the registration, puts the
// worker session token's accessapi.Bearer on each request's context.
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

// bearer is the request's worker session token, or a 401 problem for a
// request the authenticator did not admit, which the route table never
// sends.
func bearer(ctx context.Context) (accessapi.Bearer, error) {
	b, ok := accessapi.BearerFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return b, problem.Unauthenticated.Error(
			"The request carries no valid worker session token.",
		)
	}
	return b, nil
}

// answer maps the service's refusals to problems: a refused credential
// to 401, which the log records with its reason, and the private-workers
// limit to 403 limit-reached. A *kernel.Invalid is answered as it is.
func (s Server) answer(ctx context.Context, err error) error {
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
	got, err := s.svc.Poll(ctx, b, in)
	if err != nil {
		return nil, s.answer(ctx, err)
	}
	out := gen.PollResult{}
	if a := got.Assignment; a != nil {
		out.Assignment = &gen.Assignment{
			Run: a.Run.String(), State: gen.AssignmentState(a.State),
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
