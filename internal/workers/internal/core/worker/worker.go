package worker

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
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
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// Status is a worker's status: DRAINED when drained, else BUSY when busy,
// else IDLE.
//
// ref: decision 0008 § "Worker status, drain, cycle and reset"
type Status string

// The statuses a worker answers.
const (
	StatusIdle    Status = "IDLE"
	StatusBusy    Status = "BUSY"
	StatusDrained Status = "DRAINED"
)

// Worker is a registered worker of a private pool.
type Worker struct {
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
	Cycle      bool
	LastSeenAt time.Time
	// AvailableAt is when the worker last became idle; nil while busy.
	AvailableAt *time.Time
	CreatedAt   time.Time
}

// Status is w's status.
func (w Worker) Status() Status {
	switch {
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
		Values: []string{string(StatusBusy), string(StatusDrained), string(StatusIdle)},
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
	// PoolByID reads the pool id of whichever account holds it;
	// pool.ErrNotFound when none does.
	PoolByID(ctx context.Context, id kernel.ULID) (pool.Pool, error)
	// Get reads acct's pool id, as pool.Store's Get.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock bool) (pool.Pool, error)
	// CA reads the pool CA's certificate DER, nil while the installation
	// has none.
	CA(ctx context.Context) ([]byte, error)
	// Nonce records the pool's jti until expires, dropping its expired
	// ones; false when jti is recorded already.
	Nonce(
		ctx context.Context,
		acct kernel.AccountID,
		pool kernel.ULID,
		jti string,
		expires time.Time,
	) (bool, error)
	// Count counts the registered workers of org and of acct.
	Count(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
	) (featuresapi.Usage, error)
	// Worker reads the worker id with its pool's current serial, locked
	// FOR UPDATE when lock is set; ErrNotFound when none.
	Worker(ctx context.Context, id kernel.ULID, lock bool) (Worker, int64, error)
	// CreateWorker writes a new worker.
	CreateWorker(ctx context.Context, w Worker) error
	// Resume rewrites w's serial, version, os, arch and metadata, and
	// marks it seen at w.LastSeenAt.
	Resume(ctx context.Context, w Worker) error
	// Seen marks the worker id seen at now with busy, setting its
	// available_at when it becomes idle, and answers it with its pool's
	// current serial; ErrNotFound when none.
	Seen(ctx context.Context, id kernel.ULID, busy bool, now time.Time) (Worker, int64, error)
	// Remove deletes the worker id; ErrNotFound when none.
	Remove(ctx context.Context, id kernel.ULID) error
	// Drain sets the drain flag of acct's worker id of pool and wakes the
	// pool's held polls; ErrNotFound when the pool has no such worker.
	Drain(
		ctx context.Context,
		acct kernel.AccountID,
		pool, id kernel.ULID,
		drain bool,
	) (Worker, error)
	// Cycle marks every worker of acct's pool to cycle and wakes the
	// pool's held polls.
	Cycle(ctx context.Context, acct kernel.AccountID, pool kernel.ULID) error
	// SearchWorkers reads a page of the workers of acct's pool, which
	// lives in space, that in keeps.
	SearchWorkers(
		ctx context.Context,
		acct kernel.AccountID,
		pool, space kernel.ULID,
		in search.Input,
	) (search.Result[Worker], error)
	// SuggestWorkers lists the values the workers of acct's pool, which
	// lives in space, hold.
	SuggestWorkers(
		ctx context.Context,
		acct kernel.AccountID,
		pool, space kernel.ULID,
		in search.SuggestionsInput,
	) (search.Suggestions, error)
	// OfPools reads the workers of acct's pools, by pool, oldest first.
	OfPools(
		ctx context.Context,
		acct kernel.AccountID,
		pools []kernel.ULID,
	) (map[kernel.ULID][]Worker, error)
}

// Tokens mints the worker session tokens registrations and polls answer.
type Tokens interface {
	WorkerToken(ctx context.Context, worker kernel.ULID) (accessapi.Token, error)
}

// Limits admits one more registered worker, as the features domain's
// Entitlements.CheckLimit does.
type Limits interface {
	CheckLimit(
		ctx context.Context,
		org kernel.OrganizationID,
		acct kernel.AccountID,
		limit string,
		count func(context.Context) (featuresapi.Usage, error),
	) error
}

// Deps are what the service is constructed over: the store, the pool its
// registrations begin transactions on, the tokens it mints, the limits a
// new worker is checked against (none when nil), Version, the server's
// version, which a launcher's must match to its minor version or the
// one before, and PollMax, the longest a poll is held, MaxWait when zero.
type Deps struct {
	Store   Store
	DB      tx.Beginner
	Tokens  Tokens
	Limits  Limits
	Version string
	PollMax time.Duration
	Now     func() time.Time
}

// Service is the workers' logic: the worker API's registration, polls and
// exit, and the API's drain, cycle and worker reads.
type Service struct {
	store   Store
	db      tx.Beginner
	tokens  Tokens
	limits  Limits
	version string
	pollMax time.Duration
	now     func() time.Time
	hub     *hub
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
		store: d.Store, db: d.DB, tokens: d.Tokens, limits: d.Limits, version: d.Version,
		pollMax: pollMax, now: now, hub: newHub(),
	}
}

// Registration is a registered worker and its session token.
type Registration struct {
	Worker kernel.ULID
	Token  accessapi.Token
}

// claims are a registration's payload.
type claims struct {
	Pool     string            `json:"pool"`
	IssuedAt int64             `json:"iat"`
	ID       string            `json:"jti"`
	Version  string            `json:"version"`
	OS       string            `json:"os"`
	Arch     string            `json:"arch"`
	Metadata map[string]string `json:"metadata"`
	Worker   string            `json:"worker"`
}

// refuse is ErrUnauthenticated, naming why for the log.
func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnauthenticated, fmt.Sprintf(format, args...))
}

// Register registers the worker raw, a compact JWS signed with a pool's
// private key, describes, or resumes the worker of the pool it names, and
// answers it with a session token. A proof the server does not accept is
// ErrUnauthenticated; a launcher of a version it does not serve is a
// *kernel.Invalid; a new worker beyond the private-workers limit is the
// features domain's *featuresapi.LimitError.
//
// ref: decision 0008 § "The worker API"
func (s *Service) Register(ctx context.Context, raw string) (Registration, error) {
	now := s.now()
	if len(raw) > MaxRegistration {
		return Registration{}, invalid(kernel.ReasonMaxLength,
			"A registration is at most %d bytes.", MaxRegistration)
	}
	jws, err := jose.ParseSignedCompact(raw, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		return Registration{}, refuse("not an ES256 compact JWS: %v", err)
	}
	ca, err := s.store.CA(ctx)
	if err != nil {
		return Registration{}, err //nolint:wrapcheck // the store names the read
	}
	c, leaf, err := verify(jws, ca, now)
	if err != nil {
		return Registration{}, err
	}
	if err := c.bounded(); err != nil {
		return Registration{}, err
	}
	id, err := kernel.ParseULID(c.Pool)
	switch {
	case err != nil || leaf.Subject.CommonName != c.Pool:
		return Registration{}, refuse("the certificate is not pool %q's", c.Pool)
	case now.Sub(time.Unix(c.IssuedAt, 0)).Abs() > Skew:
		return Registration{}, refuse("iat %d is more than %s from now", c.IssuedAt, Skew)
	case c.ID == "":
		return Registration{}, refuse("no jti")
	}
	if err := s.supported(c.Version); err != nil {
		return Registration{}, err
	}
	var out Registration
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		p, err := s.store.PoolByID(ctx, id)
		if errors.Is(err, pool.ErrNotFound) {
			return refuse("no pool %s", id)
		}
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if leaf.SerialNumber.Cmp(big.NewInt(p.Serial)) != 0 {
			return refuse("pool %s's certificate %s was replaced by a reset", id, leaf.SerialNumber)
		}
		fresh, err := s.store.Nonce(ctx, p.Account, p.ID, c.ID, now.Add(NonceTTL))
		if err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		if !fresh {
			return refuse("jti %q was used in the last %s", c.ID, NonceTTL)
		}
		w := Worker{
			Account: p.Account, Organization: p.Organization, Pool: p.ID, Serial: p.Serial,
			Version: c.Version, OS: c.OS, Arch: c.Arch, Metadata: c.Metadata,
			LastSeenAt: now, AvailableAt: &now, CreatedAt: now,
		}
		if w.Metadata == nil {
			w.Metadata = map[string]string{}
		}
		resumed, err := s.resume(ctx, c.Worker, w)
		if err != nil || !resumed.IsZero() {
			out.Worker = resumed
			return err
		}
		if s.limits != nil {
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
		return s.store.CreateWorker(ctx, w) //nolint:wrapcheck // the store names the write
	})
	if err != nil {
		return Registration{}, err //nolint:wrapcheck // the refusal or the store's error
	}
	out.Token, err = s.tokens.WorkerToken(ctx, out.Worker)
	if err != nil {
		return Registration{}, err //nolint:wrapcheck // the access domain names the mint
	}
	return out, nil
}

// resume rewrites the worker named, when it is one of w's pool, with w's
// registration, and answers its ID; the zero ID when there is none to
// resume, which registers a new worker. A resumed worker keeps its run
// and its flags, and is not counted against private-workers again.
func (s *Service) resume(ctx context.Context, named string, w Worker) (kernel.ULID, error) {
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
	case old.Account != w.Account || old.Pool != w.Pool:
		return kernel.ULID{}, nil
	}
	w.ID = id
	return id, s.store.Resume(ctx, w) //nolint:wrapcheck // the store names the write
}

// verify checks that jws's x5c certificate chains to the pool CA ca, a
// client certificate valid at now, and that it signed jws, and answers its
// payload and certificate.
func verify(
	jws *jose.JSONWebSignature,
	ca []byte,
	now time.Time,
) (claims, *x509.Certificate, error) {
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
	for _, f := range [...]struct{ name, v string }{
		{"version", c.Version}, {"os", c.OS}, {"arch", c.Arch},
	} {
		if utf8.RuneCountInString(f.v) > MaxDescription {
			return invalid(kernel.ReasonMaxLength,
				"A registration's %s is at most %d characters.", f.name, MaxDescription)
		}
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
