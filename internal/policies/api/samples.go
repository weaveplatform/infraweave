package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrSampleMissing conceals absent, foreign, expired and unpublished evidence.
var ErrSampleMissing = errors.New("policy sample not found")

// SampleDenied names the authorization needed to read retained private evidence.
type SampleDenied struct{ Action string }

func (e *SampleDenied) Error() string { return "policy evidence read denied" }

// Sample carries sealed evidence only after its owner has authorized a reader.
type Sample struct {
	Body, Engine, Print string
	Input, Result       json.RawMessage
	Outcome             Outcome
	Error               *Failure
	PrintTruncated      bool
}

// SampleReader resolves the exact policy/evaluation pair before authorizing raw
// input. Runs owns the immutable receipt link and calls this port, never a blob.
type SampleReader interface {
	ReadSample(context.Context, kernel.Principal, kernel.ULID, kernel.ULID) (Sample, error)
	// ReadFailure records a safe classification after the outermost owner
	// transaction has rolled back. A caller adding a run fence owns this call.
	ReadFailure(context.Context, kernel.Principal, error) error
}

// SampleStatus distinguishes a request from a published sample and a lost sample.
// Expired applies only to evidence that was actually retained previously.
type SampleStatus struct{ Sampled, Expired bool }

// SampleRetentions reads account-qualified status without exposing private input.
// Runs owns its immutable links; policies owns publication and expiry.
type SampleRetentions interface {
	SampleStatuses(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]SampleStatus, error)
}
