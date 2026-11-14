package launcher

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/source"
	"github.com/weaveplatform/infraweave/internal/agent/tools"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// Deps are what a launcher takes: its settings, the HTTP client its
// worker API calls and downloads go through, the executor that runs its
// runs, the sources and tool binaries they mount, its log, a clock,
// time.Now when nil, and what it registers as. Doer is a client trusting
// the system's roots and launcher.ca_file's when nil, Sources a
// source.Fetcher trusting the same, and Tools a tools.Fetcher on the
// cache directory and the tool mirror.
type Deps struct {
	Config   config.Launcher
	Doer     wire.HttpRequestDoer
	Executor executor.Executor
	Sources  Sources
	Tools    Tools
	Log      *slog.Logger
	Now      func() time.Time
	Version  string
	OS       string
	Arch     string
}

// Sources writes a run's workspace directory, which must not exist: its
// source, or the workspace it saved for its plan's confirmation, which
// src, a pre-signed URL, answers sealed to the pool's key.
type Sources interface {
	Fetch(
		ctx context.Context,
		dir string,
		spec wire.RunSpec,
		cred *wire.RunSpecSourceCredential,
	) error
	Restore(
		ctx context.Context,
		dir, src string,
		pool *ecdh.PrivateKey,
		run string,
		spec wire.RunSpec,
	) error
}

// Tools answers the path of a verified binary of a tool's release.
type Tools interface {
	Binary(ctx context.Context, r tools.Release) (string, error)
}

// toolOS is the platform of the binaries a run mounts: a run's container
// is Linux whatever the launcher's host is, on the host's architecture.
const toolOS = "linux"

// The bounds of a launcher's calls: callTimeout for each call but the
// poll, which may take its hold and callTimeout more; a failed call is
// retried after a backoff that doubles up to maxBackoff.
const (
	callTimeout = 30 * time.Second
	maxBackoff  = 30 * time.Second
)

// maxDetails is the longest details a report carries, the worker API's
// RunReport.details bound.
const maxDetails = 1024

// Launcher registers a worker of a pool, polls for runs and executes each
// in a container, one at a time.
//
// ref: decision 0008 § "The launcher and the runner"
type Launcher struct {
	d   Deps
	id  identity
	api *client.Client

	// registered holds while the launcher has a session, and answered is
	// when the server last answered it, in Unix nanoseconds.
	registered atomic.Bool
	answered   atomic.Int64

	// Run's goroutine alone touches the rest.
	st      state
	session string
	cur     *running
	ended   *wire.ContainerExit
	drained bool
	cycle   bool
}

// running is the run container the launcher waits on.
type running struct {
	c    executor.Container
	done chan waited
}

type waited struct {
	exit executor.Exit
	err  error
}

// New is a launcher of d.Config's pool, whose token and private key it
// reads and checks against each other.
func New(d Deps) (*Launcher, error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	ca, roots, err := d.Config.CA()
	if err != nil {
		return nil, err //nolint:wrapcheck // config names the setting
	}
	if d.Doer == nil {
		d.Doer = newClient(roots)
	}
	if d.Sources == nil {
		d.Sources = source.New(d.Doer, ca)
	}
	if d.Tools == nil {
		d.Tools = tools.New(tools.Config{
			Dir: d.Config.CacheDir, Mirror: d.Config.ToolMirror, Doer: d.Doer,
		})
	}
	key, err := d.Config.Key()
	if err != nil {
		return nil, err //nolint:wrapcheck // config names the read
	}
	id, err := newIdentity(d.Config.Token, key)
	if err != nil {
		return nil, err
	}
	return &Launcher{d: d, id: id, api: client.New(client.Base(id.endpoint), d.Doer)}, nil
}

// newClient is the launcher's HTTP client, trusting roots, or the
// system's when nil. It has no timeout: each call has its context's, and
// a poll is held for up to launcher.poll_timeout.
func newClient(roots *x509.CertPool) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
	t.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: t}
}

// The readiness errors of a launcher with no session, and of one the
// server has not answered within readyPolls poll timeouts.
var (
	errNotRegistered = errors.New("the launcher is not registered")
	errNotAnswered   = errors.New("the server answered no poll within three poll timeouts")
)

// readyPolls is how many poll timeouts a launcher stays ready without an
// answer, so a server restart or a brief outage does not flap /readyz.
//
// ref: decision 0017 § "Health"
const readyPolls = 3

// Registered reports whether the launcher has a session, for /startupz.
func (l *Launcher) Registered(context.Context) error {
	if !l.registered.Load() {
		return errNotRegistered
	}
	return nil
}

// Ready reports whether the launcher has a session and the server
// answered it within readyPolls poll timeouts, for /readyz.
func (l *Launcher) Ready(ctx context.Context) error {
	if err := l.Registered(ctx); err != nil {
		return err
	}
	if l.d.Now().Sub(time.Unix(0, l.answered.Load())) > readyPolls*l.d.Config.PollTimeout {
		return errNotAnswered
	}
	return nil
}

// Run registers the worker, resuming the one the cache directory names
// and its run, and polls until ctx ends or a cycle ends it. Each poll
// reports whether a run is executing, and the exit of a container that
// ended since the last; an assignment is prepared and started, and stop,
// kill and cycle act on the run and the launcher. A refused session
// registers again: a worker the server still has keeps its run, and one
// it lost has had its run failed, so the launcher drops it. Other
// failures are retried with backoff. When ctx ends, an idle launcher
// deregisters; a busy one leaves its worker and its container for a
// restart to resume. Run answers nil once ctx ends, even before the
// launcher registered.
//
// ref: decision 0008 § "The worker API"
// ref: decision 0008 § "Liveness: offline workers and lost runs"
func (l *Launcher) Run(ctx context.Context) error {
	if err := l.start(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	var backoff time.Duration
	for {
		if l.cycle && l.cur == nil && l.ended == nil {
			l.deregister(ctx, "cycled")
			return nil
		}
		p, err := l.poll(ctx)
		switch {
		case ctx.Err() != nil:
			l.stop(ctx)
			return nil
		case client.Status(err) == http.StatusUnauthorized:
			l.registered.Store(false)
			if err := l.reregister(ctx); err != nil {
				return err
			}
			continue
		case err != nil:
			backoff = min(max(2*backoff, time.Second), maxBackoff)
			l.d.Log.WarnContext(ctx, "poll failed", "error", err, "retry_in", backoff)
			if !sleep(ctx, backoff) {
				l.stop(ctx)
				return nil
			}
			continue
		}
		backoff = 0
		l.answered.Store(l.d.Now().UnixNano())
		l.apply(ctx, p)
	}
}

// start registers and resumes what the cache directory holds: the run
// container of a resumed worker, or the exit of one that ended before
// the last stop. A worker the server no longer has had its run failed, so
// its container is removed.
func (l *Launcher) start(ctx context.Context) error {
	l.st = load(l.d.Config.CacheDir)
	if l.st.Pool != l.id.pool {
		l.st = state{Pool: l.id.pool}
	}
	old := l.st.Worker
	if err := l.register(ctx); err != nil {
		return err
	}
	if old != "" && old != l.st.Worker {
		l.discard(ctx, old)
	}
	out := &lines{log: l.d.Log}
	c, ok, err := l.d.Executor.Find(ctx, l.st.Worker, out)
	if err != nil {
		return fmt.Errorf("launcher: find the worker's run: %w", err)
	}
	switch {
	case ok:
		out.named(c.Run())
		l.st.Run = c.Run()
		l.cur = l.watch(ctx, c)
		l.d.Log.InfoContext(ctx, "resumed run", "run", c.Run())
	case l.st.Exit != nil:
		l.ended = l.st.Exit
	case l.st.Run != "":
		l.ended = &wire.ContainerExit{Code: executor.MissingCode, Reason: wire.Missing}
		l.dropWorkspace(ctx, l.st.Run)
	}
	return l.save()
}

// discard removes the container of the worker old, which the server no
// longer has, and forgets its run.
func (l *Launcher) discard(ctx context.Context, old string) {
	l.d.Log.WarnContext(ctx, "the server no longer has the worker; its run ended there",
		"worker", old, "run", l.st.Run)
	if c, ok, err := l.d.Executor.Find(ctx, old, nil); err == nil && ok {
		if err := c.Remove(ctx); err != nil {
			l.d.Log.WarnContext(
				ctx,
				"remove the lost run's container",
				"run",
				c.Run(),
				"error",
				err,
			)
		}
	}
	l.dropWorkspace(ctx, l.st.Run)
	l.st.Run, l.st.Exit = "", nil
}

// register registers the worker, resuming l.st.Worker when it names one,
// retrying with backoff until the server answers. A refusal, such as a
// pool reset, a version the server does not take or the private-workers
// limit, fails it.
func (l *Launcher) register(ctx context.Context) error {
	var backoff time.Duration
	for {
		jws, err := l.id.sign(claims{
			Version: l.d.Version, OS: l.d.OS, Arch: l.d.Arch, Metadata: l.d.Config.Metadata,
			Worker: l.st.Worker,
		}, l.d.Now())
		if err != nil {
			return err
		}
		rctx, cancel := context.WithTimeout(ctx, callTimeout)
		r, err := l.api.Register(rctx, jws)
		cancel()
		if err == nil {
			resumed := r.Worker == l.st.Worker
			l.st.Worker, l.session = r.Worker, r.SessionToken
			l.answered.Store(l.d.Now().UnixNano())
			l.registered.Store(true)
			l.d.Log.InfoContext(ctx, "registered", "pool", l.id.pool, "worker", r.Worker,
				"resumed", resumed)
			return l.save()
		}
		if s := client.Status(err); s >= 400 && s < 500 && s != http.StatusTooManyRequests {
			return fmt.Errorf("launcher: the server refused the registration: %w", err)
		}
		backoff = min(max(2*backoff, time.Second), maxBackoff)
		l.d.Log.WarnContext(ctx, "registration failed", "error", err, "retry_in", backoff)
		if !sleep(ctx, backoff) {
			return fmt.Errorf("launcher: register: %w", context.Cause(ctx))
		}
	}
}

// reregister registers again after the server refused the session. A
// different worker means the server lost the old one and failed its run,
// so its container is removed.
func (l *Launcher) reregister(ctx context.Context) error {
	old := l.st.Worker
	if err := l.register(ctx); err != nil {
		return err
	}
	if l.st.Worker == old {
		return nil
	}
	if l.cur != nil {
		l.d.Log.WarnContext(ctx, "the server no longer has the worker; its run ended there",
			"worker", old, "run", l.cur.c.Run())
		if err := l.cur.c.Remove(ctx); err != nil {
			l.d.Log.WarnContext(ctx, "remove the lost run's container", "error", err)
		}
		l.dropWorkspace(ctx, l.cur.c.Run())
		l.cur = nil
	}
	l.ended, l.st.Run, l.st.Exit = nil, "", nil
	return l.save()
}

// poll sends one poll and answers its result. A container that ends while
// the poll is held cuts it short, so its exit is reported at once: the
// result is then empty, unless the server answered first. When ctx ends,
// the container's wait ends with it, and the container keeps running for
// a restart to resume.
func (l *Launcher) poll(ctx context.Context) (wire.PollResult, error) {
	wait := int32(l.d.Config.PollTimeout / time.Second) //nolint:gosec // at most config.MaxPoll
	req := wire.PollRequest{
		Busy:    l.cur != nil,
		Drained: &l.drained,
		Version: &l.d.Version,
		Wait:    &wait,
	}
	var done <-chan waited
	reported := l.ended
	switch {
	case l.cur != nil:
		run := l.cur.c.Run()
		req.Run, done = &run, l.cur.done
	case l.ended != nil:
		req.Run, req.Exit = &l.st.Run, l.ended
	}
	pctx, cancel := context.WithTimeout(ctx, l.d.Config.PollTimeout+callTimeout)
	defer cancel()
	type polled struct {
		r   wire.PollResult
		err error
	}
	res := make(chan polled, 1)
	go func() {
		r, err := l.api.Poll(pctx, l.session, req)
		res <- polled{r, err}
	}()
	select {
	case p := <-res:
		if p.err == nil && reported != nil {
			l.ended, l.st.Run, l.st.Exit = nil, "", nil
			l.saveOrLog(ctx)
		}
		return p.r, p.err
	case w := <-done:
		cancel()
		p := <-res
		if ctx.Err() != nil {
			return wire.PollResult{}, fmt.Errorf("launcher: poll: %w", context.Cause(ctx))
		}
		l.end(ctx, w)
		if p.err != nil {
			return wire.PollResult{}, nil
		}
		return p.r, nil
	}
}

// apply acts on a poll's result: a renewed session token, the drain flag,
// the signals and an assignment.
func (l *Launcher) apply(ctx context.Context, p wire.PollResult) {
	if p.SessionToken != nil {
		l.session = *p.SessionToken
	}
	if p.Drained != nil {
		l.drained = *p.Drained
	}
	if p.Signals != nil {
		for _, s := range *p.Signals {
			l.signal(ctx, s)
		}
	}
	if p.Assignment != nil {
		l.assign(ctx, *p.Assignment)
	}
}

// signal stops or kills the current run, or marks the launcher to exit
// once it is idle.
func (l *Launcher) signal(ctx context.Context, s wire.Signal) {
	if s == wire.Cycle {
		l.cycle = true
		l.d.Log.InfoContext(ctx, "cycling: the launcher exits once it is idle")
		return
	}
	if l.cur == nil {
		return
	}
	run := l.cur.c.Run()
	act := l.cur.c.Stop
	if s == wire.Kill {
		act = l.cur.c.Kill
	}
	if err := act(ctx); err != nil {
		l.d.Log.ErrorContext(ctx, "signal the run", "run", run, "signal", s, "error", err)
		return
	}
	l.d.Log.InfoContext(ctx, "signalled the run", "run", run, "signal", s)
}

// assign prepares and starts a, recording its run first, so a restart
// that finds no container reports it missing. A preparation that fails
// reports the run failed in its preparing state.
func (l *Launcher) assign(ctx context.Context, a wire.Assignment) {
	l.st.Run = a.Run
	l.saveOrLog(ctx)
	c, err := l.prepare(ctx, a)
	if err != nil {
		l.d.Log.ErrorContext(ctx, "preparation failed", "run", a.Run, "error", err)
		l.fail(ctx, a, err)
		l.st.Run = ""
		l.saveOrLog(ctx)
		return
	}
	l.d.Log.InfoContext(ctx, "started run", "run", a.Run, "state", a.State)
	l.cur = l.watch(ctx, c)
}

// prepare fetches a's specification, opens its sealed part with the
// pool's private key, fetches the run's source into its workspace, and
// starts the run's container. A container that does not start takes its
// workspace with it.
//
// ref: decision 0008 § "The launcher and the runner"
func (l *Launcher) prepare(ctx context.Context, a wire.Assignment) (executor.Container, error) {
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	spec, err := l.api.Spec(cctx, a.RunToken, a.Run)
	cancel()
	if err != nil {
		return nil, err //nolint:wrapcheck // client names the call
	}
	plain, err := seal.OpenEnvelope(l.id.open, a.Run, a.StateVersion, spec.Sealed)
	if err != nil {
		return nil, fmt.Errorf("open the run's secrets: %w", err)
	}
	defer clear(plain)
	var secrets wire.RunSpecSecrets
	if err := json.Unmarshal(plain, &secrets); err != nil {
		return nil, fmt.Errorf("read the run's secrets: %w", err)
	}
	ws, err := l.workspace(ctx, a, spec, secrets.SourceCredential)
	if err != nil {
		return nil, err
	}
	c, err := l.container(ctx, a, spec, secrets, ws)
	if err != nil && ws != "" {
		l.dropWorkspace(ctx, a.Run)
	}
	return c, err
}

// errRun marks a run ID that cannot name a directory.
var errRun = errors.New("the run's ID names no workspace")

// workspaceDir is run's workspace directory, under the cache directory's
// workspaces, which only the launcher's user reads.
func (l *Launcher) workspaceDir(run string) (string, error) {
	if !filepath.IsLocal(run) || strings.ContainsAny(run, `/\.`) {
		return "", fmt.Errorf("%w: %q", errRun, run)
	}
	dir, err := filepath.Abs(filepath.Join(l.d.Config.CacheDir, "workspaces", run))
	if err != nil {
		return "", fmt.Errorf("the workspace: %w", err)
	}
	return dir, nil
}

// errNoWorkspace marks a run that applies or replans with no workspace
// saved by its planning.
var errNoWorkspace = errors.New("the run saved no workspace to apply or replan from")

// workspace writes a fresh workspace directory and answers it: a's
// source, or, for a run that applies or replans after its plan was
// confirmed, the workspace its planning saved, which the worker API
// answers as a pre-signed URL and the pool's private key opens.
//
// ref: decision 0008 § "The launcher and the runner"
func (l *Launcher) workspace(
	ctx context.Context,
	a wire.Assignment,
	spec wire.RunSpec,
	cred *wire.RunSpecSourceCredential,
) (string, error) {
	dir, err := l.workspaceDir(a.Run)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", fmt.Errorf("the workspace: %w", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("the workspace: %w", err)
	}
	if a.State == wire.AssignmentStatePREPARING {
		err = l.d.Sources.Fetch(ctx, dir, spec, cred)
	} else {
		err = l.restore(ctx, a, dir, spec)
	}
	if err != nil {
		l.dropWorkspace(ctx, a.Run)
		return "", err //nolint:wrapcheck // source names the step and the host
	}
	return dir, nil
}

// restore writes into dir the workspace a's planning saved.
func (l *Launcher) restore(
	ctx context.Context,
	a wire.Assignment,
	dir string,
	spec wire.RunSpec,
) error {
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	src, err := l.api.Workspace(cctx, a.RunToken, a.Run)
	cancel()
	if client.Status(err) == http.StatusNotFound {
		return errNoWorkspace
	}
	if err != nil {
		return err //nolint:wrapcheck // client names the call
	}
	err = l.d.Sources.Restore(ctx, dir, src, l.id.open, a.Run, spec)
	return err //nolint:wrapcheck // source names the step and the host
}

// dropWorkspace removes run's workspace directory. A launcher that could
// not hand the workspace to the run's user relies on the runner having
// opened every directory the run made; one the runner never reached, as
// when its container was killed, may refuse the removal, which is logged,
// and the directory stays.
func (l *Launcher) dropWorkspace(ctx context.Context, run string) {
	dir, err := l.workspaceDir(run)
	if err == nil {
		err = os.RemoveAll(dir)
	}
	if err != nil {
		l.d.Log.WarnContext(ctx, "remove the run's workspace", "run", run, "error", err)
	}
}

// container fetches the tool a's specification names and starts the
// run's container with ws as its workspace, the tool's binary mounted
// read-only at /bin/<tool>, and the handoff, which holds the opened
// secrets in memory alone. A specification whose tool download is false
// mounts none, so the image's own binary runs.
//
// ref: decision 0008 § "Runner images"
func (l *Launcher) container(
	ctx context.Context,
	a wire.Assignment,
	spec wire.RunSpec,
	secrets wire.RunSpecSecrets,
	ws string,
) (executor.Container, error) {
	var mounts []executor.Mount
	if t := spec.Tool; t != nil && t.Download {
		bin, err := l.d.Tools.Binary(ctx, tools.Release{
			Tool: t.Name, Version: t.Version, OS: toolOS, Arch: l.d.Arch,
		})
		if err != nil {
			return nil, err //nolint:wrapcheck // tools names the release and the step
		}
		mounts = append(mounts, executor.Mount{
			Source: bin, Target: "/bin/" + string(t.Name), ReadOnly: true,
		})
	}
	image := l.d.Config.DefaultRunnerImage
	if spec.RunnerImage != nil && *spec.RunnerImage != "" {
		image = *spec.RunnerImage
	}
	spec.Sealed = nil
	var h bytes.Buffer
	if err := handoff.Write(&h, handoff.Handoff{
		Endpoint: client.Base(l.id.endpoint), Run: a.Run, RunToken: a.RunToken,
		Assigned: a.State, StateVersion: a.StateVersion, Spec: spec, Secrets: secrets,
		StateLockTimeout: l.d.Config.StateLockTimeout, MaskEnvs: l.d.Config.MaskEnvs,
		PoolKey: l.id.open.PublicKey().Bytes(),
	}); err != nil {
		return nil, err //nolint:wrapcheck // handoff names the write
	}
	defer clear(h.Bytes())
	out := &lines{log: l.d.Log, run: a.Run}
	c, err := l.d.Executor.Start(ctx, executor.Job{
		Worker: l.st.Worker, Run: a.Run, Image: image, Workspace: ws, Mounts: mounts,
		Handoff: h.Bytes(), Output: out,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // the executor names the step
	}
	return c, nil
}

// fail reports a's run failed in its preparing state, with why. A report
// that fails leaves the run to the pool's preparing timeout.
func (l *Launcher) fail(ctx context.Context, a wire.Assignment, why error) {
	details := truncate(why.Error(), maxDetails)
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if _, err := l.api.Report(cctx, a.RunToken, a.Run, wire.RunReport{
		StateVersion: a.StateVersion, State: wire.WorkerState(a.State), Event: wire.Failed,
		Details: &details,
	}); err != nil {
		l.d.Log.WarnContext(ctx, "report the failed preparation", "run", a.Run, "error", err)
	}
}

// watch waits for c to end, until ctx ends.
func (l *Launcher) watch(ctx context.Context, c executor.Container) *running {
	r := &running{c: c, done: make(chan waited, 1)}
	go func() {
		exit, err := c.Wait(ctx)
		r.done <- waited{exit, err}
	}()
	return r
}

// end records how the current run's container ended, for the next poll,
// and removes it. A container whose end the executor cannot read is
// reported missing.
func (l *Launcher) end(ctx context.Context, w waited) {
	run := l.cur.c.Run()
	code := int32(w.exit.Code) //nolint:gosec // exit codes are 0 to 255, or -1
	exit := wire.ContainerExit{Code: code, Reason: w.exit.Reason}
	if w.err != nil {
		l.d.Log.ErrorContext(ctx, "wait for the run's container", "run", run, "error", w.err)
		exit = wire.ContainerExit{Code: executor.MissingCode, Reason: wire.Missing}
	}
	l.d.Log.InfoContext(ctx, "run container ended", "run", run, "code", exit.Code,
		"reason", exit.Reason)
	if err := l.cur.c.Remove(ctx); err != nil {
		l.d.Log.WarnContext(ctx, "remove the run's container", "run", run, "error", err)
	}
	l.dropWorkspace(ctx, run)
	l.cur, l.ended, l.st.Exit = nil, &exit, &exit
	l.saveOrLog(ctx)
}

// stop ends Run: an idle launcher deregisters, and a busy one keeps its
// worker, so a restart resumes its run.
func (l *Launcher) stop(ctx context.Context) {
	if l.cur != nil || l.ended != nil {
		l.d.Log.InfoContext(ctx, "stopping with a run; a restart resumes it", "run", l.st.Run)
		return
	}
	l.deregister(ctx, "stopped")
}

// deregister removes the worker from the server and forgets it. A failure
// is logged: the server reaps a worker that stops polling.
func (l *Launcher) deregister(ctx context.Context, why string) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callTimeout)
	defer cancel()
	if err := l.api.Deregister(dctx, l.session); err != nil {
		l.d.Log.WarnContext(ctx, "deregister", "error", err)
	}
	if err := forget(l.d.Config.CacheDir); err != nil {
		l.d.Log.WarnContext(ctx, "forget the worker", "error", err)
	}
	l.registered.Store(false)
	l.d.Log.InfoContext(ctx, "deregistered", "worker", l.st.Worker, "why", why)
}

func (l *Launcher) save() error { return save(l.d.Config.CacheDir, l.st) }

// saveOrLog saves the state, logging a failure: the launcher keeps
// working, and only a restart would miss what it did not save.
func (l *Launcher) saveOrLog(ctx context.Context) {
	if err := l.save(); err != nil {
		l.d.Log.ErrorContext(ctx, "save the worker", "error", err)
	}
}

// sleep waits d, false when ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// truncate cuts s to at most n bytes on a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// maxLine is the longest runner output line logged whole; a longer one is
// logged in parts.
const maxLine = 64 << 10

// lines logs a run's output, a line at a time.
type lines struct {
	log *slog.Logger
	mu  sync.Mutex
	run string
	buf []byte
}

// named sets the run the lines belong to, once a restarted launcher found
// it.
func (w *lines) named(run string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.run = run
}

func (w *lines) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 && len(w.buf) < maxLine {
			return len(p), nil
		}
		if i < 0 {
			i = len(w.buf)
		}
		w.log.Info("runner output", "run", w.run, "line", string(w.buf[:i]))
		w.buf = w.buf[min(i+1, len(w.buf)):]
	}
}
