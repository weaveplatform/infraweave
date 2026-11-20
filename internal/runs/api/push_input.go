package api

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PushRunFacts contains the currently persisted facts of an in-progress run.
// Version and Flags support internal fencing; the published GIT_PUSH input
// retains its own narrower contract. Workspace paths, runtime configuration,
// environment values and decision bodies never cross this read projection.
type PushRunFacts struct {
	ID                    kernel.ULID     `json:"id"`
	Stack                 kernel.ULID     `json:"stack"`
	Type                  kernel.RunType  `json:"type"`
	State                 kernel.RunState `json:"state"`
	Version               int64           `json:"version"`
	Commit                *Commit         `json:"commit"`
	TriggeredBy           *string         `json:"triggeredBy"`
	Branch                string          `json:"branch"`
	BasedOnLocalWorkspace bool            `json:"basedOnLocalWorkspace"`
	DriftDetection        bool            `json:"driftDetection"`
	ForceApplyMode        string          `json:"forceApplyMode"`
	Command               *string         `json:"command"`
	Flags                 []string        `json:"flags"`
	CreatedAt             time.Time       `json:"createdAt"`
	UpdatedAt             time.Time       `json:"updatedAt"`
}

// PushRunReader reads every account-owned stack run in QUEUED, READY or
// UNCONFIRMED, in creation-time/ID order, without a hidden limit. Reads join
// the caller's transaction and expose its own uncommitted receipt/flag writes.
type PushRunReader interface {
	PushInProgress(context.Context, kernel.AccountID, kernel.ULID) ([]PushRunFacts, error)
}
