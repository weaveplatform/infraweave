package pg_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

// snapshotOutage terminates an actual PostgreSQL backend at the new public
// read transaction boundary. The writer/observer uses a separate connection.
type snapshotOutage struct {
	db         *pgxpool.Pool
	connection *pgx.Conn
	stage      string
	terminated bool
	cause      error
}

func (s *snapshotOutage) terminate(ctx context.Context, connection *pgx.Conn) error {
	return s.db.QueryRow(ctx, `SELECT pg_terminate_backend($1::integer,5000)`,
		int64(connection.PgConn().PID())).Scan(&s.terminated)
}

func (s *snapshotOutage) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if s.stage == "begin" {
		if err := s.terminate(ctx, s.connection); err != nil {
			return nil, err
		}
		transaction, err := s.connection.BeginTx(ctx, options)
		s.cause = err
		return transaction, err
	}
	transaction, err := s.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return snapshotOutageCommit{Tx: transaction, outage: s}, nil
}

type snapshotOutageCommit struct {
	pgx.Tx
	outage *snapshotOutage
}

func (s snapshotOutageCommit) Commit(ctx context.Context) error {
	if err := s.outage.terminate(ctx, s.Conn()); err != nil {
		return err
	}
	err := s.Tx.Commit(ctx)
	s.outage.cause = err
	return err
}

func TestAccPublicRunSnapshotOutageKeepsRetryableHTTPContract(t *testing.T) {
	for _, route := range []string{"run", "stack run", "stack runs"} {
		for _, stage := range []string{"begin", "commit"} {
			t.Run(route+" at "+stage, func(t *testing.T) {
				w := newWorld(t)
				run := mustCreate(t, w, kernel.RunTypeTracked, "outage")
				outage := &snapshotOutage{db: w.db, stage: stage}
				if stage == "begin" {
					connection, err := pgx.ConnectConfig(t.Context(), w.db.Config().ConnConfig.Copy())
					if err != nil {
						t.Fatal(err)
					}
					outage.connection = connection
					t.Cleanup(func() { _ = connection.Close(context.WithoutCancel(t.Context())) })
				}
				access := w.access()
				reader := read.New(read.Deps{DB: outage, Store: w.store, Stacks: w.stacks, Spaces: access, Authz: access})
				var err error
				switch route {
				case "run":
					_, err = reader.Run(t.Context(), w.alice(), run.ID.String())
				case "stack run":
					_, err = reader.StackRun(t.Context(), w.alice(), "network", run.ID.String())
				case "stack runs":
					_, err = reader.StackRuns(t.Context(), w.alice(), "network", read.ListRuns, read.Query{Limit: 1})
				}
				if !outage.terminated || outage.cause == nil {
					t.Fatalf("real backend termination did not reach %s: terminated=%v cause=%v", stage, outage.terminated, outage.cause)
				}
				response := httptest.NewRecorder()
				problem.Failure(response, err)
				if !errors.Is(err, outage.cause) || !errors.Is(err, kernel.ErrUnavailable) ||
					response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "2" {
					t.Fatalf("%s outage: HTTP %d Retry-After=%q unavailable=%v cause retained=%v error=%v",
						stage, response.Code, response.Header().Get("Retry-After"), errors.Is(err, kernel.ErrUnavailable), errors.Is(err, outage.cause), err)
				}
			})
		}
	}
}

func TestAccPublicRunSnapshotPreservesOtherErrors(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "other-errors")
	access := w.access()
	access.err["Actions"] = errInjected
	reader := w.reader(access)
	_, err := reader.Run(t.Context(), w.alice(), run.ID.String())
	response := httptest.NewRecorder()
	problem.Failure(response, err)
	if !errors.Is(err, errInjected) || errors.Is(err, kernel.ErrUnavailable) || response.Code != http.StatusInternalServerError {
		t.Fatalf("non-outage failure changed: HTTP %d error=%v", response.Code, err)
	}
	_, err = reader.Run(t.Context(), w.alice(), kernel.NewULID().String())
	if !errors.Is(err, read.ErrNotFound) || errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("missing run changed to an outage: %v", err)
	}
}
