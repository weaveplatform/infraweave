package pg_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/review"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
)

type reviewMetadataSource func(context.Context, kernel.Principal, kernel.ULID) (accessapi.ReviewMetadata, error)

func (f reviewMetadataSource) ReviewMetadata(ctx context.Context, p kernel.Principal, space kernel.ULID) (accessapi.ReviewMetadata, error) {
	return f(ctx, p, space)
}

var errReviewWrite = errors.New("review write unavailable")

type failedReviewWrite struct{ review.Store }

func (s failedReviewWrite) RecordReview(ctx context.Context, r review.Record) error {
	if err := s.Store.RecordReview(ctx, r); err != nil {
		return err
	}
	return errReviewWrite
}

// This boundary test controls access-owned metadata and faults while using the
// actual controller, transaction, PostgreSQL history, lifecycle and event store.
func TestAccReviewControlPersistsPersonMetadataAndRollsBackFailures(t *testing.T) {
	w := newWorld(t)
	run := w.unconfirmed(t, "person-review")
	p := w.alice()
	p.MemberID, p.SessionID = kernel.NewMemberID(), kernel.NewULID()
	p.Name, p.Admin, p.Teams = "Alice", true, []string{"reviewers"}
	provider := kernel.NewULID()
	metadata := accessapi.ReviewMetadata{
		AuthorKey: "member:" + p.MemberID.String(), Session: p, CanReview: true,
		Roles:     []accessapi.ReviewRole{{ID: kernel.NewULID(), Slug: "reviewer", Name: "Original reviewer"}},
		Identity:  &accessapi.ReviewIdentity{Member: p.MemberID, Federations: []accessapi.ReviewFederation{{Provider: provider, Subject: "original-subject"}}},
		CreatorIP: new("203.0.113.7"), IDPSubject: new("original-subject"),
	}
	a := w.access()
	a.held = holding(accessapi.ActionRunReview)
	var metadataFailure error
	reader := reviewMetadataSource(func(context.Context, kernel.Principal, kernel.ULID) (accessapi.ReviewMetadata, error) {
		return metadata, metadataFailure
	})
	deps := control.Deps{
		DB: w.db, Store: w.store, Stacks: w.stacks, Authz: a, Lifecycle: w.lc, Views: w.reader(a),
		Now: func() time.Time { return w.now }, Reviews: w.store, ReviewMetadata: reader,
		Scheduler: schedule.New(schedule.Deps{DB: w.db, Store: w.store, Stacks: w.stacks, Lifecycle: w.lc, Queue: w.jobs}),
	}
	call := func(d control.Deps) (review.Record, error) {
		var result review.Record
		var err error
		handler := middleware.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			result, err = control.New(d).Review(r.Context(), p, "network", run.ID.String(), review.Approve, new("reviewed"), &run.Version)
		}))
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/review", nil))
		return result, err
	}
	first, err := call(deps)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Roles[0].Name = "Renamed reviewer"
	metadata.Identity.Federations[0].Subject = "changed-subject"
	stored, err := w.store.Reviews(t.Context(), w.acct, []kernel.ULID{run.ID})
	if err != nil || len(stored[run.ID]) != 1 {
		t.Fatalf("missing recorded person review: %v", err)
	}
	saved := stored[run.ID][0]
	facts := saved.Facts
	if saved.ID != first.ID || saved.AuthorKey != "member:"+p.MemberID.String() || saved.RequestID == "" ||
		facts.AuthorRoles.Value == nil || (*facts.AuthorRoles.Value)[0].Name != "Original reviewer" ||
		facts.Identity.Value == nil || facts.Identity.Value.ULID.Value == nil || *facts.Identity.Value.ULID.Value != p.MemberID.String() ||
		facts.Identity.Value.Federations.Value == nil || (*facts.Identity.Value.Federations.Value)[provider.String()].ID.Value == nil ||
		*(*facts.Identity.Value.Federations.Value)[provider.String()].ID.Value != "original-subject" ||
		facts.Session.Value == nil || facts.Session.Value.CreatorIP.Value == nil || *facts.Session.Value.CreatorIP.Value != "203.0.113.7" ||
		facts.Session.Value.IDPSubject.Value == nil || *facts.Session.Value.IDPSubject.Value != "original-subject" {
		t.Fatal("review lost original person, federation, role or session metadata")
	}
	updates := len(w.updates(t, run.ID))
	for _, phase := range []string{"revoked", "metadata", "authorization", "after-insert", "missing-metadata"} {
		t.Run(phase, func(t *testing.T) {
			metadata.CanReview, metadataFailure = true, nil
			a.err["Authorize"] = nil
			d, want := deps, errReviewWrite
			switch phase {
			case "revoked":
				metadata.CanReview, want = false, control.ErrBlocking
			case "metadata":
				metadataFailure = errReviewWrite
			case "authorization":
				a.err["Authorize"] = errReviewWrite
			case "after-insert":
				d.Reviews = failedReviewWrite{Store: w.store}
			case "missing-metadata":
				d.ReviewMetadata, want = nil, kernel.ErrUnavailable
			}
			if _, err := call(d); !errors.Is(err, want) {
				t.Fatalf("review failure %s: %v", phase, err)
			}
			rows, err := w.store.Reviews(t.Context(), w.acct, []kernel.ULID{run.ID})
			if err != nil || len(rows[run.ID]) != 1 || rows[run.ID][0].ID != first.ID || len(w.updates(t, run.ID)) != updates {
				t.Fatalf("failed review committed history or events: %v", err)
			}
		})
	}
}
