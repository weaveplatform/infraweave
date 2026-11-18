package intake

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// The protocol and inline storage limits, independently of the served API bound.
const (
	MaxBody        = 25 << 20
	InlineBody     = 256 << 10
	PruneBatch     = 500
	ReconcileBatch = 500
)

// The durable states of a delivery.
const (
	Received   = "received"
	Dispatched = "dispatched"
	Failed     = "failed"
	Expired    = "expired"
)

// The inbox processor and hourly retention task.
var (
	Process         = queue.Kind{Name: "vcs.process_delivery", Queue: queue.VCSInbox, Policy: queue.Ignore}
	Prune           = queue.Kind{Name: "vcs.deliveries.prune", Queue: queue.Maintenance, Policy: queue.Ignore}
	Reconcile       = queue.Kind{Name: "vcs.deliveries.reconcile", Queue: queue.Maintenance, Policy: queue.Ignore}
	ErrNotFound     = errors.New("vcs: delivery or integration not found")
	ErrVerification = errors.New("vcs: webhook verification refused")
	errParse        = errors.New("vcs: webhook payload could not be parsed")
	errBody         = errors.New("vcs: webhook body could not be read")
	errPayload      = errors.New("vcs: invalid delivery job payload")
	errDispatch     = errors.New("vcs: delivery preparation failed")
)

// Integration is the lookup's opened webhook secrets, never provider credentials.
type Integration struct {
	Account       kernel.AccountID
	ID            kernel.ULID
	Provider      string
	Current       []byte
	Previous      []byte
	PreviousUntil *time.Time
}

// String and GoString prevent accidental formatting from disclosing secrets.
func (Integration) String() string      { return "webhook integration [redacted]" }
func (in Integration) GoString() string { return in.String() }

// LogValue also redacts JSON handlers, which do not use String for structs.
func (in Integration) LogValue() slog.Value { return slog.StringValue(in.String()) }

// Secrets is the current secret and the previous one only while its window is
// open. Replay uses this same lookup and injected clock.
func (in Integration) Secrets(now time.Time) [][]byte {
	var out [][]byte
	if len(in.Current) > 0 {
		out = append(out, in.Current)
	}
	if len(in.Previous) > 0 && in.PreviousUntil != nil && now.Before(*in.PreviousUntil) {
		out = append(out, in.Previous)
	}
	return out
}

// Delivery retains the verified bytes and immutable provider identity. No
// credential-bearing request header is part of Headers.
type Delivery struct {
	Account     kernel.AccountID
	ID          kernel.ULID
	Integration kernel.ULID
	Provider    string
	DeliveryID  string
	Event       string
	ReceivedAt  time.Time
	Headers     http.Header
	Signature   []byte
	Body        []byte
	BodyKey     string
	Repository  string
	State       string
	Deadline    time.Time
	RunsCreated int
	Replays     int64
	LastError   string
}

// String, GoString and LogValue omit payloads and retained request headers.
func (d Delivery) String() string       { return "VCS delivery " + d.ID.String() }
func (d Delivery) GoString() string     { return d.String() }
func (d Delivery) LogValue() slog.Value { return slog.StringValue(d.String()) }

// Store joins every transaction on ctx. Insert holds the still-live integration
// before inserting, so a concurrent deletion cannot leave an acknowledged orphan.
type Store interface {
	Integration(ctx context.Context, id kernel.ULID, at time.Time) (Integration, error)
	Insert(ctx context.Context, d Delivery) (bool, error)
	Read(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Delivery, error)
	Hold(ctx context.Context, ref Ref) (bool, error)
	Failure(ctx context.Context, ref Ref) error
	State(ctx context.Context, acct kernel.AccountID, id kernel.ULID, replays int64, state, lastError string) error
	Drop(ctx context.Context, acct *kernel.AccountID, before *time.Time, batch int) ([]string, int, error)
	Pending(ctx context.Context, after kernel.ULID, limit int) ([]Ref, error)
}

// Ref is a still-received delivery considered by bounded crash reconciliation.
type Ref struct {
	Account kernel.AccountID
	ID      kernel.ULID
	Replays int64
	State   string
}

// Objects is the platform's pending/live blob lifecycle, including deferred
// deletion in the transaction that drops a delivery.
type Objects interface {
	Put(context.Context, objectstore.Name, string, io.Reader, int64) (objectstore.Object, error)
	Commit(context.Context, objectstore.Name, string) error
	Open(context.Context, objectstore.Name, string) (io.ReadCloser, error)
	Delete(context.Context, objectstore.Name, string) error
	LockLiveAt(context.Context, objectstore.Name, string, time.Time) (objectstore.Object, error)
}

// Jobs is the transactional queue boundary used by intake and its processor.
type Jobs interface {
	EnqueueTx(context.Context, queue.Kind, any, ...queue.Option) (int64, error)
	CompleteTx(context.Context, queue.Job) error
	HoldTx(context.Context, queue.Job) error
	DeadByKeyTx(context.Context, queue.Kind, string, kernel.ULID) (bool, error)
	StatusByKeyPrefixes(context.Context, kernel.ULID, []queue.Kind, []string) (map[string]queue.KeyStatus, error)
	HoldByKeyPrefixTx(context.Context, kernel.ULID, []queue.Kind, string) (queue.KeyStatus, error)
}

// DispatchPlan commits prepared VCS work under the processor's queue fence.
// Its implementation owns business rows and child jobs; it makes no remote call.
type DispatchPlan interface {
	Commit(context.Context) error
}

// Dispatcher prepares full event data and target stacks outside the completion
// transaction. Every prepared row/child commits with dispatched and CompleteTx.
type Dispatcher interface {
	Prepare(context.Context, Delivery, []vcsapi.Event) (DispatchPlan, error)
}

// Deps supplies the durable boundaries and the clock used for secret overlap and
// processing deadlines. Rejected counts only bounded provider/reason labels.
type Deps struct {
	DB           tx.Beginner
	Store        Store
	Objects      Objects
	Jobs         Jobs
	Providers    map[string]port.Webhooks
	Now          func() time.Time
	Timeout      time.Duration
	Retention    time.Duration
	Rejected     func(context.Context, string, string)
	Log          *slog.Logger
	Dispatcher   Dispatcher
	RelatedKinds []queue.Kind
	Admin        AdminStore
}

// Service receives deliveries and processes the work they commit.
type Service struct{ d Deps }

// New constructs intake; zero timing values take the documented defaults.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Timeout == 0 {
		d.Timeout = 10 * time.Minute
	}
	if d.Retention == 0 {
		d.Retention = 7 * 24 * time.Hour
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Admin == nil {
		d.Admin, _ = d.Store.(AdminStore)
	}
	return &Service{d: d}
}

// Receive verifies before filtering or writing. Its outcomes are 202 for new,
// 200 for duplicates/unconsumed events, and typed refusals for the HTTP adapter.
func (s *Service) Receive(ctx context.Context, provider, ref string, headers http.Header, body []byte) (int, error) {
	id, err := kernel.ParseULID(ref)
	if err != nil {
		return 0, ErrNotFound
	}
	now := s.d.Now()
	in, err := s.d.Store.Integration(ctx, id, now)
	if err != nil {
		return 0, err //nolint:wrapcheck // the store names the integration lookup
	}
	if strings.ToLower(in.Provider) != provider {
		return 0, ErrNotFound
	}
	p := s.d.Providers[in.Provider]
	if p == nil {
		return 0, s.refuse(ctx, in.Provider, "unsupported")
	}
	sig, err := p.Verify(headers, body, in.Secrets(now))
	if err != nil {
		return 0, s.refuse(ctx, in.Provider, "signature")
	}
	event := p.Event(headers, body)
	if !p.Consumed(event) {
		return http.StatusOK, nil
	}
	deliveryID := p.DeliveryID(headers, body)
	if deliveryID == "" {
		sum := sha256.Sum256(body)
		deliveryID = hex.EncodeToString(sum[:])
	}
	if body == nil {
		body = []byte{}
	}
	d := Delivery{
		Account: in.Account, ID: kernel.NewULID(), Integration: id, Provider: in.Provider,
		DeliveryID: deliveryID, Event: event, ReceivedAt: now, Headers: SafeHeaders(in.Provider, headers),
		Signature: sig, Body: body, Repository: p.Repository(body), State: Received, Deadline: now.Add(s.d.Timeout),
	}
	if len(body) > InlineBody {
		d.BodyKey = in.Account.String() + "/" + d.ID.String()
		if _, err := s.d.Objects.Put(ctx, objectstore.Inbox, d.BodyKey, bytes.NewReader(body), int64(len(body))); err != nil {
			return 0, err //nolint:wrapcheck // the object lifecycle names the write
		}
		d.Body = nil
	}
	return s.store(ctx, d)
}

func (s *Service) refuse(ctx context.Context, provider, reason string) error {
	if s.d.Rejected != nil {
		s.d.Rejected(ctx, provider, reason)
	}
	s.d.Log.WarnContext(ctx, "VCS webhook rejected", "provider", provider, "reason", reason)
	return ErrVerification
}

func (s *Service) store(ctx context.Context, d Delivery) (int, error) {
	created := false
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		var err error
		created, err = s.d.Store.Insert(ctx, d)
		if err != nil {
			return err //nolint:wrapcheck // the store names the insert
		}
		if !created {
			if d.BodyKey != "" {
				return s.d.Objects.Delete(ctx, objectstore.Inbox, d.BodyKey)
			}
			return nil
		}
		if d.BodyKey != "" {
			if err := s.d.Objects.Commit(ctx, objectstore.Inbox, d.BodyKey); err != nil {
				return err //nolint:wrapcheck // the object lifecycle names the commit
			}
		}
		_, err = s.d.Jobs.EnqueueTx(ctx, Process, job{Account: d.Account, ID: d.ID},
			queue.Account(d.Account.ULID), queue.UniqueKey(ProcessKey(d.ID, d.Replays)),
			queue.OrderingKey(d.Integration.String()+"/"+d.Repository))
		return err //nolint:wrapcheck // the queue names the job kind
	})
	if err != nil {
		return 0, fmt.Errorf("vcs: store verified delivery: %w", err)
	}
	if created {
		return http.StatusAccepted, nil
	}
	return http.StatusOK, nil
}

// SafeHeaders selects only replay/parse metadata. GitLab tokens and Basic
// credentials never enter storage, even when a provider's parser fails.
func SafeHeaders(provider string, headers http.Header) http.Header {
	names := []string{"Content-Type", "User-Agent"}
	switch provider {
	case "GITHUB", "GITHUB_ENTERPRISE":
		names = append(names, "X-GitHub-Event", "X-GitHub-Delivery", "X-Hub-Signature-256")
	case "GITLAB":
		names = append(names, "X-Gitlab-Event", "X-Gitlab-Event-UUID")
	case "BITBUCKET_CLOUD":
		names = append(names, "X-Event-Key", "X-Request-UUID", "X-Hub-Signature")
	case "BITBUCKET_DATACENTER":
		names = append(names, "X-Event-Key", "X-Request-Id", "X-Hub-Signature")
	}
	out := http.Header{}
	for _, name := range names {
		if values := headers.Values(name); len(values) > 0 {
			out[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	return out
}
