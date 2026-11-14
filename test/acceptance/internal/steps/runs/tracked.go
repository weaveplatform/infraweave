package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// trackedSteps check what a run a real launcher ran leaves in the
// database: its run_updated events in the outbox, and the state version
// it wrote, against the state its own hook pulled.
type trackedSteps struct {
	w *harness.World
}

func (t *trackedSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the outbox holds the run "(\w+)"'s run_updated events of the states "([^"]*)"$`,
		t.events)
	sc.Step(`^the current state version is the run "(\w+)"'s, with the serial and lineage its `+
		`([A-Z_]+) log names after "`+pulled+`", as token "(\w+)" reads it through ([AB])$`,
		t.current)
}

// events checks the states of the run_updated events the outbox holds of
// the run kept as run, in the order a receiver takes them, against want,
// comma-separated.
func (t *trackedSteps) events(ctx context.Context, run, want string) error {
	var got string
	if err := t.w.Row(ctx, `SELECT coalesce(string_agg(document->>'state', ', '
			ORDER BY sequence, occurred_at), '')
		FROM events WHERE kind = 'run_updated' AND subject_kind = 'run' AND subject_id = $1`,
		[]any{t.w.IDs[run]}, &got); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if got != want {
		return fmt.Errorf("%w: the run_updated events are of %s, want %s", harness.ErrStep, got,
			want)
	}
	return nil
}

// pulled starts the line a hook prints the state with, as
// `echo "state pull: $(terraform state pull | jq -c '{serial,lineage}')"`.
const pulled = "state pull: "

// current checks that the current version of the workspace the run kept
// as run wrote to is one the run wrote, with the serial and lineage of
// the last state its log of state names after pulled.
func (t *trackedSteps) current(ctx context.Context, run, state, token, replica string) error {
	text, err := logText(ctx, t.w, token, run, state, replica)
	if err != nil {
		return err
	}
	var local struct {
		Serial  *int64 `json:"serial"`
		Lineage string `json:"lineage"`
	}
	for line := range strings.Lines(text) {
		if s, ok := strings.CutPrefix(strings.TrimSpace(line), pulled); ok {
			local.Serial = nil
			if err := json.Unmarshal([]byte(s), &local); err != nil {
				return fmt.Errorf("%w: the pulled state %q: %w", harness.ErrStep, s, err)
			}
		}
	}
	if local.Serial == nil || local.Lineage == "" {
		return fmt.Errorf("%w: the %s log names no pulled state: %q", harness.ErrStep, state,
			text)
	}
	id := harness.UUIDOf(t.w.IDs[run])
	var (
		writer, lineage string
		serial          int64
	)
	if err := t.w.Row(ctx, `SELECT v.run_id::text, v.serial, v.lineage
		FROM state_workspaces s JOIN managed_state_versions v
			ON v.account_id = s.account_id AND v.id = s.current_version_id
		WHERE (s.account_id, s.stack_id, s.name) IN (
			SELECT account_id, stack_id, workspace FROM managed_state_versions WHERE run_id = $1)`,
		[]any{id}, &writer, &serial, &lineage); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	if writer != id || serial != *local.Serial || lineage != local.Lineage {
		return fmt.Errorf("%w: the current version is serial %d of %s by %s, the pulled %d of %s",
			harness.ErrStep, serial, lineage, writer, *local.Serial, local.Lineage)
	}
	return nil
}
