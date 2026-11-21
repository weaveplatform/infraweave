package api

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrLoginPolicyEditor refuses a stale or no-longer-authorized editor at the
// security commit boundary, even if earlier route authorization was cached.
var ErrLoginPolicyEditor = errors.New("login policy editor is no longer authorized")

// LoginPolicyChanges joins the policy owner's mutation transaction. HoldAccount
// precedes policy advisory/row/space locks; LoginPolicyChanged runs only after
// an accepted LOGIN write and synchronously invalidates active peer sessions.
type LoginPolicyChanges interface {
	HoldAccount(context.Context, kernel.AccountID) error
	LoginPolicyChanged(context.Context, kernel.Principal) error
}
