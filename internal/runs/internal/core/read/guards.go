package read

import (
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Comment is a comment on a run: the login that wrote it, its Markdown
// body and when it was written.
type Comment struct {
	ID       kernel.ULID
	Username string
	Body     string
	At       time.Time
}

// Guards are why a run cannot be retried and why it cannot be promoted,
// each empty when it can be.
type Guards struct {
	Retry   string
	Promote string
}

// What a retry and a promotion need of a run, which a blocker answers.
const (
	needsRetryType       = "a retry needs a proposed or tracked run"
	needsRetryEnded      = "a retry needs a run that has ended"
	needsRetryUnfinished = "a retry needs a run that ended other than in FINISHED"
	needsRetryCommit     = "a retry needs a run on a commit, not on a local workspace"
	needsRetryTracked    = "a retry needs a tracked run on the stack's tracked commit"
	needsPromoteType     = "a promotion needs a proposed run"
	needsPromoteCommit   = "a promotion needs a run on a commit, not on a local workspace"
	needsPromoteStack    = "a promotion needs a stack that allows run promotion"
	needsPromoteNewer    = "a promotion needs a commit newer than the stack's tracked commit"
)

// RetryBlocker is why r cannot be retried, empty when it can: a retry
// takes a proposed or tracked run that ended other than in FINISHED,
// on a commit, and a tracked run must still be on st's tracked commit.
// Whether the repository still has the commit is the retry's to find
// out, as a read does not ask the repository.
//
// ref: decision 0008 § "The state machine is the authority", runRetry
func RetryBlocker(r lifecycle.Run, st stacksapi.Settings) string {
	switch {
	case r.Type != kernel.RunTypeProposed && r.Type != kernel.RunTypeTracked:
		return needsRetryType
	case !r.State.Terminal():
		return needsRetryEnded
	case r.State == kernel.RunStateFinished:
		return needsRetryUnfinished
	case r.LocalPreview():
		return needsRetryCommit
	case r.Type == kernel.RunTypeTracked && (st.TrackedCommit == nil || r.Commit.Hash != st.TrackedCommit.Hash):
		return needsRetryTracked
	}
	return ""
}

// PromoteBlocker is why r, a run of the stack st, cannot be promoted,
// empty when it can: a promotion takes a proposed run on a commit newer,
// by commit time, than the stack's tracked commit, on a stack that allows
// promotion. Any commit is newer than none.
//
// ref: report 08 § "7. Promotion, autodeploy, autoretry and the PR loop"
func PromoteBlocker(r lifecycle.Run, st stacksapi.Settings) string {
	switch {
	case r.Type != kernel.RunTypeProposed:
		return needsPromoteType
	case r.LocalPreview():
		return needsPromoteCommit
	case !st.GithubActionDeploy:
		return needsPromoteStack
	case st.TrackedCommit != nil && !r.Commit.Timestamp.After(st.TrackedCommit.Timestamp):
		return needsPromoteNewer
	}
	return ""
}
