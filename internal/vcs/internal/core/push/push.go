package push

import (
	"context"
	"log/slog"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// Evaluate is isolated per stack and deduplicated per delivery/stack.
var Evaluate = queue.Kind{Name: "vcs.evaluate_event", Queue: queue.VCSInbox, Policy: queue.Ignore}

// Prepared keeps one event's full diff outside queue payloads. Commit belongs to
// the immutable head SHA; Branch is the ref that supplied that head.
type Prepared struct {
	Event               vcsapi.Event             `json:"event"`
	Commit              stacksapi.Commit         `json:"commit"`
	Files               []string                 `json:"files"`
	Action              *port.ActionFacts        `json:"action,omitempty"`
	AffectedStacksCount *int64                   `json:"affectedStacksCount"`
	Request             *port.RequestFacts       `json:"request,omitempty"`
	RecoveredCommits    *vcsapi.CommitCollection `json:"recoveredCommits,omitempty"`
}

// Evaluation is a small durable reference, irrespective of diff or ref count.
type Evaluation struct {
	Account  kernel.AccountID `json:"account"`
	Delivery kernel.ULID      `json:"delivery"`
	Stack    kernel.ULID      `json:"stack"`
	Deadline time.Time        `json:"deadline"`
	Replays  int64            `json:"replays"`
}

// Store joins the processor/evaluator transaction. Prepared rows are owned by
// their delivery and disappear with its retention or account purge.
type Store interface {
	EvaluationFailed(context.Context, kernel.AccountID, kernel.ULID, int64) error
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
	Replays     int64
}

// Integrations reads effective bindings without opening provider credentials.
type Integrations interface {
	HeldEventUse(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Use, bool, error)
	EventUse(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Use, bool, error)
}

// PolicyIntegrations reads only credential-free integration metadata.
type PolicyIntegrations interface {
	PolicyIntegration(context.Context, kernel.AccountID, kernel.ULID) (policyapi.VCSIntegrationFacts, error)
}

// Sources computes one full push diff and head metadata before any write lock.
type Sources interface {
	PreparePush(context.Context, kernel.AccountID, kernel.ULID, string, vcsapi.Event) (port.PreparedPush, error)
	PrepareTag(context.Context, kernel.AccountID, kernel.ULID, string, vcsapi.Event) (stacksapi.Commit, error)
}

// RequestSources captures optional native request facts outside transactions.
type RequestSources interface {
	PrepareRequest(context.Context, kernel.AccountID, kernel.ULID, string, vcsapi.Event) (port.RequestFacts, error)
}

// ActionSources prepares native facts outside transactions and checks their
// credential-free binding under the completing integration hold.
type ActionSources interface {
	PrepareCheckAction(context.Context, kernel.AccountID, kernel.ULID, string, vcsapi.Event) (port.ActionFacts, error)
	CurrentCheckAction(context.Context, kernel.AccountID, kernel.ULID, string, vcsapi.Repository, port.ActionFacts) (bool, error)
	RefreshCheckAction(context.Context, kernel.AccountID, kernel.ULID, string, vcsapi.Event, port.ActionFacts) (bool, error)
}

// ActionEffects retains successful intent across replay generations. Both
// operations require the evaluator's completing transaction. The original
// delivery and immutable target identify a press; another delivery is new intent.
type ActionEffects interface {
	HasCheckAction(context.Context, kernel.AccountID, kernel.ULID, kernel.ULID) (bool, error)
	RecordCheckAction(context.Context, kernel.AccountID, kernel.ULID, kernel.ULID) error
}

// IgnoredRecorder persists a reason under the same live account/stack holds and
// queue completion transaction as evaluation. Tier gates apply only to reads.
type IgnoredRecorder interface {
	Record(context.Context, kernel.AccountID, kernel.ULID, stacksapi.Commit, string) error
}

// CapReporter schedules the failed commit check in the completing child
// transaction; the cap has no run_updated event of its own.
type CapReporter interface {
	Capped(context.Context, kernel.AccountID, kernel.ULID, kernel.ULID, string, kernel.RunType, int) error
}

// PolicyReporter commits no-run provider checks through the durable effect path.
type PolicyReporter interface {
	ReportPush(context.Context, port.PushFeedback) error
}

// Lifecycle commits provider identity changes before selecting fan-out targets.
type Lifecycle interface {
	FanoutRepository(context.Context, kernel.AccountID, kernel.ULID, time.Time, vcsapi.Repository) (vcsapi.Repository, bool, error)
	EventRepository(context.Context, kernel.AccountID, kernel.ULID, time.Time, vcsapi.Repository) (vcsapi.Repository, error)
	Apply(context.Context, kernel.AccountID, kernel.ULID, stacksapi.Use, time.Time, []vcsapi.Event) error
}

// Deps is the forward domain graph and transactional stores used by VCS jobs.
type Deps struct {
	Lifecycle           Lifecycle
	DB                  tx.Beginner
	Store               Store
	Stacks              stacksapi.Stacks
	Spaces              accessapi.Spaces
	Integrations        Integrations
	Sources             Sources
	Requests            RequestSources
	Actions             ActionSources
	Effects             ActionEffects
	ActionRefused       func(context.Context, string)
	Creator             runsapi.Creator
	Supersede           runsapi.Supersede
	Jobs                intake.Jobs
	Limits              *Limits
	Checks              CapReporter
	PolicyChecks        PolicyReporter
	Ignored             IgnoredRecorder
	Policies            policyapi.Evaluator
	PolicySelection     policyapi.Selection
	PolicyInputs        policyapi.PushInputBuilder
	PolicyStacks        stacksapi.PolicySnapshots
	PolicyLocks         stacksapi.PolicyLocks
	PolicySpaces        accessapi.PolicySpaceReader
	PolicyPools         workersapi.PoolMetadataReader
	PolicyRoles         accessapi.StackRoleReader
	PolicyContexts      configapi.PolicyContextReader
	PolicyIntegrations  PolicyIntegrations
	PolicyRuns          runsapi.PushRunReader
	PolicyCancellations runsapi.PushCancellations
	PolicyAllowed       func(context.Context, kernel.AccountID) (bool, error)
	Now                 func() time.Time
	Log                 *slog.Logger
}

// Service prepares outside transactions and evaluates only prepared facts under
// the stack creation lock.
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
