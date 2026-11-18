package pullrequest

import (
	"context"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

const (
	Open   = "open"
	Closed = "closed"
	Merged = "merged"
	Limit  = 50
	Batch  = 100
)

// ErrNoTransaction refuses upkeep outside the inbox completion transaction.
var ErrNoTransaction = errors.New("vcs: pull request upkeep requires a transaction")

// Request retains stable repository identity and its current path separately.
// ID is internal; Number is the provider's repository-local request identity.
type Request struct {
	ID                                                                             kernel.ULID
	Account                                                                        kernel.AccountID
	Integration                                                                    kernel.ULID
	RepositoryID, Namespace, Repository, Number                                    string
	BaseBranch, HeadBranch, HeadSHA, AuthorLogin, Title, URL, HeadBranchURL, State string
	Draft                                                                          bool
	CreatedAt, UpdatedAt                                                           time.Time
}

// Cursor continues a descending update-time/identity candidate scan.
type Cursor struct {
	UpdatedAt time.Time
	ID        kernel.ULID
}

// Store joins the caller's transaction and scopes every query to its account.
type Store interface {
	Upsert(context.Context, Request) error
	Author(context.Context, kernel.AccountID, kernel.ULID, string, string) (string, error)
	Candidates(context.Context, kernel.AccountID, kernel.ULID, string, string, string, *Cursor, int) ([]Request, error)
	Heads(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]string, error)
	Prune(context.Context, *kernel.AccountID, *time.Time, int) (int, error)
}

// Integrations resolves bindings without opening secrets for stack reads.
type Integrations interface {
	Binding(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error)
	Use(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error)
}

// Authors resolves only the missing creator identity; it never refreshes heads.
type Authors interface {
	AuthorLogin(context.Context, port.Integration, string) (string, error)
}

// Deps composes PR upkeep with existing push preparation, without moving
// provider calls or business state into generic queue machinery.
type Deps struct {
	Store        Store
	Integrations Integrations
	Authors      map[string]Authors
	Runs         runsapi.CommitRuns
	Pushes       intake.Dispatcher
	Now          func() time.Time
	Retention    time.Duration
}

type Service struct{ d Deps }

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Retention == 0 {
		d.Retention = 7 * 24 * time.Hour
	}
	return &Service{d: d}
}
