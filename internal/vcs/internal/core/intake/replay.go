package intake

import (
	"context"
	"crypto/fips140"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// AdminStore keeps replay mutations in the caller's transaction. Owned locks
// the integration against rotation/deletion and the delivery when lock is true.
// List reads metadata only; it never selects retained bodies or signatures.
type AdminStore interface {
	Owned(context.Context, kernel.AccountID, kernel.ULID, kernel.ULID, time.Time, bool) (Delivery, error)
	List(context.Context, kernel.AccountID, kernel.ULID, time.Time, search.Input) (search.Result[Delivery], error)
	Reset(context.Context, Delivery, time.Time) (Delivery, error)
	Audit(context.Context, kernel.AuditEntry) error
}

// ReplayConflict is a fixed safe refusal, never a provider or storage error.
type ReplayConflict struct{ Detail string }

func (e *ReplayConflict) Error() string { return e.Detail }

// ReplayPrefix scopes one replay generation. Generation zero preserves the
// keys already enqueued by older replicas. All generations share ID.String().
func ReplayPrefix(id kernel.ULID, replays int64) string {
	if replays == 0 {
		return id.String()
	}
	return id.String() + ":r" + strconv.FormatInt(replays, 10) + ":"
}

// ProcessKey and EvaluationKey give both levels a fresh identity on replay.
func ProcessKey(id kernel.ULID, replays int64) string {
	if replays == 0 {
		return id.String()
	}
	return ReplayPrefix(id, replays) + "process"
}
func EvaluationKey(id kernel.ULID, replays int64, stack kernel.ULID) string {
	if replays == 0 {
		return id.String() + ":" + stack.String()
	}
	return ReplayPrefix(id, replays) + "stack:" + stack.String()
}

func (s *Service) kinds() []queue.Kind {
	return append([]queue.Kind{Process}, s.d.RelatedKinds...)
}
func terminal(status queue.KeyStatus) bool { return status.Dead > 0 || status.Cancelled > 0 }

// List projects a sticky generation failure without changing the DISPATCHED
// state that still-active sibling evaluations need. The serializable snapshot
// keeps a row's generation and the queue summary in the same database view.
func (s *Service) List(ctx context.Context, acct kernel.AccountID, integration kernel.ULID, in search.Input) (search.Result[Delivery], error) {
	var out search.Result[Delivery]
	if s.d.Admin == nil {
		return out, kernel.ErrUnavailable
	}
	err := tx.Do(ctx, s.d.DB, tx.Options{Serializable: true}, func(ctx context.Context) error {
		var err error
		out, err = s.d.Admin.List(ctx, acct, integration, s.d.Now().Add(-s.d.Retention), in)
		if err != nil || len(out.Edges) == 0 {
			return err
		}
		prefixes := make([]string, len(out.Edges))
		for i, e := range out.Edges {
			prefixes[i] = ReplayPrefix(e.Node.ID, e.Node.Replays)
		}
		statuses, err := s.d.Jobs.StatusByKeyPrefixes(ctx, acct.ULID, s.kinds(), prefixes)
		if err != nil {
			return err
		}
		for i := range out.Edges {
			d := &out.Edges[i].Node
			if terminal(statuses[prefixes[i]]) || d.State == Dispatched && d.LastError != "" {
				d.State = Failed
				d.LastError = exhausted
			}
		}
		return nil
	})
	return out, err
}

// Replay verifies immutable retained bytes again. Queue rows are held before
// delivery/integration rows, matching workers' completion lock order. Every
// mutation, including the caller audit, rolls back if enqueue or sealing fails.
func (s *Service) Replay(ctx context.Context, p kernel.Principal, integration, id kernel.ULID, remoteIP string) (Delivery, error) {
	if s.d.Admin == nil {
		return Delivery{}, kernel.ErrUnavailable
	}
	initial, err := s.d.Admin.Owned(ctx, p.AccountID, integration, id, s.d.Now().Add(-s.d.Retention), false)
	if err != nil {
		return Delivery{}, err
	}
	body, err := s.replayBody(ctx, initial)
	if err != nil {
		return Delivery{}, err
	}
	var out Delivery
	err = tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		status, err := s.d.Jobs.HoldByKeyPrefixTx(ctx, p.AccountID.ULID, s.kinds(), id.String())
		if err != nil {
			return err
		}
		if status.Pending > 0 || status.Running > 0 {
			return &ReplayConflict{"The delivery still has pending or running work."}
		}
		now := s.d.Now()
		d, err := s.d.Admin.Owned(ctx, p.AccountID, integration, id, now.Add(-s.d.Retention), true)
		if err != nil {
			return err
		}
		if d.Replays != initial.Replays {
			return &ReplayConflict{"The delivery changed while replay was being requested."}
		}
		if d.BodyKey != "" {

			if _, err = s.d.Objects.LockLiveAt(ctx, objectstore.Inbox, d.BodyKey, now); err != nil {
				if errors.Is(err, objectstore.ErrNotFound) {
					return &ReplayConflict{"The retained inbox body has expired or is no longer available."}
				}
				return err
			}
		}
		in, err := s.d.Store.Integration(ctx, integration, now)
		if err != nil {
			return err
		}
		if in.Account != p.AccountID || !verifyStored(body, d.Signature, in.Secrets(now)) {
			return &ReplayConflict{"The stored signature no longer verifies with an eligible webhook secret."}
		}
		out, err = s.d.Admin.Reset(ctx, d, now.Add(s.d.Timeout))
		if err != nil {
			return err
		}
		if _, err = s.d.Jobs.EnqueueTx(ctx, Process, job{Account: out.Account, ID: out.ID, Replays: out.Replays},
			queue.Account(out.Account.ULID), queue.UniqueKey(ProcessKey(out.ID, out.Replays)), queue.OrderingKey(out.Integration.String()+"/"+out.Repository)); err != nil {
			return err
		}
		actor := kernel.AuditActor{Username: p.Login}
		if actor.Username == "" {
			actor.Username = p.ActorID.String()
		}
		if p.Kind == kernel.PrincipalRun {
			actor = kernel.AuditActor{Username: "infraweave", Run: p.ActorID}
		}
		return s.d.Admin.Audit(ctx, kernel.AuditEntry{
			AccountID: p.AccountID.ULID, Actor: actor, Action: "vcs_integration.replay_delivery", EventType: "UPDATE",
			AffectedResource: kernel.AuditResource{Type: "VCS_INTEGRATION", ID: integration.String()},
			RemoteIP:         remoteIP, CreatedAt: now,
		})
	})
	if err != nil {
		return Delivery{}, err
	}
	return out, nil
}

func (s *Service) replayBody(ctx context.Context, d Delivery) ([]byte, error) {
	if d.BodyKey == "" {
		return d.Body, nil
	}
	r, err := s.d.Objects.Open(ctx, objectstore.Inbox, d.BodyKey)
	if errors.Is(err, objectstore.ErrNotFound) {
		return nil, &ReplayConflict{"The retained inbox body has expired or is no longer available."}
	}
	if err != nil {
		return nil, fmt.Errorf("vcs: open replay body: %w", err)
	}
	defer r.Close()
	body, err := io.ReadAll(io.LimitReader(r, MaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("vcs: read replay body: %w", err)
	}
	if len(body) > MaxBody {
		return nil, &ReplayConflict{"The retained body exceeds the processing limit."}
	}
	return body, nil
}

// GitHub/Bitbucket retain the exact sha256= header; token/basic providers
// retain a plain body MAC. Incoming credential headers are deliberately absent.
func verifyStored(body, signature []byte, secrets [][]byte) bool {
	text := string(signature)
	if strings.HasPrefix(text, "sha256=") {
		text = strings.TrimPrefix(text, "sha256=")
	}
	if len(text) != sha256.Size*2 {
		return false
	}
	expected, err := hex.DecodeString(text)
	if err != nil {
		return false
	}
	matched := 0
	for _, secret := range secrets {
		if len(secret) == 0 || fips140.Enforced() && len(secret) < 14 {
			continue
		}
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(body)
		matched |= subtle.ConstantTimeCompare(expected, mac.Sum(nil))
	}
	return matched == 1
}
