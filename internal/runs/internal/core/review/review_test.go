package review

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

func TestUnitEffectiveReviewUsesLatestVotePerPersonAndVersion(t *testing.T) {
	at := time.Date(2026, 11, 20, 6, 0, 0, 0, time.UTC)
	row := func(author string, state kernel.RunState, version int64, decision Decision, offset time.Duration) Record {
		return Record{
			ID: kernel.NewULID(), AuthorKey: author, Decision: decision, State: state, StateVersion: version, CreatedAt: at.Add(offset),
			Facts: policyapi.ReviewFacts{Author: author, State: state, StateVersion: version},
		}
	}
	old := row("member:alice", kernel.RunStateQueued, 1, Approve, 0)
	earlier := row("member:alice", kernel.RunStateUnconfirmed, 6, Approve, time.Second)
	change := row("member:alice", kernel.RunStateUnconfirmed, 6, Reject, 2*time.Second)
	bob := row("member:bob", kernel.RunStateUnconfirmed, 6, Approve, 3*time.Second)
	previous := row("member:alice", kernel.RunStateUnconfirmed, 3, Approve, -time.Second)
	records := []Record{bob, old, change, previous, earlier}
	input := append([]Record(nil), records...)
	got, err := Effective(records, kernel.RunStateUnconfirmed, 6)
	if err != nil || len(got.Current.Approvals) != 1 || got.Current.Approvals[0].Author != "member:bob" || len(got.Current.Rejections) != 1 ||
		got.Current.Rejections[0].Author != "member:alice" ||
		len(got.Older.Approvals) != 2 ||
		len(got.Older.Rejections) != 0 {
		t.Fatalf("latest state/version-scoped votes changed: %+v", got)
	}
	if !reflect.DeepEqual(records, input) {
		t.Fatal("selection mutated immutable review history")
	}
	// A later repeat of the original approve is a new decision, not reuse of an
	// older matching row that would leave the rejection effective.
	again := row("member:alice", kernel.RunStateUnconfirmed, 6, Approve, 4*time.Second)
	got, err = Effective(append(records, again), kernel.RunStateUnconfirmed, 6)
	if err != nil || len(got.Current.Approvals) != 2 || len(got.Current.Rejections) != 0 {
		t.Fatalf("returning to approval kept an older rejection: %+v", got)
	}
	empty, err := Effective(nil, kernel.RunStateQueued, 1)
	if err != nil || empty.Current.Approvals == nil || empty.Current.Rejections == nil || empty.Older.Approvals == nil || empty.Older.Rejections == nil {
		t.Fatalf("known empty reviews became null: %+v", empty)
	}
}

func TestUnitEffectiveReviewUsesIDToBreakEqualTimeAndDeduplicatesInput(t *testing.T) {
	at := time.Date(2026, 11, 20, 7, 0, 0, 0, time.UTC)
	first := Record{
		ID:           kernel.NewULID(),
		AuthorKey:    "member:alice",
		Decision:     Approve,
		State:        kernel.RunStateQueued,
		StateVersion: 1,
		CreatedAt:    at,
		Facts:        policyapi.ReviewFacts{Author: "alice", State: kernel.RunStateQueued, StateVersion: 1},
	}
	last := first
	last.ID = kernel.NewULID()
	last.Decision = Reject
	got, err := Effective([]Record{last, first, last}, kernel.RunStateQueued, 1)
	if err != nil || len(got.Current.Approvals) != 0 || len(got.Current.Rejections) != 1 {
		t.Fatalf("equal-time/replayed votes counted incorrectly: %+v %v", got, err)
	}
	invalid := last
	invalid.Decision = "UNKNOWN"
	if _, err := Effective([]Record{first, invalid}, kernel.RunStateQueued, 1); !errors.Is(err, ErrReview) {
		t.Fatalf("invalid decision accepted: %v", err)
	}
}
