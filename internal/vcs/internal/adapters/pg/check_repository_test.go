package pg_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/test/deps"
)

func TestAccCommitCheckFollowsNativeRepositoryAndStopsAfterRemoval(t *testing.T) {
	w := newCheckWorld(t)
	ctx := t.Context()
	native := lifecycle.Repository{Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}, ObservedAt: w.now}
	if err := w.f.store.PutRepositoryIdentity(ctx, w.f.acct, w.b.Integration, native); err != nil {
		t.Fatal(err)
	}
	var err error
	w.b, err = w.f.store.ResolveCheckRepository(ctx, w.b)
	if err != nil || w.b.RepositoryID != native.ID {
		t.Fatal("selection did not retain known identity", w.b, err)
	}
	w.enqueue(t, w.b, 1)
	if err := w.fake.RenameProject("acme", "infra", "new-owner", "renamed"); err != nil {
		t.Fatal(err)
	}
	native.Namespace, native.Name = "new-owner", "renamed"
	if err := w.f.store.PutRepositoryIdentity(ctx, w.f.acct, w.b.Integration, native); err != nil {
		t.Fatal(err)
	}
	w.report(t)
	if writes := w.fake.CheckWrites("new-owner", "renamed"); len(writes) != 1 || writes[0].SHA != w.b.Commit {
		t.Fatal("queued check used a stale repository path", writes)
	}
	if err := w.f.store.DropRepositoryIdentity(ctx, w.f.acct, w.b.Integration, native.ID); err != nil {
		t.Fatal(err)
	}
	// A new repository occupying either retained path must not inherit the
	// old repository's check identity or receive its late state transitions.
	native.ID = "replacement"
	if err := w.f.store.PutRepositoryIdentity(ctx, w.f.acct, w.b.Integration, native); err != nil {
		t.Fatal(err)
	}
	w.state(w.b.Run, kernel.RunStateFinished, 3)
	w.enqueue(t, w.b, 3)
	w.report(t)
	if w.checks.calls != 1 {
		t.Fatal("removed identity fell back to an occupied path", w.checks.calls)
	}
	unknown := w.b
	unknown.RepositoryID = ""
	unknown.Namespace, unknown.Repository = "historical", "unknown"
	if got, err := w.f.store.ResolveCheckRepository(ctx, unknown); err != nil || got != unknown {
		t.Fatal("unknown historical mapping lost its explicit fallback", got, err)
	}
	foreign := w.b
	foreign.Account = kernel.NewAccountID()
	if _, err := w.f.store.ResolveCheckRepository(ctx, foreign); !errors.Is(err, feedback.ErrNoCheck) {
		t.Fatal("native identity escaped account scope", err)
	}
}

func TestAccCommitCheckRepositoryHoldFencesRename(t *testing.T) {
	w := newCheckWorld(t)
	ctx := t.Context()
	native := lifecycle.Repository{Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}, ObservedAt: w.now}
	if err := w.f.store.PutRepositoryIdentity(ctx, w.f.acct, w.b.Integration, native); err != nil {
		t.Fatal(err)
	}
	w.b.RepositoryID = native.ID
	connection, err := w.f.db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	held, release, owner := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		owner <- tx.Do(ctx, connection, tx.Options{}, func(ctx context.Context) error {
			if _, err := w.f.store.ResolveCheckRepository(ctx, w.b); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	changed := make(chan error, 1)
	go func() {
		next := native
		next.Name = "renamed"
		changed <- w.f.store.PutRepositoryIdentity(ctx, w.f.acct, w.b.Integration, next)
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	blocked := deps.Eventually(waitCtx, "check's current native path fences rename until its effect commits", func(ctx context.Context) (bool, error) {
		var waits bool
		err := w.f.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, connection.Conn().PgConn().PID()).Scan(&waits)
		return waits, err
	})
	close(release)
	if err := <-owner; err != nil {
		t.Fatal(err)
	}
	if err := <-changed; err != nil {
		t.Fatal(err)
	}
	if blocked != nil {
		t.Fatal(blocked)
	}
}

func TestAccCommitCheckPRsUseCurrentIdentityAndCurrentHeads(t *testing.T) {
	w := newCheckWorld(t)
	ctx := t.Context()
	const id = "7bf80420-965b-4f23-90c0-ec114b064264"
	r := storedRequest(w.f, w.b.Integration)
	r.RepositoryID, r.HeadSHA = "{7BF80420-965B-4F23-90C0-EC114B064264}", w.b.Commit
	upsertRequest(t, w.f, r)
	r.Number = "18"
	r.ID = kernel.NewULID()
	upsertRequest(t, w.f, r)
	r.Number, r.ID, r.HeadSHA = "19", kernel.NewULID(), requestHash("other-head")
	upsertRequest(t, w.f, r)
	r.Number, r.ID, r.HeadSHA, r.RepositoryID = "20", kernel.NewULID(), w.b.Commit, "different-native-repo"
	upsertRequest(t, w.f, r)
	native := lifecycle.Repository{Repository: vcsapi.Repository{ID: id, Namespace: "new-owner", Name: "renamed"}, ObservedAt: w.now}
	if err := w.f.store.PutRepositoryIdentity(ctx, w.f.acct, w.b.Integration, native); err != nil {
		t.Fatal(err)
	}
	b := w.b
	b.Namespace, b.Repository, b.RepositoryID = native.Namespace, native.Name, id
	got, err := w.f.store.CheckPullRequests(ctx, b)
	if err != nil || !slices.Equal(got, []int{17, 18}) {
		t.Fatal("current-head read lost canonical native identity or shared heads", got, err)
	}
	b.Namespace, b.Repository = "acme", "infra"
	if got, err := w.f.store.CheckPullRequests(ctx, b); err != nil || len(got) != 0 {
		t.Fatal("retained PR coordinates bypassed the current map", got, err)
	}
	b.Namespace, b.Repository = native.Namespace, native.Name
	b.Account = kernel.NewAccountID()
	if got, err := w.f.store.CheckPullRequests(ctx, b); err != nil || len(got) != 0 {
		t.Fatal("PR heads escaped account scope", got, err)
	}
}

func TestAccCommitCheckCapFactsSurviveRepositoryRename(t *testing.T) {
	w := newCheckWorld(t)
	ctx := t.Context()
	native := lifecycle.Repository{Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}, ObservedAt: w.now}
	if err := w.f.store.PutRepositoryIdentity(ctx, w.f.acct, w.b.Integration, native); err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(ctx, w.f.db, tx.Options{}, func(ctx context.Context) error {
		return w.service.Capped(ctx, w.b.Account, w.b.Integration, w.b.Stack, w.b.Commit, w.b.Type, 2)
	}); err != nil {
		t.Fatal(err)
	}
	b := w.b
	b.RepositoryID, b.Namespace, b.Repository = native.ID, "new-owner", "renamed"
	caps, err := w.f.store.NoRunChecks(ctx, b, b.Type)
	if err != nil || len(caps) != 1 || caps[0].Limit != 2 || caps[0].RepositoryID != native.ID {
		t.Fatal("renamed aggregate lost its original cap outcome", caps, err)
	}
	b.RepositoryID = "other-repository"
	if caps, err := w.f.store.NoRunChecks(ctx, b, b.Type); err != nil || len(caps) != 0 {
		t.Fatal("cap facts followed a reused path instead of native identity", caps, err)
	}
}
