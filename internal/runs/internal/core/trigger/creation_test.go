package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
)

type creationDB struct{}

func (creationDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return creationTx{}, nil }

type creationTx struct{ pgx.Tx }

func (creationTx) Commit(context.Context) error   { return nil }
func (creationTx) Rollback(context.Context) error { return nil }

func TestUnitInternalCreationValidatesPolicyEvidenceAndPriority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*runsapi.CreateRequest)
		valid  bool
	}{
		{"default priority", func(*runsapi.CreateRequest) {}, true},
		{"high priority", func(r *runsapi.CreateRequest) { r.Priority = kernel.RunPriorityHigh }, true},
		{"invalid priority", func(r *runsapi.CreateRequest) { r.Priority = "urgent" }, false},
		{"policy caller", func(r *runsapi.CreateRequest) { r.PolicyLockCaller = "policy:deploy" }, true},
		{"human caller", func(r *runsapi.CreateRequest) { r.PolicyLockCaller = "user:owner" }, false},
		{"empty policy caller", func(r *runsapi.CreateRequest) { r.PolicyLockCaller = "policy:" }, false},
		{"missing receipt ID", func(r *runsapi.CreateRequest) { r.Receipts[0].ID = kernel.ULID{} }, false},
		{"missing policy ID", func(r *runsapi.CreateRequest) { r.Receipts[0].Policy = kernel.ULID{} }, false},
		{"missing policy name", func(r *runsapi.CreateRequest) { r.Receipts[0].PolicyName = "" }, false},
		{"missing version", func(r *runsapi.CreateRequest) { r.Receipts[0].PolicyVersion = 0 }, false},
		{"missing time", func(r *runsapi.CreateRequest) { r.Receipts[0].CreatedAt = time.Time{} }, false},
		{"wrong policy type", func(r *runsapi.CreateRequest) { r.Receipts[0].Type = "APPROVAL" }, false},
		{"wrong outcome", func(r *runsapi.CreateRequest) { r.Receipts[0].Outcome = "ALLOW" }, false},
		{"wrong engine", func(r *runsapi.CreateRequest) { r.Receipts[0].Engine = "REGO_V2" }, false},
		{"v0 engine", func(r *runsapi.CreateRequest) { r.Receipts[0].Engine = "REGO_V0" }, true},
		{"malformed decision", func(r *runsapi.CreateRequest) { r.Receipts[0].Decision = json.RawMessage(`{"bad"`) }, false},
		{"failure code missing", func(r *runsapi.CreateRequest) { r.Receipts[0].Failure = &runsapi.PolicyFailure{Message: "safe"} }, false},
		{"failure message missing", func(r *runsapi.CreateRequest) {
			r.Receipts[0].Failure = &runsapi.PolicyFailure{Code: "evaluation-failed"}
		}, false},
		{"safe failure pair", func(r *runsapi.CreateRequest) {
			r.Receipts[0].Failure = &runsapi.PolicyFailure{Code: "evaluation-failed", Message: "safe"}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := runsapi.CreateRequest{
				Account: kernel.NewAccountID(), Stack: kernel.NewULID(), Type: kernel.RunTypeProposed,
				Commit: runsapi.Commit{Hash: "head"}, TriggeredBy: "vcs/commit", Mode: runsapi.OncePerCommit,
				Receipts: []runsapi.PolicyReceipt{{
					ID: kernel.NewULID(), Policy: kernel.NewULID(), PolicyName: "Push guard", PolicyVersion: 1,
					Type: "GIT_PUSH", Engine: "REGO_V1", Outcome: "PUSH", CreatedAt: time.Now(), Decision: json.RawMessage(`{"propose":true}`),
				}},
			}
			tc.change(&request)
			s := New(Deps{})
			if _, err := s.Create(t.Context(), request); !errors.Is(err, runsapi.ErrNoTransaction) {
				t.Fatalf("unfenced creation error=%v, want no transaction", err)
			}
			err := tx.Do(t.Context(), creationDB{}, tx.Options{}, func(ctx context.Context) error {
				_, err := s.Create(ctx, request)
				return err
			})
			want := runsapi.ErrCreationInput
			if tc.valid {
				// Valid input reaches the uninstalled owner port without using
				// a fake lifecycle, SQL result or successful run creation.
				want = kernel.ErrUnavailable
			}
			if !errors.Is(err, want) {
				t.Fatalf("creation error=%v, want %v", err, want)
			}
		})
	}
}
