package pg_test

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/policies/internal/sampling"
)

// pausedObjects delegates the actual sealed filesystem upload, then pauses at
// the external-I/O/transaction boundary. Every metadata and queue fence is real.
type pausedObjects struct {
	*blobs.Objects
	put       atomic.Int32
	completed chan struct{}
	resume    chan struct{}
}

func (o *pausedObjects) Put(ctx context.Context, store objectstore.Name, key string, reader io.Reader, size int64) (objectstore.Object, error) {
	o.put.Add(1)
	obj, err := o.Objects.Put(ctx, store, key, reader, size)
	close(o.completed)
	if err == nil {
		<-o.resume
	}
	return obj, err
}

func (w *sampleWorld) pausedUpload(t *testing.T) (*pausedObjects, *sampling.Service) {
	t.Helper()
	objects := &pausedObjects{Objects: w.objects, completed: make(chan struct{}), resume: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-objects.resume:
		default:
			close(objects.resume)
		}
	})
	svc := sampling.New(sampling.Deps{DB: w.f.db, Store: w.f.store, Queue: w.q, Objects: objects, Now: func() time.Time { return w.f.now }})
	return objects, svc
}

func awaitUpload(t *testing.T, completed <-chan struct{}) {
	t.Helper()
	select {
	case <-completed:
	case <-t.Context().Done():
		t.Fatal("real upload did not reach publication boundary")
	}
}

func TestAccSampleUploadRecoversCompletedPutWithoutRewritingAfterGenerationLoss(t *testing.T) {
	w := newSampleWorld(t)
	id := w.evaluate(t).Receipts[0].ID
	job := w.claim(t)
	objects, service := w.pausedUpload(t)
	finished := make(chan error, 1)
	go func() { finished <- service.Upload(t.Context(), job) }()
	awaitUpload(t, objects.completed)
	// The one-connection pool is free while the external call is paused.
	// Lease recovery creates a different real generation before the old
	// writer attempts to commit its publication transaction.
	if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET lease_until=statement_timestamp()-interval '1 minute' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if count, err := w.q.Recover(t.Context()); err != nil || count != 1 {
		t.Fatalf("recover uploader lease: %d %v", count, err)
	}
	jobs, err := w.q.Claim(t.Context(), sampling.UploadKind.Queue, []string{sampling.UploadKind.Name}, 2, time.Minute, "publication-recovery")
	if err != nil {
		t.Fatal(err)
	}
	var replacement queue.Job
	for _, candidate := range jobs {
		if candidate.ID == job.ID {
			replacement = candidate
		} else if err := w.samples.Upload(t.Context(), candidate); err != nil {
			t.Fatal(err)
		}
	}
	if replacement.ID != job.ID || replacement.Generation <= job.Generation {
		t.Fatalf("recovery did not reclaim the same immutable upload: %+v -> %+v", job, replacement)
	}
	close(objects.resume)
	if err := <-finished; !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatalf("stale generation published completed upload: %v", err)
	}
	if err := service.Upload(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if objects.put.Load() != 1 {
		t.Fatal("publication recovery rewrote the immutable object")
	}
	statuses, err := w.samples.SampleStatuses(t.Context(), w.f.principal.AccountID, []kernel.ULID{id})
	if err != nil || !statuses[id].Sampled || statuses[id].Expired {
		t.Fatalf("completed upload did not recover publication: %+v %v", statuses, err)
	}
	if _, err := w.samples.Read(t.Context(), w.f.principal.AccountID, id); err != nil {
		t.Fatalf("recovered sealed evidence is unreadable: %v", err)
	}
}

func TestAccPurgedInputCannotPublishAfterCompletedExternalUpload(t *testing.T) {
	w := newSampleWorld(t)
	id := w.evaluate(t).Receipts[0].ID
	job := w.claim(t)
	objects, service := w.pausedUpload(t)
	finished := make(chan error, 1)
	go func() { finished <- service.Upload(t.Context(), job) }()
	awaitUpload(t, objects.completed)
	if err := w.samples.PurgeAccount(t.Context(), w.f.principal.AccountID); err != nil {
		t.Fatal(err)
	}
	close(objects.resume)
	if err := <-finished; err != nil {
		t.Fatalf("late put did not settle a removed owner: %v", err)
	}
	if _, err := w.f.store.Input(t.Context(), w.f.principal.AccountID, id, false); !errors.Is(err, sampling.ErrNotFound) {
		t.Fatalf("late upload recreated purged private input: %v", err)
	}
	var live int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM blob_objects WHERE store='policy-inputs' AND state='live'`).Scan(&live); err != nil || live != 0 {
		t.Fatalf("purged upload published a live blob: %d %v", live, err)
	}
}

func TestAccSupersededCaptureUploadCannotReplaceANewerPublishedHead(t *testing.T) {
	w := newSampleWorld(t)
	w.capturePolicy(t, rego.V1)
	w.captureEvaluate(t, "first")
	w.uploadAll(t)
	age := func() {
		t.Helper()
		if _, err := w.f.db.Exec(
			t.Context(),
			`UPDATE policy_input_capture_heads SET reserved_at=statement_timestamp()-interval '2 minutes' WHERE account_id=$1`,
			w.f.principal.AccountID,
		); err != nil {
			t.Fatal(err)
		}
	}
	age()
	w.captureEvaluate(t, "slow-replacement")
	_, pending := w.captureHead(t)
	if pending == nil {
		t.Fatal("replacement missing")
	}
	stale := *pending
	job := w.claim(t)
	objects, service := w.pausedUpload(t)
	finished := make(chan error, 1)
	go func() { finished <- service.Upload(t.Context(), job) }()
	awaitUpload(t, objects.completed)
	age()
	w.captureEvaluate(t, "newer-replacement")
	w.uploadAll(t)
	current, pending := w.captureHead(t)
	if current == nil || *current == stale || pending != nil {
		t.Fatal("newer replacement did not win before stale publication")
	}
	winner := *current
	close(objects.resume)
	if err := <-finished; err != nil {
		t.Fatalf("superseded uploader did not settle: %v", err)
	}
	current, pending = w.captureHead(t)
	if current == nil || *current != winner || pending != nil {
		t.Fatal("stale upload replaced or cleared the newer published head")
	}
	w.inputState(t, stale, "superseded", false)
}
