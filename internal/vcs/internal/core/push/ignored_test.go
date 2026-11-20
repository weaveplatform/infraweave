package push

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

type (
	capturedIgnore struct {
		account kernel.AccountID
		stack   kernel.ULID
		commit  stacksapi.Commit
		reason  string
	}
	ignoredRecorder struct {
		w     *evaluationWorld
		rows  []capturedIgnore
		cause error
	}
)

func (r *ignoredRecorder) Record(ctx context.Context, a kernel.AccountID, s kernel.ULID, c stacksapi.Commit, reason string) error {
	if !tx.In(ctx) || !slices.Contains(r.w.order, "fence") || !slices.Contains(r.w.order, "owner hold") || !slices.Contains(r.w.order, "stack lock") {
		r.w.t.Fatal("ignored row escaped live claim/owner/stack holds")
	}
	r.rows = append(r.rows, capturedIgnore{a, s, c, reason})
	return r.cause
}

func TestUnitIgnoredRecordingScope(t *testing.T) {
	for _, tc := range []struct {
		name, kind, branch, outcome, reason string
		want                                bool
	}{
		{"tracked outside", "push", "main", Ignored, NoAffectedFiles, true},
		{"tag", "tag", "", Ignored, TagsIgnored, true},
		{"tracked capped", "push", "main", Capped, RunLimitReached, true},
		{"other branch capped", "push", "feature", Capped, RunLimitReached, false},
		{"other branch outside", "push", "feature", Ignored, NoAffectedFiles, false},
		{"request", "pull_request", "main", Ignored, NoAffectedFiles, false},
		{"comment", "comment", "main", Ignored, NoAffectedFiles, false},
		{"duplicate", "push", "main", Duplicate, NoAffectedFiles, false},
		{"refused", "push", "main", Refused, NoAffectedFiles, false},
		{"policy", "push", "main", Ignored, CustomPolicy, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := recordIgnored(vcsapi.Event{Type: tc.kind, Branch: tc.branch}, stacksapi.Settings{Branch: "main"}, Decision{tc.outcome, tc.reason})
			if got != tc.want {
				t.Fatal("recording scope changed", got)
			}
		})
	}
}

func TestUnitIgnoredRecordingPrecedesCompletion(t *testing.T) {
	for _, fault := range []string{"none", "recorder", "unbound"} {
		t.Run(fault, func(t *testing.T) {
			w := newEvaluation(t)
			root := "root"
			w.stack.ProjectRoot = &root
			w.events = []Prepared{w.push("main", "first", "outside.txt")}
			recorder := &ignoredRecorder{w: w}
			cause := errors.New("record refused")
			w.s.d.Ignored = recorder
			if fault == "recorder" {
				recorder.cause = cause
			}
			if fault == "unbound" {
				w.s.d.Ignored = nil
				cause = kernel.ErrUnavailable
			}
			err := w.s.Evaluated(t.Context(), w.job)
			if fault == "none" {
				if err != nil || w.complete != 1 || len(recorder.rows) != 1 || recorder.rows[0].reason != NoAffectedFiles || recorder.rows[0].account != w.input.Account ||
					recorder.rows[0].stack != w.input.Stack ||
					recorder.rows[0].commit.Hash != "first" {
					t.Fatal("ignored decision not stored in completing claim", recorder.rows, err)
				}
				return
			}
			if !errors.Is(err, cause) || w.complete != 0 {
				t.Fatal("failed recording completed/acknowledged work", err, w.complete)
			}
		})
	}
}
