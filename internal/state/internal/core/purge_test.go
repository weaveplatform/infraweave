package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// purger is a store whose purge answers batches in turn, then done, and
// records each call's account and stack and whether it ran in a
// transaction; the objects it hands on are recorded as deleted.
type purger struct {
	*mem
	batches                 [][]string
	calls                   []string
	deleted                 []string
	enqueued                []any
	failDelete, failEnqueue error
}

func (p *purger) Purge(
	ctx context.Context,
	acct kernel.AccountID,
	stack *kernel.ULID,
	batch int,
) ([]string, bool, error) {
	at := acct.String()
	if stack != nil {
		at += "/" + stack.String()
	}
	p.calls = append(p.calls, fmt.Sprintf("%s %d%s", at, batch, inTx(ctx)))
	if err := p.failing("Purge"); err != nil {
		return nil, false, err
	}
	if len(p.batches) == 0 {
		return nil, true, nil
	}
	keys := p.batches[0]
	p.batches = p.batches[1:]
	return keys, false, nil
}

func (p *purger) Delete(ctx context.Context, store objectstore.Name, key string) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	p.deleted = append(p.deleted, string(store)+"/"+key)
	return p.failDelete
}

func (p *purger) EnqueueTx(
	_ context.Context,
	k queue.Kind,
	payload any,
	_ ...queue.Option,
) (int64, error) {
	p.enqueued = append(p.enqueued, k.Name, payload)
	return 1, p.failEnqueue
}

func newPurger() (*purger, *Service) {
	p := &purger{mem: newMem()}
	return p, New(Deps{DB: db{}, Store: p, Stacks: p, Objects: p, Jobs: p})
}

// TestUnitDeleting checks that a stack's delete enqueues its purge, that
// a failed enqueue fails the delete, and that a move loses nothing.
func TestUnitDeleting(t *testing.T) {
	p, s := newPurger()
	st := stacksapi.Stack{ID: kernel.NewULID(), Account: kernel.NewAccountID()}
	if err := s.Deleting(t.Context(), st); err != nil ||
		!reflect.DeepEqual(p.enqueued, []any{"state.purge", purge{st.Account.ULID, st.ID}}) {
		t.Errorf("Deleting = %v, enqueued %v", err, p.enqueued)
	}
	p.failEnqueue = errDown
	if err := s.Deleting(t.Context(), st); !errors.Is(err, errDown) {
		t.Errorf("a failed enqueue = %v", err)
	}
	if lost, err := s.LostOnMove(t.Context(), stacksapi.StackMove{Stack: st}); lost != nil ||
		err != nil {
		t.Errorf("LostOnMove = %v, %v", lost, err)
	}
}

// TestUnitPurge checks that a purge deletes batch after batch, each in a
// transaction that hands the batch's version objects of the states store
// on, until the store has none left; that the job purges its stack and
// dies at once on a payload that does not decode; and that a failing
// store or object deletion stops the purge with its error.
func TestUnitPurge(t *testing.T) {
	p, s := newPurger()
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	p.batches = [][]string{{"a", "b"}, nil, {"c"}}
	if err := s.Purge(t.Context(), acct, nil); err != nil {
		t.Fatal(err)
	}
	every := fmt.Sprintf("%s %d in tx", acct, PurgeBatch)
	if !reflect.DeepEqual(p.calls, []string{every, every, every, every}) ||
		!reflect.DeepEqual(p.deleted, []string{"states/a", "states/b", "states/c"}) {
		t.Errorf("an account's purge called %v and deleted %v", p.calls, p.deleted)
	}

	p.calls, p.batches = nil, [][]string{{"d"}}
	payload, err := json.Marshal(purge{Account: acct.ULID, Stack: stack})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Purged(t.Context(), queue.Job{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	one := fmt.Sprintf("%s/%s %d in tx", acct, stack, PurgeBatch)
	if !reflect.DeepEqual(p.calls, []string{one, one}) {
		t.Errorf("a stack's purge called %v", p.calls)
	}
	err = s.Purged(t.Context(), queue.Job{Payload: []byte("{")})
	if fmt.Sprintf("%T", err) != fmt.Sprintf("%T", queue.Permanent(err)) {
		t.Errorf("a payload that does not decode = %T %v", err, err)
	}

	p.batches, p.failDelete = [][]string{{"e"}}, errDown
	if err := s.Purge(t.Context(), acct, nil); !errors.Is(err, errDown) {
		t.Errorf("a failed object deletion = %v", err)
	}
	p.fail = "Purge"
	if err := s.Purge(t.Context(), acct, nil); !errors.Is(err, errDown) {
		t.Errorf("a failed store = %v", err)
	}
}
