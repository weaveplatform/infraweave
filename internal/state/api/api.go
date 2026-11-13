package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// DefaultWorkspace is the name of a stack's default workspace, the one
// Terraform's and OpenTofu's http backend reaches.
const DefaultWorkspace = ""

// BaseURL is the state backend's prefix on the API listener, and
// StackPattern and WorkspacePattern the chi patterns of a stack's default
// workspace and of a named one under it, which internal/app's route table
// names. {stack} is a stack's ULID, or its slug when the segment is no
// canonical ULID; {workspace} is URL-encoded.
//
// ref: decision 0010 § "Endpoints"
const (
	BaseURL          = "/state/v1"
	StackPattern     = BaseURL + "/stacks/{stack}"
	WorkspacePattern = StackPattern + "/workspaces/{workspace}"
)

// MethodLock and MethodUnlock are the methods Terraform's and OpenTofu's
// http backend takes and releases a lock with.
const (
	MethodLock   = "LOCK"
	MethodUnlock = "UNLOCK"
)

// Version is a managed state version's metadata, without its contents.
// Serial and Lineage are nil for a client-encrypted version, whose body
// the server never parses; Run is nil for a version no run wrote, such as
// an import.
//
// ref: decision 0010 § "Storage"
type Version struct {
	ID              kernel.ULID
	Stack           kernel.ULID
	Workspace       string
	Size            int64
	SHA256          []byte
	Serial          *int64
	Lineage         *string
	ClientEncrypted bool
	Run             *kernel.ULID
	Imported        bool
	Rollback        bool
	CreatedAt       time.Time
}

// Output is one of a state's root module outputs: Value is its JSON
// value, and Sensitive whether the configuration marks it so.
type Output struct {
	Name      string
	Value     json.RawMessage
	Sensitive bool
}

// Versions is what other domains read of a stack's managed state.
type Versions interface {
	// Latest reads the metadata of the current version of acct's stack's
	// workspace, false when the workspace has none.
	Latest(
		ctx context.Context,
		acct kernel.AccountID,
		stack kernel.ULID,
		workspace string,
	) (Version, bool, error)
	// Outputs reads the outputs of the current version of acct's stack's
	// default workspace, by name; none when it has no version or the
	// version is client-encrypted.
	Outputs(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) ([]Output, error)
}
