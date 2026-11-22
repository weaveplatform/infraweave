package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// SampleStatus distinguishes a request from a published sample and a lost sample.
// Expired applies only to evidence that was actually retained previously.
type SampleStatus struct{ Sampled, Expired bool }

// SampleRetentions reads account-qualified status without exposing private input.
// Runs owns its immutable links; policies owns publication and expiry.
type SampleRetentions interface {
	SampleStatuses(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]SampleStatus, error)
}
