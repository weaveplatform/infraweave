package api

import "time"

// Event is a provider-independent VCS event. Provider adapters retain stable
// repository identities as well as paths, so a rename does not change ordering.
// Comments are parsed as policy input; intake creates no runs from them.
//
// ref: decision 0012
type Event struct {
	Type         string            `json:"type"`
	Action       string            `json:"action,omitempty"`
	Repository   Repository        `json:"repository"`
	Branch       string            `json:"branch,omitempty"`
	Tag          string            `json:"tag,omitempty"`
	Before       string            `json:"before,omitempty"`
	Head         string            `json:"head,omitempty"`
	Forced       bool              `json:"forced,omitempty"`
	Author       string            `json:"author,omitempty"`
	PullRequest  *PullRequest      `json:"pullRequest,omitempty"`
	Comment      *Comment          `json:"comment,omitempty"`
	CheckAction  *CheckAction      `json:"checkAction,omitempty"`
	Installation *Installation     `json:"installation,omitempty"`
	OwnerRename  *OwnerRename      `json:"ownerRename,omitempty"`
	Commits      *CommitCollection `json:"commits,omitempty"`
}

// CommitCollection preserves the provider's native push collection and its
// documented location. Omission explains why that collection cannot supply
// the policy contract's complete per-commit facts; a partial list is retained
// for provenance but never projected as the complete policy collection.
type CommitCollection struct {
	Source   string       `json:"source"`
	Omission string       `json:"omission,omitempty"`
	Items    []PushCommit `json:"items"`
}

// PushCommit is native metadata, with nil Files meaning unavailable rather
// than an assertion that this commit changed no files.
type PushCommit struct {
	Hash    string    `json:"hash"`
	Author  string    `json:"author"`
	Message string    `json:"message"`
	URL     string    `json:"url,omitempty"`
	At      time.Time `json:"at"`
	Files   *[]string `json:"files,omitempty"`
}

// Repository identifies the provider's repository and its current path.
type Repository struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	OwnerID   string `json:"ownerId,omitempty"`
}

// PullRequest holds native snapshot metadata, source identity and policy state.
type PullRequest struct {
	// HeadRepository retains the source's stable identity when a provider
	// advertises a commit prefix that preparation must resolve before storage.
	HeadRepository *Repository `json:"headRepository,omitempty"`
	AuthorID       string      `json:"authorId,omitempty"`
	AuthorLogin    string      `json:"authorLogin,omitempty"`
	Title          string      `json:"title,omitempty"`
	URL            string      `json:"url,omitempty"`
	HeadBranchURL  string      `json:"headBranchUrl,omitempty"`
	State          string      `json:"state,omitempty"`
	CreatedAt      time.Time   `json:"createdAt,omitempty"`
	UpdatedAt      time.Time   `json:"updatedAt,omitempty"`
	Number         int         `json:"number"`
	HeadBranch     string      `json:"headBranch"`
	BaseBranch     string      `json:"baseBranch"`
	HeadOwner      string      `json:"headOwner"`
	Draft          bool        `json:"draft"`
	Fork           bool        `json:"fork"`
	Approved       bool        `json:"approved"`
	Mergeable      bool        `json:"mergeable"`
	Undiverged     bool        `json:"undiverged"`
	Labels         []string    `json:"labels"`
}

// Comment includes the request identity supplied by every provider's event.
type Comment struct {
	Body        string `json:"body"`
	Author      string `json:"author"`
	PullRequest int    `json:"pullRequest"`
	Head        string `json:"head"`
	BaseBranch  string `json:"baseBranch"`
	Fork        bool   `json:"fork"`
}

// CheckAction identifies the check button and the external check it belongs to.
type CheckAction struct {
	Action     string `json:"action"`
	ExternalID string `json:"externalId"`
}

// Installation carries an installation action and its affected repositories.
type Installation struct {
	ID           string       `json:"id"`
	Action       string       `json:"action"`
	Owner        Owner        `json:"owner"`
	Repositories []Repository `json:"repositories"`
	Added        []Repository `json:"added,omitempty"`
	Removed      []Repository `json:"removed,omitempty"`
}

// Owner is the provider's stable account identity, never an infraweave account.
type Owner struct {
	ID    string `json:"id"`
	Login string `json:"login"`
}

// OwnerRename retains identity across a provider account's login change.
type OwnerRename struct {
	ID            string `json:"id"`
	PreviousLogin string `json:"previousLogin"`
	Login         string `json:"login"`
}
