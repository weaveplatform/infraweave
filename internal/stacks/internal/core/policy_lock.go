package core

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

var _ stacksapi.PolicyLocks = (*Service)(nil)

// UpdatePushLock applies unlock then lock under the stack's current owner row.
// Human caller keys are a disjoint namespace and cannot be guessed by a policy.
func (s *Service) UpdatePushLock(ctx context.Context, account kernel.AccountID, id kernel.ULID, lock, unlock string) error {
	if !tx.In(ctx) {
		return stacksapi.ErrPolicyLockTransaction
	}
	if account.IsZero() || id.IsZero() {
		return stacksapi.ErrPolicyLockInput
	}
	st, err := s.store.Read(ctx, account, id, true)
	if err != nil {
		return err //nolint:wrapcheck // the owned store names the read
	}
	changed := false
	if unlock != "" && st.Lock != nil && st.Lock.Holder == stacksapi.PolicyLockPrefix+unlock {
		st.Lock = nil
		changed = true
	}
	if lock != "" {
		holder := stacksapi.PolicyLockPrefix + lock
		if st.Lock != nil && st.Lock.Holder != holder {
			return stacksapi.ErrPolicyLockConflict
		}
		if st.Lock == nil {
			st.Lock = &stacksapi.Lock{Holder: holder, By: lock, Note: "locked by push policy", At: s.now()}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	st.Version++
	if err := s.store.Write(ctx, st); err != nil {
		return err //nolint:wrapcheck // the owned store names the write
	}
	what := stacksapi.ChangeUnlocked
	if st.Lock != nil {
		what = stacksapi.ChangeLocked
	}
	return s.changed(ctx, st, what, false)
}
