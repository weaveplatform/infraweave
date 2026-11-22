package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policiesapi "github.com/weaveplatform/infraweave/internal/policies/api"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Errors of Transition and Create. Each refusal is also a typed error
// that names what refused it.
var (
	// ErrAccountMissing refuses new work whose tenant owner no longer exists.
	ErrAccountMissing = errors.New("runs: account has no organization")
	// ErrNoTransaction reports a call outside a transaction, whose writes
	// could commit apart.
	ErrNoTransaction = runsapi.ErrNoTransaction
	// ErrNotFound reports a run the account lacks.
	ErrNotFound = errors.New("runs: run not found")
	// ErrConflict reports a transition whose expected state_version is
	// not the run's: another transition moved it first.
	ErrConflict = errors.New("runs: the run moved since it was read")
	// ErrRefused reports a transition no edge of the machine allows, or
	// one taken without a guard checked.
	ErrRefused = errors.New("runs: transition refused")
	// ErrDisabled reports a run created on a disabled stack.
	ErrDisabled = errors.New("runs: the stack is disabled")
	// ErrDeleting reports a run created on a stack whose delete waits for
	// its destroy run.
	ErrDeleting = errors.New("runs: the stack is being deleted")
	// ErrLocked reports a run created on a stack another caller locked.
	ErrLocked = errors.New("runs: the stack is locked")
	// ErrTask reports a TASK run created without its task, or another
	// type with one.
	ErrTask = errors.New("runs: a TASK run alone carries a task")
)

// Conflict is ErrConflict with the run's current state and version.
type Conflict struct {
	Run     kernel.ULID
	State   kernel.RunState
	Version int64
}

func (c *Conflict) Error() string {
	return fmt.Sprintf("%v: run %s is %s at version %d", ErrConflict, c.Run, c.State, c.Version)
}

// Is matches ErrConflict.
func (c *Conflict) Is(target error) bool { return target == ErrConflict }

// Refused is ErrRefused with the move refused; Guard is the guard left
// unchecked, empty when no edge allows the move at all.
type Refused struct {
	Type     kernel.RunType
	From, To kernel.RunState
	Driver   machine.Driver
	Guard    machine.Guard
}

func (r *Refused) Error() string {
	if r.Guard != "" {
		return fmt.Sprintf("%v: %s run %s → %s by %s without guard %s",
			ErrRefused, r.Type, r.From, r.To, r.Driver, r.Guard)
	}
	return fmt.Sprintf("%v: %s run %q → %s by %s", ErrRefused, r.Type, r.From, r.To, r.Driver)
}

// Is matches ErrRefused.
func (r *Refused) Is(target error) bool { return target == ErrRefused }

// Locked is ErrLocked with the lock's holder as the API shows it.
type Locked struct{ By string }

func (l *Locked) Error() string { return fmt.Sprintf("%v by %s", ErrLocked, l.By) }

// Is matches ErrLocked.
func (l *Locked) Is(target error) bool { return target == ErrLocked }

// ScheduleKind is the job that walks a stack's queued runs, keyed by the
// stack so a burst of requests coalesces into one walk, and one that
// arrives mid-walk runs it once more.
//
// ref: decision 0008 § "From QUEUED to READY"
var ScheduleKind = queue.Kind{Name: "run.schedule", Queue: queue.Default, Policy: queue.Coalesce}

// PolicyTriggerKind evaluates one committed terminal run once per state version.
// It is declared beside the transition so enqueue and consumption share a kind
// without introducing a dependency from lifecycle back to its consumer.
var PolicyTriggerKind = queue.Kind{Name: "policy.trigger", Queue: queue.Default, Policy: queue.Ignore}

// PolicyTrigger identifies the immutable source of a terminal policy job.
type PolicyTrigger struct {
	Run          kernel.ULID `json:"run"`
	Stack        kernel.ULID `json:"stack"`
	StateVersion int64       `json:"stateVersion"`
}

// Schedule is a ScheduleKind job's payload.
type Schedule struct {
	Stack kernel.ULID `json:"stack"`
}

// CompactKind is the job that writes a state's log chunks to one object
// once the run has left the state, keyed by the run and state version so
// it runs once for each.
//
// ref: decision 0008 § "Logs and artifacts"
var CompactKind = queue.Kind{Name: "run.logs.compact", Queue: queue.Default, Policy: queue.Ignore}

// Compact is a CompactKind job's payload: the run, and the state it left
// with that state's version.
type Compact struct {
	Run          kernel.ULID     `json:"run"`
	State        kernel.RunState `json:"state"`
	StateVersion int64           `json:"stateVersion"`
}

// Actor is who moves or creates a run. Caller is the principal's caller
// key, which a stack lock's holder is matched against, and Login the name
// a transition records; the zero Actor is the server.
type Actor struct {
	Caller string
	Login  string
}

// ActorOf is p as an Actor.
func ActorOf(p kernel.Principal) Actor { return Actor{Caller: p.Caller(), Login: p.Login} }

// Run is a run as the lifecycle reads and writes it. Version is its
// state_version; TriggeredBy is the login or trigger that created it;
// RuntimeConfig is the runtime configuration its trigger gave, nil for
// none, and ForceApply SINGLE or CASCADE when it applies with no changes.
// ResolvedConfig is the configuration its first successful handover fixed,
// or a retry inherited, nil until one exists; later handovers reuse it
// without retaining variable values.
// LocalWorkspace is the uploads store's key of the local workspace a run
// proposed from one previews instead of a commit of the repository, empty
// for a run on a commit. WorkerPool is the stack's
// private pool when the run was created, nil for the public pool, and
// Worker the worker that last claimed it. Delta is what its latest plan
// changes, nil until it plans; the report service keeps it. Task is a
// TASK run's command, nil for every other type. ReplanTargets are the
// addresses its last targeted replan named, nil until one did. Stop and
// Kill are a caller's requests to stop and to kill it, nil until one
// asks.
// LogsDeletedAt is when a caller deleted its logs ahead of their
// retention, nil until one did.
type Run struct {
	Account kernel.AccountID
	// Organization is the immutable account owner resolved during creation.
	// Public admission counts its in-flight runs without querying another
	// domain's persistence.
	Organization   *kernel.OrganizationID
	ID             kernel.ULID
	Stack          kernel.ULID
	Type           kernel.RunType
	State          kernel.RunState
	Version        int64
	StateChangedAt time.Time
	Commit         stacksapi.Commit
	TriggeredBy    string
	Priority       kernel.RunPriority
	DriftDetection bool
	RuntimeConfig  json.RawMessage
	ResolvedConfig *runtimeconfig.Record
	ForceApply     string
	LocalWorkspace string
	WorkerPool     *kernel.ULID
	// PublicWorkerPool binds public execution to its installation identity
	// across apply and replan; nil before the first public assignment.
	PublicWorkerPool *kernel.ULID
	Worker           *kernel.ULID
	Delta            *kernel.PlanDelta
	Task             *kernel.Task
	ReplanTargets    []string
	Stop             *Signal
	Kill             *Signal
	LogsDeletedAt    *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Signal is a caller's request to stop or to kill a run: when it was
// made, the login that made it and its note, which the transition the
// signal ends the run with records, and when a poll delivered it to the
// run's worker, nil until one did.
//
// ref: decision 0008 § "Stop and kill"
type Signal struct {
	At          time.Time
	By          string
	Note        string
	DeliveredAt *time.Time
}

// LocalPreview reports whether r previews a local workspace.
func (r Run) LocalPreview() bool { return r.LocalWorkspace != "" }

// Stoppable reports whether r's state takes a stop: it has an edge to
// STOPPED its worker's report drives.
func (r Run) Stoppable() bool {
	_, ok := machine.Allowed(r.Type, r.State, kernel.RunStateStopped, machine.DriverWorkerReport)
	return ok
}

// Killable reports whether r takes a kill: a stop was asked of it, and a
// worker holds it.
func (r Run) Killable() bool { return r.Stop != nil && r.Worker != nil && r.State.OnWorker() }

// Ends is who the transition s ends a run with records, and its note: the
// request's own, else cause. A nil s is the server's, with cause.
func (s *Signal) Ends(cause string) (Actor, string) {
	if s == nil {
		return Actor{}, cause
	}
	if s.Note != "" {
		cause = s.Note
	}
	return Actor{Login: s.By}, cause
}

// Step is one run_state_transitions row: the state a transition enters,
// its sequence, the run's new state_version, and who took it. HasLogs is
// whether the state wrote logs, which the index of its first log chunk
// sets.
type Step struct {
	Sequence int64
	State    kernel.RunState
	At       time.Time
	Until    *time.Time
	Username *string
	Note     *string
	HasLogs  bool
}

// Store is the lifecycle's storage port. Each method joins the transaction
// its context carries.
type Store interface {
	// Lock reads acct's run id and locks its row FOR UPDATE; ErrNotFound
	// when acct lacks it.
	Lock(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Run, error)
	// Move writes run's state, state_version and state_changed_at as
	// step sets them, and appends step to its transitions.
	Move(ctx context.Context, run Run, step Step) error
	// Insert writes run with step, its first transition.
	Insert(ctx context.Context, run Run, step Step) error
	// Active reads acct's run of stack of type typ on commit hash and
	// local workspace workspace, none when empty, that has not ended and
	// has no environment overrides, the oldest when several have not.
	Active(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, typ kernel.RunType, hash, workspace string) (Run, bool, error)
	// OnCommit reads the oldest run of this stack, type and commit in any
	// state and from any trigger. The caller holds the stack's creation lock.
	OnCommit(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, typ kernel.RunType, hash string) (Run, bool, error)
	// SetOverrides seals ovs, run's environment overrides, to its row.
	SetOverrides(ctx context.Context, run Run, ovs []Override) error
	// Track makes run the tracked run of its stack.
	Track(ctx context.Context, run Run) error
	// Unlock deletes the stack run lock run holds, and reports whether it
	// held one.
	Unlock(ctx context.Context, acct kernel.AccountID, run kernel.ULID) (bool, error)
	// Announce tells the held polls of run's pool, once the transaction
	// commits, that run waits for a worker.
	Announce(ctx context.Context, run Run) error
	// Projection reads stack's state, the last state of its tracked run
	// that is one of states, NONE without one, and whether a run holds its
	// run lock.
	Projection(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, states []kernel.StackState) (kernel.StackState, bool, error)
}

// Events appends an event in its change's transaction: the outbox.
type Events interface {
	Append(ctx context.Context, ev outbox.Event) (outbox.Event, error)
}

// Jobs enqueues a job in its change's transaction: the queue.
type Jobs interface {
	EnqueueTx(ctx context.Context, k queue.Kind, payload any, opts ...queue.Option) (int64, error)
}

// Deps are what the lifecycle writes through. Sources is nil until
// internal/app binds the vcs domain's; Now defaults to time.Now.
type Deps struct {
	AccountOrganization func(context.Context, kernel.AccountID) (kernel.OrganizationID, error)
	PublicPoolEnabled   bool
	Store               Store
	Stacks              stacksapi.Stacks
	Events              Events
	Jobs                Jobs
	Sources             runsapi.RunSources
	Now                 func() time.Time
	Evidence            evidence.Store
	Plans               evidence.PlanStore
	Approvals           Approvals
}

// Approvals evaluates and records a decision under the run's current fence.
type Approvals interface {
	Evaluate(context.Context, Run) (policiesapi.Evaluation, error)
}

// Lifecycle is the one place a run is created or changes state.
//
// ref: decision 0008 § "One entry point"
type Lifecycle struct{ d Deps }

// New returns the lifecycle over d.
func New(d Deps) *Lifecycle {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Lifecycle{d: d}
}

// BindSources binds the vcs domain's revocation of a run's source
// credentials, which every run's end calls.
func (l *Lifecycle) BindSources(s runsapi.RunSources) { l.d.Sources = s }

// Request is one transition: acct's run, the state_version its caller
// read, the state it enters, the driver taking it, the guards the driver
// checked, who takes it, a note, when a timed state ends, and the
// run.timers rule that takes it, which its run_updated event names.
type Request struct {
	Account  kernel.AccountID
	Run      kernel.ULID
	Version  int64
	To       kernel.RunState
	Driver   machine.Driver
	Checked  []machine.Guard
	Actor    Actor
	Note     string
	Until    *time.Time
	Timer    runsapi.Timer
	Receipts []policiesapi.Receipt
}

// Transition moves req's run in the transaction ctx carries, so its row,
// its transitions, its stack's projection, its event, its stack's run lock
// and its stack's scheduling commit or roll back together. It locks the
// run's row, refuses a stale version with a *Conflict and a move no edge
// allows, or one without the edge's guards checked, with a *Refused. On a
// terminal state it releases the stack run lock the run holds and the
// credentials minted for it, and a DESTROY run ends its stack's deletion:
// FINISHED deletes the stack, with the run, and any other end leaves it.
// It returns the run as moved.
//
// ref: decision 0008 § "One entry point"
// ref: decision 0008 § "Destroy on delete"
func (l *Lifecycle) Transition(ctx context.Context, req Request) (Run, error) {
	if !tx.In(ctx) {
		return Run{}, ErrNoTransaction
	}
	run, err := l.d.Store.Lock(ctx, req.Account, req.Run)
	if err != nil {
		return Run{}, err //nolint:wrapcheck // the store names the read
	}
	if run.Version != req.Version {
		return Run{}, &Conflict{Run: run.ID, State: run.State, Version: run.Version}
	}
	edge, ok := machine.Allowed(run.Type, run.State, req.To, req.Driver)
	if !ok {
		return Run{}, &Refused{Type: run.Type, From: run.State, To: req.To, Driver: req.Driver}
	}
	for _, g := range edge.Guards {
		if !slices.Contains(req.Checked, g) {
			return Run{}, &Refused{
				Type: run.Type, From: run.State, To: req.To, Driver: req.Driver, Guard: g,
			}
		}
	}
	now := l.now()
	step := Step{
		Sequence: run.Version + 1, State: req.To, At: now, Until: req.Until,
		Username: optional(req.Actor.Login), Note: optional(req.Note),
	}
	if err := l.d.Store.Move(ctx, run, step); err != nil {
		return Run{}, err //nolint:wrapcheck // the store names the write
	}
	from := run.State
	run.State, run.Version, run.StateChangedAt, run.UpdatedAt = req.To, step.Sequence, now, now
	if len(req.Receipts) != 0 {
		if l.d.Evidence == nil {
			return Run{}, kernel.ErrUnavailable
		}
		if err := l.d.Evidence.RecordPolicyEvidence(ctx, run.Account, run.ID, run.Version, req.Receipts); err != nil {
			return Run{}, err //nolint:wrapcheck // the recorder names the transactional write
		}
	}
	var approval policiesapi.Evaluation
	approvalEntry := l.d.Approvals != nil && (run.State == kernel.RunStateUnconfirmed || run.State == kernel.RunStatePendingReview)
	if approvalEntry {
		approval, err = l.EvaluateApproval(ctx, run)
		if err != nil {
			return Run{}, err
		}
	}
	if err := l.consequences(ctx, run, step, req.Timer); err != nil {
		return Run{}, err
	}
	if from.OnWorker() {
		// Only a worker writes a state's log, and no chunk of the state
		// is indexed once the run has left it, so the job finds them all.
		_, err := l.d.Jobs.EnqueueTx(ctx, CompactKind,
			Compact{Run: run.ID, State: from, StateVersion: req.Version},
			queue.Account(run.Account.ULID),
			queue.UniqueKey(fmt.Sprintf("%s:%d", run.ID, req.Version)))
		if err != nil {
			return Run{}, err //nolint:wrapcheck // the queue names the kind
		}
	}
	if run.Type == kernel.RunTypeDestroy && run.State.Terminal() {
		// Last, since deleting the stack deletes its runs with it.
		err := l.d.Stacks.Destroyed(ctx, run.Account, run.Stack,
			run.State == kernel.RunStateFinished)
		if err != nil {
			return Run{}, err //nolint:wrapcheck // the stacks domain names the write
		}
	}
	if approvalEntry {
		return l.ApplyApproval(ctx, run, approval)
	}
	return run, nil
}

// NewRun is a run to create: acct's stack, its type, the commit it works
// on, who creates it, its priority, normal when empty, whether drift
// detection starts it, its runtime configuration and force-apply mode,
// the local workspace it previews, the environment overrides of a
// proposed run, a TASK run's task, and a note for its first transition.
// Resolved is a retry's inherited first-handover record, nil when the
// retried run ended before it recorded one. Variables and mounted files
// are read afresh at handover rather than inherited in this record.
type NewRun struct {
	Account        kernel.AccountID
	Stack          kernel.ULID
	Type           kernel.RunType
	Commit         stacksapi.Commit
	Actor          Actor
	Priority       kernel.RunPriority
	DriftDetection bool
	RuntimeConfig  json.RawMessage
	Resolved       *runtimeconfig.Record
	ForceApply     string
	LocalWorkspace string
	Overrides      []Override
	Task           *kernel.Task
	Note           string
	// Receipts seed this run's policy-owned flags before QUEUED consequences.
	Receipts []policiesapi.Receipt
	// AlwaysCreate is an operation's explicit request for a fresh run.
	// RuntimeConfig alone does not imply it: retries can reuse an active run.
	AlwaysCreate bool
	// OncePerCommit checks every prior run, including terminal runs and
	// runs with overrides or local workspaces, under the stack's row lock.
	OncePerCommit bool
	// BeforeCreate admits a genuinely new run in this transaction. It is
	// skipped for every refusal and reused run, before any insert or event.
	BeforeCreate func(context.Context) error
}

// Override is a run-local environment value from a proposal or a trigger's
// runtime configuration, stored only in the sealed override column.
type Override struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Create creates n's run in QUEUED, T1, in the transaction ctx carries,
// and reports whether it created one: a run of the stack, type, commit
// and local workspace that has not ended is returned instead, unless n
// has overrides, a task or AlwaysCreate. OncePerCommit instead reuses any
// matching run, including one that ended or has overrides. BeforeCreate runs
// only after refusals and reuse checks, before any new run or consequence.
// It locks the stack's row first, so two
// creations of one run wait on each other and a delete of the stack
// waits for the creation to commit. A disabled stack refuses with
// ErrDisabled, a deleting one with ErrDeleting, a stack locked by another
// caller with a *Locked, and a
// TASK without its task, or another type with one, with ErrTask.
//
// ref: decision 0008 § "The human lock"
func (l *Lifecycle) Create(ctx context.Context, n NewRun) (Run, bool, error) {
	if !tx.In(ctx) {
		return Run{}, false, ErrNoTransaction
	}
	if _, ok := machine.Allowed(n.Type, machine.Created, kernel.RunStateQueued,
		machine.DriverCreation); !ok {
		return Run{}, false, &Refused{
			Type: n.Type, From: machine.Created, To: kernel.RunStateQueued,
			Driver: machine.DriverCreation,
		}
	}
	if (n.Type == kernel.RunTypeTask) != (n.Task != nil) {
		return Run{}, false, fmt.Errorf("%w: a %s run", ErrTask, n.Type)
	}
	st, err := l.d.Stacks.LockForRun(ctx, n.Account, n.Stack)
	if err != nil {
		return Run{}, false, err //nolint:wrapcheck // the stacks domain names the read
	}
	switch {
	case st.Disabled:
		return Run{}, false, fmt.Errorf("%w: %s", ErrDisabled, st.Slug)
	case st.Deleting:
		return Run{}, false, fmt.Errorf("%w: %s", ErrDeleting, st.Slug)
	}
	if st.Lock != nil && st.Lock.Holder != n.Actor.Caller {
		return Run{}, false, &Locked{By: st.Lock.By}
	}
	// Overrides and tasks always create a run. Other operations explicitly
	// choose fresh creation; retries keep their normal active-run reuse.
	if n.OncePerCommit {
		existing, found, err := l.d.Store.OnCommit(ctx, n.Account, n.Stack, n.Type, n.Commit.Hash)
		if err != nil || found {
			return existing, false, err //nolint:wrapcheck // the store names the read
		}
	} else if len(n.Overrides) == 0 && n.Task == nil && !n.AlwaysCreate {
		existing, found, err := l.d.Store.Active(ctx, n.Account, n.Stack, n.Type, n.Commit.Hash,
			n.LocalWorkspace)
		if err != nil || found {
			return existing, false, err //nolint:wrapcheck // the store names the read
		}
	}
	if n.BeforeCreate != nil {
		if err := n.BeforeCreate(ctx); err != nil {
			return Run{}, false, err //nolint:wrapcheck // the caller identifies its admission refusal
		}
	}
	var organization *kernel.OrganizationID
	if l.d.AccountOrganization != nil {
		id, err := l.d.AccountOrganization(ctx, n.Account)
		if err != nil {
			return Run{}, false, fmt.Errorf("runs: resolve account organization: %w", err)
		}
		if id.IsZero() {
			return Run{}, false, ErrAccountMissing
		}
		organization = &id
	}
	now := l.now()
	priority := n.Priority
	if priority == "" {
		priority = kernel.RunPriorityNormal
	}
	run := Run{
		Organization: organization,
		Account:      n.Account, ID: kernel.NewULID(), Stack: n.Stack, Type: n.Type,
		State: kernel.RunStateQueued, Version: 1, StateChangedAt: now, Commit: n.Commit,
		TriggeredBy: n.Actor.Login, Priority: priority, DriftDetection: n.DriftDetection,
		RuntimeConfig: n.RuntimeConfig, ResolvedConfig: n.Resolved,
		ForceApply: n.ForceApply, LocalWorkspace: n.LocalWorkspace,
		WorkerPool: st.WorkerPool, Task: n.Task, CreatedAt: now, UpdatedAt: now,
	}
	step := Step{
		Sequence: 1, State: kernel.RunStateQueued, At: now,
		Username: optional(n.Actor.Login), Note: optional(n.Note),
	}
	if err := l.d.Store.Insert(ctx, run, step); err != nil {
		return Run{}, false, err //nolint:wrapcheck // the store names the write
	}
	if len(n.Receipts) != 0 {
		if l.d.Evidence == nil {
			return Run{}, false, fmt.Errorf("%w: initial policy evidence recorder", kernel.ErrUnavailable)
		}
		if err := l.d.Evidence.RecordPolicyEvidence(ctx, run.Account, run.ID, run.Version, n.Receipts); err != nil {
			return Run{}, false, err //nolint:wrapcheck // the evidence recorder names the fenced write
		}
	}
	if len(n.Overrides) > 0 {
		if err := l.d.Store.SetOverrides(ctx, run, n.Overrides); err != nil {
			return Run{}, false, err //nolint:wrapcheck // the store names the write
		}
	}
	if err := l.consequences(ctx, run, step, ""); err != nil {
		return Run{}, false, err
	}
	return run, true, nil
}

// consequences are what run's entering step.State causes in its
// transaction: on a terminal state the release of its stack run lock and
// of its credentials, the stack's projection when the run tracks the
// stack or released the lock, its event, the stack's scheduling, and the
// announcement to its private pool when it waits for a worker.
func (l *Lifecycle) consequences(ctx context.Context, run Run, step Step, timer runsapi.Timer) error {
	released := false
	if run.State.Terminal() {
		var err error
		if released, err = l.d.Store.Unlock(ctx, run.Account, run.ID); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		if l.d.Sources != nil {
			if err := l.d.Sources.Release(ctx, run.Account, run.ID); err != nil {
				return fmt.Errorf("runs: release the credentials of run %s: %w", run.ID, err)
			}
		}
	}
	_, stackState := kernel.StackStateOf(run.State)
	tracks := stackState && (run.Type == kernel.RunTypeTracked || run.Type == kernel.RunTypeDestroy)
	if tracks {
		if err := l.d.Store.Track(ctx, run); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
	}
	if tracks || released {
		if err := l.Project(ctx, run.Account, run.Stack); err != nil {
			return err
		}
	}
	if err := l.updated(ctx, run, step, timer); err != nil {
		return err
	}
	if run.State.Terminal() && (run.Type == kernel.RunTypeTracked || run.Type == kernel.RunTypeTask) {
		_, err := l.d.Jobs.EnqueueTx(ctx, PolicyTriggerKind,
			PolicyTrigger{Run: run.ID, Stack: run.Stack, StateVersion: run.Version},
			queue.Account(run.Account.ULID), queue.UniqueKey(fmt.Sprintf("run:%s:%d", run.ID, run.Version)))
		if err != nil {
			return err //nolint:wrapcheck // the queue names the terminal work
		}
	}
	if (run.WorkerPool != nil || l.d.PublicPoolEnabled) && slices.Contains(machine.Claimable(), run.State) {
		if err := l.d.Store.Announce(ctx, run); err != nil {
			return err //nolint:wrapcheck // the store names the notification
		}
	}
	if step.Sequence == 1 || run.State.Terminal() || run.State == kernel.RunStateUnconfirmed {
		_, err := l.d.Jobs.EnqueueTx(ctx, ScheduleKind, Schedule{Stack: run.Stack},
			queue.Account(run.Account.ULID), queue.UniqueKey("stack:"+run.Stack.String()))
		if err != nil {
			return err //nolint:wrapcheck // the queue names the kind
		}
	}
	return nil
}

// Project writes acct's stack's state and blocked into the stacks domain,
// from its tracked run's last state that is a StackState and from its run
// lock, in the transaction ctx carries.
func (l *Lifecycle) Project(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error {
	state, blocked, err := l.d.Store.Projection(ctx, acct, stack, kernel.StackStates())
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	//nolint:wrapcheck // the stacks domain names the write
	return l.d.Stacks.SetRunState(ctx, acct, stack, state, blocked)
}

// updated appends run's run_updated event for step, which timer took
// when it is not empty.
func (l *Lifecycle) updated(ctx context.Context, run Run, step Step, timer runsapi.Timer) error {
	snapshot := evidence.Empty()
	if l.d.Evidence != nil {
		byRun, err := l.d.Evidence.PolicyEvidence(ctx, run.Account, []kernel.ULID{run.ID})
		if err != nil {
			return err //nolint:wrapcheck // the evidence store names the read
		}
		snapshot = byRun[run.ID]
	}
	receipts := make([]runsapi.PolicyReceiptSummary, 0, len(snapshot.Receipts))
	for _, r := range snapshot.Receipts {
		receipts = append(receipts, runsapi.PolicyReceiptSummary{Type: string(r.Type), Name: r.PolicyName, Outcome: string(r.Outcome), Flags: r.Flags})
	}
	st, err := l.d.Stacks.Get(ctx, run.Account, run.Stack)
	if err != nil {
		return err //nolint:wrapcheck // the stacks domain names the read
	}
	var planDecision *runsapi.PlanPolicyDecision
	if l.d.Plans != nil {
		outcomes, err := l.d.Plans.PlanOutcomes(ctx, run.Account, []kernel.ULID{run.ID})
		if err != nil {
			return fmt.Errorf("runs: read notification plan outcome: %w", err)
		}
		for _, outcome := range outcomes[run.ID] {
			if outcome.Current {
				planDecision = &runsapi.PlanPolicyDecision{Deny: outcome.Deny, Warn: outcome.Warn}
			}
		}
	}
	doc, err := json.Marshal(runsapi.Updated{
		State: run.State, Username: step.Username, Note: step.Note, Timer: timer,
		PolicyReceipts: receipts, PlanPolicyDecision: planDecision,
		Run: runsapi.UpdatedRun{
			ID: run.ID, Type: run.Type, State: run.State,
			Commit: runsapi.UpdatedCommit{
				Author: run.Commit.AuthorName, Hash: run.Commit.Hash,
				Message: run.Commit.Message, URL: run.Commit.URL, Tag: run.Commit.Tag,
				CreatedAt: run.Commit.Timestamp.UnixNano(),
			},
			CreatedAt: run.CreatedAt.UnixNano(), UpdatedAt: run.UpdatedAt.UnixNano(),
			DriftDetection: run.DriftDetection, TriggeredBy: optional(run.TriggeredBy),
			Flags: snapshot.Flags,
		},
		Stack: runsapi.UpdatedStack{ID: st.Slug, ULID: st.ID, Name: st.Name},
	})
	if err != nil {
		return fmt.Errorf("runs: encode the run_updated of run %s: %w", run.ID, err)
	}
	_, err = l.d.Events.Append(ctx, outbox.Event{
		Account:       run.Account.ULID,
		Kind:          runsapi.UpdatedKind,
		SchemaVersion: runsapi.UpdatedVersion,
		Space:         st.Space.String(),
		Subject:       outbox.Subject{Kind: "run", ID: run.ID.String()},
		Sequence:      run.Version,
		OccurredAt:    step.At,
		Document:      doc,
	})
	return err //nolint:wrapcheck // the outbox names the append
}

// now is the clock at PostgreSQL's precision, so a run read back equals
// the run written.
func (l *Lifecycle) now() time.Time { return l.d.Now().UTC().Truncate(time.Microsecond) }

// optional is s, or nil when s is empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
