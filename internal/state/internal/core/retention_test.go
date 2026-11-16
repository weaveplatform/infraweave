package core

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// retainer is a store whose Expired answers batches in turn, and whose
// DeleteVersions answers the key "k-" and the ID of each version it is
// handed; under are the runs that have not ended. It records each read
// with its cursor, retention and batch, each question about runs and
// each delete, with whether it ran in a transaction, and the objects it
// is handed. broken names the one call that fails with errDown once
// more than brokenAfter reads have been made.
type retainer struct {
	*mem
	*bucket
	batches     [][]Expired
	under       []kernel.ULID
	log         []string
	objects     []string
	broken      string
	brokenAfter int
	reads       int
}

func (r *retainer) breaks(call string) error {
	if r.broken == call && r.reads > r.brokenAfter {
		return errDown
	}
	return nil
}

func (r *retainer) Expired(ctx context.Context, after Expired, retention time.Duration, batch int) ([]Expired, error) {
	r.reads++
	r.log = append(r.log, fmt.Sprintf("read after %s/%s %s %d%s", after.Account, after.ID,
		retention, batch, inTx(ctx)))
	if err := r.breaks("Expired"); err != nil {
		return nil, err
	}
	if len(r.batches) == 0 {
		return nil, nil
	}
	b := r.batches[0]
	r.batches = r.batches[1:]
	return b, nil
}

func (r *retainer) NotEnded(ctx context.Context, acct kernel.AccountID, runs []kernel.ULID) ([]kernel.ULID, error) {
	r.log = append(r.log, fmt.Sprintf("runs of %s %s%s", acct, names(runs), inTx(ctx)))
	if err := r.breaks("NotEnded"); err != nil {
		return nil, err
	}
	var out []kernel.ULID
	for _, run := range runs {
		if slices.Contains(r.under, run) {
			out = append(out, run)
		}
	}
	return out, nil
}

func (r *retainer) DeleteVersions(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) ([]string, error) {
	r.log = append(r.log, fmt.Sprintf("delete %s %s%s", acct, names(ids), inTx(ctx)))
	if err := r.breaks("DeleteVersions"); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, "k-"+id.String())
	}
	return keys, nil
}

func (r *retainer) Delete(ctx context.Context, store objectstore.Name, key string) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	r.objects = append(r.objects, string(store)+"/"+key)
	return r.breaks("Delete")
}

// names are ids, comma-separated.
func names(ids []kernel.ULID) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return strings.Join(out, ",")
}

func newRetainer(retention time.Duration) (*retainer, *Service) {
	r := &retainer{mem: newMem(), bucket: newBucket()}
	return r, New(Deps{DB: db{}, Store: r, Objects: r, Ends: r, Retention: retention})
}

// TestUnitRetain checks that a retention of 0 reads nothing; that a pass
// reads PurgeBatch versions at a time, each read in a transaction of its
// own after the last version the one before read, until a read answers
// fewer; that of each read it asks once about each run of each account,
// in the read's order, and nothing of an account whose versions no run
// wrote, deletes each version no run under way wrote and
// hands their objects of the states store on in that transaction; and
// that it deletes nothing of an account whose every version a run under
// way wrote.
//
// ref: decision 0010 § "Rollback, searches and retention"
func TestUnitRetain(t *testing.T) {
	r, s := newRetainer(0)
	if n, err := s.Retain(t.Context()); n != 0 || err != nil || len(r.log) != 0 {
		t.Fatalf("a retention of 0 = %d, %v after %v", n, err, r.log)
	}

	r, s = newRetainer(time.Hour)
	a, b, c := kernel.NewAccountID(), kernel.NewAccountID(), kernel.NewAccountID()
	ended, going := kernel.NewULID(), kernel.NewULID()
	r.under = []kernel.ULID{going}
	full := make([]Expired, 0, PurgeBatch)
	for len(full) < PurgeBatch-4 {
		full = append(full, Expired{Account: a, ID: kernel.NewULID()})
	}
	full = append(full,
		Expired{Account: a, ID: kernel.NewULID(), Run: &ended},
		Expired{Account: a, ID: kernel.NewULID(), Run: &going},
		Expired{Account: a, ID: kernel.NewULID(), Run: &ended},
		Expired{Account: b, ID: kernel.NewULID(), Run: &going},
	)
	last := []Expired{{Account: c, ID: kernel.NewULID()}}
	r.batches = [][]Expired{full, last}
	n, err := s.Retain(t.Context())
	if err != nil || n != PurgeBatch-1 {
		t.Fatalf("Retain = %d, %v; want %d deleted", n, err, PurgeBatch-1)
	}
	kept := []kernel.ULID{full[PurgeBatch-3].ID, full[PurgeBatch-1].ID}
	var ofA, objects []string
	for _, v := range append(slices.Clone(full), last...) {
		if slices.Contains(kept, v.ID) {
			continue
		}
		if v.Account == a {
			ofA = append(ofA, v.ID.String())
		}
		objects = append(objects, "states/k-"+v.ID.String())
	}
	zero := kernel.AccountID{}
	want := []string{
		fmt.Sprintf("read after %s/%s 1h0m0s %d in tx", zero, kernel.ULID{}, PurgeBatch),
		fmt.Sprintf("runs of %s %s in tx", a, names([]kernel.ULID{ended, going})),
		fmt.Sprintf("delete %s %s in tx", a, strings.Join(ofA, ",")),
		fmt.Sprintf("runs of %s %s in tx", b, going),
		fmt.Sprintf("read after %s/%s 1h0m0s %d in tx", b, full[PurgeBatch-1].ID, PurgeBatch),
		fmt.Sprintf("delete %s %s in tx", c, last[0].ID),
	}
	if !reflect.DeepEqual(r.log, want) {
		t.Errorf("the pass called\n%s\nwant\n%s", strings.Join(r.log, "\n"),
			strings.Join(want, "\n"))
	}
	if !reflect.DeepEqual(r.objects, objects) {
		t.Errorf("the pass handed on %d objects, want %d", len(r.objects), len(objects))
	}

	r, s = newRetainer(time.Hour)
	if n, err := s.Retain(t.Context()); n != 0 || err != nil || len(r.log) != 1 {
		t.Errorf("a pass over nothing = %d, %v after %v", n, err, r.log)
	}
}

// TestUnitRetainFails checks that a failed read, question about runs,
// delete or object deletion stops the pass with its error, answering only
// the versions of the batches that committed before it, so a pass stopped
// part way leaves the failed batch's rows, and their objects, to the next.
func TestUnitRetainFails(t *testing.T) {
	acct, run := kernel.NewAccountID(), kernel.NewULID()
	batch := func() []Expired {
		out := make([]Expired, PurgeBatch)
		for i := range out {
			out[i] = Expired{Account: acct, ID: kernel.NewULID(), Run: &run}
		}
		return out
	}
	for _, call := range []string{"Expired", "NotEnded", "DeleteVersions", "Delete"} {
		r, s := newRetainer(time.Hour)
		r.batches = [][]Expired{batch(), batch()}
		r.broken, r.brokenAfter = call, 1
		n, err := s.Retain(t.Context())
		if !errors.Is(err, errDown) || n != PurgeBatch {
			t.Errorf("a failing %s = %d, %v; want the first batch's %d", call, n, err, PurgeBatch)
		}
		if r.reads != 2 {
			t.Errorf("a failing %s stopped after %d reads, want 2", call, r.reads)
		}
	}
}
