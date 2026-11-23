package worker

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-jose/go-jose/v4"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/images"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// Status is a worker's status: OFFLINE when offline, else DRAINED when
// drained, else BUSY when busy, else IDLE.
//
// ref: decision 0008 § "Worker status, drain, cycle and reset"
type Status string

// The statuses a worker answers.
const (
	StatusIdle    Status = "IDLE"
	StatusBusy    Status = "BUSY"
	StatusDrained Status = "DRAINED"
	StatusOffline Status = "OFFLINE"
)

// Worker is a registered worker of a private pool.
type Worker struct {
	// Public workers carry installation authority and have no tenant owner.
	Public       bool
	Account      kernel.AccountID
	Organization kernel.OrganizationID
	ID           kernel.ULID
	Pool         kernel.ULID
	// Serial is the pool certificate's serial the worker registered
	// under; a reset leaves it behind.
	Serial            int64
	Version, OS, Arch string
	Metadata          map[string]string
	Busy              bool
	Drained           bool
	// Cycle is set by a cycle of the pool, and answered on each poll
	// until the worker deregisters.
	Cycle bool
	// Offline is set by the reap once the worker is unseen for
	// worker.offline_after, and cleared by its next call.
	Offline    bool
	LastSeenAt time.Time
	// AvailableAt is when the worker last became idle; nil while busy.
	AvailableAt *time.Time
	CreatedAt   time.Time
	// Slot is a controller's logical slot, nil for a launcher's worker. A
	// pool holds at most one worker in each slot.
	Slot *int32
}

// Status is w's status.
func (w Worker) Status() Status {
	switch {
	case w.Offline:
		return StatusOffline
	case w.Drained:
		return StatusDrained
	case w.Busy:
		return StatusBusy
	}
	return StatusIdle
}

// Fields are what a search of a pool's workers filters and orders by. A
// search that names no order reads the newest first.
var Fields = []search.Field{
	{Name: "id", Type: search.String},
	{
		Name: "status", Type: search.Enum,
		Values: []string{
			string(StatusBusy), string(StatusDrained), string(StatusIdle), string(StatusOffline),
		},
	},
	{Name: "busy", Type: search.Boolean},
	{Name: "drained", Type: search.Boolean},
	{Name: "createdAt", Type: search.Time, Orderable: true},
}

var (
	// ErrNotFound reports a worker that does not exist.
	ErrNotFound = errors.New("workers: no such worker")
	// ErrUnauthenticated reports a registration, or a worker session, the
	// worker API refuses: its proof, or the worker it names, is not one
	// the server accepts.
	ErrUnauthenticated = errors.New("workers: the worker's credential is refused")
)

// The worker API's clocks.
//
// ref: decision 0008 § "The worker API"
const (
	// Skew is how far a registration's iat may be from the server's clock.
	Skew = 5 * time.Minute
	// NonceTTL is how long a registration's jti is kept, so a replay
	// within it is refused.
	NonceTTL = 10 * time.Minute
	// RenewWithin is how near its expiry a poll's session token is renewed.
	RenewWithin = 15 * time.Minute
	// DefaultWait, MinWait and MaxWait bound the hold a poll asks for.
	DefaultWait = 60 * time.Second
	MinWait     = 5 * time.Second
	MaxWait     = 120 * time.Second
)

// The bounds of a registration, so a pool's key cannot make the server
// parse, verify or store an unbounded proof or description.
// MaxRegistration must match the maxLength of RegisterRequest's
// registration in api/src/worker/paths-workers.yaml, which the shared
// body checks enforce before the service is called.
const (
	MaxRegistration  = 32 << 10
	MaxDescription   = 64
	MaxMetadata      = 32
	MaxMetadataKey   = 64
	MaxMetadataValue = 255
)

// Store is what the service needs of the workers' storage. Every method
// joins the transaction its context carries.
type Store interface {
	Public(ctx context.Context, lock bool) (pool.Pool, error)
	PublicShare(ctx context.Context) (pool.Pool, error)
	// PoolByID reads and share-locks a private pool, or reads the public pool;
	// public registration takes PublicShare before any worker access.
	// pool.ErrNotFound when none does.
	PoolByID(ctx context.Context, id kernel.ULID) (pool.Pool, error)
	// Get reads acct's pool id, as pool.Store's Get.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock bool) (pool.Pool, error)
	// CA reads the pool CA's certificate DER, nil while the installation
	// has none.
	CA(ctx context.Context) ([]byte, error)
	// Nonce records the pool's jti until expires, dropping its expired
	// ones; false when jti is recorded already.
	Nonce(ctx context.Context, acct kernel.AccountID, pool kernel.ULID, jti string, expires time.Time) (bool, error)
	// Count counts the registered workers of org and of acct.
	Count(ctx context.Context, org kernel.OrganizationID, acct kernel.AccountID) (featuresapi.Usage, error)
	// Worker reads the worker id with its pool's current serial. A locked read
	// takes pool FOR SHARE before worker FOR UPDATE; ErrNotFound when none.
	Worker(ctx context.Context, id kernel.ULID, lock bool) (Worker, int64, error)
	// CreateWorker writes a new worker.
	CreateWorker(ctx context.Context, w Worker) error
	// Resume rewrites w's serial, version, os, arch and metadata, and
	// marks it seen at w.LastSeenAt, and no longer offline.
	Resume(ctx context.Context, w Worker) error
	// Seen first fences the positive bearer serial against registration and
	// current pool under a pool share lock, then marks the worker seen at now,
	// and no longer
	// offline, setting its available_at when it becomes idle, and answers
	// it with its pool's current serial; ErrNotFound for missing/stale credentials.
	Seen(ctx context.Context, id kernel.ULID, serial int64, busy bool, now time.Time) (Worker, int64, error)
	// Claimed marks the worker id busy, as a claim leaves it until its
	// launcher's next poll reports.
	Claimed(ctx context.Context, id kernel.ULID) error
	// Remove deletes the worker id; ErrNotFound when none.
	Remove(ctx context.Context, id kernel.ULID) error
	// MarkOffline marks the workers last seen before before offline, of
	// every account, and answers how many it marked.
	MarkOffline(ctx context.Context, before time.Time) (int, error)
	// Lost deletes the worker longest unseen of those last seen before
	// before, of every account, skipping workers another transaction
	// locks, and answers its ID; false when none is left.
	Lost(ctx context.Context, before time.Time) (kernel.ULID, bool, error)
	// Drain sets the drain flag of acct's worker id of pool and wakes the
	// pool's held polls; ErrNotFound when the pool has no such worker.
	Drain(ctx context.Context, acct kernel.AccountID, pool, id kernel.ULID, drain bool) (Worker, error)
	// Cycle marks every worker of acct's pool to cycle and wakes the
	// pool's held polls.
	Cycle(ctx context.Context, acct kernel.AccountID, pool kernel.ULID) error
	// SearchWorkers reads a page of the workers of acct's pool, which
	// lives in space, that in keeps.
	SearchWorkers(ctx context.Context, acct kernel.AccountID, pool, space kernel.ULID, in search.Input) (search.Result[Worker], error)
	// SuggestWorkers lists the values the workers of acct's pool, which
	// lives in space, hold.
	SuggestWorkers(ctx context.Context, acct kernel.AccountID, pool, space kernel.ULID, in search.SuggestionsInput) (search.Suggestions, error)
	// RunnerImages reads acct's default runner images, both nil when it
	// sets none.
	RunnerImages(ctx context.Context, acct kernel.AccountID) (images.Images, error)
	// OfPools reads the workers of acct's pools, by pool, oldest first.
	OfPools(ctx context.Context, acct kernel.AccountID, pools []kernel.ULID) (map[kernel.ULID][]Worker, error)
	// Binding reads the hosting owner of pool, holding a lock that
	// serializes the pool's binds, releases and registrations until the
	// transaction ends; false when the pool has no binding.
	Binding(ctx context.Context, pool kernel.ULID) (kernel.WorkerOwner, bool, error)
	// Bind records owner as p's hosting owner; ErrOwnership when owner
	// hosts another pool.
	Bind(ctx context.Context, p pool.Pool, owner kernel.WorkerOwner) error
	// Unbind removes pool's hosting binding.
	Unbind(ctx context.Context, pool kernel.ULID) error
	// ManagedPool reads the pool owner's management identity reserves:
	// reserved is false when owner manages none, and the pool is nil for a
	// deleted pool's retained reservation.
	ManagedPool(ctx context.Context, owner kernel.WorkerOwner) (*kernel.ULID, bool, error)
	// HasWorkers reports whether pool has a registered worker.
	HasWorkers(ctx context.Context, pool kernel.ULID) (bool, error)
	// SlotWorker reads the worker in pool's slot, locked FOR UPDATE;
	// ErrNotFound when the slot is empty.
	SlotWorker(ctx context.Context, pool kernel.ULID, slot int32) (Worker, error)
}

// Tokens mints the worker session tokens registrations and polls answer,
// and the run tokens of the runs polls claim, and answers the keys that
// verify those run tokens.
type Tokens interface {
	WorkerToken(ctx context.Context, worker kernel.ULID, serial int64) (accessapi.Token, error)
	RunToken(ctx context.Context, run kernel.ULID) (accessapi.Token, error)
	RunTokenKeys(ctx context.Context) (accessapi.RunTokenKeys, error)
}

// Limits admits one more registered worker, as the features domain's
// Entitlements.CheckLimit does.
type Limits interface {
	CheckLimit(ctx context.Context, org kernel.OrganizationID, acct kernel.AccountID, limit string, count func(context.Context) (featuresapi.Usage, error)) error
}

// Deps are what the service is constructed over: the store, the pool its
// registrations begin transactions on, the tokens it mints, the limits a
// new worker is checked against (none when nil), Version, the server's
// version, which a launcher's must match to its minor version or the
// one before, PollMax, the longest a poll is held, MaxWait when zero,
// and the reap's clocks: OfflineAfter, worker.offline_after,
// DefaultOfflineAfter when zero; LostAfter, run.lost_after,
// DefaultLostAfter when zero; and PreparingTimeout, the preparing timeout
// of a pool that sets none, pool.DefaultPreparingTimeout when zero.
type Deps struct {
	PublicRegistries       []string
	PublicPoolEnabled      bool
	PublicPreparingTimeout time.Duration
	Store                  Store
	DB                     tx.Beginner
	Tokens                 Tokens
	Limits                 Limits
	Version                string
	PollMax                time.Duration
	OfflineAfter           time.Duration
	LostAfter              time.Duration
	PreparingTimeout       time.Duration
	Now                    func() time.Time
}

// Service is the workers' logic: the worker API's registration, polls and
// exit, and the API's drain, cycle and worker reads.
type Service struct {
	publicRegistries  []string
	publicPoolEnabled bool
	store             Store
	db                tx.Beginner
	tokens            Tokens
	limits            Limits
	scheduler         workersapi.Scheduler
	reports           workersapi.Reports
	liveness          workersapi.Liveness
	signals           workersapi.Signals
	assignments       workersapi.PoolAssignments
	version           string
	pollMax           time.Duration
	clocks            clocks
	now               func() time.Time
	hub               *hub
}

// New returns the service over d.
func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	pollMax := d.PollMax
	if pollMax == 0 {
		pollMax = MaxWait
	}
	return &Service{
		publicRegistries:  slices.Clone(d.PublicRegistries),
		publicPoolEnabled: d.PublicPoolEnabled,
		store:             d.Store, db: d.DB, tokens: d.Tokens, limits: d.Limits, version: d.Version,
		pollMax: pollMax, clocks: newClocks(d), now: now, hub: newHub(),
	}
}

// BindScheduler binds the runs domain's claim, which polls claim their
// pool's next run through; until then a poll claims none.
func (s *Service) BindScheduler(sch workersapi.Scheduler) { s.scheduler = sch }

// BindReports binds the runs domain's reports, which a claimed run's
// reports and uploads go to; until then each answers ErrNotOnWorker.
func (s *Service) BindReports(r workersapi.Reports) { s.reports = r }

// BindLiveness binds the runs domain's liveness, which the reap and a
// poll that reports an ended container fail runs through; until then
// neither fails a run.
func (s *Service) BindLiveness(l workersapi.Liveness) { s.liveness = l }

// BindSignals binds the runs domain's signals, which a busy worker's poll
// delivers its run's stop or kill through; until then a poll delivers
// none.
func (s *Service) BindSignals(sig workersapi.Signals) { s.signals = sig }

// Registration is a registered worker and its session token.
type Registration struct {
	Worker kernel.ULID
	Token  accessapi.Token
}

// claims are a registration's payload.
type claims struct {
	Capabilities []string          `json:"capabilities"`
	Pool         string            `json:"pool"`
	IssuedAt     int64             `json:"iat"`
	ID           string            `json:"jti"`
	Version      string            `json:"version"`
	OS           string            `json:"os"`
	Arch         string            `json:"arch"`
	Metadata     map[string]string `json:"metadata"`
	Worker       string            `json:"worker"`
	// Owner and Slot name a controller's hosting identity and logical slot.
	Owner *kernel.WorkerOwner `json:"owner"`
	Slot  *int32              `json:"slot"`
}

// refuse is ErrUnauthenticated, naming why for the log.
func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnauthenticated, fmt.Sprintf(format, args...))
}

// Register registers the worker raw, a compact JWS signed with a pool's
// private key, describes, or resumes the worker of the pool it names, and
// answers it with a session token. A proof the server does not accept is
// ErrUnauthenticated; a launcher of a version it does not serve is a
// *kernel.Invalid; a registration that does not match the pool's hosting
// binding is ErrOwnership; a new worker beyond the private-workers limit is
// the features domain's *featuresapi.LimitError.
//
// ref: decision 0008 § "The worker API"
func (s *Service) Register(ctx context.Context, raw string) (Registration, error) {
	now := s.now()
	c, leaf, id, err := s.proof(ctx, raw, now)
	if err != nil {
		return Registration{}, err
	}
	if err := s.supported(c.Version); err != nil {
		return Registration{}, err
	}
	if !slices.Contains(c.Capabilities, "plan-sanitization-v1") {
		return Registration{}, invalid(kernel.ReasonRule, "The launcher must support plan-sanitization-v1; upgrade the launcher before registering.")
	}
	if c.Owner != nil && c.Slot == nil {
		return Registration{}, invalid(kernel.ReasonRequired, "A controller registration names its logical slot.")
	}
	if c.Slot != nil && c.Owner == nil {
		return Registration{}, invalid(kernel.ReasonRequired, "A logical slot belongs to a controller registration, which names its owner.")
	}
	var out Registration
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		p, err := s.admit(ctx, c, leaf, id, true, now)
		if err != nil {
			return err
		}
		if err := s.hosted(ctx, p, c.Owner); err != nil {
			return err
		}
		w := Worker{
			Public:  p.Public,
			Account: p.Account, Organization: p.Organization, Pool: p.ID, Serial: p.Serial,
			Version: c.Version, OS: c.OS, Arch: c.Arch, Metadata: c.Metadata, Slot: c.Slot,
			LastSeenAt: now, AvailableAt: &now, CreatedAt: now,
		}
		if w.Metadata == nil {
			w.Metadata = map[string]string{}
		}
		resumed, err := s.resume(ctx, c.Worker, w)
		if err != nil {
			return err
		}
		if !resumed.IsZero() {
			out.Worker = resumed
			out.Token, err = s.tokens.WorkerToken(ctx, resumed, p.Serial)
			return err //nolint:wrapcheck // access names token issuance
		}
		if s.limits != nil && !p.Public {
			if err := s.limits.CheckLimit(ctx, p.Organization, p.Account,
				featuresapi.PrivateWorkers,
				func(ctx context.Context) (featuresapi.Usage, error) {
					return s.store.Count(ctx, p.Organization, p.Account)
				}); err != nil {
				return err //nolint:wrapcheck // a *featuresapi.LimitError is answered as it is
			}
		}
		w.ID = kernel.NewULID()
		out.Worker = w.ID
		if err := s.store.CreateWorker(ctx, w); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		out.Token, err = s.tokens.WorkerToken(ctx, w.ID, p.Serial)
		return err //nolint:wrapcheck // access names token issuance
	})
	if err != nil {
		return Registration{}, err //nolint:wrapcheck // the refusal or the store's error
	}
	return out, nil
}

// proof verifies raw, a compact JWS a pool's private key signed, against
// the pool CA at now, and answers its bounded payload, the certificate that
// signed it and the pool it names.
func (s *Service) proof(ctx context.Context, raw string, now time.Time) (claims, *x509.Certificate, kernel.ULID, error) {
	if len(raw) > MaxRegistration {
		return claims{}, nil, kernel.ULID{}, invalid(kernel.ReasonMaxLength,
			"A registration is at most %d bytes.", MaxRegistration)
	}
	jws, err := jose.ParseSignedCompact(raw, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		return claims{}, nil, kernel.ULID{}, refuse("not an ES256 compact JWS: %v", err)
	}
	ca, err := s.store.CA(ctx)
	if err != nil {
		return claims{}, nil, kernel.ULID{}, err //nolint:wrapcheck // the store names the read
	}
	c, leaf, err := verify(jws, ca, now)
	if err != nil {
		return claims{}, nil, kernel.ULID{}, err
	}
	if err := c.bounded(); err != nil {
		return claims{}, nil, kernel.ULID{}, err
	}
	id, err := kernel.ParseULID(c.Pool)
	switch {
	case err != nil || leaf.Subject.CommonName != c.Pool:
		return claims{}, nil, kernel.ULID{}, refuse("the certificate is not pool %q's", c.Pool)
	case now.Sub(time.Unix(c.IssuedAt, 0)).Abs() > Skew:
		return claims{}, nil, kernel.ULID{}, refuse("iat %d is more than %s from now", c.IssuedAt, Skew)
	case c.ID == "":
		return claims{}, nil, kernel.ULID{}, refuse("no jti")
	}
	return c, leaf, id, nil
}

// admit reads the pool id that c names inside the caller's transaction,
// requires leaf to carry its current serial and records c's jti. A
// registration, as opposed to an ownership proof, also needs the public
// pool enabled and the public-pool-v1 capability.
func (s *Service) admit(ctx context.Context, c claims, leaf *x509.Certificate, id kernel.ULID, registration bool, now time.Time) (pool.Pool, error) {
	p, err := s.store.PoolByID(ctx, id)
	if errors.Is(err, pool.ErrNotFound) {
		return pool.Pool{}, refuse("no pool %s", id)
	}
	if err != nil {
		return pool.Pool{}, err //nolint:wrapcheck // the store names the read
	}
	if p.Public {
		if registration && !s.publicPoolEnabled {
			return pool.Pool{}, refuse("the installation public pool is disabled")
		}
		if registration && !slices.Contains(c.Capabilities, "public-pool-v1") {
			return pool.Pool{}, invalid(kernel.ReasonRule, "Public workers must support public-pool-v1; upgrade the launcher before registering.")
		}
		p, err = s.store.PublicShare(ctx)
		if err != nil {
			return pool.Pool{}, err //nolint:wrapcheck // the store names the locked read
		}
	}
	if leaf.SerialNumber.Cmp(big.NewInt(p.Serial)) != 0 {
		return pool.Pool{}, refuse("pool %s's certificate %s was replaced by a reset", id, leaf.SerialNumber)
	}
	fresh, err := s.store.Nonce(ctx, p.Account, p.ID, c.ID, now.Add(NonceTTL))
	if err != nil {
		return pool.Pool{}, err //nolint:wrapcheck // the store names the write
	}
	if !fresh {
		return pool.Pool{}, refuse("jti %q was used in the last %s", c.ID, NonceTTL)
	}
	return p, nil
}

// resume rewrites the worker named, when it is one of w's pool, with w's
// registration, and answers its ID; the zero ID when there is none to
// resume, which registers a new worker. A controller's registration
// resumes the worker of its slot instead, so a retry after a lost response
// never registers a second worker for one slot. A resumed worker keeps its run
// and its flags, and is not counted against private-workers again.
func (s *Service) resume(ctx context.Context, named string, w Worker) (kernel.ULID, error) {
	if w.Slot != nil {
		old, err := s.store.SlotWorker(ctx, w.Pool, *w.Slot)
		if errors.Is(err, ErrNotFound) {
			return kernel.ULID{}, nil
		}
		if err != nil {
			return kernel.ULID{}, err //nolint:wrapcheck // the store names the read
		}
		w.ID = old.ID
		return old.ID, s.store.Resume(ctx, w) //nolint:wrapcheck // the store names the write
	}
	id, err := kernel.ParseULID(named)
	if named == "" || err != nil {
		return kernel.ULID{}, nil
	}
	old, _, err := s.store.Worker(ctx, id, true)
	switch {
	case errors.Is(err, ErrNotFound):
		return kernel.ULID{}, nil
	case err != nil:
		return kernel.ULID{}, err //nolint:wrapcheck // the store names the read
	case old.Account != w.Account || old.Pool != w.Pool || old.Public && old.Serial != w.Serial:
		return kernel.ULID{}, nil
	}
	w.ID = id
	return id, s.store.Resume(ctx, w) //nolint:wrapcheck // the store names the write
}

// verify checks that jws's x5c certificate chains to the pool CA ca, a
// client certificate valid at now, and that it signed jws, and answers its
// payload and certificate.
func verify(jws *jose.JSONWebSignature, ca []byte, now time.Time) (claims, *x509.Certificate, error) {
	if ca == nil {
		return claims{}, nil, refuse("the installation has no pool CA")
	}
	root, err := x509.ParseCertificate(ca)
	if err != nil {
		return claims{}, nil, fmt.Errorf("workers: the pool CA's certificate: %w", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	chains, err := jws.Signatures[0].Protected.Certificates(x509.VerifyOptions{
		Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		return claims{}, nil, refuse("x5c: %v", err)
	}
	leaf := chains[0][0]
	payload, err := jws.Verify(leaf.PublicKey)
	if err != nil {
		return claims{}, nil, refuse("signature: %v", err)
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return claims{}, nil, refuse("payload: %v", err)
	}
	return c, leaf, nil
}

// bounded refuses a payload whose description exceeds the registration's
// bounds, counting characters as the API's labels do.
func (c claims) bounded() error {
	if len(c.Capabilities) > 16 {
		return invalid(kernel.ReasonMaxLength, "A registration carries at most 16 capabilities.")
	}
	for _, capability := range c.Capabilities {
		if utf8.RuneCountInString(capability) > 64 {
			return invalid(kernel.ReasonMaxLength, "A capability is at most 64 characters.")
		}
	}
	for _, f := range [...]struct{ name, v string }{
		{"version", c.Version}, {"os", c.OS}, {"arch", c.Arch},
	} {
		if utf8.RuneCountInString(f.v) > MaxDescription {
			return invalid(kernel.ReasonMaxLength,
				"A registration's %s is at most %d characters.", f.name, MaxDescription)
		}
	}
	if c.Owner != nil {
		if faults := kernel.CheckWorkerOwner("/registration/owner", *c.Owner); len(faults) > 0 {
			return &kernel.Invalid{Detail: "The hosting owner is refused.", Errors: faults}
		}
	}
	if c.Slot != nil && *c.Slot < 0 {
		return invalid(kernel.ReasonMinimum, "A logical slot is at least 0.")
	}
	if len(c.Metadata) > MaxMetadata {
		return invalid(kernel.ReasonMaxLength,
			"A registration's metadata holds at most %d entries.", MaxMetadata)
	}
	for k, v := range c.Metadata {
		if utf8.RuneCountInString(k) > MaxMetadataKey ||
			utf8.RuneCountInString(v) > MaxMetadataValue {
			return invalid(kernel.ReasonMaxLength, "A registration's metadata keys are at most "+
				"%d characters and its values at most %d.", MaxMetadataKey, MaxMetadataValue)
		}
	}
	return nil
}

// invalid is a *kernel.Invalid of the registration, for reason.
func invalid(reason, format string, args ...any) *kernel.Invalid {
	msg := fmt.Sprintf(format, args...)
	return &kernel.Invalid{Detail: msg, Errors: []kernel.FieldError{{
		Pointer: "/registration", Reason: reason, Message: msg,
	}}}
}

// supported refuses a launcher whose version is not the server's minor
// version or the one before. A server that is no release, such as a
// devel build, serves every launcher.
//
// ref: decision 0008 § "The worker API"
func (s *Service) supported(v string) error {
	major, newest, ok := minor(s.version)
	if !ok {
		return nil
	}
	oldest := max(newest-1, 0)
	if m, n, ok := minor(v); ok && m == major && n >= oldest && n <= newest {
		return nil
	}
	inv := invalid(kernel.ReasonRule, "The launcher's version %q is not accepted.", v)
	inv.Detail = fmt.Sprintf("This server accepts launchers from %d.%d.0 "+
		"to %d.%d; the oldest accepted is %d.%d.0.", major, oldest, major, newest, major, oldest)
	return inv
}

// minor is v's major and minor version, with or without a leading v.
func minor(v string) (major, minor int, ok bool) {
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, errMajor := strconv.Atoi(parts[0])
	minor, errMinor := strconv.Atoi(parts[1])
	return major, minor, errMajor == nil && errMinor == nil
}
