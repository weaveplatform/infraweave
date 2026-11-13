package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrNoPool reports a worker pool the account lacks or that the space
// asking for it does not reach.
var ErrNoPool = errors.New("workers: no such worker pool in reach")

// Pools is what other domains call of the workers domain's pools.
type Pools interface {
	// Hold checks that acct's pool id lives in space or in a space space
	// inherits from, through an unbroken chain of inheriting spaces, and
	// holds the pool for the transaction ctx carries, so no delete or move
	// of it commits before the caller's write; ErrNoPool otherwise.
	Hold(ctx context.Context, acct kernel.AccountID, space, id kernel.ULID) error
}

// PoolUse is a stack that names a pool: its slug and the space it lives
// in.
type PoolUse struct {
	Stack string
	Space kernel.ULID
}

// PoolGuard is a domain whose rows name pools. A pool's delete and move
// ask it, after the pool's row is locked, which of its rows name the pool.
type PoolGuard interface {
	// PoolInUse lists the uses of acct's pool id.
	PoolInUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID) ([]PoolUse, error)
}

// Claim is a worker's claim of a run: the worker, its account and pool,
// and DriftLimit, the pool's drift detection run limit, nil or negative
// for none.
type Claim struct {
	Account    kernel.AccountID
	Pool       kernel.ULID
	Worker     kernel.ULID
	DriftLimit *int
}

// Assignment is a claimed run, the state the claim moved it to,
// PREPARING, PREPARING_APPLY or PREPARING_REPLAN, and its state version
// after the claim.
type Assignment struct {
	Run          kernel.ULID
	State        kernel.RunState
	StateVersion int64
}

// ErrNotOnWorker reports a run that is not on a private pool's worker, or
// that no account has, whose run token the worker API refuses.
var ErrNotOnWorker = errors.New("workers: the run is not on a worker")

// Handover is what a claimed run needs, as the runs domain builds it: the
// run's account and pool, which the workers domain seals Secrets to, the
// specification, and Secrets, the JSON of the worker API's
// RunSpecSecrets, which Reveal alone renders.
type Handover struct {
	Account kernel.AccountID
	Pool    kernel.ULID
	Spec    Spec
	Secrets Secret
}

// Secret is a value that renders as [redacted] everywhere but Reveal.
type Secret interface{ Reveal() []byte }

// Spec is a run's specification, what the worker API's RunSpec answers
// but sealed: no write-only value is in it.
type Spec struct {
	Run    SpecRun
	Stack  SpecStack
	Commit SpecCommit
	// RuntimeConfig is the runtime configuration the run's trigger gave,
	// nil when it gave none.
	RuntimeConfig json.RawMessage
	// RunnerImage is the runtime configuration's or the stack's image;
	// nil when neither names one.
	RunnerImage *string
	// StateBackend is the stack's managed state backend, nil when
	// infraweave does not keep the stack's state.
	StateBackend *StateBackend
	// Source is where the run's worker fetches its source, whose
	// credential is in the secrets.
	Source SpecSource
	// Tool is the infrastructure tool the run's launcher mounts, nil for a
	// vendor with none.
	Tool        *SpecTool
	Hooks       map[string][]string
	Environment []Variable
	Files       []File
}

// SpecRun is the run a Spec is for, at the state version its secrets are
// sealed for.
type SpecRun struct {
	ID             kernel.ULID
	Type           kernel.RunType
	State          kernel.RunState
	StateVersion   int64
	TriggeredBy    string
	Priority       kernel.RunPriority
	DriftDetection bool
	CreatedAt      time.Time
}

// SpecStack is a Spec's stack: Slug, and Space as its slug, as the API
// names them; ProjectRoot is nil for the repository's root.
// EnableWellKnownSecretMasking turns on the runner's well-known secret
// patterns.
type SpecStack struct {
	Slug                         string
	Name                         string
	Space                        string
	Labels                       []string
	Repository                   string
	Branch                       string
	ProjectRoot                  *string
	Vendor                       string
	VendorConfig                 json.RawMessage
	EnableWellKnownSecretMasking bool
}

// The kinds of a SpecSource: SourceGit is an HTTPS Git URL of the stack's
// repository, from which the launcher fetches the run's commit, and
// SourceArchive a signed GET of a local preview's uploaded tar.gz.
const (
	SourceGit     = "git"
	SourceArchive = "archive"
)

// SpecSource is where a Spec's worker fetches the run's source: Kind, and
// URL, which needs the credential in the secrets where one is sealed.
// Narrowed is whether that credential reads the stack's repository alone.
//
// ref: decision 0008 § "The run specification and its secrets"
type SpecSource struct {
	Kind     string
	URL      string
	Narrowed bool
}

// SpecTool is the infrastructure tool a Spec's launcher mounts: Name, the
// binary the runner calls, terraform or tofu; Version, the release; and
// Download, false when the runner image's own binary runs instead.
type SpecTool struct {
	Name     string
	Version  string
	Download bool
}

// StateBackend is a Spec's managed state backend: Path is where the
// server serves the stack's state, which the runner resolves against the
// server URL its launcher reaches.
type StateBackend struct {
	Path string
}

// SpecCommit is the commit a Spec's run works on.
type SpecCommit struct {
	Hash        string
	Tag         string
	URL         string
	Message     string
	AuthorName  string
	AuthorLogin *string
	Timestamp   time.Time
}

// Variable is an environment variable of a Spec: Value is nil when it is
// write-only, its value being in the Handover's Secrets.
type Variable struct {
	Name      string
	Value     *string
	WriteOnly bool
}

// File is a file mounted into a Spec's run: Content is nil when it is
// write-only, its content being in the Handover's Secrets.
type File struct {
	Path      string
	Mode      *string
	Checksum  string
	Content   []byte
	WriteOnly bool
}

// Scheduler is the runs domain's side of the worker protocol, a back edge
// internal/app binds after it constructs that domain.
//
// ref: decisions 0004 and 0008 § "Claiming runs"
type Scheduler interface {
	// Claim moves the run that has waited longest in the first tier of
	// c's pool to its preparing state for c's worker, in the transaction
	// ctx carries, skipping rows another transaction holds, so two
	// claims never take one run. It claims nothing, and reports false,
	// when no run waits, when the worker holds a run already, or when
	// only drift runs wait and the pool's drift runs on workers reach
	// DriftLimit.
	Claim(ctx context.Context, c Claim) (Assignment, bool, error)
	// Handover builds the specification of run, which a run token names,
	// in the transaction ctx carries: ErrNotOnWorker when no account has
	// the run or it is not on a private pool's worker, and a
	// *kernel.Invalid naming the variable whose template fails to render.
	// It mints the run's in-run API token, and the run's artifact key the
	// first time.
	//
	// ref: decision 0008 § "The run specification and its secrets"
	Handover(ctx context.Context, run kernel.ULID) (Handover, error)
	// Queued counts the runs waiting for a worker of each private pool,
	// of every account: the runs a claim may take. A pool none waits for
	// is absent.
	//
	// ref: decision 0008 § "Pool samples"
	Queued(ctx context.Context) (map[kernel.ULID]int, error)
}

// Waiting is the infraweave_runs payload that says run waits in pool,
// which wakes one held poll of the pool. The run's ID keeps two runs made
// ready in one transaction from folding into one notification, which
// PostgreSQL does to identical payloads. A payload of the pool's ID alone
// wakes every held poll of the pool.
func Waiting(pool, run kernel.ULID) string { return pool.String() + "/" + run.String() }

// ReportEvent is what a worker's report says of a phase of its run.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
type ReportEvent string

// The report events.
const (
	ReportStarted   ReportEvent = "started"
	ReportSucceeded ReportEvent = "succeeded"
	ReportFailed    ReportEvent = "failed"
	ReportStopped   ReportEvent = "stopped"
)

// Report is a worker's report on a phase of its run: the state version
// it last learned, the state the phase is, what happened, and what the
// worker says about it, which the transition records as its note. Delta
// is the plan's, which a succeeded report of PLANNING carries and no
// other report does.
type Report struct {
	StateVersion int64
	State        kernel.RunState
	Event        ReportEvent
	Details      string
	Delta        *Delta
}

// Delta is what a run's plan changes: the resources it adds, changes in
// place, deletes, imports, moves and forgets, whether any root module
// output changes, and Resources, the state's resource count before the
// plan. A worker that does not count moves, forgets or resources reports
// zero.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
type Delta struct {
	Add       int
	Change    int
	Delete    int
	Import    int
	Move      int
	Forget    int
	Resources int
	Outputs   bool
}

// Changes reports whether the plan changes any resource or output. A
// move or a forget changes the state only when applied, so either is a
// change; Resources counts what the state holds, not what the plan does.
func (d Delta) Changes() bool {
	return d.Add > 0 || d.Change > 0 || d.Delete > 0 || d.Import > 0 || d.Move > 0 ||
		d.Forget > 0 || d.Outputs
}

// Action is what a report's answer tells the worker to do.
type Action string

// The actions: execute the phase of the answer's state, pause while the
// run waits for a decision, or exit, the run having ended.
const (
	ActionExecute Action = "execute"
	ActionPause   Action = "pause"
	ActionExit    Action = "exit"
)

// Step is a report's answer: what the worker does next, and the run's
// state and state version now, which its next report names.
type Step struct {
	Action       Action
	State        kernel.RunState
	StateVersion int64
}

// UploadKind is what an upload holds, which decides its store.
type UploadKind string

// The upload kinds: a log chunk, in run-logs; a run artifact the runner
// sealed with the run's artifact key, in run-artifacts; the workspace,
// encrypted to the pool's key, in workspaces; an observability artifact,
// in run-observability.
//
// ref: decision 0008 § "Logs and artifacts"
const (
	UploadLogChunk      UploadKind = "log-chunk"
	UploadArtifact      UploadKind = "artifact"
	UploadWorkspace     UploadKind = "workspace"
	UploadObservability UploadKind = "observability"
)

// Upload is an object a worker declares before it uploads it: its kind,
// its name within the run's state version, a log chunk's being its
// sequence number in decimal, and the size and SHA-256 of its bytes as
// uploaded.
type Upload struct {
	Kind   UploadKind
	Name   string
	Size   int64
	SHA256 []byte
}

// SignedUpload is where to upload a declared object: its kind and name,
// its key within its store, the pre-signed PUT URL, and the headers the
// PUT must carry.
type SignedUpload struct {
	Kind   UploadKind
	Name   string
	Key    string
	URL    string
	Header map[string]string
}

// LogChunk is an uploaded log chunk: its sequence number and when its
// first and last lines were written.
type LogChunk struct {
	Seq     int64
	FirstAt time.Time
	LastAt  time.Time
}

// Recorded is an uploaded object other than a log chunk, to record by
// its kind and name.
type Recorded struct {
	Kind UploadKind
	Name string
}

// Refusals of Reports, besides ErrNotOnWorker.
var (
	// ErrMovedOn reports a call naming a state version the run moved on
	// from.
	ErrMovedOn = errors.New("workers: the run moved on from that state version")
	// ErrReportRefused reports a report the run's state cannot take.
	ErrReportRefused = errors.New("workers: the run's state cannot take the report")
	// ErrDeltaRefused reports a succeeded report of PLANNING without a
	// delta, or another report with one.
	ErrDeltaRefused = errors.New(
		"workers: a delta comes with a succeeded report of PLANNING, and with it alone",
	)
	// ErrUploadExists reports an upload whose name the run's state version
	// already declared with another size or SHA-256, or recorded.
	ErrUploadExists = errors.New("workers: the upload's name is taken")
	// ErrNoWorkspace reports a run that recorded no workspace.
	ErrNoWorkspace = errors.New("workers: the run recorded no workspace")
)

// Reports is the runs domain's side of a claimed run's calls, a back edge
// internal/app binds after it constructs that domain. Each call names the
// run a run token names and joins the transaction ctx carries; each
// answers ErrNotOnWorker when no account has the run or it is not on a
// private pool's worker, and each that names a state version answers
// ErrMovedOn unless it is the run's.
//
// ref: decisions 0004 and 0008 § "The worker API"
type Reports interface {
	// Report moves run as r reports and answers what the worker does
	// next. A started report of the state the run's preparation leads to
	// ends the handover, and one of the state the run is in changes
	// nothing. A report that repeats the one that took the run's last
	// transition answers as that one did.
	Report(ctx context.Context, run kernel.ULID, r Report) (Step, error)
	// Uploads records each declared object of run's state version as
	// pending, under the run's own key prefix, and signs a PUT URL for it.
	// A *kernel.Invalid names the declarations it refuses.
	Uploads(ctx context.Context, run kernel.ULID, version int64, u []Upload) ([]SignedUpload, error)
	// LogChunks indexes uploaded log chunks of run's state version, once
	// each upload matches its declaration; a chunk already indexed is
	// left as it is.
	LogChunks(ctx context.Context, run kernel.ULID, version int64, c []LogChunk) error
	// Artifacts records uploaded objects of run's state version, as
	// LogChunks indexes log chunks.
	Artifacts(ctx context.Context, run kernel.ULID, version int64, r []Recorded) error
	// Workspace is a pre-signed GET URL of the workspace run last
	// recorded, or ErrNoWorkspace.
	Workspace(ctx context.Context, run kernel.ULID) (string, error)
}

// ExitReason is why a run's container ended, as its launcher reports it.
type ExitReason string

// The exit reasons: the runner ended by itself, the launcher killed it on
// a kill signal, the host killed it for its memory, or the container is
// gone, such as after the host restarted.
const (
	ExitExited      ExitReason = "exited"
	ExitKilled      ExitReason = "killed"
	ExitOutOfMemory ExitReason = "out-of-memory"
	ExitMissing     ExitReason = "missing"
)

// Exit is how a run's container ended: its root process's exit code, and
// why.
type Exit struct {
	Code   int
	Reason ExitReason
}

// PoolTimeout is a pool that sets its own preparing timeout.
type PoolTimeout struct {
	Pool    kernel.ULID
	Timeout time.Duration
}

// Liveness is the runs domain's side of a worker that stops polling, of a
// run whose container ended before a report ended the run, and of a run
// left in preparation: a back edge internal/app binds after it constructs
// that domain. Each call joins the transaction ctx carries and fails runs
// through the liveness edge out of the state each is in.
//
// ref: decision 0008 § "Liveness: offline workers and lost runs"
type Liveness interface {
	// Lost fails each run worker holds, with the note "worker lost", and
	// answers how many it failed.
	Lost(ctx context.Context, worker kernel.ULID) (int, error)
	// Ended fails run, its container having ended as exit, when worker
	// still holds it, and reports whether it failed it: a report that
	// ended the run first leaves nothing to fail.
	Ended(ctx context.Context, worker, run kernel.ULID, exit Exit) (bool, error)
	// Overdue fails up to batch runs that entered a preparing state before
	// now less their pool's preparing timeout, the one timeouts names or
	// else fallback, with the note "preparing timeout", skipping runs
	// another transaction holds, and answers how many it failed.
	Overdue(
		ctx context.Context,
		now time.Time,
		timeouts []PoolTimeout,
		fallback time.Duration,
		batch int,
	) (int, error)
}

// Sample is a pool's figures at the minute At: its busy and registered
// workers and the runs waiting for one.
type Sample struct {
	At         time.Time
	Busy       int
	Registered int
	Queued     int
}

// Samples is what other domains read of the pools' minute samples, the
// worker usage assessment and the usage read models among them; no other
// domain queries the samples' table.
//
// ref: decision 0008 § "Pool samples"
type Samples interface {
	// SamplesBetween reads acct's samples of pool taken from from up to,
	// not including, to, oldest first. Samples older than
	// worker.samples.retention are gone, so a window reaching further back
	// answers only what is kept.
	SamplesBetween(
		ctx context.Context,
		acct kernel.AccountID,
		pool kernel.ULID,
		from, to time.Time,
	) ([]Sample, error)
}
