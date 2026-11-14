package handoff

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// Handoff is what the launcher hands the runner: the worker API it
// reports to, the run with its run token and the state its preparation
// leads to, the run's specification with its secrets opened, and the
// launcher's settings the runner applies.
//
// ref: decision 0008 § "The launcher and the runner"
type Handoff struct {
	// Endpoint is the worker API's base URL, as in https://host/worker/v1.
	Endpoint string `json:"endpoint"`
	Run      string `json:"run"`
	// RunToken is the bearer token of the run's calls.
	RunToken string `json:"runToken"`
	// Assigned is the state the claim moved the run to, and StateVersion
	// the run's state version after it.
	Assigned     wire.AssignmentState `json:"assigned"`
	StateVersion int64                `json:"stateVersion"`
	Spec         wire.RunSpec         `json:"spec"`
	Secrets      wire.RunSpecSecrets  `json:"secrets"`
	// StateLockTimeout is launcher.state_lock_timeout: how long each
	// command that locks the state waits for the lock.
	StateLockTimeout time.Duration `json:"stateLockTimeout"`
	// MaskEnvs is launcher.mask_envs: the names of the run's variables
	// whose values the runner masks in its log.
	MaskEnvs []string `json:"maskEnvs,omitempty"`
	// PoolKey is the pool's public key, an uncompressed P-256 point, to
	// which the runner seals a saved workspace's content key.
	PoolKey []byte `json:"poolKey,omitempty"`
}

// MaxSize bounds a handoff the runner reads. A specification holds a
// stack's variables and mounted files, which the configuration domain
// bounds well below it.
const MaxSize = 64 << 20

// ErrHandoff marks a handoff the runner cannot read.
var ErrHandoff = errors.New("not a runner handoff")

// Write writes h to w as one JSON document.
func Write(w io.Writer, h Handoff) error {
	if err := json.NewEncoder(w).Encode(h); err != nil {
		return fmt.Errorf("write the runner handoff: %w", err)
	}
	return nil
}

// Read reads the handoff Write wrote to r, at most MaxSize bytes of it.
func Read(r io.Reader) (Handoff, error) {
	var h Handoff
	dec := json.NewDecoder(io.LimitReader(r, MaxSize))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return Handoff{}, fmt.Errorf("%w: %w", ErrHandoff, err)
	}
	if h.Endpoint == "" || h.Run == "" || h.RunToken == "" || h.First() == "" {
		return Handoff{}, fmt.Errorf("%w: endpoint, run, runToken and assigned are required",
			ErrHandoff)
	}
	return h, nil
}

// First is the state the runner's first started report names, the one
// the run's preparation leads to: INITIALIZING after PREPARING, PLANNING
// after PREPARING_REPLAN and APPLYING after PREPARING_APPLY; empty for
// any other state.
func (h Handoff) First() wire.WorkerState {
	switch h.Assigned {
	case wire.AssignmentStatePREPARING:
		return wire.WorkerStateINITIALIZING
	case wire.AssignmentStatePREPARINGREPLAN:
		return wire.WorkerStatePLANNING
	case wire.AssignmentStatePREPARINGAPPLY:
		return wire.WorkerStateAPPLYING
	}
	return ""
}
