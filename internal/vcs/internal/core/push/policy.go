package push

import (
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

const (
	Track     = "track"
	Propose   = "propose"
	Ignored   = "ignored"
	Duplicate = "duplicate"
	Capped    = "capped"
	Expired   = "expired"
	Refused   = "refused"
	NoAction  = "none"

	NoAffectedFiles = "NO_AFFECTED_FILES_IN_PROJECT_ROOT"
	TagsIgnored     = "TAGS_IGNORED_BY_DEFAULT"
	RunLimitReached = "RUN_LIMIT_REACHED"
)

// Decision is the default push behavior before lifecycle admission. Recording
// ignored triggers and custom push-policy decisions belong to later consumers.
type Decision struct{ Outcome, Reason string }

// recordIgnored is deliberately narrower than every capped decision: other-
// branch proposals and non-push/non-tag inputs leave no ignored-trigger row.
func recordIgnored(event vcsapi.Event, stack stacksapi.Settings, decision Decision) bool {
	if decision.Outcome == Ignored && event.Type == "tag" && decision.Reason == TagsIgnored {
		return true
	}
	return event.Type == "push" && event.Branch == stack.Branch &&
		(decision.Outcome == Ignored && decision.Reason == NoAffectedFiles || decision.Outcome == Capped && decision.Reason == RunLimitReached)
}

// Default applies only the default branch/path behavior. It does not interpret
// pull requests, comments or policy source. Tag ignores stand on their own.
func Default(event vcsapi.Event, stack stacksapi.Settings, files []string) Decision {
	if (event.Type == "tag" || event.Type == "push") && port.ZeroCommit(event.Head) {
		return Decision{Outcome: NoAction}
	}
	if event.Type == "tag" {
		return Decision{Ignored, TagsIgnored}
	}
	if event.Type != "push" || port.ZeroCommit(event.Head) {
		return Decision{Outcome: NoAction}
	}
	if !affected(stack, files) {
		if event.Branch == stack.Branch {
			return Decision{Ignored, NoAffectedFiles}
		}
		return Decision{Outcome: NoAction}
	}
	if event.Branch == stack.Branch {
		return Decision{Outcome: Track}
	}
	return Decision{Outcome: Propose}
}

func affected(stack stacksapi.Settings, files []string) bool {
	root := ""
	if stack.ProjectRoot != nil {
		root = strings.Trim(path.Clean(*stack.ProjectRoot), "/")
		if root == "." {
			root = ""
		}
	}
	for _, file := range files {
		file = strings.TrimPrefix(path.Clean(strings.TrimLeft(file, "/")), "./")
		if file == "." || file == ".." || strings.HasPrefix(file, "../") {
			continue
		}
		if root == "" || file == root || strings.HasPrefix(file, root+"/") {
			return true
		}
		for _, pattern := range stack.AdditionalProjectGlobs {
			if matches, err := doublestar.Match(strings.TrimLeft(pattern, "/"), file); err == nil && matches {
				return true
			}
		}
	}
	return false
}
