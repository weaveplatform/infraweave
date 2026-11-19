package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// Keep enough recent transitions to distinguish preparation, execution and a
// terminal failure without unbounded diagnostics or any provider error text.
const labHistoryLimit = 16

type labRunProgress struct {
	Request   string             `json:"request"`
	State     kernel.RunState    `json:"state"`
	History   []labRunTransition `json:"history"`
	Truncated bool               `json:"historyTruncated"`
}

type labRunTransition struct {
	State    kernel.RunState `json:"state"`
	Terminal bool            `json:"terminal"`
	Logs     bool            `json:"logs"`
	Note     bool            `json:"note"`
}

// waitLabRun retains only safe facts from the existing polls. State/history
// describe the last readable response; Request describes the latest attempt.
// A condition error must be sanitized before Eventually can format its last
// error, and the returned error must never retain a raw response or cause.
func waitLabRun(ctx context.Context, what, want string, poll func(context.Context) (harness.Response, error)) error {
	progress := labRunProgress{Request: "unobserved", State: kernel.RunStateUnknown}
	err := deps.Eventually(ctx, what, func(ctx context.Context) (bool, error) {
		response, err := poll(ctx)
		if err != nil {
			progress.Request = "transport"
			if errors.Is(err, deps.ErrPermanent) {
				return false, fmt.Errorf("%w: lab run request failed", deps.ErrPermanent)
			}
			return false, fmt.Errorf("%w: lab run request failed", harness.ErrStep)
		}
		progress.Request = labRunStatus(response.Status)
		if response.Status != http.StatusOK {
			return false, fmt.Errorf("%w: lab run request status=%s", harness.ErrStep, progress.Request)
		}
		var run struct {
			State   kernel.RunState `json:"state"`
			History json.RawMessage `json:"history"`
		}
		if json.Unmarshal(response.Body, &run) != nil {
			progress.Request = "invalid_json"
			return false, fmt.Errorf("%w: lab run response is not readable", harness.ErrStep)
		}
		if string(run.State) == want {
			return true, nil
		}
		progress.State = labRunState(run.State)
		progress.History = nil
		progress.Truncated = false
		var history []struct {
			State    kernel.RunState `json:"state"`
			Terminal bool            `json:"terminal"`
			HasLogs  bool            `json:"hasLogs"`
			Note     *string         `json:"note"`
		}
		if len(run.History) > 0 && json.Unmarshal(run.History, &history) != nil {
			progress.Request = "invalid_history"
			return false, nil
		}
		progress.Truncated = len(history) > labHistoryLimit
		for _, h := range history[max(0, len(history)-labHistoryLimit):] {
			progress.History = append(progress.History, labRunTransition{
				State: labRunState(h.State), Terminal: h.Terminal, Logs: h.HasLogs, Note: h.Note != nil,
			})
		}
		return false, nil
	})
	if err == nil {
		return nil
	}
	summary, encodeErr := json.Marshal(progress)
	if encodeErr != nil {
		return fmt.Errorf("%w; lab progress unavailable", err)
	}
	return fmt.Errorf("%w; lab progress=%s", err, summary)
}

func labRunState(state kernel.RunState) kernel.RunState {
	if !state.Valid() {
		return kernel.RunStateUnknown
	}
	return state
}

func labRunStatus(status int) string {
	switch status {
	case http.StatusOK:
		return "ok"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusTooManyRequests:
		return "throttled"
	default:
		if status >= 500 && status <= 599 {
			return "server_error"
		}
		return "other_status"
	}
}
