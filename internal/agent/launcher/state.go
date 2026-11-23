package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// stateFile is the file in the cache directory where a launcher keeps
// what a restart resumes.
const stateFile = "worker.json"

// State is what a restarted launcher resumes: the worker it registered
// for the pool; the run it executes or whose container's end it has not
// yet reported, with the assignment's state, state version and preparing
// deadline; when that run's container started, from which its run
// timeout counts; its end once the container is gone; and Kill, the kill
// a signal asked for, which holds until the run's end is reported. It
// holds no token or secret.
type State struct {
	Pool              string               `json:"pool"`
	Worker            string               `json:"worker"`
	Run               string               `json:"run,omitempty"`
	Assigned          wire.AssignmentState `json:"assigned,omitempty"`
	StateVersion      int64                `json:"stateVersion,omitempty"`
	PreparingDeadline time.Time            `json:"preparingDeadline,omitzero"`
	Started           time.Time            `json:"started,omitzero"`
	Exit              *wire.ContainerExit  `json:"exit,omitempty"`
	Kill              bool                 `json:"kill,omitempty"`
}

// reset forgets s's run, once its end is reported or the server no longer
// has it.
func (s *State) reset() {
	*s = State{Pool: s.Pool, Worker: s.Worker}
}

// Store keeps a launcher's State, which it saves before each effect a
// restart must know of: before it prepares a run, before it kills one,
// and once a run's container started or ended. A launcher hosted by a
// worker controller keeps it in its Worker resource, where a replica
// that takes over the slot finds it.
type Store interface {
	// Load answers the saved state, the zero State when there is none.
	Load(ctx context.Context) (State, error)
	// Save replaces the saved state with s.
	Save(ctx context.Context, s State) error
	// Forget removes the saved state, once the worker deregistered.
	Forget(ctx context.Context) error
}

// FileStore keeps the state in worker.json of a directory, the
// launcher's cache directory.
type FileStore string

var _ Store = FileStore("")

// Load reads the state; the zero state when there is none or it cannot be
// read, which registers a new worker.
func (dir FileStore) Load(context.Context) (State, error) {
	b, err := os.ReadFile(filepath.Join(string(dir), stateFile))
	var s State
	if err != nil || json.Unmarshal(b, &s) != nil {
		return State{}, nil
	}
	return s, nil
}

// Save writes s through a rename, so a crash leaves the old state or the
// new one, never part of either.
func (dir FileStore) Save(_ context.Context, s State) error {
	if err := os.MkdirAll(string(dir), 0o700); err != nil {
		return fmt.Errorf("launcher: the cache directory: %w", err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("launcher: save the worker: %w", err)
	}
	tmp := filepath.Join(string(dir), stateFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("launcher: save the worker: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(string(dir), stateFile)); err != nil {
		return fmt.Errorf("launcher: save the worker: %w", err)
	}
	return nil
}

// Forget removes the state file.
func (dir FileStore) Forget(context.Context) error {
	err := os.Remove(filepath.Join(string(dir), stateFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("launcher: forget the worker: %w", err)
	}
	return nil
}
