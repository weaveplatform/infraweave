package push

import (
	"context"
	"log/slog"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

// Evaluate is isolated per stack and deduplicated per delivery/stack.
var Evaluate = queue.Kind{Name: "vcs.evaluate_event", Queue: queue.VCSInbox, Policy: queue.Ignore}

// Prepared keeps one event's full diff outside queue payloads. Commit belongs to
// the immutable head SHA; Branch is the ref that supplied that head.
type Prepared struct {
	Event  vcsapi.Event     `json:"event"`
	Commit stacksapi.Commit `json:"commit"`
	Files  []string         `json:"files"`
}

// Evaluation is a small durable reference, irrespective of diff or ref count.
type Evaluation struct {
	Account  kernel.AccountID `json:"account"`
	Delivery kernel.ULID      `json:"delivery"`
	Stack    kernel.ULID      `json:"stack"`
	Deadline time.Time        `json:"deadline"`
}

// Store joins the processor/evaluator transaction. Prepared rows are owned by
// their delivery and disappear with its retention or account purge.
type Store interface {
	PutPrepared(context.Context, kernel.AccountID, kernel.ULID, []Prepared) error
	Prepared(context.Context, kernel.AccountID, kernel.ULID) ([]Prepared, error)
	EventContext(context.Context, kernel.AccountID, kernel.ULID) (EventContext, bool, error)
}

// EventContext is delivery metadata needed by a child, with no retained body or
// request headers. A child rechecks it rather than trusting queue identity alone.
type EventContext struct {
	Integration kernel.ULID
	Provider    string
	State       string
	Deadline    time.Time
}

// Integrations reads effective bindings without opening provider credentials.
type Integrations interface {
	EventUse(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Use, bool, error)
}

// Sources computes one full push diff and head metadata before any write lock.
type Sources interface {
	PreparePush(context.Context, kernel.AccountID, kernel.ULID, string, vcsapi.Event) (stacksapi.Commit, []string, error)
}

// Deps is the forward domain graph and transactional stores used by VCS jobs.
type Deps struct {
	DB           tx.Beginner
	Store        Store
	Stacks       stacksapi.Stacks
	Spaces       accessapi.Spaces
	Integrations Integrations
	Sources      Sources
	Creator      runsapi.Creator
	Supersede    runsapi.Supersede
	Jobs         intake.Jobs
	Limits       *Limits
	Now          func() time.Time
	Log          *slog.Logger
}

// Service prepares outside transactions and evaluates only prepared facts under
// the stack creation lock. It evaluates no push policy or comment command.
type Service struct{ d Deps }

// New uses the supplied clock/log, with ordinary process defaults when omitted.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	return &Service{d: d}
}
