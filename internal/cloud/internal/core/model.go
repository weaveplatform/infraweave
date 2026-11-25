package core

import (
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Integration is an AWS integration: a role in a customer's AWS account
// that runs of the stacks it attaches to assume. Worker is
// generateCredentialsInWorker: a private worker assumes the role, with
// ExternalID when it is set, and the server never does. A Legacy
// integration is the one a stack's legacy AWS role made, bound to that
// stack alone.
type Integration struct {
	Account    kernel.AccountID
	ID         kernel.ULID
	Space      kernel.ULID
	Name       string
	RoleARN    string
	Region     *string
	Duration   int32
	Worker     bool
	ExternalID string
	Labels     []string
	Autoattach bool
	TagSession bool
	Legacy     bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Version    int64
}

// Attachment is an integration serving a stack's read phases, its write
// phases or both. An Auto attachment is computed from the labels of the
// integration and the stack and never stored; its ID is AutoID's.
type Attachment struct {
	Account     kernel.AccountID
	ID          kernel.ULID
	Integration kernel.ULID
	Stack       kernel.ULID
	Read, Write bool
	Auto        bool
	// Subscription is an Azure attachment's subscription, nil when the
	// integration's default applies and for an AWS attachment.
	Subscription *string
	CreatedAt    time.Time
}

// AutoPrefix starts the ID the API gives an automatic attachment, which
// no stored attachment's ULID can.
const AutoPrefix = "auto-"

// APIID is the attachment's ID as the API gives it: the stored
// attachment's ULID, or AutoID's for an automatic one.
func (a Attachment) APIID() string {
	if a.Auto {
		return AutoID(a.Integration, a.Stack)
	}
	return a.ID.String()
}

// AutoID is the ID of the automatic attachment of integration to stack,
// fixed by the two, so the same attachment keeps one ID across reads.
func AutoID(integration, stack kernel.ULID) string {
	return AutoPrefix + integration.String() + "-" + stack.String()
}

// Input is what a create or an update sets of an integration. Space is
// the space's slug, the account's top space when empty; Duration nil is
// DefaultDuration.
type Input struct {
	Name       string
	Space      string
	RoleARN    string
	Region     *string
	Duration   *int32
	Worker     bool
	ExternalID string
	Labels     []string
	Autoattach bool
	TagSession bool
}

// The session durations an integration may ask for, in seconds: the
// server assumes a role for at most MaxServerDuration, a private worker
// for at most MaxWorkerDuration, and both for at least MinDuration, which
// a trust check asks for.
//
// ref: report 12 § "2.3 Session duration"
const (
	MinDuration       = 900
	MaxServerDuration = 3600
	MaxWorkerDuration = 43200
	DefaultDuration   = 3600
)

// ErrNotFound reports an integration or an attachment the account lacks.
var ErrNotFound = errors.New("cloud: not found")

// NameTaken is a name another integration of the account of the Kind it
// names holds, an AWS integration when Kind is empty.
type NameTaken struct{ Kind, Name string }

// What is the kind of integration that holds the name.
func (e *NameTaken) What() string {
	if e.Kind == "" {
		return "AWS integration"
	}
	return e.Kind
}

func (e *NameTaken) Error() string {
	return fmt.Sprintf("cloud: an %s named %q exists", e.What(), e.Name)
}

// Conflict is a write the stack's or the integration's attachments
// refuse: a second read or write attachment of a stack, or a second
// attachment of one integration to one stack.
type Conflict struct{ Detail string }

func (e *Conflict) Error() string { return "cloud: " + e.Detail }

// Missing is an entity a request names that the account lacks or the
// caller cannot read, which answers as one that does not exist: a space,
// a stack, an integration or an attachment, by Kind.
type Missing struct{ Kind, ID string }

func (m *Missing) Error() string { return fmt.Sprintf("cloud: no %s %q", m.Kind, m.ID) }

// Denied is an action the caller may not perform, beyond the one the
// operation's route decided.
type Denied struct{ Action string }

func (d *Denied) Error() string { return fmt.Sprintf("you may not perform %s here", d.Action) }

// ErrUnsupported reports an operation the installation cannot serve because
// the server holds no AWS credentials.
var ErrUnsupported = errors.New("cloud: the installation has no AWS credentials")

// Upstream is an STS failure other than a refusal: an outage, a
// throttle or an answer that does not parse. Code is STS's error code
// when it gave one.
type Upstream struct {
	Code string
	Err  error
}

func (e *Upstream) Error() string { return "cloud: AWS STS: " + e.Err.Error() }

func (e *Upstream) Unwrap() error { return e.Err }
