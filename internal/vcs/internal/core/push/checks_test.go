package push

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

type (
	capturedCap struct {
		account            kernel.AccountID
		integration, stack kernel.ULID
		sha                string
		kind               kernel.RunType
		limit              int
	}
	capReporter struct {
		w     *evaluationWorld
		rows  []capturedCap
		cause error
	}
)

func (r *capReporter) Capped(ctx context.Context, account kernel.AccountID, integration, stack kernel.ULID, sha string, kind kernel.RunType, limit int) error {
	if !tx.In(ctx) || !slices.Contains(r.w.order, "fence") || !slices.Contains(r.w.order, "owner hold") || !slices.Contains(r.w.order, "stack lock") ||
		!slices.Contains(r.w.order, "admit") {
		r.w.t.Fatal("cap feedback escaped its live completion transaction")
	}
	r.rows = append(r.rows, capturedCap{account, integration, stack, sha, kind, limit})
	return r.cause
}

func TestUnitCappedChecksCompleteAtomically(t *testing.T) {
	for _, branch := range []string{"main", "feature"} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failure=%t", branch, fail), func(t *testing.T) {
				w := newEvaluation(t)
				w.used = w.cap
				w.events = []Prepared{w.push(branch, "capped", "main.tf")}
				recorder := &capReporter{w: w}
				w.s.d.Checks = recorder
				cause := errors.New("check enqueue failed")
				if fail {
					recorder.cause = cause
				}
				err := w.s.Evaluated(t.Context(), w.job)
				if fail {
					if !errors.Is(err, cause) || w.complete != 0 {
						t.Fatal("failed feedback committed completion", err, w.complete)
					}
					return
				}
				if err != nil || w.complete != 1 || len(recorder.rows) != 1 || len(w.created) != 0 || len(w.tracked) != 0 {
					t.Fatal("cap did not enqueue without run/tracked projection", recorder.rows, err)
				}
				row := recorder.rows[0]
				kind := kernel.RunTypeProposed
				if branch == "main" {
					kind = kernel.RunTypeTracked
				}
				if row.account != w.input.Account || row.integration != w.meta.Integration || row.stack != w.input.Stack || row.sha != "capped" || row.kind != kind ||
					row.limit != 1 {
					t.Fatal("wrong budget/source/kind", row)
				}
			})
		}
	}
}
