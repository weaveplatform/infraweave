package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// stateFile is the file in the cache directory where a launcher keeps
// what a restart resumes.
const stateFile = "worker.json"

// state is what a restarted launcher resumes: the worker it registered
// for the pool, the run it executes or whose container's end it has not
// yet reported, and that end once the container is gone.
type state struct {
	Pool   string              `json:"pool"`
	Worker string              `json:"worker"`
	Run    string              `json:"run,omitempty"`
	Exit   *wire.ContainerExit `json:"exit,omitempty"`
}

// load reads dir's state; the zero state when there is none or it cannot
// be read, which registers a new worker.
func load(dir string) state {
	b, err := os.ReadFile(filepath.Join(dir, stateFile))
	var s state
	if err != nil || json.Unmarshal(b, &s) != nil {
		return state{}
	}
	return s
}

// save writes s to dir through a rename, so a crash leaves the old state
// or the new one, never part of either.
func save(dir string, s state) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("launcher: the cache directory: %w", err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("launcher: save the worker: %w", err)
	}
	tmp := filepath.Join(dir, stateFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("launcher: save the worker: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, stateFile)); err != nil {
		return fmt.Errorf("launcher: save the worker: %w", err)
	}
	return nil
}

// forget removes dir's state, after the worker deregistered.
func forget(dir string) error {
	err := os.Remove(filepath.Join(dir, stateFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("launcher: forget the worker: %w", err)
	}
	return nil
}
