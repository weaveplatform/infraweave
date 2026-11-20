package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PushInputBuilder projects owner-supplied event snapshots into the published
// GIT_PUSH input. Its caller binds it lazily after effective policy selection.
type PushInputBuilder interface {
	BuildPushInput(context.Context, PushInputFacts) (json.RawMessage, error)
}

// PushInputFacts contains the stack and immutable prepared provider facts.
// Module execution has a separate owner and does not substitute for a stack.
type PushInputFacts struct {
	Account        kernel.AccountID          `json:"account"`
	Stack          StackFacts                `json:"stack"`
	InProgress     Fact[[]RunFacts]          `json:"inProgress"`
	Push           Fact[PushFacts]           `json:"push"`
	PullRequest    Fact[PullRequestFacts]    `json:"pullRequest"`
	VCSIntegration Fact[VCSIntegrationFacts] `json:"vcsIntegration"`
}

// PushFacts identifies the resolved head and complete affected-file set.
type PushFacts struct {
	Author              string                  `json:"author"`
	Branch              string                  `json:"branch"`
	Hash                string                  `json:"hash"`
	Message             string                  `json:"message"`
	At                  time.Time               `json:"at"`
	AffectedFiles       []string                `json:"affectedFiles"`
	AffectedStacksCount int64                   `json:"affectedStacksCount"`
	Tag                 Fact[string]            `json:"tag"`
	Commits             Fact[[]PushCommitFacts] `json:"commits"`
}

// PushCommitFacts retains each native commit when the provider supplies one.
type PushCommitFacts struct {
	Commit        CommitFacts `json:"commit"`
	AffectedFiles []string    `json:"affectedFiles"`
}

// PullRequestFacts is the resolved native request snapshot, including the
// current comment and actor when a stored comment event caused evaluation.
type PullRequestFacts struct {
	ID                  int64        `json:"id"`
	Action              string       `json:"action"`
	Author              string       `json:"author"`
	Title               string       `json:"title"`
	Base                CommitFacts  `json:"base"`
	Head                CommitFacts  `json:"head"`
	Closed              bool         `json:"closed"`
	Draft               bool         `json:"draft"`
	Diff                []string     `json:"diff"`
	Labels              []string     `json:"labels"`
	AffectedStacksCount int64        `json:"affectedStacksCount"`
	ActionInitiator     Fact[string] `json:"actionInitiator"`
	HeadOwner           Fact[string] `json:"headOwner"`
	Approved            Fact[bool]   `json:"approved"`
	Mergeable           Fact[bool]   `json:"mergeable"`
	Undiverged          Fact[bool]   `json:"undiverged"`
	Comment             Fact[string] `json:"comment"`
}

// VCSIntegrationFacts contains credential-free metadata under its owner hold.
type VCSIntegrationFacts struct {
	ID          kernel.ULID      `json:"id"`
	Description string           `json:"description"`
	IsDefault   bool             `json:"isDefault"`
	Labels      []string         `json:"labels"`
	Name        string           `json:"name"`
	Provider    string           `json:"provider"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	Space       Fact[SpaceFacts] `json:"space"`
}
