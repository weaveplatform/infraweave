package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
)

// vcsSteps read the commits a stack tracks, against the commits the
// Gitea steps kept, which the scenario's requests name as {key}.
type vcsSteps struct{ o *orgSteps }

func (v *vcsSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^the answer tracks the commit kept as "(\w+)" at the branch head kept as "(\w+)"$`,
		v.tracks,
	)
	sc.Step(`^the answer's tracked commit was (not )?set by infraweave$`, v.setBy)
	sc.Step(`^the answer's nextCursor is kept as "(\w+)"$`, v.next)
	sc.Step(`^the answer finds the stacks "([^"]*)" using it$`, v.users)
	sc.Step(
		`^token "(\w+)" eventually reads the stack "([a-z0-9-]+)" tracking the commit kept as "(\w+)" through ([AB])$`,
		v.eventually,
	)
	sc.Step(
		`^an? ([A-Z_]+) integration "([^"]+)" in the space "([^"]+)"( as the default)? is kept as "(\w+)"$`,
		v.integration,
	)
}

// integration writes an integration of the home account as the vcs
// domain stores one, its credential and webhook secret sealed, since no
// operation creates one yet, and keeps its ID as key.
func (v *vcsSteps) integration(
	ctx context.Context,
	provider, name, space, isDefault, key string,
) error {
	keys, pool, err := v.o.a.s.keyring(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	acct, id := uuidOf(tenants["home"].account), kernel.NewULID()
	sealed := make([][]byte, 2)
	for i, col := range []string{"credentials_sealed", "webhook_secret_sealed"} {
		c := crypt.Column{Table: "vcs_integrations", Name: col, Key: []string{"account_id", "id"}}
		if sealed[i], err = keys.SealColumn(ctx, c, []string{acct, id.UUID()},
			[]byte(col+" of "+key)); err != nil {
			return fmt.Errorf("seal the %s of %s: %w", col, key, err)
		}
	}
	tag, err := pool.Exec(ctx, `INSERT INTO vcs_integrations (account_id, id, provider, slug,
			name, space_id, description, labels, is_default, credentials_sealed,
			webhook_secret_sealed)
		SELECT $1, $2, $3, $4, $5, sp.id, $6, '{infra}', $7, $8, $9 FROM spaces sp
		WHERE sp.account_id = $1 AND sp.slug = $10`,
		acct, id.UUID(), provider, kernel.SlugFrom(name).String(), name, name+" of the team",
		isDefault != "", sealed[0], sealed[1], v.o.resolve(space))
	if err != nil {
		return fmt.Errorf("write integration %s: %w", key, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: no space %q for integration %s", errStep, space, key)
	}
	v.o.ids[key] = id.String()
	return nil
}

// next keeps the last answer's nextCursor, a page's, as key.
func (v *vcsSteps) next(key string) error {
	var page struct {
		Next string `json:"nextCursor"`
	}
	r := v.o.a.s.last()
	if json.Unmarshal(r.body, &page) != nil || page.Next == "" {
		return fmt.Errorf("%w: the answer has no nextCursor: %d %s", errStep, r.status, r.body)
	}
	v.o.ids[key] = page.Next
	return nil
}

// users checks the last answer, a search of an integration's users,
// against want: their stacks' slugs, comma-separated, in its order.
func (v *vcsSteps) users(want string) error {
	var page struct {
		Edges []struct {
			Node struct {
				StackID string `json:"stackId"`
			} `json:"node"`
		} `json:"edges"`
	}
	r := v.o.a.s.last()
	if json.Unmarshal(r.body, &page) != nil {
		return fmt.Errorf("%w: the answer is no search: %d %s", errStep, r.status, r.body)
	}
	got := make([]string, 0, len(page.Edges))
	for _, e := range page.Edges {
		got = append(got, e.Node.StackID)
	}
	if strings.Join(got, ", ") != want {
		return fmt.Errorf("%w: the stacks using it are %q, not %q", errStep, got, want)
	}
	return nil
}

// tracked is what a stack's answer says it tracks.
type tracked struct {
	Commit *struct {
		Hash string `json:"hash"`
	} `json:"trackedCommit"`
	Head *struct {
		Hash string `json:"hash"`
	} `json:"trackedBranchHead"`
	SetBy string `json:"trackedCommitSetBy"`
}

func (v *vcsSteps) answer() (tracked, error) {
	var t tracked
	r := v.o.a.s.last()
	if err := json.Unmarshal(r.body, &t); err != nil {
		return t, fmt.Errorf("%w: no stack in %d %s", errStep, r.status, r.body)
	}
	return t, nil
}

// check reports whether t tracks the commit kept as commit, and the head
// kept as head when it is not empty.
func (v *vcsSteps) check(t tracked, commit, head string) error {
	switch {
	case t.Commit == nil || t.Commit.Hash != v.o.ids[commit]:
		return fmt.Errorf("%w: tracked commit %+v, want %s", errStep, t.Commit, v.o.ids[commit])
	case head != "" && (t.Head == nil || t.Head.Hash != v.o.ids[head]):
		return fmt.Errorf("%w: branch head %+v, want %s", errStep, t.Head, v.o.ids[head])
	}
	return nil
}

func (v *vcsSteps) tracks(commit, head string) error {
	t, err := v.answer()
	if err != nil {
		return err
	}
	return v.check(t, commit, head)
}

func (v *vcsSteps) setBy(not string) error {
	t, err := v.answer()
	if err != nil {
		return err
	}
	if (t.SetBy == "infraweave") == (not != "") {
		return fmt.Errorf("%w: tracked commit set by %q", errStep, t.SetBy)
	}
	return nil
}

// eventually reads the stack until it tracks the commit, which the vcs
// domain's consumer sets after the stack's write commits.
func (v *vcsSteps) eventually(ctx context.Context, token, stack, commit, replica string) error {
	//nolint:wrapcheck // names what it waited for
	return Eventually(
		ctx,
		"stack "+stack+" to track "+commit,
		func(ctx context.Context) (bool, error) {
			if err := v.o.send(
				ctx,
				`token "`+token+`"`,
				"GET",
				"/api/v1/stacks/"+stack,
				"",
				replica,
			); err != nil {
				return false, err
			}
			t, err := v.answer()
			if err != nil {
				return false, err
			}
			return v.check(t, commit, commit) == nil, nil
		},
	)
}
