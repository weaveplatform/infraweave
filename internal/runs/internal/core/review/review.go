package review

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

// ErrReview refuses an incomplete review rather than manufacturing metadata.
var ErrReview = errors.New("runs: invalid review")

// Decision is one reviewer's vote, not a resolved policy approval.
type Decision string

const (
	Approve Decision = "APPROVE"
	Reject  Decision = "REJECT"
)

// Record is immutable history. AuthorKey unifies a person's sessions and keys;
// Caller and Session identify the credential that actually made this request.
// Facts contains only the review's metadata, never an entire policy input.
type Record struct {
	Account           kernel.AccountID
	Run, ID           kernel.ULID
	AuthorKey, Caller string
	Session           kernel.ULID
	Decision          Decision
	Note              *string
	State             kernel.RunState
	StateVersion      int64
	CreatedAt         time.Time
	RequestID         string
	Facts             policyapi.ReviewFacts
}

// Store scopes every read by account and joins the review transaction for writes.
type Store interface {
	RecordReview(context.Context, Record) error
	Reviews(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]Record, error)
}

// Valid checks the immutable review envelope before persistence or projection.
func (r Record) Valid() bool {
	missingID := r.Account.IsZero() || r.Run.IsZero() || r.ID.IsZero() || r.Session.IsZero()
	missingActor := r.AuthorKey == "" || r.Caller == "" || r.RequestID == ""
	validDecision := r.Decision == Approve || r.Decision == Reject
	validState := slices.Contains([]kernel.RunState{kernel.RunStateQueued, kernel.RunStateUnconfirmed, kernel.RunStatePendingReview}, r.State)
	validSnapshot := r.Facts.Author != "" && r.Facts.State == r.State && r.Facts.StateVersion == r.StateVersion
	if missingID || missingActor || !validDecision || !validState || !validSnapshot || r.CreatedAt.IsZero() || r.StateVersion < 1 {
		return false
	}
	if r.Note == nil {
		return !r.Facts.Note.Known && r.Facts.Note.Value == nil
	}
	return r.Facts.Note.Known && r.Facts.Note.Value != nil && *r.Facts.Note.Value == *r.Note
}

// Effective selects the newest vote per author and state version, then retains
// older states separately. A targeted replan's new version needs a new vote.
func Effective(records []Record, state kernel.RunState, version int64) (policyapi.ReviewsFacts, error) {
	out := policyapi.ReviewsFacts{
		Current: policyapi.ReviewGroupFacts{Approvals: []policyapi.ReviewFacts{}, Rejections: []policyapi.ReviewFacts{}},
		Older:   policyapi.ReviewGroupFacts{Approvals: []policyapi.ReviewFacts{}, Rejections: []policyapi.ReviewFacts{}},
	}
	type authorState struct {
		author  string
		version int64
	}
	latest := map[authorState]Record{}
	for _, r := range records {
		if r.ID.IsZero() || r.AuthorKey == "" || r.StateVersion < 1 || r.CreatedAt.IsZero() || r.Decision != Approve && r.Decision != Reject ||
			r.Facts.State != r.State || r.Facts.StateVersion != r.StateVersion {
			return out, ErrReview
		}
		key := authorState{r.AuthorKey, r.StateVersion}
		old, ok := latest[key]
		if !ok || compare(old, r) < 0 {
			latest[key] = r
		}
	}
	ordered := make([]Record, 0, len(latest))
	for _, r := range latest {
		ordered = append(ordered, r)
	}
	slices.SortFunc(ordered, compare)
	for _, r := range ordered {
		group := &out.Older
		if r.State == state && r.StateVersion == version {
			group = &out.Current
		}
		if r.Decision == Approve {
			group.Approvals = append(group.Approvals, r.Facts)
		} else {
			group.Rejections = append(group.Rejections, r.Facts)
		}
	}
	return out, nil
}

func compare(a, b Record) int {
	if at := a.CreatedAt.Compare(b.CreatedAt); at != 0 {
		return at
	}
	return bytes.Compare(a.ID[:], b.ID[:])
}
