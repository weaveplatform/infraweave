package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// workspaceSteps are the Tofu Workspaces operations' answers and what
// their releases record.
//
// ref: decision 0010 § "Storage"
type workspaceSteps struct{ w *harness.World }

func (s *workspaceSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's Tofu workspaces are "([^"]*)"$`, s.are)
	sc.Step(`^the answer's Tofu workspace "([^"]+)" names "(\w+)"$`, s.names)
	sc.Step(
		`^the audit trail holds (\d+) releases? of the Tofu workspace "(\w+)" by the (\w+) key$`,
		s.audited,
	)
}

// tofuWorkspace is a workspace as the API answers it.
type tofuWorkspace struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	CreatedAt int64   `json:"createdAt"`
	IsLocked  bool    `json:"isLocked"`
	LockedBy  *string `json:"lockedBy"`
}

// workspaces are the workspaces the last answer holds: a search's edges'
// nodes, a stack's release's unlockedWorkspaces, or one workspace. Each
// must carry a ULID, a creation time and a lockedBy exactly while it is
// locked.
func (s *workspaceSteps) workspaces() ([]tofuWorkspace, error) {
	r := s.w.Last()
	var body struct {
		Edges []struct {
			Node tofuWorkspace `json:"node"`
		} `json:"edges"`
		Unlocked []tofuWorkspace `json:"unlockedWorkspaces"`
		tofuWorkspace
	}
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &body) != nil {
		return nil, fmt.Errorf("%w: no workspaces: %d %s", harness.ErrStep, r.Status, r.Body)
	}
	out := body.Unlocked
	for _, e := range body.Edges {
		out = append(out, e.Node)
	}
	if body.Name != "" {
		out = append(out, body.tofuWorkspace)
	}
	for _, w := range out {
		if _, err := kernel.ParseULID(w.ID); err != nil || w.CreatedAt <= 0 ||
			w.IsLocked != (w.LockedBy != nil) {
			return nil, fmt.Errorf("%w: a malformed workspace %+v: %s", harness.ErrStep, w, r.Body)
		}
	}
	return out, nil
}

// lockedByKey is a "locked by the <tenant> key" of a step's workspace.
var lockedByKey = regexp.MustCompile(`locked by the (\w+) key`)

// are checks the last answer's workspaces, in order, against want,
// comma-separated: each its name, then "locked by the <tenant> key" while
// that tenant's API key triggered the run holding it, whose lockedBy
// answers the key's ID.
func (s *workspaceSteps) are(want string) error {
	ws, err := s.workspaces()
	if err != nil {
		return err
	}
	got := []string{}
	for _, w := range ws {
		entry := w.Name
		if w.LockedBy != nil {
			entry += " locked by " + *w.LockedBy
		}
		got = append(got, entry)
	}
	wanted := s.w.SlugList(lockedByKey.ReplaceAllStringFunc(want, func(m string) string {
		return "locked by " + harness.Tenants[lockedByKey.FindStringSubmatch(m)[1]].Key
	}))
	if !slices.Equal(got, wanted) {
		return fmt.Errorf("%w: the workspaces are %q, want %q", harness.ErrStep, got, wanted)
	}
	return nil
}

// names keeps the ID of the last answer's workspace named name as id.
func (s *workspaceSteps) names(name, id string) error {
	ws, err := s.workspaces()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(ws, func(w tofuWorkspace) bool { return w.Name == name })
	if i < 0 {
		return fmt.Errorf("%w: no workspace %q in %+v", harness.ErrStep, name, ws)
	}
	s.w.IDs[id] = ws[i].ID
	return nil
}

// audited waits until the home account's audit trail holds n releases of
// the workspace kept as ws, each by the tenant's API key, from the
// caller's address, and naming at least one released LockInfo.
func (s *workspaceSteps) audited(ctx context.Context, n int, ws, tenant string) error {
	id, ok := s.w.IDs[ws]
	if !ok {
		return fmt.Errorf("%w: no workspace %q", harness.ErrStep, ws)
	}
	var got int
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, fmt.Sprintf("%d releases of the workspace %s", n, id),
		func(ctx context.Context) (bool, error) {
			err := s.w.Row(ctx, `SELECT count(*) FROM audit_trail_entries
				WHERE account_id = $1 AND action = 'stack.state_lock_break'
				AND actor_username = $2 AND affected_metadata::jsonb->>'workspaceId' = $3
				AND jsonb_array_length(affected_metadata::jsonb->'released') > 0
				AND coalesce(remote_ip, '') <> ''`,
				[]any{harness.UUIDOf(harness.Home.Account), harness.Tenants[tenant].Key, id}, &got)
			return got == n, err
		})
}
