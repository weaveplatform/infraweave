package pool

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/credential"
)

// Pool is one private worker pool of an account. Certificate is the DER
// of the certificate the pool CA last signed for the pool's key, and
// Serial its serial: a registration under any other is refused.
type Pool struct {
	Account kernel.AccountID
	// Organization is Account's organization, which a registration counts
	// the organization's workers in.
	Organization kernel.OrganizationID
	ID           kernel.ULID
	Name         string
	Space        kernel.ULID
	Description  string
	Labels       []string
	// PreparingTimeout is the pool's own preparing timeout; zero takes
	// the installation's default.
	PreparingTimeout time.Duration
	// DriftDetectionRunLimit bounds the pool's concurrent drift detection
	// runs; nil or negative is no limit.
	DriftDetectionRunLimit *int32
	Certificate            []byte
	Serial                 int64
	CreatedAt              time.Time
	UpdatedAt              time.Time
	Version                int64
}

// Authority is the pool CA as the store keeps it, with a serial it has
// taken for one certificate: Certificate is its certificate's DER and
// Key its private key's PKCS #8 DER, opened.
type Authority struct {
	Certificate []byte
	Key         []byte
	Serial      int64
}

var (
	// ErrNotFound reports a pool the account lacks.
	ErrNotFound = errors.New("workers: no such worker pool")
	// ErrNameTaken reports a name another pool of the account has.
	ErrNameTaken = errors.New("workers: worker pool name taken")
)

// Fields are what a pool search filters and orders by: name,
// description, label, any of its labels, space, its place in the tree,
// and its timestamps. A search that names no order reads by name.
var Fields = []search.Field{
	{Name: "name", Type: search.String, Orderable: true},
	{Name: "description", Type: search.String},
	{Name: "label", Type: search.String},
	{Name: "space", Type: search.Hierarchy},
	{Name: "createdAt", Type: search.Time, Orderable: true},
	{Name: "updatedAt", Type: search.Time, Orderable: true},
}

// Scope is what a pool search sees: the spaces whose pools it may keep,
// and the account's space tree, which the space field reads.
type Scope struct {
	Reachable []kernel.ULID
	Spaces    []accessapi.SpaceNode
}

// Store is what the service needs of the pools' storage. Every method
// joins the transaction its context carries.
type Store interface {
	// Authority reads the pool CA, creating it with mint when the
	// installation has none, and takes its next serial; the CA's row
	// stays locked until the transaction ends.
	Authority(ctx context.Context, mint func() (Authority, error)) (Authority, error)
	// Create writes a new pool; ErrNameTaken when another of its account
	// has its name.
	Create(ctx context.Context, p Pool) error
	// Write replaces the pool's fields and sets its version; ErrNameTaken
	// as Create.
	Write(ctx context.Context, p Pool) error
	// Get reads acct's pool id, locked FOR UPDATE when lock is set;
	// ErrNotFound when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock bool) (Pool, error)
	// Share reads the space of acct's pool id, locked FOR SHARE, so a
	// delete or move waits for the transaction; ErrNotFound when acct
	// lacks it.
	Share(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (kernel.ULID, error)
	// Delete deletes acct's pool id with its workers and their nonces.
	Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// InSpace reports whether a pool of acct lives in space.
	InSpace(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error)
	// Search reads a page of acct's pools in the scope that in keeps.
	Search(
		ctx context.Context,
		acct kernel.AccountID,
		in search.Input,
		scope Scope,
	) (search.Result[Pool], error)
	// Suggest lists the values acct's pools in the scope that in keeps
	// hold.
	Suggest(
		ctx context.Context,
		acct kernel.AccountID,
		in search.SuggestionsInput,
		scope Scope,
	) (search.Suggestions, error)
	// Purge deletes up to batch of the deleted account acct's pools,
	// workers and nonces; true when none is left.
	Purge(ctx context.Context, acct kernel.AccountID, batch int) (bool, error)
}

// DefaultPreparingTimeout is a pool's preparing timeout when neither the
// pool nor the installation sets one.
const DefaultPreparingTimeout = 15 * time.Minute

// Deps are what the service is constructed over: the store, the pool its
// writes begin transactions on, the spaces pools live in, the
// authorizer, Public, the server's public URL, which a pool token names
// for its launchers, and PreparingTimeout, a pool's preparing timeout
// when it sets none, DefaultPreparingTimeout when zero.
type Deps struct {
	Store            Store
	DB               tx.Beginner
	Spaces           accessapi.Spaces
	Authz            accessapi.Authorizer
	Public           string
	PreparingTimeout time.Duration
	Now              func() time.Time
}

// Service is the pools' logic. Every write runs in one transaction.
type Service struct {
	store     Store
	db        tx.Beginner
	spaces    accessapi.Spaces
	authz     accessapi.Authorizer
	public    string
	preparing time.Duration
	now       func() time.Time

	mu     sync.RWMutex
	guards []workersapi.PoolGuard
	stacks accessapi.SpaceOf
}

// New returns the service over d.
func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	preparing := d.PreparingTimeout
	if preparing == 0 {
		preparing = DefaultPreparingTimeout
	}
	return &Service{
		store: d.Store, db: d.DB, spaces: d.Spaces, authz: d.Authz,
		public: strings.TrimSuffix(d.Public, "/"), preparing: preparing, now: now,
	}
}

var (
	_ accessapi.SpaceGuard = SpaceGuard{}
	_ workersapi.Pools     = (*Service)(nil)
)

// Guard registers g, which every later delete and move of a pool asks.
func (s *Service) Guard(g workersapi.PoolGuard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guards = append(s.guards, g)
}

// BindStacks makes stacks what a stack's attachable pools find its space
// with; until it runs, every stack is missing. It runs during
// construction, before the service serves a request.
func (s *Service) BindStacks(stacks accessapi.SpaceOf) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stacks = stacks
}

// Missing is an entity a request names that the account lacks or the
// caller cannot read, which answers as one that does not exist: a space
// or a pool, by Kind.
type Missing struct{ Kind, ID string }

func (m *Missing) Error() string { return fmt.Sprintf("workers: no %s %q", m.Kind, m.ID) }

// Denied is an action the caller may not perform, beyond the one the
// route decided.
type Denied struct{ Action accessapi.Action }

func (d *Denied) Error() string { return fmt.Sprintf("you may not perform %s here", d.Action) }

// InUse is a delete or a move refused because stacks name the pool and
// would no longer reach it: Stacks are their slugs, sorted.
type InUse struct{ Stacks []string }

func (u *InUse) Error() string {
	return "workers: the worker pool is used by stacks " + strings.Join(u.Stacks, ", ")
}

// Token is p's pool token, which its launchers register with.
func (s *Service) Token(p Pool) string {
	return credential.NewToken(s.public, p.ID, p.Certificate).Encode()
}

// PreparingTimeout is p's preparing timeout: its own, or the
// installation's default.
func (s *Service) PreparingTimeout(p Pool) time.Duration {
	if p.PreparingTimeout > 0 {
		return p.PreparingTimeout
	}
	return s.preparing
}

// SpaceOf is the space of acct's pool id, which the access domain's
// middleware decides a pool's operations on; accessapi.ErrNoSubject when
// acct lacks it.
func (s *Service) SpaceOf(
	ctx context.Context,
	acct kernel.AccountID,
	id string,
) (kernel.ULID, error) {
	p, err := s.Read(ctx, acct, id)
	if errors.Is(err, ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return p.Space, err
}

// Read reads acct's pool id. The route has decided SPACE_READ in its
// space.
func (s *Service) Read(ctx context.Context, acct kernel.AccountID, id string) (Pool, error) {
	return s.get(ctx, acct, id, false)
}

// get reads acct's pool id, locked FOR UPDATE when lock is set.
func (s *Service) get(
	ctx context.Context,
	acct kernel.AccountID,
	id string,
	lock bool,
) (Pool, error) {
	ulid, err := kernel.ParseULID(id)
	if err != nil {
		return Pool{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return s.store.Get(ctx, acct, ulid, lock) //nolint:wrapcheck // the store names the read
}

// Search reads a page of the pools of p's account in the spaces it may
// read.
func (s *Service) Search(
	ctx context.Context,
	p kernel.Principal,
	in search.Input,
) (search.Result[Pool], error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return search.Result[Pool]{}, err
	}
	return s.store.Search(ctx, p.AccountID, in, scope) //nolint:wrapcheck // the store names it
}

// Suggest lists the values the pools of p's account in the spaces it may
// read hold, so no value or count names one p cannot read.
func (s *Service) Suggest(
	ctx context.Context,
	p kernel.Principal,
	in search.SuggestionsInput,
) (search.Suggestions, error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return search.Suggestions{}, err
	}
	return s.store.Suggest(ctx, p.AccountID, in, scope) //nolint:wrapcheck // as above
}

// Hold checks that space reaches acct's pool id and holds the pool for
// the transaction ctx carries; workersapi.ErrNoPool otherwise.
func (s *Service) Hold(ctx context.Context, acct kernel.AccountID, space, id kernel.ULID) error {
	at, err := s.store.Share(ctx, acct, id)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: %s", workersapi.ErrNoPool, id)
	}
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	tree, err := s.spaces.Tree(ctx, acct)
	if err != nil {
		return err //nolint:wrapcheck // the access domain names the read
	}
	if !slices.Contains(Reach(tree, space), at) {
		return fmt.Errorf("%w: %s out of reach", workersapi.ErrNoPool, id)
	}
	return nil
}

// Reach is the spaces whose pools a stack in space may use: space, then
// each ancestor reached through an unbroken chain of spaces that inherit
// their parent's entities, nearest first.
//
// ref: decision 0007 § "Evaluation"
func Reach(tree []accessapi.SpaceNode, space kernel.ULID) []kernel.ULID {
	byID := make(map[kernel.ULID]accessapi.SpaceNode, len(tree))
	for _, n := range tree {
		byID[n.ID] = n
	}
	out := []kernel.ULID{space}
	for x := byID[space]; x.Inherit && !x.Parent.IsZero(); x = byID[x.Parent] {
		out = append(out, x.Parent)
	}
	return out
}

// AttachableToStack reads a page of the pools that p's account's stack
// may use and p may read. The route has decided SPACE_READ on the
// stack's space.
func (s *Service) AttachableToStack(
	ctx context.Context,
	p kernel.Principal,
	stack string,
	in search.Input,
) (search.Result[Pool], error) {
	s.mu.RLock()
	stacks := s.stacks
	s.mu.RUnlock()
	if stacks == nil {
		return search.Result[Pool]{}, &Missing{Kind: "stack", ID: stack}
	}
	space, err := stacks(ctx, p.AccountID, stack)
	if errors.Is(err, accessapi.ErrNoSubject) {
		return search.Result[Pool]{}, &Missing{Kind: "stack", ID: stack}
	}
	if err != nil {
		return search.Result[Pool]{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	return s.attachable(ctx, p, space, in)
}

// AttachableInSpace reads a page of the pools that a stack in p's
// account's space slug may use and p may read. The route has decided
// SPACE_READ on the space.
func (s *Service) AttachableInSpace(
	ctx context.Context,
	p kernel.Principal,
	slug string,
	in search.Input,
) (search.Result[Pool], error) {
	space, err := s.spaces.Find(ctx, p.AccountID, slug)
	if errors.Is(err, accessapi.ErrNoSpace) {
		return search.Result[Pool]{}, &Missing{Kind: "space", ID: slug}
	}
	if err != nil {
		return search.Result[Pool]{}, err //nolint:wrapcheck // the access domain names the read
	}
	return s.attachable(ctx, p, space, in)
}

// attachable reads a page of the pools space reaches in the spaces p may
// read.
func (s *Service) attachable(
	ctx context.Context,
	p kernel.Principal,
	space kernel.ULID,
	in search.Input,
) (search.Result[Pool], error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return search.Result[Pool]{}, err
	}
	reach := Reach(scope.Spaces, space)
	scope.Reachable = slices.DeleteFunc(scope.Reachable, func(id kernel.ULID) bool {
		return !slices.Contains(reach, id)
	})
	return s.store.Search(ctx, p.AccountID, in, scope) //nolint:wrapcheck // the store names it
}

// Input is a pool's settings as a create or an update gives them, with
// its space by slug, empty for the top space. Nil leaves
// PreparingTimeoutSeconds to the installation and DriftDetectionRunLimit
// unlimited.
type Input struct {
	Name                    string
	Space                   string
	Description             string
	Labels                  []string
	PreparingTimeoutSeconds *int32
	DriftDetectionRunLimit  *int32
}

// invalidDetail is the detail of an input the checks refuse.
const invalidDetail = "The worker pool's settings are refused."

// csrPointer is where a create's or a reset's request names its CSR.
const csrPointer = "/certificateSigningRequest"

// check refuses each field of in a pool cannot hold.
func check(in Input) error {
	var errs []kernel.FieldError
	if strings.TrimSpace(in.Name) == "" {
		errs = append(errs, kernel.FieldError{
			Pointer: "/name", Reason: kernel.ReasonRequired, Message: "A worker pool needs a name.",
		})
	}
	if t := in.PreparingTimeoutSeconds; t != nil && *t < 1 {
		errs = append(errs, kernel.FieldError{
			Pointer: "/preparingTimeoutSeconds", Reason: kernel.ReasonMinimum,
			Message: "The preparing timeout is at least 1 second; null takes the default.",
		})
	}
	errs = append(errs, kernel.CheckLabels("/labels", in.Labels)...)
	if len(errs) == 0 {
		return nil
	}
	return &kernel.Invalid{Detail: invalidDetail, Errors: errs}
}

// csr reads a create's or a reset's certificate signing request, refused
// at csrPointer unless it is a request for an EC P-256 key.
//
// ref: decision 0008 § "The worker API"
func csr(in string) (*x509.CertificateRequest, error) {
	req, err := credential.ParseCSR(in)
	if err == nil {
		return req, nil
	}
	msg := "certificateSigningRequest is not a certificate signing request in PEM, or base64 of its PEM or DER."
	if errors.Is(err, credential.ErrKey) {
		msg = "The pool key must be an EC P-256 key; generate one with " +
			"`openssl ecparam -name prime256v1 -genkey`."
	}
	return nil, &kernel.Invalid{
		Detail: "The certificate signing request is refused.",
		Errors: []kernel.FieldError{{Pointer: csrPointer, Reason: kernel.ReasonRule, Message: msg}},
	}
}

// Create creates a pool of p's account from in, and signs its
// certificate for the key of the request req. The route has decided
// WORKER_POOL_CREATE on in's space.
func (s *Service) Create(
	ctx context.Context,
	p kernel.Principal,
	in Input,
	req string,
) (Pool, error) {
	if err := check(in); err != nil {
		return Pool{}, err
	}
	key, err := csr(req)
	if err != nil {
		return Pool{}, err
	}
	var out Pool
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		space, err := s.space(ctx, p, in.Space)
		if err != nil {
			return err
		}
		now := s.now()
		out = Pool{
			Account: p.AccountID, Organization: p.OrganizationID, ID: kernel.NewULID(),
			CreatedAt: now, Version: 1,
		}
		apply(&out, space, in, now)
		if err := s.sign(ctx, &out, key); err != nil {
			return err
		}
		return s.store.Create(ctx, out) //nolint:wrapcheck // the store names the write
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// apply sets in's settings on out, in space.
func apply(out *Pool, space kernel.ULID, in Input, now time.Time) {
	out.Name, out.Space = strings.TrimSpace(in.Name), space
	out.Description, out.Labels = in.Description, slices.Clone(in.Labels)
	if out.Labels == nil {
		out.Labels = []string{}
	}
	out.PreparingTimeout = 0
	if t := in.PreparingTimeoutSeconds; t != nil {
		out.PreparingTimeout = time.Duration(*t) * time.Second
	}
	out.DriftDetectionRunLimit, out.UpdatedAt = in.DriftDetectionRunLimit, now
}

// sign has the pool CA, created on first use, sign out's certificate for
// key under a new serial.
func (s *Service) sign(ctx context.Context, out *Pool, key *x509.CertificateRequest) error {
	now := s.now()
	a, err := s.store.Authority(ctx, func() (Authority, error) {
		ca, err := credential.NewCA(now)
		if err != nil {
			return Authority{}, err //nolint:wrapcheck // names the step
		}
		cert, priv, err := ca.Marshal()
		return Authority{Certificate: cert, Key: priv}, err //nolint:wrapcheck // as above
	})
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	ca, err := credential.ParseCA(a.Certificate, a.Key)
	if err != nil {
		return err //nolint:wrapcheck // names the part that does not parse
	}
	cert, err := ca.Sign(key, out.ID, a.Serial, now)
	if err != nil {
		return err //nolint:wrapcheck // names the pool
	}
	out.Certificate, out.Serial = cert.Raw, a.Serial
	return nil
}

// Update replaces the settings of p's account's pool id with in's, when
// expect accepts its version. The route has decided WORKER_POOL_UPDATE
// on its space; a move needs WORKER_POOL_CREATE on the new space too.
func (s *Service) Update(
	ctx context.Context,
	p kernel.Principal,
	id string,
	expect kernel.Expect,
	in Input,
) (Pool, error) {
	if err := check(in); err != nil {
		return Pool{}, err
	}
	return s.write(ctx, p.AccountID, id, expect, func(ctx context.Context, out *Pool) error {
		space, err := s.space(ctx, p, in.Space)
		if err != nil {
			return err
		}
		if space != out.Space {
			if err := s.require(ctx, p, accessapi.ActionWorkerPoolCreate, space); err != nil {
				return err
			}
			if err := s.inUse(ctx, p.AccountID, out.ID, &space); err != nil {
				return err
			}
		}
		apply(out, space, in, s.now())
		return nil
	})
}

// Reset signs a new certificate for the key of the request req, under a
// new serial, for p's account's pool id, so registrations under the
// old one are refused. The route has decided WORKER_POOL_RESET on its
// space.
func (s *Service) Reset(ctx context.Context, p kernel.Principal, id, req string) (Pool, error) {
	key, err := csr(req)
	if err != nil {
		return Pool{}, err
	}
	return s.write(
		ctx,
		p.AccountID,
		id,
		kernel.Expect{},
		func(ctx context.Context, out *Pool) error {
			out.UpdatedAt = s.now()
			return s.sign(ctx, out, key)
		},
	)
}

// write locks acct's pool id, checks expect against its version, has
// change change it, and writes it with its next version.
func (s *Service) write(
	ctx context.Context,
	acct kernel.AccountID,
	id string,
	expect kernel.Expect,
	change func(ctx context.Context, out *Pool) error,
) (Pool, error) {
	var out Pool
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if out, err = s.get(ctx, acct, id, true); err != nil {
			return err
		}
		if err := expect.Check(out.Version); err != nil {
			return err //nolint:wrapcheck // kernel.ErrStale is the answer
		}
		if err := change(ctx, &out); err != nil {
			return err
		}
		out.Version++
		return s.store.Write(ctx, out) //nolint:wrapcheck // the store names the write
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// Delete deletes p's account's pool id with its workers, when expect
// accepts its version, and answers it as it was. The route has decided
// WORKER_POOL_DELETE on its space.
func (s *Service) Delete(
	ctx context.Context,
	p kernel.Principal,
	id string,
	expect kernel.Expect,
) (Pool, error) {
	var out Pool
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if out, err = s.get(ctx, p.AccountID, id, true); err != nil {
			return err
		}
		if err := expect.Check(out.Version); err != nil {
			return err //nolint:wrapcheck // kernel.ErrStale is the answer
		}
		if err := s.inUse(ctx, p.AccountID, out.ID, nil); err != nil {
			return err
		}
		return s.store.Delete(ctx, p.AccountID, out.ID) //nolint:wrapcheck // the store names it
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// inUse refuses, as an *InUse, a delete of acct's pool id while any
// stack names it, or, when to is set, a move of it to the space to that
// leaves such a stack out of reach. The pool's row is locked, so no stack
// names it meanwhile.
func (s *Service) inUse(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	to *kernel.ULID,
) error {
	s.mu.RLock()
	guards := slices.Clone(s.guards)
	s.mu.RUnlock()
	var tree []accessapi.SpaceNode
	if to != nil {
		var err error
		if tree, err = s.spaces.Tree(ctx, acct); err != nil {
			return err //nolint:wrapcheck // the access domain names the read
		}
	}
	var stacks []string
	for _, g := range guards {
		uses, err := g.PoolInUse(ctx, acct, id)
		if err != nil {
			return err //nolint:wrapcheck // the guarding domain names the read
		}
		for _, u := range uses {
			if to == nil || !slices.Contains(Reach(tree, u.Space), *to) {
				stacks = append(stacks, u.Stack)
			}
		}
	}
	if len(stacks) == 0 {
		return nil
	}
	slices.Sort(stacks)
	return &InUse{Stacks: stacks}
}

// Purge deletes up to batch of the deleted account acct's pools, with
// their workers and nonces; true when none is left.
func (s *Service) Purge(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	return s.store.Purge(ctx, acct, batch) //nolint:wrapcheck // the store names the write
}

// scope is what p's searches see: the spaces it may read and its
// account's tree.
func (s *Service) scope(ctx context.Context, p kernel.Principal) (Scope, error) {
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return Scope{}, err //nolint:wrapcheck // the snapshot's read names itself
	}
	tree, err := s.spaces.Tree(ctx, p.AccountID)
	if err != nil {
		return Scope{}, err //nolint:wrapcheck // the access domain names the read
	}
	return Scope{Reachable: reach, Spaces: tree}, nil
}

// space is the ID of the space slug, the top space when it is empty,
// held for the transaction, when p may read it; a *Missing otherwise.
func (s *Service) space(ctx context.Context, p kernel.Principal, slug string) (kernel.ULID, error) {
	id := p.AccountID.ULID
	if slug != "" {
		var err error
		if id, err = s.spaces.Find(ctx, p.AccountID, slug); errors.Is(err, accessapi.ErrNoSpace) {
			return kernel.ULID{}, &Missing{Kind: "space", ID: slug}
		} else if err != nil {
			return kernel.ULID{}, err //nolint:wrapcheck // the access domain names the read
		}
	}
	read, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, id)
	switch {
	case err != nil:
		return kernel.ULID{}, err //nolint:wrapcheck // the snapshot's read names itself
	case !read:
		return kernel.ULID{}, &Missing{Kind: "space", ID: slug}
	}
	if err := s.spaces.Hold(ctx, p.AccountID, id); errors.Is(err, accessapi.ErrNoSpace) {
		return kernel.ULID{}, &Missing{Kind: "space", ID: slug}
	} else if err != nil {
		return kernel.ULID{}, err //nolint:wrapcheck // the access domain names the lock
	}
	return id, nil
}

// require refuses p action in space, as a *Denied.
func (s *Service) require(
	ctx context.Context,
	p kernel.Principal,
	action accessapi.Action,
	space kernel.ULID,
) error {
	ok, err := s.authz.Authorize(ctx, p, action, space)
	switch {
	case err != nil:
		return err //nolint:wrapcheck // the snapshot's read names itself
	case !ok:
		return &Denied{Action: action}
	}
	return nil
}

// SpaceGuard is the service's say in the access domain's space deletes
// and moves.
type SpaceGuard struct{ s *Service }

// SpaceGuard is the service as an accessapi.SpaceGuard.
func (s *Service) SpaceGuard() SpaceGuard { return SpaceGuard{s} }

// SpaceInUse reports whether a pool lives in space, which refuses its
// deletion.
func (g SpaceGuard) SpaceInUse(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) (bool, error) {
	return g.s.store.InSpace(ctx, acct, space) //nolint:wrapcheck // the store names the read
}

// LostOnMove refuses no move: a pool's workers serve its stacks
// wherever its space moves.
func (SpaceGuard) LostOnMove(context.Context, accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	return nil, nil
}
