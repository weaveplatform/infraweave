package api

// Event is a provider-independent VCS event. Provider adapters retain stable
// repository identities as well as paths, so a rename does not change ordering.
// Comments are parsed as policy input; intake creates no runs from them.
//
// ref: decision 0012
type Event struct {
	Type         string        `json:"type"`
	Action       string        `json:"action,omitempty"`
	Repository   Repository    `json:"repository"`
	Branch       string        `json:"branch,omitempty"`
	Tag          string        `json:"tag,omitempty"`
	Before       string        `json:"before,omitempty"`
	Head         string        `json:"head,omitempty"`
	Author       string        `json:"author,omitempty"`
	PullRequest  *PullRequest  `json:"pullRequest,omitempty"`
	Comment      *Comment      `json:"comment,omitempty"`
	CheckAction  *CheckAction  `json:"checkAction,omitempty"`
	Installation *Installation `json:"installation,omitempty"`
}

// Repository identifies the provider's repository and its current path.
type Repository struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// PullRequest holds the request state consumed by push policies.
type PullRequest struct {
	Number     int      `json:"number"`
	HeadBranch string   `json:"headBranch"`
	BaseBranch string   `json:"baseBranch"`
	HeadOwner  string   `json:"headOwner"`
	Draft      bool     `json:"draft"`
	Approved   bool     `json:"approved"`
	Mergeable  bool     `json:"mergeable"`
	Undiverged bool     `json:"undiverged"`
	Labels     []string `json:"labels"`
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
	Repositories []Repository `json:"repositories"`
}
