package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Access is the kind of principal a route admits.
//
// ref: decision 0006 § "The principal"
type Access int

const (
	// Account routes admit a principal that works in an account.
	Account Access = iota
	// Organization routes also admit a principal of an organization that
	// works in no account: the break-glass admin, or a member no account
	// admits.
	Organization
	// Public routes take no credential.
	Public
	// OrganizationAdmin routes, the Organization tag's, admit the
	// principals of an organization that may hold the organization role,
	// members and the break-glass admin, and organization tokens, which
	// every other route refuses. An API key's or a run's token works in an
	// account, and is refused.
	OrganizationAdmin
	// Worker routes, the worker API's, admit worker session tokens alone,
	// which every other route refuses.
	Worker
	// Runner routes, the worker API's calls of a claimed run, admit run
	// tokens alone, which every other route refuses.
	Runner
	// State routes, the managed state backend's, admit run tokens alone,
	// sent as the password of HTTP Basic credentials whose user name is
	// run, which every other route refuses.
	//
	// ref: decision 0010 § "Who may call what"
	State
)

// Authenticator authenticates the API's requests.
//
// ref: decision 0006
type Authenticator interface {
	// Middleware authenticates every request access does not make public.
	// It puts the principal the request's bearer token or browser session
	// acts as in the request's context, where kernel.PrincipalFrom reads
	// it, or answers 401 unauthenticated with WWW-Authenticate: Bearer. A
	// public request passes with no principal, whatever it carries.
	Middleware(access func(*http.Request) Access) func(http.Handler) http.Handler
}

// Required is what an API operation requires before its handler runs: its
// x-infraweave-action, decided in the space its x-infraweave-subject
// finds, with the path parameters the router matched, which a path
// subject reads.
type Required struct {
	Action  Action
	Subject Subject
	Params  map[string]string
}

// Requirer decides each API request's Required after authentication.
//
// ref: decision 0007 § "Enforcement point"
type Requirer interface {
	// Middleware decides every request require reports ok for, and passes
	// the others: the operator API's, a public route's, and a route that
	// matches no operation, which the router answers itself. A subject
	// the caller cannot read answers 404 not-found, as a missing one does,
	// and an action it may not perform on a readable one 403 forbidden,
	// naming the action.
	Middleware(require func(*http.Request) (Required, bool)) func(http.Handler) http.Handler
}

// SpaceOf finds the space of one kind of entity by the ID a request
// names, for a path or body subject: ErrNoSubject when the account holds
// no such entity. A domain registers one for each kind it owns.
type SpaceOf func(ctx context.Context, acct kernel.AccountID, id string) (kernel.ULID, error)

// ErrNoSubject is a subject the account does not hold.
var ErrNoSubject = errors.New("no such subject")

// LoginPattern is the route of an organization's break-glass login: GET
// answers its form, and a POST of the form signs in. It is outside the
// API's base URL, a page for a browser rather than an operation of the API.
//
// ref: decision 0006 § "The break-glass admin"
const LoginPattern = "/admin-login/{organization}"

// The routes of the authorization server for MCP clients, outside the
// API's base URL as the OAuth protocol places them: RFC 8414 metadata, RFC
// 7591 registration, and the authorization and token endpoints. None takes
// a credential.
//
// ref: decision 0006 § "The OAuth authorization server for MCP"
const (
	OAuthMetadataPattern  = "/.well-known/oauth-authorization-server"
	OAuthRegisterPattern  = "/oauth/register"
	OAuthAuthorizePattern = "/oauth/authorize"
	OAuthTokenPattern     = "/oauth/token" //nolint:gosec // a route, not a credential
)

// Authorizer decides the authorization checks the same way for every
// domain: whether a principal may perform an action in a space, every
// space of its account in which it may, for filtering a list by
// space_id = ANY($n), whether it may in any space, as an anywhere action
// is decided, and every assignable action it may perform in a space, for
// a resource's allowedActions. A principal of no account may do nothing;
// an error is the store's, and the check it fails refuses.
//
// ref: decision 0007
type Authorizer interface {
	Authorize(
		ctx context.Context,
		p kernel.Principal,
		action Action,
		space kernel.ULID,
	) (bool, error)
	Reachable(ctx context.Context, p kernel.Principal, action Action) ([]kernel.ULID, error)
	Holds(ctx context.Context, p kernel.Principal, action Action) (bool, error)
	Actions(ctx context.Context, p kernel.Principal, space kernel.ULID) ([]Action, error)
}

// SpaceGuard is a domain's say in changes to an account's space tree: a
// domain whose rows name spaces registers one, and the tree refuses a
// delete or a move it objects to. Both methods run inside the change's
// transaction, which the context carries, after the tree and the changed
// spaces are locked FOR UPDATE. That lock waits for every transaction
// holding one of those spaces, and each method's statement then sees what
// it committed, so a row written under a hold is never missed.
//
// ref: decisions 0004 and 0007
type SpaceGuard interface {
	// SpaceInUse reports whether a row of the domain names space, which
	// refuses its deletion.
	SpaceInUse(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error)
	// LostOnMove lists each stack that would no longer reach an entity
	// it reaches today if m were made, which refuses it.
	LostOnMove(ctx context.Context, m SpaceMove) ([]LostReach, error)
}

// ErrNoSpace reports a space the account lacks.
var ErrNoSpace = errors.New("no such space")

// Spaces is what a domain that stores a space's ID calls, since no foreign
// key reaches another domain's table. Hold locks the account's space in
// the caller's transaction, which the context carries, so no delete or
// move removes it before that transaction commits; a space the account
// lacks is ErrNoSpace. The holding domain also registers a SpaceGuard,
// which a delete or move asks after waiting for every hold. A request
// names a space by its slug, which Find turns into the ID the domain
// stores, ErrNoSpace when the account lacks it, and Slugs names each of
// the account's spaces by, for the domain's answers. Tree lists the
// account's spaces with their parents, top space first, for a domain that
// searches its entities by their place in the tree.
//
// ref: decision 0004
type Spaces interface {
	Hold(ctx context.Context, acct kernel.AccountID, space kernel.ULID) error
	Find(ctx context.Context, acct kernel.AccountID, slug string) (kernel.ULID, error)
	Slugs(ctx context.Context, acct kernel.AccountID) (map[kernel.ULID]string, error)
	Tree(ctx context.Context, acct kernel.AccountID) ([]SpaceNode, error)
}

// ErrNoAccount is an account ID no account has.
var ErrNoAccount = errors.New("no such account")

// Accounts names an account to a domain that keeps only its ID: Slug is
// acct's slug, ErrNoAccount when no account has the ID.
type Accounts interface {
	Slug(ctx context.Context, acct kernel.AccountID) (string, error)
}

// SpaceNode is a space of an account's tree: its ID, slug and name, its
// parent's ID, zero for the top space, and whether it inherits its
// parent's entities, which decides what a stack in it reaches.
//
// ref: decision 0007 § "Evaluation"
type SpaceNode struct {
	ID      kernel.ULID
	Slug    string
	Name    string
	Parent  kernel.ULID
	Inherit bool
}

// The kinds of actor another domain keeps that a role binding grants to:
// a stack, and a Flows project.
const (
	ActorStack        = "stack"
	ActorFlowsProject = "flows_project"
)

// ErrNoBinding is a role binding the account lacks.
var ErrNoBinding = errors.New("no such role binding")

// ErrBindingHeld is a role binding of an actor, role and space the
// account already holds.
var ErrBindingHeld = errors.New("the actor already holds the role in the space")

// BindingRequest names a role binding's actor by its ID and its role and
// space by their slugs. A change of a binding names no actor, since its
// actor never changes.
type BindingRequest struct {
	Actor kernel.ULID
	Role  string
	Space string
}

// RoleBinding grants the role Role, by slug, to the actor of kind Actor
// and ID ActorID in the space SpaceID, slug Space, and the spaces below
// it.
type RoleBinding struct {
	ID        kernel.ULID
	Actor     string
	ActorID   kernel.ULID
	Role      string
	SpaceID   kernel.ULID
	Space     string
	CreatedAt time.Time
	CreatedBy string
	Version   int64
}

// Bindings are the role bindings of the actors another domain keeps, such
// as stacks. Bind records, as p, the binding of an actor of a kind each
// request names, all or none, in the transaction ctx carries when there
// is one: a role or space the account lacks, or a space p does not
// administer, is a *kernel.Invalid pointing at pointer/<index>/roleID or
// spaceID, or at /roleID or /spaceID for an empty pointer, and a binding
// the account holds ErrBindingHeld. Binding reads one binding of
// an actor of a kind, ErrNoBinding when the account lacks it. Rebind
// moves one to the role and space req names, which p must administer as
// Bind's, and Unbind deletes one; each locks the binding, has expect
// accept its version and check refuse it as it is, so a check of its
// space sees the space the write changes, and answers ErrNoBinding for
// one the account lacks. UnbindActor deletes every binding of an actor
// the owning domain deletes. ByActor and BySpace read an actor's bindings
// and a space's, oldest first. Each write moves the account's
// authorization.
//
// ref: decision 0007 § "Bindings"
type Bindings interface {
	Bind(
		ctx context.Context,
		p kernel.Principal,
		actor string,
		pointer string,
		reqs []BindingRequest,
	) ([]RoleBinding, error)
	Binding(
		ctx context.Context,
		acct kernel.AccountID,
		actor string,
		id kernel.ULID,
	) (RoleBinding, error)
	Rebind(
		ctx context.Context,
		p kernel.Principal,
		actor string,
		id kernel.ULID,
		expect kernel.Expect,
		req BindingRequest,
		check func(RoleBinding) error,
	) (RoleBinding, error)
	Unbind(
		ctx context.Context,
		acct kernel.AccountID,
		actor string,
		id kernel.ULID,
		expect kernel.Expect,
		check func(RoleBinding) error,
	) (RoleBinding, error)
	UnbindActor(ctx context.Context, acct kernel.AccountID, actor string, id kernel.ULID) error
	ByActor(
		ctx context.Context,
		acct kernel.AccountID,
		actor string,
		id kernel.ULID,
	) ([]RoleBinding, error)
	BySpace(ctx context.Context, acct kernel.AccountID, space kernel.ULID) ([]RoleBinding, error)
}

// StackHome is a stack and the space it lives in.
type StackHome struct {
	Stack kernel.ULID
	Space kernel.ULID
}

// StackGrant is one role reaching a space for a stack: Source is RESIDENT
// for a grant on the space itself, PROPAGATED for one on an ancestor and
// INHERITED for one on a descendant whose path up inherits entities,
// which grants space-reader whatever role it binds. Binding is the
// binding's ID, zero for the stack's implicit read of the space it lives
// in, and Space the space the grant is on.
//
// ref: decision 0007 § "Evaluation"
type StackGrant struct {
	Source  string
	Binding kernel.ULID
	Role    string
	Space   kernel.ULID
}

// Grants explains what reaches a space for actors other than the caller.
// Stacks is, for each of stacks, every grant that reaches acct's space
// for it, in binding order, decided as Authorizer decides a run of that
// stack, and nil for a stack no grant reaches.
//
// ref: decision 0007 § "Evaluation"
type Grants interface {
	Stacks(
		ctx context.Context,
		acct kernel.AccountID,
		space kernel.ULID,
		stacks []StackHome,
	) ([][]StackGrant, error)
}

// SpaceMove is a proposed move of Space, with every space below it, to a
// new parent. From and To are the moved space's ancestors before and
// after the move, its parent first and the top space last.
type SpaceMove struct {
	Account  kernel.AccountID
	Space    kernel.ULID
	Subtree  []kernel.ULID
	From, To []kernel.ULID
}

// LostReach is a stack that a tree change would cut off from an entity:
// a worker pool, a cloud integration, a context or a policy, by its kind
// and name.
type LostReach struct {
	Stack  string
	Entity string
}

// DeletedKind is the outbox event an account's deletion appends in its
// transaction, at DeletedVersion, with a Deleted document: one per account,
// so deleting an organization appends one for each of its accounts. Its
// subject is the account's ULID. Each domain that keeps rows of an account
// consumes it and deletes them, since no other domain's table keys on
// access' own.
//
// ref: decision 0004
const (
	DeletedKind    = "account.deleted"
	DeletedVersion = 1
)

// Deleted is a DeletedKind event's document: the account and its
// organization, and whether the organization went with it, so a consumer
// deletes the organization's rows as well as the account's.
type Deleted struct {
	Account             kernel.ULID `json:"account"`
	Organization        kernel.ULID `json:"organization"`
	OrganizationDeleted bool        `json:"organizationDeleted"`
}

// DeletedSchema is the JSON Schema of DeletedVersion's documents.
var DeletedSchema = []byte(`{
	"type": "object",
	"required": ["account", "organization", "organizationDeleted"],
	"properties": {
		"account": {"type": "string", "minLength": 26, "maxLength": 26},
		"organization": {"type": "string", "minLength": 26, "maxLength": 26},
		"organizationDeleted": {"type": "boolean"}
	}
}`)
