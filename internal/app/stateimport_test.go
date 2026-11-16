package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
)

// importWorld is the claim world with a state domain of its own over an
// fs bucket, bound into the stacks domain's creation, and a member of the
// account who holds no role.
type importWorld struct {
	*claimWorld
	s        stateServer
	stranger context.Context
}

func newImportWorld(t *testing.T) importWorld {
	t.Helper()
	w := newClaimWorld(t)
	s := newStateServer(t, w, objectstore.DriverFS)
	w.d.Stacks.BindManagedState(s.state.ManagedState())
	return importWorld{claimWorld: w, s: s, stranger: kernel.WithPrincipal(t.Context(),
		kernel.Principal{
			Kind: kernel.PrincipalUser, ActorID: kernel.NewULID(), AccountID: w.acct,
			MemberID: kernel.NewMemberID(),
		})}
}

// upload uploads body as the caller of ctx through a signed upload URL,
// answering its object ID.
func (w importWorld) upload(t *testing.T, ctx context.Context, body string) string {
	t.Helper()
	res, err := w.s.state.API().StateUploadUrl(ctx, api.StateUploadUrlRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	u := res.(api.StateUploadUrl200JSONResponse).Body
	//nolint:contextcheck // a signed URL's request carries no principal, only its token
	if rec := signedCall(t, w.s.h, http.MethodPut, u.Url, []byte(body),
		int64(len(body))); rec.Code != http.StatusOK {
		t.Fatalf("an upload = %d %s", rec.Code, rec.Body)
	}
	return u.ObjectId
}

// uploadState is the state of the upload of object, "" once it is gone.
func (w importWorld) uploadState(t *testing.T, object string) string {
	t.Helper()
	var state string
	err := w.q.QueryRow(t.Context(), `SELECT state FROM blob_objects
		WHERE store = 'uploads' AND starts_with(key, $1)`,
		w.acct.String()+"/state/"+object+"/").Scan(&state)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	return state
}

// create creates the stack name, in the space kept, whose state
// infraweave keeps when manage is set, from object when it is not empty.
func (w importWorld) create(ctx context.Context, name string, manage bool, object string) error {
	space := "kept"
	body := &api.StackCreateRequest{
		Input:       api.StackInput{Name: name, Repository: "infra", Branch: "main", Space: &space},
		ManageState: manage,
	}
	if object != "" {
		body.StackObjectID = &object
	}
	_, err := w.d.Stacks.API().StackCreate(ctx, api.StackCreateRequestObject{Body: body})
	return err //nolint:wrapcheck // the test reads it as it is
}

// importInto imports object into the stack slug as the caller of ctx.
func (w importWorld) importInto(ctx context.Context, slug, object string) error {
	_, err := w.s.state.API().StackManagedStateImport(ctx, api.StackManagedStateImportRequestObject{
		StackId: slug, Body: &api.StackManagedStateImportJSONRequestBody{State: object},
	})
	return err //nolint:wrapcheck // as above
}

// stackID is the ID of the account's stack slug, zero without one.
func (w importWorld) stackID(t *testing.T, slug string) kernel.ULID {
	t.Helper()
	var id kernel.ULID
	err := w.q.QueryRow(t.Context(), `SELECT id FROM stacks WHERE account_id = $1 AND slug = $2`,
		w.acct, slug).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	return id
}

// latest is the current version of stack's default workspace.
func (w importWorld) latest(t *testing.T, stack kernel.ULID) stateapi.Version {
	t.Helper()
	v, ok, err := w.s.state.Versions().Latest(t.Context(), w.acct, stack, stateapi.DefaultWorkspace)
	if err != nil || !ok {
		t.Fatalf("the current version of %s: %t, %v", stack, ok, err)
	}
	return v
}

// invalidAt reports whether err is a *kernel.Invalid whose first field
// is pointer, refused for reason.
func invalidAt(err error, pointer, reason string) bool {
	inv, ok := errors.AsType[*kernel.Invalid](err)
	return ok && len(inv.Errors) > 0 && inv.Errors[0].Pointer == pointer &&
		inv.Errors[0].Reason == reason
}

// TestAccStateImportAtCreation checks a stack's creation from an
// uploaded state file as the server wires the access, stacks, runs and
// state domains over PostgreSQL and an fs bucket: the stack and its
// default workspace's first version, resealed into states, marked
// imported by the caller with no run, commit together, the upload goes
// and the trail records the import; a second use of the upload, another
// principal's, a stack without managed state and a file that is no state
// each answer 422 at /stackObjectID and create no stack, leaving the
// upload as it was.
//
// ref: decision 0010 § "Upload, import and download URLs"
func TestAccStateImportAtCreation(t *testing.T) {
	w := newImportWorld(t)
	ctx := t.Context()
	secret := "s3cr3t-" + kernel.NewULID().String()
	obj := w.upload(t, w.admin, secretState(7, secret))
	if err := w.create(w.admin, "imported", true, obj); err != nil {
		t.Fatalf("a creation with an upload = %v", err)
	}
	id := w.stackID(t, "imported")
	v := w.latest(t, id)
	p, _ := kernel.PrincipalFrom(w.admin)
	if !v.Imported || v.ImportedUsername == nil || *v.ImportedUsername != p.ActorID.String() ||
		v.ImportedAt == nil || v.Run != nil || v.Serial == nil || *v.Serial != 7 ||
		v.ClientEncrypted {
		t.Errorf("the imported version = %+v", v)
	}
	var key string
	if err := w.q.QueryRow(ctx, `SELECT object_key FROM managed_state_versions
		WHERE account_id = $1 AND id = $2`, w.acct, v.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	r, err := w.s.store.Get(ctx, objectstore.States, key)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(r)
	r.Close()
	if len(stored) == 0 || bytes.Contains(stored, []byte(secret)) {
		t.Errorf("the imported version's object holds %d bytes, its plaintext among them",
			len(stored))
	}
	if st := w.uploadState(t, obj); st != "deleting" && st != "" {
		t.Errorf("the upload is %s after its import", st)
	}
	var audits int
	if err := w.q.QueryRow(ctx, `SELECT count(*) FROM audit_trail_entries
		WHERE account_id = $1 AND action = 'stack.state_import' AND affected_id = $2`,
		w.acct, id.String()).Scan(&audits); err != nil || audits != 1 {
		t.Errorf("%d audit entries of the import, %v", audits, err)
	}

	theirs := w.upload(t, w.stranger, secretState(1, secret))
	plain := w.upload(t, w.admin, secretState(1, secret))
	broken := w.upload(t, w.admin, `{"version":3,"serial":1,"lineage":"l"}`)
	for _, tc := range []struct {
		name, slug, object, reason string
		manage                     bool
		kept                       string
	}{
		{"an upload imported already", "again", obj, "not-found", true, ""},
		{"another principal's upload", "theirs", theirs, "not-found", true, "live"},
		{"a stack without managed state", "plain", plain, "rule", false, "live"},
		{"a file that is no state", "broken", broken, "rule", true, "live"},
	} {
		if err := w.create(w.admin, tc.slug, tc.manage, tc.object); !invalidAt(err,
			"/stackObjectID", tc.reason) {
			t.Errorf("%s = %v", tc.name, err)
		}
		if id := w.stackID(t, tc.slug); !id.IsZero() {
			t.Errorf("%s created stack %s", tc.name, id)
		}
		if st := w.uploadState(t, tc.object); tc.kept != "" && st != tc.kept {
			t.Errorf("%s left the upload %s", tc.name, st)
		}
	}
}

// TestAccStateImport checks an import into a stack after its creation:
// it needs the stack's managed state, the caller's lock on it and no
// tracked run, task or destroy run under way, each a 409 naming what
// blocks it, while a proposed run does not count; it adds an imported
// version even of the bytes the current version holds; and a refused
// upload answers 422 at /state.
//
// ref: decision 0010 § "Upload, import and download URLs"
func TestAccStateImport(t *testing.T) {
	w := newImportWorld(t)
	one := w.stacks["one"]
	file := secretState(9, "s3cr3t")
	obj := w.upload(t, w.admin, file)
	if err := w.importInto(w.admin, "one", obj); status(err) != http.StatusConflict ||
		!strings.Contains(err.Error(), "not managed") {
		t.Errorf("an import into a stack without managed state = %v", err)
	}
	w.exec(t, `UPDATE stacks SET manages_state = true WHERE id = $1`, one)
	if err := w.importInto(w.admin, "one", obj); status(err) != http.StatusConflict ||
		!strings.Contains(err.Error(), "not locked") {
		t.Errorf("an import into an unlocked stack = %v", err)
	}
	w.exec(t, `UPDATE stacks SET lock_holder = 'user:x', locked_by = 'bob', locked_at = now()
		WHERE id = $1`, one)
	if err := w.importInto(w.admin, "one", obj); status(err) != http.StatusConflict ||
		!strings.Contains(err.Error(), "locked by bob") {
		t.Errorf("an import into a stack another holds = %v", err)
	}
	w.exec(t, `UPDATE stacks SET lock_holder = NULL, locked_by = NULL, locked_at = NULL
		WHERE id = $1`, one)
	if _, err := w.d.Stacks.API().StackLock(w.admin, api.StackLockRequestObject{
		StackId: "one",
	}); err != nil {
		t.Fatal(err)
	}
	pool := w.pool(t, w.acct, nil)
	for _, typ := range []kernel.RunType{
		kernel.RunTypeTracked, kernel.RunTypeTask, kernel.RunTypeDestroy,
	} {
		run := w.run(t, w.acct, pool, runSpec{typ, kernel.RunPriorityNormal, false}, time.Now())
		if err := w.importInto(w.admin, "one", obj); status(err) != http.StatusConflict ||
			!strings.Contains(err.Error(), run.String()) {
			t.Errorf("an import beside a %s run = %v", typ, err)
		}
		w.exec(t, `UPDATE runs SET state = 'FINISHED' WHERE id = $1`, run)
	}
	w.run(t, w.acct, pool, runSpec{kernel.RunTypeProposed, kernel.RunPriorityNormal, false},
		time.Now())
	if err := w.importInto(w.admin, "one", obj); err != nil {
		t.Fatalf("an import into an idle stack = %v", err)
	}
	first := w.latest(t, one)
	again := w.upload(t, w.admin, file)
	if err := w.importInto(w.admin, "one", again); err != nil {
		t.Fatalf("a second import of the same file = %v", err)
	}
	if v := w.latest(t, one); v.ID == first.ID || !v.Imported ||
		!bytes.Equal(v.SHA256, first.SHA256) {
		t.Errorf("the second import = %+v after %+v", v, first)
	}
	for name, object := range map[string]string{
		"an upload imported already": again,
		"another principal's upload": w.upload(t, w.stranger, file),
		"a file that is no state":    w.upload(t, w.admin, `{"serial":1}`),
	} {
		if err := w.importInto(w.admin, "one", object); !invalidAt(err, "/state", "not-found") &&
			!invalidAt(err, "/state", "rule") {
			t.Errorf("%s = %v", name, err)
		}
	}
}

// TestAccStateImportHoldsRuns checks that an import holds its stack's
// row from its checks to its commit, so a tracked run triggered while it
// runs is created only once it has committed: with the import held
// inside its transaction after its checks, on the upload's row, the
// trigger waits on the stack's row, and the import, released, succeeds,
// having seen no run.
//
// ref: decision 0010 § "Upload, import and download URLs"
func TestAccStateImportHoldsRuns(t *testing.T) {
	w := newImportWorld(t)
	ctx := t.Context()
	one := w.stacks["one"]
	pool := w.pool(t, w.acct, nil)
	w.exec(t, `UPDATE stacks SET manages_state = true, worker_pool_id = $1,
		tracked_commit = '{"hash":"abc","message":"m","authorName":"a","timestamp":"2026-01-01T00:00:00Z"}'
		WHERE id = $2`, pool, one)
	if _, err := w.d.Stacks.API().StackLock(w.admin, api.StackLockRequestObject{
		StackId: "one",
	}); err != nil {
		t.Fatal(err)
	}
	obj := w.upload(t, w.admin, secretState(3, "s3cr3t"))

	// A transaction of the test's locks the upload's row, which the
	// import locks after the stack's.
	held, err := w.q.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := held.Exec(ctx, `SELECT 1 FROM blob_objects
		WHERE store = 'uploads' AND starts_with(key, $1) FOR UPDATE`,
		w.acct.String()+"/state/"+obj+"/"); err != nil {
		t.Fatal(err)
	}
	imported := make(chan error, 1)
	go func() { imported <- w.importInto(w.admin, "one", obj) }()
	awaitWaiting(ctx, t, w.q, 1)
	tracked := api.RunTypeTRACKED
	triggered := make(chan error, 1)
	go func() {
		_, err := w.d.Runs.API().RunTrigger(w.admin, api.RunTriggerRequestObject{
			StackId: "one", Body: &api.RunTriggerRequest{RunType: &tracked},
		})
		triggered <- err
	}()
	awaitWaiting(ctx, t, w.q, 2)
	var runs int
	if err := held.QueryRow(ctx, `SELECT count(*) FROM runs WHERE stack_id = $1`,
		one).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("%d runs while the import is held, %v", runs, err)
	}
	if err := held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-imported; err != nil {
		t.Errorf("the held import = %v", err)
	}
	if err := <-triggered; err != nil {
		t.Errorf("the trigger that waited for the import = %v", err)
	}
	if v := w.latest(t, one); !v.Imported {
		t.Errorf("the current version after the import = %+v", v)
	}
	if err := w.q.QueryRow(ctx, `SELECT count(*) FROM runs WHERE stack_id = $1 AND type = 'TRACKED'`,
		one).Scan(&runs); err != nil || runs != 1 {
		t.Errorf("%d tracked runs after the import, %v", runs, err)
	}
}

// awaitWaiting returns once n sessions of the database wait on a lock.
func awaitWaiting(ctx context.Context, t *testing.T, q tx.Querier, n int) {
	t.Helper()
	for ctx.Err() == nil {
		var waiting int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			return
		}
	}
	t.Fatalf("fewer than %d sessions waited on a lock", n)
}
