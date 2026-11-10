package acceptance

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cucumber/godog"
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
	sc.Step(
		`^token "(\w+)" eventually reads the stack "([a-z0-9-]+)" tracking the commit kept as "(\w+)" through ([AB])$`,
		v.eventually,
	)
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
