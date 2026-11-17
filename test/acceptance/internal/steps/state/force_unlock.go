package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/agent/backend"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// forceUnlockSteps arrange a stopped server's locks and check what breaking
// them records, independently of the tool's exit code.
type forceUnlockSteps struct {
	w       *harness.World
	backend *backendSteps
	locks   []string
	infos   []map[string]string
	holder  string
	stack   kernel.ULID
}

func (s *forceUnlockSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^(\d+) stopped-server locks? of the run "(\w+)" are seeded on its default workspace$`, s.seed)
	sc.Step(`^the seeded locks (remain held|are recorded as broken)$`, s.locksAre)
	sc.Step(`^the run "(\w+)" sends (an empty-body UNLOCK|its own UNLOCK|a write) through ([AB])$`, s.request)
	sc.Step(`^the former holder "(\w+)" is arranged as a writing caller$`, s.writing)
	sc.Step(`^the task "(\w+)" records (one|no) release of exactly the seeded LockInfo$`, s.audited)
	sc.Step(`^the workspace has no broken-lock records$`, s.noneBroken)
}

// seed models a server stopped before terminal-event cleanup: no live run
// leaves a lock behind after it ends. Shared locks let a task be scheduled
// beside their proposed holder, and multiple locks prove an empty UNLOCK
// releases every holder rather than only the command's named ID.
//
// ref: decision 0016 § "Determinism"
func (s *forceUnlockSteps) seed(ctx context.Context, n int, run string) error {
	tr, err := s.backend.run(run)
	if err != nil {
		return err
	}
	s.holder = tr.id
	if err := s.w.Row(ctx, `SELECT stack_id::text FROM runs WHERE id = $1`,
		[]any{harness.UUIDOf(tr.id)}, &s.stack); err != nil {
		return err
	}
	for i := range n {
		id := fmt.Sprintf("11111111-1111-4111-8111-%012d", i+1)
		info := map[string]string{"ID": id, "Operation": "OperationTypePlan", "Who": "runner@infraweave", "Info": "stopped-server lock"}
		raw, err := json.Marshal(info)
		if err != nil {
			return fmt.Errorf("lock info: %w", err)
		}
		var rows int
		if err := s.w.Row(ctx, `WITH added AS (
   INSERT INTO state_locks (account_id, stack_id, workspace, lock_id, mode, run_id, triggered_by, info)
   SELECT account_id, stack_id, '', $2, 'shared', id, triggered_by, $3::json
   FROM runs WHERE id = $1 RETURNING 1)
   SELECT count(*) FROM added`, []any{harness.UUIDOf(tr.id), id, string(raw)}, &rows); err != nil {
			return err
		}
		if rows != 1 {
			return fmt.Errorf("%w: seeded %d locks for %s", harness.ErrStep, rows, run)
		}
		s.locks = append(s.locks, id)
		s.infos = append(s.infos, info)
	}
	s.w.IDs["lock"] = s.locks[0]
	return nil
}

// locksAre checks the exact IDs and holder, so a denial cannot silently
// remove a lock and a successful break cannot record only some of them.
func (s *forceUnlockSteps) locksAre(ctx context.Context, which string) error {
	table := "state_locks"
	if which == "are recorded as broken" {
		table = "state_broken_locks"
	}
	var got string
	if err := s.w.Row(ctx, `SELECT coalesce(string_agg(lock_id || ':' || run_id::text, ',' ORDER BY lock_id), '')
  FROM `+table+` WHERE account_id = $1 AND stack_id = $2 AND workspace = ''`,
		[]any{harness.UUIDOf(harness.Home.Account), s.stack}, &got); err != nil {
		return err
	}
	want := make([]string, len(s.locks))
	for i, id := range s.locks {
		want[i] = id + ":" + harness.UUIDOf(s.holder)
	}
	slices.Sort(want)
	if got != strings.Join(want, ",") {
		return fmt.Errorf("%w: %s holds %q, want %q", harness.ErrStep, table, got, want)
	}
	return nil
}

// writing gives the proposed holder a writable phase without applying:
// shared planning locks let the task coexist with its holder, while a
// writing caller reaches the broken-lock checks rather than a read-only
// caller's authorization refusal.
func (s *forceUnlockSteps) writing(ctx context.Context, run string) error {
	var rows int
	if err := s.w.Row(ctx, `WITH changed AS (UPDATE runs SET state = 'APPLYING' WHERE id = $1 RETURNING 1)
  SELECT count(*) FROM changed`, []any{harness.UUIDOf(s.w.IDs[run])}, &rows); err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w: arranged %d former holders", harness.ErrStep, rows)
	}
	return nil
}

// request preserves the difference between Terraform's empty force-unlock
// and a holder's LockInfo, using the run token the real worker claimed.
func (s *forceUnlockSteps) request(ctx context.Context, run, what, replica string) error {
	tr, err := s.backend.run(run)
	if err != nil {
		return err
	}
	method, body := stateapi.MethodUnlock, ""
	path := tr.path
	switch what {
	case "its own UNLOCK":
		body = `{"ID":"` + s.locks[0] + `"}`
	case "a write":
		method, body = http.MethodPost, `{"version":4,"serial":1,"lineage":"broken-holder"}`
		path += "?ID=" + url.QueryEscape(s.locks[0])
	}
	_, err = s.backend.stateBody(ctx, method, path, backend.Username, s.w.Tokens[tr.token], body, replica)
	return err
}

// audited checks one release event, its run caller and workspace, and every
// LockInfo's complete content. Ordinary initialization releases create no
// such event, and a no-op force-unlock must create none either.
func (s *forceUnlockSteps) audited(ctx context.Context, task, which string) error {
	var got int
	want := 0
	if which == "one" {
		want = 1
	}
	id := s.w.IDs[task]
	query := `SELECT count(*) FROM audit_trail_entries WHERE account_id = $1
  AND action = 'stack.state_lock_break' AND actor_run = $2`
	args := []any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(id)}
	if err := deps.Eventually(ctx, "the task's force-unlock audit entry", func(ctx context.Context) (bool, error) {
		err := s.w.Row(ctx, query, args, &got)
		return got == want, err
	}); err != nil {
		return err
	} //nolint:wrapcheck // names the awaited entry
	if want == 0 {
		return nil
	}
	var meta string
	if err := s.w.Row(ctx, `SELECT affected_metadata::text FROM audit_trail_entries
  WHERE account_id = $1 AND action = 'stack.state_lock_break' AND actor_run = $2
  AND actor_username = 'infraweave' AND affected_type = 'STACK' AND affected_id = $3
  AND related_type = 'RUN' AND related_id = $4 AND coalesce(remote_ip, '') <> ''`,
		[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(id), s.stack.String(), id}, &meta); err != nil {
		return err
	}
	var entry struct {
		Workspace string              `json:"workspace"`
		Released  []map[string]string `json:"released"`
	}
	if json.Unmarshal([]byte(meta), &entry) != nil || entry.Workspace != "" || !reflect.DeepEqual(entry.Released, s.infos) {
		return fmt.Errorf("%w: force-unlock metadata %s, want workspace default and LockInfo %v", harness.ErrStep, meta, s.infos)
	}
	return nil
}

func (s *forceUnlockSteps) noneBroken(ctx context.Context) error {
	var n int
	if err := s.w.Row(ctx, `SELECT count(*) FROM state_broken_locks WHERE account_id = $1`,
		[]any{harness.UUIDOf(harness.Home.Account)}, &n); err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("%w: %d broken locks after no release", harness.ErrStep, n)
	}
	return nil
}
