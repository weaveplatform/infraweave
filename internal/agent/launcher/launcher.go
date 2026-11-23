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
	"github.com/weaveplatform/infraweave/internal/agent/initcommand"
	"github.com/weaveplatform/infraweave/internal/agent/initpolicy"
	"github.com/weaveplatform/infraweave/internal/agent/phaseinputs"
	"github.com/weaveplatform/infraweave/internal/agent/signedrun"
	"github.com/weaveplatform/infraweave/internal/agent/source"
	"github.com/weaveplatform/infraweave/internal/agent/tools"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/runnerimages"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// Deps are what a launcher takes: its settings, the HTTP client its
// worker API calls and downloads go through, the executor that runs its
// runs, the sources and tool binaries they mount, its log, a clock,
// time.Now when nil, and what it registers as. Doer is a client trusting
// the system's roots and launcher.ca_file's when nil, Sources a
// source.Fetcher trusting the same, and Tools a tools.Fetcher on the
// cache directory and the tool mirror.
//
// A worker controller's slot sets the rest. Endpoint, when set, is where
// the launcher and its runners call the worker API instead of the pool
// token's endpoint. Owner and Slot name the
// hosting owner and logical slot it registers, so registering again
// resumes the slot's worker. Retain keeps the worker registered when Run
// ends idle, for its controller to deregister. Store keeps the state a
// restart resumes, worker.json in the cache directory when nil. Inputs
// resolves each assignment's phase-scoped inputs; none when nil.
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
	Endpoint string
	Owner    *kernel.WorkerOwner
	Slot     *int32
	Retain   bool
	Store    Store
	Inputs   Inputs
}

// Sources writes a run's workspace directory, which must not exist: its
// source, or the workspace it saved for its plan's confirmation, which
// src, a pre-signed URL, answers sealed to the pool's key.
type Sources interface {
	Fetch(ctx context.Context, dir string, spec wire.RunSpec, cred *wire.RunSpecSourceCredential) error
	Restore(ctx context.Context, dir, src string, pool *ecdh.PrivateKey, run string, spec wire.RunSpec) error
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
// retried after a backoff that doubles up to maxBackoff. A workspace whose
// pre-signed URL the store refuses is fetched again from a URL signed
// afresh for up to transferRetry, the ten minutes the runner's uploads
// take.
const (
	callTimeout   = 30 * time.Second
	maxBackoff    = 30 * time.Second
	transferRetry = 10 * time.Minute
)

// maxDetails is the longest details a report carries, the worker API's
// RunReport.details bound.
const maxDetails = 1024

// killedCode is SIGKILL's exit code, which a run whose container a kill
// removed before its end was read reports.
const killedCode = 128 + 9

// Launcher registers a worker of a pool, polls for runs and executes each
// in a container, one at a time.
//
// ref: decision 0008 § "The launcher and the runner"
type Launcher struct {
	d      Deps
	id     identity
	api    *client.Client
	policy *initpolicy.Policy
	// admit verifies each assignment's run token against the keys of
	// the server api reaches.
	admit *signedrun.Verifier
	// endpoint is where the launcher and its runners reach the worker
	// API; roots is launcher.ca_file's PEM bundle, which its runners
	// trust too.
	endpoint string
	roots    []byte

	// registered holds while the launcher has a session, and answered is
	// when the server last answered it, in Unix nanoseconds.
	registered atomic.Bool
	answered   atomic.Int64

	// retiring holds once Retire is called; status is what Status
	// answers.
	retiring atomic.Bool
	mu       sync.Mutex
	status   Status

	// Run's goroutine alone touches the rest.
	st      State
	session string
	cur     *running
	ended   *wire.ContainerExit
	drained bool
	cycle   bool
}

// Status is what a launcher shows of its worker: its ID, once registered,
// the drain flag it last learned, and the run it executes or whose end it
// has not yet reported, with the assignment's state, version and
// preparing deadline, when the run's container started, whether a kill
// was asked of it, and its exit once it ended. It holds no token.
type Status struct {
	Worker            string
	Drained           bool
	Run               string
	State             wire.AssignmentState
	StateVersion      int64
	PreparingDeadline time.Time
	Started           time.Time
	Kill              bool
	Exit              *wire.ContainerExit
}

// running is the run container the launcher waits on. Its run timeout
// counts from started and its no-log timeout from out's last line;
// timedOut is the timeout the launcher killed it on, empty until then,
// and killed holds once a kill signal's kill reached it.
type running struct {
	c        executor.Container
	done     chan waited
	out      *lines
	started  time.Time
	timedOut wire.ContainerExitReason
	killed   bool
}

// expiry is when r's nearer timeout ends, and the reason a kill then
// reports.
func (r *running) expiry(c config.Launcher) (time.Time, wire.ContainerExitReason) {
	run, quiet := r.started.Add(c.RunTimeout), r.out.last().Add(c.LogTimeout)
	if quiet.Before(run) {
		return quiet, wire.LogTimeout
	}
	return run, wire.RunTimeout
}

type waited struct {
	exit executor.Exit
	err  error
}

// New is a launcher of d.Config's pool, whose token and private key it
// reads and checks against each other.
func New(d Deps) (*Launcher, error) {
	return NewContext(context.Background(), d)
}

// NewContext cancels startup policy compilation when its caller stops startup.
func NewContext(ctx context.Context, d Deps) (*Launcher, error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Store == nil {
		d.Store = FileStore(d.Config.CacheDir)
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
	policy, err := initpolicy.Load(ctx, initpolicy.Settings{
		Path: d.Config.InitializationPolicy, Engine: d.Config.InitializationPolicyEngine,
		Timeout: d.Config.InitializationTimeout,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // initpolicy exposes only safe startup failures
	}
	endpoint := id.endpoint
	if d.Endpoint != "" {
		endpoint = d.Endpoint
	}
	api := client.New(client.Base(endpoint), d.Doer)
	return &Launcher{
		d: d, id: id, api: api, policy: policy, admit: signedrun.New(api, d.Now),
		endpoint: endpoint, roots: ca,
	}, nil
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

// Retire makes Run end once the launcher is idle: after the run it
// executes, or the poll it holds, which executes a run it claims first.
// A launcher that does not retain its worker deregisters it.
func (l *Launcher) Retire() { l.retiring.Store(true) }

// Status answers what the launcher last showed of its worker.
func (l *Launcher) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.status
	if s.Exit != nil {
		exit := *s.Exit
		s.Exit = &exit
	}
	return s
}

// publish records the state Status answers.
func (l *Launcher) publish() {
	s := Status{
		Worker: l.st.Worker, Drained: l.drained, Run: l.st.Run, State: l.st.Assigned,
		StateVersion: l.st.StateVersion, PreparingDeadline: l.st.PreparingDeadline,
		Started: l.st.Started, Kill: l.st.Kill, Exit: l.st.Exit,
	}
	l.mu.Lock()
	l.status = s
	l.mu.Unlock()
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
		if l.retiring.Load() && l.cur == nil && l.ended == nil {
			l.stop(ctx)
			return nil
		}
		if l.st.Kill && l.cur != nil && !l.cur.killed {
			l.kill(ctx)
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

// start registers and resumes what the store holds: the run container
// of a resumed worker, killed again when a kill was asked of it, or the
// exit of one that ended before the last stop. A worker the server no
// longer has had its run failed, so its container is removed, and so is
// a container of another run than the store's, whose removal failed
// before. A run whose container is gone ends missing, or killed when a
// kill was asked of it.
func (l *Launcher) start(ctx context.Context) error {
	st, err := l.d.Store.Load(ctx)
	if err != nil {
		return fmt.Errorf("launcher: load the worker: %w", err)
	}
	l.st = st
	if l.st.Pool != l.id.pool {
		l.st = State{Pool: l.id.pool}
	}
	old := l.st.Worker
	if err := l.register(ctx); err != nil {
		return err
	}
	if old != "" && old != l.st.Worker {
		l.discard(ctx, old)
	}
	out := &lines{log: l.d.Log, now: l.d.Now}
	c, ok, err := l.d.Executor.Find(ctx, l.st.Worker, out)
	if err != nil {
		return fmt.Errorf("launcher: find the worker's run: %w", err)
	}
	if ok && l.st.Run != "" && c.Run() != l.st.Run {
		l.d.Log.WarnContext(ctx, "removing the container of a run the worker no longer executes",
			"run", c.Run(), "assigned", l.st.Run)
		l.lose(ctx, c, c.Run())
		ok = false
	}
	switch {
	case ok:
		out.named(c.Run())
		if l.st.Run != c.Run() || l.st.Started.IsZero() {
			l.st.Started = l.d.Now()
		}
		l.st.Run = c.Run()
		l.cur = l.watch(ctx, c, out, l.st.Started)
		l.d.Log.InfoContext(ctx, "resumed run", "run", c.Run())
		if l.st.Kill {
			l.kill(ctx)
		}
	case l.st.Exit != nil:
		l.ended = l.st.Exit
	case l.st.Run != "":
		exit := wire.ContainerExit{Code: executor.MissingCode, Reason: wire.Missing}
		if l.st.Kill {
			exit = wire.ContainerExit{Code: killedCode, Reason: wire.Killed}
		}
		l.ended, l.st.Exit, l.st.Started = &exit, &exit, time.Time{}
		l.dropWorkspace(ctx, l.st.Run, nil)
	}
	return l.save(ctx)
}

// discard removes the container of the worker old, which the server no
// longer has, and forgets its run.
func (l *Launcher) discard(ctx context.Context, old string) {
	l.d.Log.WarnContext(ctx, "the server no longer has the worker; its run ended there",
		"worker", old, "run", l.st.Run)
	if c, ok, err := l.d.Executor.Find(ctx, old, nil); err == nil && ok {
		l.lose(ctx, c, l.st.Run)
	} else {
		l.dropWorkspace(ctx, l.st.Run, nil)
	}
	l.st.reset()
}

// lose kills c, the container of run, which the server no longer has, and
// removes run's workspace and then c, whose mounts hand the workspace
// back, as the killed runner did not.
func (l *Launcher) lose(ctx context.Context, c executor.Container, run string) {
	if err := c.Kill(ctx); err != nil {
		l.d.Log.WarnContext(ctx, "kill the lost run's container", "run", run, "error", err)
	}
	l.dropWorkspace(ctx, run, c)
	if err := c.Remove(ctx); err != nil {
		l.d.Log.WarnContext(ctx, "remove the lost run's container", "run", run, "error", err)
	}
}

// register registers the worker, resuming l.st.Worker when it names one,
// retrying with backoff until the server answers. A refusal, such as a
// pool reset, a version the server does not take or the private-workers
// limit, fails it.
func (l *Launcher) register(ctx context.Context) error {
	var backoff time.Duration
	for {
		jws, err := l.id.sign(claims{
			Capabilities: []string{"plan-sanitization-v1", "public-pool-v1"},
			Version:      l.d.Version, OS: l.d.OS, Arch: l.d.Arch, Metadata: l.d.Config.Metadata,
			Worker: l.st.Worker, Owner: l.d.Owner, Slot: l.d.Slot,
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
			return l.save(ctx)
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
		l.lose(ctx, l.cur.c, l.cur.c.Run())
		l.cur = nil
	}
	l.ended = nil
	l.st.reset()
	return l.save(ctx)
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
	var expired <-chan time.Time
	if l.cur != nil && l.cur.timedOut == "" {
		at, _ := l.cur.expiry(l.d.Config)
		t := time.NewTimer(at.Sub(l.d.Now()))
		defer t.Stop()
		expired = t.C
	}
	for {
		select {
		case p := <-res:
			if p.err == nil && reported != nil {
				l.ended = nil
				l.st.reset()
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
		case <-expired:
			expired = l.expire(ctx)
		}
	}
}

// expire kills the current run's container once its nearer timeout has
// ended, so that its end reports that timeout's reason, and otherwise
// answers a timer for the nearer end, which each line on the output moves
// for the no-log timeout. A kill that fails is tried again at the next
// poll, not at once, so a host that refuses it is not asked in a loop.
//
// ref: decision 0008 § "Timeouts and timers"
func (l *Launcher) expire(ctx context.Context) <-chan time.Time {
	at, why := l.cur.expiry(l.d.Config)
	if left := at.Sub(l.d.Now()); left > 0 {
		return time.After(left)
	}
	run := l.cur.c.Run()
	if err := l.cur.c.Kill(ctx); err != nil {
		l.d.Log.ErrorContext(ctx, "kill the run on its timeout", "run", run, "reason", why,
			"error", err)
		return nil
	}
	l.cur.timedOut = why
	l.d.Log.WarnContext(ctx, "killed the run on its timeout", "run", run, "reason", why)
	return nil
}

// apply acts on a poll's result: a renewed session token, the drain flag,
// the signals and an assignment.
func (l *Launcher) apply(ctx context.Context, p wire.PollResult) {
	if p.SessionToken != nil {
		l.session = *p.SessionToken
	}
	if p.Drained != nil && *p.Drained != l.drained {
		l.drained = *p.Drained
		l.d.Log.InfoContext(ctx, "drain flag changed", "drained", l.drained)
		l.publish()
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
// once it is idle. A kill is saved before the container is killed, so a
// restart that finds the container kills it again and one that finds it
// gone reports it killed rather than missing.
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
	if s == wire.Kill {
		l.st.Kill = true
		l.saveOrLog(ctx)
		l.kill(ctx)
		return
	}
	if err := l.cur.c.Stop(ctx); err != nil {
		l.d.Log.ErrorContext(ctx, "signal the run", "run", run, "signal", s, "error", err)
		return
	}
	l.d.Log.InfoContext(ctx, "signalled the run", "run", run, "signal", s)
}

// kill kills the current run's container on the kill its state holds. A
// kill the host refuses is tried again before the next poll.
func (l *Launcher) kill(ctx context.Context) {
	run := l.cur.c.Run()
	if err := l.cur.c.Kill(ctx); err != nil {
		l.d.Log.ErrorContext(ctx, "signal the run", "run", run, "signal", wire.Kill, "error", err)
		return
	}
	l.cur.killed = true
	l.d.Log.InfoContext(ctx, "signalled the run", "run", run, "signal", wire.Kill)
}

// assign prepares and starts a, recording its run first, so a restart
// that finds no container reports it missing; a run whose record cannot
// be saved is not prepared. The whole preparation is bounded by the
// assignment's preparing deadline, which a restart never extends. A
// preparation that fails reports the run failed in its preparing state.
func (l *Launcher) assign(ctx context.Context, a wire.Assignment) {
	l.st.Run, l.st.Assigned, l.st.StateVersion = a.Run, a.State, a.StateVersion
	if a.PreparingDeadline > 0 {
		l.st.PreparingDeadline = time.Unix(a.PreparingDeadline, 0)
	}
	out := &lines{log: l.d.Log, run: a.Run, now: l.d.Now}
	var c executor.Container
	err := l.save(ctx)
	if err != nil {
		err = fmt.Errorf("record the assignment: %w", err)
	} else {
		c, err = l.prepareBy(ctx, a, out)
	}
	if err != nil {
		l.d.Log.ErrorContext(ctx, "preparation failed", "run", a.Run, "error", err)
		l.fail(ctx, a, err)
		l.st.reset()
		l.saveOrLog(ctx)
		return
	}
	l.d.Log.InfoContext(ctx, "started run", "run", a.Run, "state", a.State)
	l.st.Started = l.d.Now()
	l.saveOrLog(ctx)
	l.cur = l.watch(ctx, c, out, l.st.Started)
}

// prepareBy prepares a within its preparing deadline, naming the deadline
// in the failure of a preparation it cut short.
func (l *Launcher) prepareBy(ctx context.Context, a wire.Assignment, out *lines) (executor.Container, error) {
	deadline := l.st.PreparingDeadline
	if deadline.IsZero() {
		return l.prepare(ctx, a, out)
	}
	pctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	c, err := l.prepare(pctx, a, out)
	if err != nil && ctx.Err() == nil && pctx.Err() != nil {
		err = fmt.Errorf("the preparing deadline %s passed: %w", deadline.UTC().Format(time.RFC3339), err)
	}
	return c, err
}

// prepare admits a's run token as its run's, signed by the server's
// keys, before it uses it, fetches a's specification, opens its sealed
// part with the pool's private key, resolves and admits the run's image,
// evaluates the initialization policy, whose exec calls run in helpers of
// that image, fetches the run's source into its workspace, and starts the
// run's container. The image is the specification's, else the launcher's
// default when set, else the stack's vendor's built-in image. A container
// that does not start takes its workspace with it.
//
// ref: decision 0008 § "The launcher and the runner"
func (l *Launcher) prepare(ctx context.Context, a wire.Assignment, out *lines) (executor.Container, error) {
	trust, err := l.admit.Verify(ctx, l.session, a.RunToken, a.Run)
	if err != nil {
		return nil, err //nolint:wrapcheck // signedrun's text is one fixed reason that names no token
	}
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
	image := l.runnerImage(spec)
	if spec.PublicPool != nil && !runnerimages.RegistryAllowed(image, spec.PublicPool.AllowedRegistries) {
		return nil, errPublicRunnerImage
	}
	if l.policy != nil {
		input, err := initpolicy.Snapshot(a, spec, l.id.pool, image, initpolicy.TokenTrust(trust))
		if err != nil {
			return nil, err //nolint:wrapcheck // initpolicy exposes a safe refusal
		}
		// The policy's exec calls run in helpers of the run's own admitted
		// image, where its runs run; an executor that cannot start helpers
		// fails each call.
		var commands rego.Commands
		if c, ok := l.d.Executor.(executor.Commander); ok {
			commands = initcommand.NewSession(c, executor.Command{
				Worker: l.st.Worker, Run: a.Run, StateVersion: a.StateVersion, Image: image,
			})
		}
		result, err := l.policy.Evaluate(ctx, input, commands)
		if result.Print != "" {
			l.d.Log.InfoContext(ctx, "worker initialization policy print", "run", a.Run,
				"print", result.Print, "truncated", result.PrintTruncated)
		}
		if err != nil {
			return nil, err //nolint:wrapcheck // policy denies are masked before reporting
		}
	}
	ws, err := l.workspace(ctx, a, spec, secrets.SourceCredential)
	if err != nil {
		return nil, err
	}
	c, err := l.container(ctx, a, spec, image, secrets, ws, out)
	if err != nil && ws != "" {
		l.dropWorkspace(ctx, a.Run, nil)
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
// answers as a pre-signed URL and the pool's private key opens. When the
// store refuses a pre-signed URL's signature, a local preview's archive
// or the saved workspace, it asks again for the specification or the
// saved workspace, which the worker API signs afresh, and fetches again
// after a backoff, for up to transferRetry.
//
// ref: decision 0008 § "The launcher and the runner"
// ref: decision 0008 § "Logs and artifacts"
func (l *Launcher) workspace(ctx context.Context, a wire.Assignment, spec wire.RunSpec, cred *wire.RunSpecSourceCredential) (string, error) {
	dir, err := l.workspaceDir(a.Run)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", fmt.Errorf("the workspace: %w", err)
	}
	deadline := l.d.Now().Add(transferRetry)
	if pd := l.st.PreparingDeadline; !pd.IsZero() && pd.Before(deadline) {
		deadline = pd
	}
	var backoff time.Duration
	for {
		if err := os.RemoveAll(dir); err != nil {
			return "", fmt.Errorf("the workspace: %w", err)
		}
		if a.State == wire.AssignmentStatePREPARING {
			err = l.d.Sources.Fetch(ctx, dir, spec, cred)
		} else {
			err = l.restore(ctx, a, dir, spec)
		}
		backoff = min(max(2*backoff, time.Second), maxBackoff)
		if !errors.Is(err, client.ErrSigned) || l.d.Now().Add(backoff).After(deadline) {
			break
		}
		l.d.Log.WarnContext(ctx, "the store refused the workspace's signed URL", "run", a.Run,
			"error", err, "retry_in", backoff)
		if !sleep(ctx, backoff) {
			break
		}
		if a.State == wire.AssignmentStatePREPARING {
			cctx, cancel := context.WithTimeout(ctx, callTimeout)
			spec, err = l.api.Spec(cctx, a.RunToken, a.Run)
			cancel()
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		l.dropWorkspace(ctx, a.Run, nil)
		return "", err //nolint:wrapcheck // source names the step and the host
	}
	return dir, nil
}

// restore writes into dir the workspace a's planning saved.
func (l *Launcher) restore(ctx context.Context, a wire.Assignment, dir string, spec wire.RunSpec) error {
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
// opened every directory the run made. When the removal is refused, as
// when the run's container was killed before its runner opened them, c,
// the run's ended container, hands the workspace back and the removal is
// tried again. A refusal that stays, or one with no container to hand the
// workspace back, as for a container that is gone, is logged, and the
// directory stays.
func (l *Launcher) dropWorkspace(ctx context.Context, run string, c executor.Container) {
	dir, err := l.workspaceDir(run)
	if err == nil {
		err = os.RemoveAll(dir)
	}
	if err != nil && dir != "" && c != nil {
		if herr := c.HandBack(ctx); herr != nil {
			err = errors.Join(err, herr)
		} else {
			err = os.RemoveAll(dir)
		}
	}
	if err != nil {
		l.d.Log.WarnContext(ctx, "remove the run's workspace", "run", run, "error", err)
	}
}

var errPublicRunnerImage = errors.New("runner image is not admitted by the installation public pool")

// container fetches the tool a's specification names, and the Terragrunt
// that drives it for a Terragrunt stack's run, and starts the run's
// container of image with ws as its workspace, each binary mounted
// read-only at /bin/<tool>, and the handoff, which holds the opened
// secrets in memory alone. A binary whose download is false is not
// mounted, so the image's own runs.
//
// ref: decision 0008 § "Runner images"
func (l *Launcher) container(
	ctx context.Context,
	a wire.Assignment,
	spec wire.RunSpec,
	image string,
	secrets wire.RunSpecSecrets,
	ws string,
	out *lines,
) (executor.Container, error) {
	var res Resolved
	if l.d.Inputs != nil {
		var err error
		if res, err = l.d.Inputs.Resolve(ctx); err == nil {
			err = res.Check()
		}
		if err != nil {
			return nil, fmt.Errorf("the pool's phase-scoped inputs: %w", err)
		}
	}
	defer func() {
		for _, f := range res.Inputs.Files {
			clear(f.Content)
		}
	}()
	var mounts []executor.Mount
	var verified []executor.Tool
	for _, b := range binaries(spec) {
		bin, err := l.d.Tools.Binary(ctx, tools.Release{
			Tool: b.Tool, Version: b.Version, OS: toolOS, Arch: l.d.Arch,
		})
		if err != nil {
			return nil, err //nolint:wrapcheck // tools names the release and the step
		}
		mounts = append(mounts, executor.Mount{
			Source: bin, Target: "/bin/" + string(b.Tool), ReadOnly: true,
		})
		verified = append(verified, executor.Tool{Name: string(b.Tool), Version: b.Version, Path: bin})
	}
	spec.Sealed = nil
	var h bytes.Buffer
	if err := handoff.Write(&h, handoff.Handoff{
		Endpoint: client.Base(l.endpoint), Roots: l.roots, Run: a.Run, RunToken: a.RunToken,
		Assigned: a.State, StateVersion: a.StateVersion, Spec: spec, Secrets: secrets,
		StateLockTimeout: l.d.Config.StateLockTimeout, MaskEnvs: l.d.Config.MaskEnvs,
		SensitiveOutputUpload: l.d.Config.SensitiveOutputUpload,
		MaxRequestsPerSecond:  l.d.Config.MaxRequestsPerSecond,
		MaxRequestsBurst:      l.d.Config.MaxRequestsBurst,
		PoolKey:               l.id.open.PublicKey().Bytes(),
		Inputs:                res.Inputs,
	}); err != nil {
		return nil, err //nolint:wrapcheck // handoff names the write
	}
	defer clear(h.Bytes())
	exe := execution(spec)
	c, err := l.d.Executor.Start(ctx, executor.Job{
		Worker: l.st.Worker, Run: a.Run, StateVersion: a.StateVersion, Image: image, Workspace: ws,
		Mounts: append(mounts, res.Mounts...), Tools: verified, Volumes: res.Volumes,
		Scoped: phaseinputs.Dirs(res.Inputs.Files), Handoff: h.Bytes(), Output: out,
		PreparingDeadline: l.st.PreparingDeadline, Execution: exe,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // the executor names the step
	}
	// The image's own binary's release is not known before it runs.
	version := ""
	if spec.Tool != nil && spec.Tool.Download {
		version = spec.Tool.Version
	}
	l.d.Log.InfoContext(ctx, "run container started", "run", a.Run, "runtime", runtimeOf(l.d.Executor),
		"vendor", exe.Vendor, "tool", exe.Tool, "tool_version", version, "tool_source", toolSource(spec),
		"image", image)
	return c, nil
}

// runnerImage resolves the image once by the same precedence for admission
// and execution, so a policy never sees a display default instead of its image.
func (l *Launcher) runnerImage(spec wire.RunSpec) string {
	if spec.RunnerImage != nil && *spec.RunnerImage != "" {
		return *spec.RunnerImage
	}
	if l.d.Config.DefaultRunnerImage != "" {
		return l.d.Config.DefaultRunnerImage
	}
	return config.VendorRunnerImage(spec.Stack.Vendor)
}

// binaries are the releases spec mounts, each without its platform: the
// tool, then the Terragrunt that drives it, each that downloads.
func binaries(spec wire.RunSpec) []tools.Release {
	var out []tools.Release
	if t := spec.Tool; t != nil && t.Download {
		out = append(out, tools.Release{Tool: tools.Tool(t.Name), Version: t.Version})
	}
	if t := spec.Terragrunt; t != nil && t.Download {
		out = append(out, tools.Release{Tool: tools.Terragrunt, Version: t.Version})
	}
	return out
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

// watch waits for c to end, until ctx ends. Its run timeout counts from
// started, and its no-log timeout from its last line on out or from now,
// whichever is later, so a resumed run's no-log timeout starts afresh.
func (l *Launcher) watch(ctx context.Context, c executor.Container, out *lines, started time.Time) *running {
	out.mark(l.d.Now())
	r := &running{c: c, done: make(chan waited, 1), out: out, started: started}
	go func() {
		exit, err := c.Wait(ctx)
		r.done <- waited{exit, err}
	}()
	return r
}

// end records how the current run's container ended, for the next poll,
// and removes its workspace and then it, whose mounts hand the workspace
// back when its runner did not. A container whose end the executor cannot read is
// reported missing.
func (l *Launcher) end(ctx context.Context, w waited) {
	run := l.cur.c.Run()
	code := int32(w.exit.Code) //nolint:gosec // exit codes are 0 to 255, or -1
	exit := wire.ContainerExit{Code: code, Reason: w.exit.Reason}
	if l.cur.timedOut != "" && exit.Reason == wire.Killed {
		exit.Reason = l.cur.timedOut
	}
	if w.err != nil {
		l.d.Log.ErrorContext(ctx, "wait for the run's container", "run", run, "error", w.err)
		exit = wire.ContainerExit{Code: executor.MissingCode, Reason: wire.Missing}
	}
	l.d.Log.InfoContext(ctx, "run container ended", "run", run, "code", exit.Code,
		"reason", exit.Reason)
	l.dropWorkspace(ctx, run, l.cur.c)
	if err := l.cur.c.Remove(ctx); err != nil {
		l.d.Log.WarnContext(ctx, "remove the run's container", "run", run, "error", err)
	}
	l.cur, l.ended, l.st.Exit, l.st.Started = nil, &exit, &exit, time.Time{}
	l.saveOrLog(ctx)
}

// stop ends Run: an idle launcher deregisters, unless it retains its
// worker, and a busy one keeps its worker, so a restart resumes its run.
func (l *Launcher) stop(ctx context.Context) {
	if l.cur != nil || l.ended != nil {
		l.d.Log.InfoContext(ctx, "stopping with a run; a restart resumes it", "run", l.st.Run)
		return
	}
	if l.d.Retain {
		l.registered.Store(false)
		l.d.Log.InfoContext(ctx, "stopped; the worker stays registered for its controller", "worker", l.st.Worker)
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
	if err := l.d.Store.Forget(dctx); err != nil {
		l.d.Log.WarnContext(ctx, "forget the worker", "error", err)
	}
	l.registered.Store(false)
	l.d.Log.InfoContext(ctx, "deregistered", "worker", l.st.Worker, "why", why)
}

// save writes the state a restart resumes and publishes it to Status.
func (l *Launcher) save(ctx context.Context) error {
	l.publish()
	return l.d.Store.Save(ctx, l.st) //nolint:wrapcheck // the store names the write
}

// saveOrLog saves the state, logging a failure: the launcher keeps
// working, and only a restart would miss what it did not save.
func (l *Launcher) saveOrLog(ctx context.Context) {
	if err := l.save(ctx); err != nil {
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

// lines logs a run's output, a line at a time, and keeps when the last
// line arrived.
type lines struct {
	log *slog.Logger
	now func() time.Time
	mu  sync.Mutex
	run string
	buf []byte
	at  time.Time
}

// mark records t as the last line's arrival when no later line arrived.
func (w *lines) mark(t time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t.After(w.at) {
		w.at = t
	}
}

// last is when the last line arrived.
func (w *lines) last() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.at
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
		w.at = w.now()
		w.buf = w.buf[min(i+1, len(w.buf)):]
	}
}
