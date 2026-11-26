package core

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Lock is how a read locks an integration's row until its transaction
// ends.
type Lock int

const (
	// NoLock reads the row without locking it.
	NoLock Lock = iota
	// Share locks the row FOR SHARE, for an attachment that holds it, so
	// no move or delete passes it unseen.
	Share
	// Update locks the row FOR UPDATE, for a write of the integration.
	Update
)

// Page is a page of integrations, by name: After is the name the previous
// page ended at, empty for the first, and Limit how many to read.
type Page struct {
	After string
	Limit int
}

// Store is what the service needs of the domain's storage. Every method
// joins the transaction its context carries.
type Store interface {
	// Create writes a new integration; a *NameTaken when another of its
	// account holds its name.
	Create(ctx context.Context, in Integration) error
	// Write replaces the integration's fields and sets its version; a
	// *NameTaken as Create.
	Write(ctx context.Context, in Integration) error
	// Get reads acct's integration id, locked as lock says; ErrNotFound
	// when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock Lock) (Integration, error)
	// ByName reads acct's integration name; ErrNotFound when acct lacks
	// it.
	ByName(ctx context.Context, acct kernel.AccountID, name string) (Integration, error)
	// ByIDs reads those of acct's integrations ids that it has, keyed by
	// ID.
	ByIDs(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]Integration, error)
	// List reads a page of acct's integrations in spaces, by name, the
	// legacy ones too when legacy is set, and reports whether another
	// page follows.
	List(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, legacy bool, page Page) ([]Integration, bool, error)
	// Labelled reads acct's integrations in spaces with auto-attachment
	// enabled that carry any of labels, by name, legacy ones left out.
	Labelled(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string) ([]Integration, error)
	// InSpace reports whether an integration of acct lives in space.
	InSpace(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error)
	// Delete deletes acct's integration id with its attachments.
	Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error

	// Attach writes a; a *Conflict when its stack has the sides it sets
	// already, or its integration is attached to the stack already.
	Attach(ctx context.Context, a Attachment) error
	// SetSides sets the sides of acct's attachment id; a *Conflict as
	// Attach.
	SetSides(ctx context.Context, acct kernel.AccountID, id kernel.ULID, read, write bool) error
	// Attachment reads acct's attachment id; ErrNotFound when acct lacks
	// it.
	Attachment(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Attachment, error)
	// Detach deletes acct's attachment id.
	Detach(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// Attachments reads the stored attachments of integrations, or of
	// stacks when byStack is set, keyed by that ID, oldest first.
	Attachments(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID, byStack bool) (map[kernel.ULID][]Attachment, error)
	// AttachedStacks lists the stacks a stored attachment of acct names.
	AttachedStacks(ctx context.Context, acct kernel.AccountID) ([]kernel.ULID, error)
	// Legacy reads the legacy integration bound to acct's stack;
	// ErrNotFound when it has none.
	Legacy(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) (Integration, error)
	// DeleteStack deletes the stack's attachments and the legacy
	// integration bound to it.
	DeleteStack(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error
	// Audit records e on its account's trail.
	Audit(ctx context.Context, e kernel.AuditEntry) error
}

// AzureStore is what the service needs of the storage of Azure
// integrations and their attachments. Every method joins the transaction
// its context carries.
type AzureStore interface {
	// Create writes a new integration; a *NameTaken when another Azure
	// integration of its account holds its name.
	Create(ctx context.Context, in AzureIntegration) error
	// Write replaces the integration's fields but its tenant and its
	// application, and sets its version; a *NameTaken as Create.
	Write(ctx context.Context, in AzureIntegration) error
	// Get reads acct's integration id, locked as lock says; ErrNotFound
	// when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock Lock) (AzureIntegration, error)
	// ByName reads acct's integration name; ErrNotFound when acct lacks
	// it.
	ByName(ctx context.Context, acct kernel.AccountID, name string) (AzureIntegration, error)
	// ByIDs reads those of acct's integrations ids that it has, keyed by
	// ID.
	ByIDs(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]AzureIntegration, error)
	// List reads a page of acct's integrations in spaces, by name, and
	// reports whether another page follows.
	List(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, page Page) ([]AzureIntegration, bool, error)
	// Labelled reads acct's integrations in spaces with auto-attachment
	// enabled and a default subscription that carry any of labels, by
	// name.
	Labelled(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string) ([]AzureIntegration, error)
	// InSpace reports whether an integration of acct lives in space.
	InSpace(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error)
	// Delete deletes acct's integration id with its attachments.
	Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// Count is how many integrations acct has.
	Count(ctx context.Context, acct kernel.AccountID) (int, error)
	// Oldest reads up to n of acct's integrations, oldest first.
	Oldest(ctx context.Context, acct kernel.AccountID, n int) ([]AzureIntegration, error)

	// Attach writes a; a *Conflict when its stack has the sides it sets
	// already, or its integration is attached to the stack already.
	Attach(ctx context.Context, a Attachment) error
	// SetAttachment sets the sides and the subscription of acct's
	// attachment id; a *Conflict as Attach.
	SetAttachment(ctx context.Context, acct kernel.AccountID, id kernel.ULID, read, write bool, subscription *string) error
	// Attachment reads acct's attachment id; ErrNotFound when acct lacks
	// it.
	Attachment(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Attachment, error)
	// Detach deletes acct's attachment id.
	Detach(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// Attachments reads the stored attachments of integrations, or of
	// stacks when byStack is set, keyed by that ID, oldest first.
	Attachments(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID, byStack bool) (map[kernel.ULID][]Attachment, error)
	// AttachedStacks lists the stacks a stored attachment of acct names.
	AttachedStacks(ctx context.Context, acct kernel.AccountID) ([]kernel.ULID, error)
	// DeleteStack deletes the stack's attachments.
	DeleteStack(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error
}

// Assumption is one role assumption; Tags are its session tags, none
// when empty.
type Assumption struct {
	RoleARN     string
	ExternalID  string
	SessionName string
	Region      string
	Duration    time.Duration
	Tags        map[string]string
}

// Session is the temporary credentials an assumption issued.
type Session struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Roles is the server's AWS identity: CallerAccount is the AWS account
// it assumes roles as, Assume assumes a role and answers the session's
// credentials. Both answer ErrUnsupported without AWS credentials; Assume
// answers *Refused when STS denies the assumption, and *Upstream when
// STS fails otherwise.
type Roles interface {
	CallerAccount(ctx context.Context) (string, error)
	Assume(ctx context.Context, a Assumption) (Session, error)
}

// Refused is an assumption STS denied, with its message.
type Refused struct{ Message string }

func (e *Refused) Error() string { return "cloud: AWS STS denied the assumption: " + e.Message }
