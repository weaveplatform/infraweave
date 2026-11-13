package report

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// Chunk is a log chunk as the store indexes it: the state version and
// state it was written in, its sequence number there, its object's key
// and size, and when its first and last lines were written.
type Chunk struct {
	Version int64
	State   kernel.RunState
	Seq     int64
	Key     string
	Bytes   int64
	FirstAt time.Time
	LastAt  time.Time
}

// Artifact is an uploaded object other than a log chunk as the store
// records it: the state version it was uploaded in, its kind and name,
// and its object's key, size and SHA-256 as uploaded.
type Artifact struct {
	Version int64
	Kind    workersapi.UploadKind
	Name    string
	Key     string
	Size    int64
	SHA256  [32]byte
}

// Object is a stored object a purge deletes.
type Object struct {
	Store objectstore.Name
	Key   string
}

// Store is what reports need of the runs' storage. Each method joins the
// transaction its context carries.
type Store interface {
	// Handed reads the run id of whichever account has it and locks its
	// row; lifecycle.ErrNotFound when none has it.
	Handed(ctx context.Context, id kernel.ULID) (lifecycle.Run, error)
	// StateAt reads the state acct's run entered at state version
	// version.
	StateAt(ctx context.Context, acct kernel.AccountID, run kernel.ULID,
		version int64) (kernel.RunState, error)
	// ArtifactKey is run's artifact key, made the first time it is asked
	// for.
	ArtifactKey(ctx context.Context, run lifecycle.Run) (crypt.Value, error)
	// Indexed reports whether run's log chunk seq of state version
	// version is indexed.
	Indexed(ctx context.Context, run lifecycle.Run, version, seq int64) (bool, error)
	// IndexChunk indexes c, a log chunk of run.
	IndexChunk(ctx context.Context, run lifecycle.Run, c Chunk) error
	// Recorded reports whether run recorded the object of kind named
	// name in state version version.
	Recorded(ctx context.Context, run lifecycle.Run, version int64,
		kind workersapi.UploadKind, name string) (bool, error)
	// Record records a, an uploaded object of run.
	Record(ctx context.Context, run lifecycle.Run, a Artifact) error
	// SetDelta keeps d, run's plan's delta, on the run.
	SetDelta(ctx context.Context, run lifecycle.Run, d kernel.PlanDelta) error
	// Workspace reads the key of the workspace run recorded last, and
	// false when it recorded none.
	Workspace(ctx context.Context, run lifecycle.Run) (string, bool, error)
	// PurgeObjects deletes up to batch of acct's log chunks or other
	// recorded objects and answers their objects, with the compacted log
	// of each state whose chunks it deleted; none once both are empty.
	PurgeObjects(ctx context.Context, acct kernel.AccountID, batch int) ([]Object, error)
}

// Objects records and signs objects in blob_objects; blobs.Objects is
// one.
type Objects interface {
	Begin(ctx context.Context, store objectstore.Name, key string, size int64, sum []byte) error
	BeginSealed(ctx context.Context, store objectstore.Name, key string, size int64,
		sum, content []byte) error
	SignPut(ctx context.Context, store objectstore.Name, key string, size int64,
		sum []byte) (objectstore.Signed, error)
	SignGet(ctx context.Context, store objectstore.Name, key string) (objectstore.Signed, error)
	CommitUpload(
		ctx context.Context,
		store objectstore.Name,
		key string,
	) (objectstore.Object, error)
	Delete(ctx context.Context, store objectstore.Name, key string) error
}

// Mover moves a run; the lifecycle is one.
type Mover interface {
	Transition(ctx context.Context, req lifecycle.Request) (lifecycle.Run, error)
}

// Stacks reads a run's stack for the plan decision; stacksapi.Stacks is
// one.
type Stacks interface {
	Settings(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error)
}

// Deps are what reports read and write through: the database a purge
// opens its transactions on, the runs' storage, the objects, the
// lifecycle, and the stacks the plan decision reads.
type Deps struct {
	DB        tx.Beginner
	Store     Store
	Objects   Objects
	Lifecycle Mover
	Stacks    Stacks
}

// Service is the runs domain's workersapi.Reports.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
// ref: decision 0008 § "Logs and artifacts"
type Service struct{ d Deps }

var _ workersapi.Reports = (*Service)(nil)

// New returns the service over d.
func New(d Deps) *Service { return &Service{d: d} }

// The largest object of each kind a worker may declare, as uploaded: a
// log chunk the runner cuts at 1 MiB, an artifact as large as a plan's
// JSON grows, a workspace large enough for providers and modules, and
// observability artifacts, which are traces and metrics. The API
// document's size bound is the largest of them.
//
// ref: decision 0008 § "Logs and artifacts"
var maxSize = map[workersapi.UploadKind]int64{
	workersapi.UploadLogChunk:      1 << 20,
	workersapi.UploadArtifact:      512 << 20,
	workersapi.UploadWorkspace:     2 << 30,
	workersapi.UploadObservability: 64 << 20,
}

// storeOf is each upload kind's store.
var storeOf = map[workersapi.UploadKind]objectstore.Name{
	workersapi.UploadLogChunk:      objectstore.RunLogs,
	workersapi.UploadArtifact:      objectstore.RunArtifacts,
	workersapi.UploadWorkspace:     objectstore.Workspaces,
	workersapi.UploadObservability: objectstore.RunObservability,
}

// name is an upload's name: one key segment, never . or .., so a key
// stays under its run's prefix.
var name = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// seqName is a log chunk's name: its sequence number in decimal, as
// strconv.FormatInt writes it, so one sequence number has one key.
var seqName = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})$`)

// held reads run id, as a run token names it, and locks its row; the run
// must be on a private pool's worker.
func (s *Service) held(ctx context.Context, id kernel.ULID) (lifecycle.Run, error) {
	if !tx.In(ctx) {
		return lifecycle.Run{}, lifecycle.ErrNoTransaction
	}
	r, err := s.d.Store.Handed(ctx, id)
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		return r, fmt.Errorf("%w: %s", workersapi.ErrNotOnWorker, id)
	case err != nil:
		return r, err //nolint:wrapcheck // the store names the read
	case r.WorkerPool == nil || !r.State.OnWorker():
		return r, fmt.Errorf("%w: run %s is %s", workersapi.ErrNotOnWorker, id, r.State)
	}
	return r, nil
}

// current is held, at state version version.
func (s *Service) current(
	ctx context.Context,
	id kernel.ULID,
	version int64,
) (lifecycle.Run, error) {
	r, err := s.held(ctx, id)
	if err == nil && r.Version != version {
		err = fmt.Errorf("%w: run %s is at %d, not %d", workersapi.ErrMovedOn, id, r.Version,
			version)
	}
	return r, err
}

// Report moves run id as r reports, as workersapi.Reports says, through
// the edges a worker's report drives, and answers the next step; a
// succeeded report of PLANNING takes the plan decision instead. A report
// one state version behind is answered as it was when it took the run's
// last transition, so a worker whose answer was lost asks again.
func (s *Service) Report(
	ctx context.Context,
	id kernel.ULID,
	r workersapi.Report,
) (workersapi.Step, error) {
	if planned(r) != (r.Delta != nil) {
		return workersapi.Step{}, fmt.Errorf("%w: %s %s", workersapi.ErrDeltaRefused, r.State,
			r.Event)
	}
	run, err := s.held(ctx, id)
	if err != nil {
		return workersapi.Step{}, err
	}
	switch r.StateVersion {
	case run.Version:
		if planned(r) && run.State == kernel.RunStatePlanning {
			return s.decide(ctx, run, r)
		}
		to, err := target(run.Type, run.State, r)
		if err != nil {
			return workersapi.Step{}, err
		}
		if to == run.State {
			return step(run), nil
		}
		return s.move(ctx, run, to, machine.DriverWorkerReport, r.Details)
	case run.Version - 1:
		from, err := s.d.Store.StateAt(ctx, run.Account, run.ID, r.StateVersion)
		if err != nil {
			return workersapi.Step{}, err //nolint:wrapcheck // the store names the read
		}
		if planned(r) && from == kernel.RunStatePlanning && decided(run.Type, run.State) {
			return step(run), nil
		}
		if to, err := target(run.Type, from, r); err == nil && to == run.State && to != from {
			return step(run), nil
		}
	}
	return workersapi.Step{}, fmt.Errorf("%w: run %s is at %d, not %d", workersapi.ErrMovedOn,
		id, run.Version, r.StateVersion)
}

// move moves run to to, driven by driver, with note as the transition's,
// and answers the next step.
func (s *Service) move(
	ctx context.Context,
	run lifecycle.Run,
	to kernel.RunState,
	driver machine.Driver,
	note string,
) (workersapi.Step, error) {
	moved, err := s.d.Lifecycle.Transition(ctx, lifecycle.Request{
		Account: run.Account, Run: run.ID, Version: run.Version, To: to, Driver: driver,
		Note: note,
	})
	if err != nil {
		return workersapi.Step{}, err //nolint:wrapcheck // the lifecycle names the move
	}
	return step(moved), nil
}

// decide takes the plan decision for run, in PLANNING, on r's delta, in
// the report's transaction: it keeps the delta on the run and moves it
// to the state Decision answers for its stack.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
func (s *Service) decide(
	ctx context.Context,
	run lifecycle.Run,
	r workersapi.Report,
) (workersapi.Step, error) {
	st, err := s.d.Stacks.Settings(ctx, run.Account, run.Stack)
	if err != nil {
		return workersapi.Step{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	if err := s.d.Store.SetDelta(ctx, run, *r.Delta); err != nil {
		return workersapi.Step{}, err //nolint:wrapcheck // the store names the write
	}
	return s.move(ctx, run, Decision(run.Type, *r.Delta, st), machine.DriverDecision, r.Details)
}

// Decision is the state the plan decision moves a run of type t out of
// PLANNING to when no plan policy is attached, from its plan's delta d and
// its stack st: a test case applies (T21); a tracked run with changes
// applies when its stack deploys automatically and is unlocked (T21), and
// waits for confirmation otherwise (T20); any other run, or a tracked one
// whose plan changes nothing, finishes (T17). A type with none of these
// edges is refused by the lifecycle.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
func Decision(t kernel.RunType, d kernel.PlanDelta, st stacksapi.Settings) kernel.RunState {
	switch {
	case t == kernel.RunTypeTesting:
		return kernel.RunStateApplying
	case t != kernel.RunTypeTracked || !d.Changes():
		return kernel.RunStateFinished
	case st.Autodeploy && st.Lock == nil:
		return kernel.RunStateApplying
	default:
		return kernel.RunStateUnconfirmed
	}
}

// planned reports whether r is a succeeded report of PLANNING, the one
// that carries the plan's delta.
func planned(r workersapi.Report) bool {
	return r.Event == workersapi.ReportSucceeded && r.State == kernel.RunStatePlanning
}

// decided reports whether the plan decision moves a run of type t out of
// PLANNING to state, so a repeated planning report finds its decision
// whatever the stack is now.
func decided(t kernel.RunType, state kernel.RunState) bool {
	return slices.ContainsFunc(machine.Next(t, kernel.RunStatePlanning), func(e machine.Edge) bool {
		return e.To == state && slices.Contains(e.Drivers, machine.DriverDecision)
	})
}

// target is the state a run of type t in state from enters on r: for a
// started report of the state from leads to, its handover's edge; for a
// started report of from itself, from; for a succeeded, failed or stopped
// report of from, the edge a worker's report drives out of from to the
// next phase, to FAILED or to STOPPED. A succeeded report of PLANNING has
// no such edge: Report takes the plan decision for it.
func target(t kernel.RunType, from kernel.RunState, r workersapi.Report) (kernel.RunState, error) {
	if r.Event == workersapi.ReportStarted && r.State == from && !preparing(from) {
		return from, nil
	}
	for _, e := range machine.Next(t, from) {
		if !slices.Contains(e.Drivers, machine.DriverWorkerReport) {
			continue
		}
		var ok bool
		switch r.Event {
		case workersapi.ReportStarted:
			ok = preparing(from) && e.To == r.State
		case workersapi.ReportSucceeded:
			ok = r.State == from && !preparing(from) && e.To != kernel.RunStateFailed &&
				e.To != kernel.RunStateStopped
		case workersapi.ReportFailed:
			ok = r.State == from && e.To == kernel.RunStateFailed
		case workersapi.ReportStopped:
			ok = r.State == from && e.To == kernel.RunStateStopped
		}
		if ok {
			return e.To, nil
		}
	}
	return "", fmt.Errorf("%w: %s %s while %s", workersapi.ErrReportRefused, r.State, r.Event,
		from)
}

// preparing reports whether a worker prepares a run in s, before its
// first started report.
func preparing(s kernel.RunState) bool {
	return s == kernel.RunStatePreparing || s == kernel.RunStatePreparingApply ||
		s == kernel.RunStatePreparingReplan
}

// step is what a worker does with run as it is: exit once it ended, pause
// while it waits for a decision, and execute its state otherwise.
func step(run lifecycle.Run) workersapi.Step {
	out := workersapi.Step{
		Action: workersapi.ActionExecute, State: run.State, StateVersion: run.Version,
	}
	switch {
	case run.State.Terminal():
		out.Action = workersapi.ActionExit
	case run.State == kernel.RunStateUnconfirmed || run.State == kernel.RunStatePendingReview:
		out.Action = workersapi.ActionPause
	}
	return out
}

// key is the key of run's object named name in its current state.
//
// ref: decision 0005 § "Keys"
func key(run lifecycle.Run, name string) string {
	return fmt.Sprintf("%s/%s/%s-%d/%s", run.Account, run.ID, run.State, run.Version, name)
}

// LogKey is the key, in the run-logs store, of the object acct's run's
// log of state at state version version is compacted into: its chunks'
// prefix ending in .log.
//
// ref: decision 0008 § "Logs and artifacts"
func LogKey(acct kernel.AccountID, run kernel.ULID, state kernel.RunState, version int64) string {
	return fmt.Sprintf("%s/%s/%s-%d.log", acct, run, state, version)
}

// Uploads records and signs each of ups, as workersapi.Reports says,
// after checking every declaration: its kind, its name, a log chunk's
// being a sequence number, its size within its kind's limit, and a
// 32-byte SHA-256. An artifact's row holds the content key the runner
// seals it under, derived from the run's artifact key and its key, so
// the server opens it later.
func (s *Service) Uploads(
	ctx context.Context,
	id kernel.ULID,
	version int64,
	ups []workersapi.Upload,
) ([]workersapi.SignedUpload, error) {
	run, err := s.current(ctx, id, version)
	if err != nil {
		return nil, err
	}
	if err := checkUploads(ups); err != nil {
		return nil, err
	}
	var (
		artifact crypt.Value
		opened   bool
	)
	out := make([]workersapi.SignedUpload, 0, len(ups))
	for _, u := range ups {
		store, k := storeOf[u.Kind], key(run, u.Name)
		if u.Kind == workersapi.UploadArtifact {
			if !opened {
				if artifact, err = s.d.Store.ArtifactKey(ctx, run); err != nil {
					return nil, err //nolint:wrapcheck // the store names the key
				}
				opened = true
			}
			var content []byte
			if content, err = seal.ArtifactKey(artifact.Reveal(), k); err != nil {
				return nil, err //nolint:wrapcheck // the seal package names the key
			}
			err = s.d.Objects.BeginSealed(ctx, store, k, u.Size, u.SHA256, content)
		} else {
			err = s.d.Objects.Begin(ctx, store, k, u.Size, u.SHA256)
		}
		if errors.Is(err, blobs.ErrKeyInUse) {
			return nil, fmt.Errorf("%w: %s %s", workersapi.ErrUploadExists, u.Kind, u.Name)
		}
		if err != nil {
			return nil, err //nolint:wrapcheck // blobs names the object
		}
		signed, err := s.d.Objects.SignPut(ctx, store, k, u.Size, u.SHA256)
		if err != nil {
			return nil, err //nolint:wrapcheck // the drivers name the object
		}
		header := make(map[string]string, len(signed.Header))
		for _, h := range slices.Sorted(maps.Keys(signed.Header)) {
			header[h] = signed.Header.Get(h)
		}
		out = append(out, workersapi.SignedUpload{
			Kind: u.Kind, Name: u.Name, Key: k, URL: signed.URL, Header: header,
		})
	}
	return out, nil
}

// checkUploads refuses, in one *kernel.Invalid, every declaration of ups
// that names no kind, a name a key cannot hold, a log chunk's name that
// is not its sequence number, a size outside its kind's limit or a
// SHA-256 of other than 32 bytes, as the worker API's schema does.
func checkUploads(ups []workersapi.Upload) error {
	var errs []kernel.FieldError
	refuse := func(i int, field, reason, msg string) {
		errs = append(errs, kernel.FieldError{
			Pointer: fmt.Sprintf("/uploads/%d/%s", i, field), Reason: reason, Message: msg,
		})
	}
	for i, u := range ups {
		limit, ok := maxSize[u.Kind]
		switch {
		case !ok:
			refuse(i, "kind", kernel.ReasonEnum, "The kind is not an upload kind.")
		case u.Kind == workersapi.UploadLogChunk && !seqName.MatchString(u.Name):
			refuse(i, "name", kernel.ReasonPattern,
				"A log chunk's name is its sequence number in decimal.")
		case !name.MatchString(u.Name):
			refuse(i, "name", kernel.ReasonPattern, "The name is not one key segment.")
		}
		if u.Size < 0 || (ok && u.Size > limit) {
			refuse(i, "size", kernel.ReasonMaximum,
				fmt.Sprintf("The size exceeds the %d bytes of the kind.", limit))
		}
		if len(u.SHA256) != 32 {
			refuse(i, "sha256", kernel.ReasonFormat, "The SHA-256 is not 32 bytes.")
		}
	}
	if errs == nil {
		return nil
	}
	return &kernel.Invalid{Detail: "The uploads are refused.", Errors: errs}
}

// uploadInvalid is a commit's refusal of the upload at pointer, whose
// object is missing or does not match its declaration, as a
// *kernel.Invalid; any other error is returned as it is.
func uploadInvalid(err error, pointer string) error {
	if errors.Is(err, blobs.ErrNotUploaded) || errors.Is(err, objectstore.ErrSize) ||
		errors.Is(err, blobs.ErrChecksum) {
		return &kernel.Invalid{
			Detail: "An upload is missing or does not match its declaration.",
			Errors: []kernel.FieldError{{
				Pointer: pointer, Reason: kernel.ReasonRule, Message: err.Error(),
			}},
		}
	}
	return err
}

// LogChunks commits and indexes each of chunks, as workersapi.Reports
// says, in the run's current state.
func (s *Service) LogChunks(
	ctx context.Context,
	id kernel.ULID,
	version int64,
	chunks []workersapi.LogChunk,
) error {
	run, err := s.current(ctx, id, version)
	if err != nil {
		return err
	}
	for i, c := range chunks {
		if c.Seq < 0 || c.LastAt.Before(c.FirstAt) {
			return &kernel.Invalid{
				Detail: "The log chunks are refused.",
				Errors: []kernel.FieldError{{
					Pointer: fmt.Sprintf("/chunks/%d", i), Reason: kernel.ReasonRule,
					Message: "A chunk's sequence number is at least 0 and its last line is not " +
						"before its first.",
				}},
			}
		}
		done, err := s.d.Store.Indexed(ctx, run, version, c.Seq)
		if err != nil || done {
			if err != nil {
				return err //nolint:wrapcheck // the store names the read
			}
			continue
		}
		k := key(run, strconv.FormatInt(c.Seq, 10))
		obj, err := s.d.Objects.CommitUpload(ctx, objectstore.RunLogs, k)
		if err != nil {
			return uploadInvalid(err, fmt.Sprintf("/chunks/%d", i))
		}
		if err := s.d.Store.IndexChunk(ctx, run, Chunk{
			Version: version, State: run.State, Seq: c.Seq, Key: k, Bytes: obj.Size,
			FirstAt: c.FirstAt, LastAt: c.LastAt,
		}); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
	}
	return nil
}

// Artifacts commits and records each of recs, as workersapi.Reports says,
// in the run's current state.
func (s *Service) Artifacts(
	ctx context.Context,
	id kernel.ULID,
	version int64,
	recs []workersapi.Recorded,
) error {
	run, err := s.current(ctx, id, version)
	if err != nil {
		return err
	}
	for i, r := range recs {
		store, ok := storeOf[r.Kind]
		if !ok || r.Kind == workersapi.UploadLogChunk || !name.MatchString(r.Name) {
			return &kernel.Invalid{
				Detail: "The artifacts are refused.",
				Errors: []kernel.FieldError{
					{
						Pointer: fmt.Sprintf("/artifacts/%d", i),
						Reason:  kernel.ReasonRule,
						Message: "An artifact is an artifact, workspace or observability upload with a " +
							"name of one key segment.",
					},
				},
			}
		}
		done, err := s.d.Store.Recorded(ctx, run, version, r.Kind, r.Name)
		if err != nil || done {
			if err != nil {
				return err //nolint:wrapcheck // the store names the read
			}
			continue
		}
		k := key(run, r.Name)
		obj, err := s.d.Objects.CommitUpload(ctx, store, k)
		if err != nil {
			return uploadInvalid(err, fmt.Sprintf("/artifacts/%d", i))
		}
		if err := s.d.Store.Record(ctx, run, Artifact{
			Version: version, Kind: r.Kind, Name: r.Name, Key: k, Size: obj.Size,
			SHA256: obj.SHA256,
		}); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
	}
	return nil
}

// Workspace signs a GET URL of the workspace run id recorded last, as
// workersapi.Reports says.
func (s *Service) Workspace(ctx context.Context, id kernel.ULID) (string, error) {
	run, err := s.held(ctx, id)
	if err != nil {
		return "", err
	}
	k, ok, err := s.d.Store.Workspace(ctx, run)
	if err != nil {
		return "", err //nolint:wrapcheck // the store names the read
	}
	if !ok {
		return "", fmt.Errorf("%w: %s", workersapi.ErrNoWorkspace, id)
	}
	signed, err := s.d.Objects.SignGet(ctx, objectstore.Workspaces, k)
	if errors.Is(err, objectstore.ErrNotFound) {
		return "", fmt.Errorf("%w: run %s's workspace expired", workersapi.ErrNoWorkspace, id)
	}
	if err != nil {
		return "", err //nolint:wrapcheck // blobs names the object
	}
	return signed.URL, nil
}

// PurgeObjects deletes up to batch of the deleted account acct's log
// chunks, or else of its other recorded objects, with their objects, in
// one transaction, and reports whether none was left.
func (s *Service) PurgeObjects(
	ctx context.Context,
	acct kernel.AccountID,
	batch int,
) (bool, error) {
	var done bool
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		objs, err := s.d.Store.PurgeObjects(ctx, acct, batch)
		if err != nil {
			return err //nolint:wrapcheck // the store names the purge
		}
		done = len(objs) == 0
		for _, o := range objs {
			if err := s.d.Objects.Delete(ctx, o.Store, o.Key); err != nil {
				return err //nolint:wrapcheck // blobs names the object
			}
		}
		return nil
	})
	return done, err //nolint:wrapcheck // named above
}
