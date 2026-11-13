package changes

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Phase is the phase of a run that made a change, the reference's
// EntityChangePhase.
type Phase string

// The phases: a drift detection's, the plan's and the apply's.
const (
	PhaseDrift Phase = "DRIFT"
	PhasePlan  Phase = "PLAN"
	PhaseApply Phase = "APPLY"
)

// Document is the artifact name a run's worker uploads its changes
// document under.
const Document = "changes.json"

// ErrExpired reports a run whose changes document retention removed.
var ErrExpired = errors.New("runs: the run's changes have expired")

// Store reads runs and where their changes are. Each method joins the
// transaction its context carries.
type Store interface {
	// Get reads acct's run id; read.ErrNotFound when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (lifecycle.Run, error)
	// Changes reads the key of the changes document run recorded last
	// while planning, and false when it recorded none.
	Changes(ctx context.Context, run lifecycle.Run) (string, bool, error)
}

// Objects opens objects of the run-artifacts store, unsealed, as
// blob_objects records them; blobs.Objects is one.
type Objects interface {
	Open(ctx context.Context, store objectstore.Name, key string) (io.ReadCloser, error)
}

// Stacks reads a stack's settings; stacksapi.Stacks is one.
type Stacks interface {
	Settings(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Settings,
		error)
}

// Deps are what the changes are read through.
type Deps struct {
	Store   Store
	Objects Objects
	Stacks  Stacks
}

// Service reads the changes a run's phases made.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
type Service struct{ d Deps }

// New returns the service over d.
func New(d Deps) *Service { return &Service{d: d} }

// Query selects a run's changes: those of Phase, the latest phase that
// recorded changes when empty, and of Type, every type when empty.
type Query struct {
	Phase Phase
	Type  plan.Type
}

// Set is the changes of one phase of a run, in the order its plan lists
// them; no changes, with Phase empty, when no phase the query selects
// has recorded any.
type Set struct {
	Phase   Phase
	Changes []plan.Change
}

// Entities reads p's run raw's changes of q. The route has decided
// SPACE_READ on the run's stack's space.
func (s *Service) Entities(ctx context.Context, p kernel.Principal, raw string, q Query) (Set,
	error,
) {
	run, err := s.get(ctx, p.AccountID, raw)
	if err != nil {
		return Set{}, err
	}
	return s.read(ctx, run, q)
}

// Resources reads p's run raw's changes of q, as Entities does, of a
// stack of the Terraform family only: another vendor's run is a
// *kernel.Invalid.
func (s *Service) Resources(ctx context.Context, p kernel.Principal, raw string, q Query) (Set,
	error,
) {
	run, err := s.get(ctx, p.AccountID, raw)
	if err != nil {
		return Set{}, err
	}
	st, err := s.d.Stacks.Settings(ctx, run.Account, run.Stack)
	if err != nil {
		return Set{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	if k := iac.Kind(st.Vendor); k != iac.Terraform && k != iac.OpenTofu {
		return Set{}, &kernel.Invalid{Detail: fmt.Sprintf("The run's stack's vendor %s has "+
			"no resource changes; read its changes-v2.", st.Vendor)}
	}
	return s.read(ctx, run, q)
}

// read reads run's changes of q. Only the plan records changes yet, so
// it is the latest phase, and an apply or drift detection has none.
func (s *Service) read(ctx context.Context, run lifecycle.Run, q Query) (Set, error) {
	empty := Set{Changes: []plan.Change{}}
	if q.Phase != "" && q.Phase != PhasePlan {
		return empty, nil
	}
	key, ok, err := s.d.Store.Changes(ctx, run)
	if err != nil || !ok {
		return empty, err //nolint:wrapcheck // the store names the read
	}
	r, err := s.d.Objects.Open(ctx, objectstore.RunArtifacts, key)
	switch {
	case errors.Is(err, objectstore.ErrNotFound):
		return Set{}, fmt.Errorf("%w: %s", ErrExpired, run.ID)
	case err != nil:
		return Set{}, err //nolint:wrapcheck // blobs names the object
	}
	defer r.Close()
	all, err := plan.Read(r)
	if err != nil {
		return Set{}, fmt.Errorf("runs: read run %s's changes %s: %w", run.ID, key, err)
	}
	out := Set{Phase: PhasePlan, Changes: all[:0]}
	for _, c := range all {
		if q.Type == "" || c.Metadata.Type == q.Type {
			out.Changes = append(out.Changes, c)
		}
	}
	return out, nil
}

// get reads acct's run raw; read.ErrNotFound when raw is no run ID.
func (s *Service) get(ctx context.Context, acct kernel.AccountID, raw string) (lifecycle.Run,
	error,
) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return lifecycle.Run{}, fmt.Errorf("%w: %q", read.ErrNotFound, raw)
	}
	return s.d.Store.Get(ctx, acct, id) //nolint:wrapcheck // the store names the read
}
