package port

import "context"

// CheckState is the common state of a named commit check. Adapters map neutral,
// canceled and skipped to the closest successful or failed native state when
// their provider cannot represent them directly.
type CheckState string

const (
	CheckQueued     CheckState = "queued"
	CheckInProgress CheckState = "in_progress"
	CheckSuccess    CheckState = "success"
	CheckNeutral    CheckState = "neutral"
	CheckFailure    CheckState = "failure"
	CheckCanceled   CheckState = "canceled"
	CheckSkipped    CheckState = "skipped"
)

// CheckAction is a requested action on an individual check. Only GitHub uses
// these fields; its webhook carries Identifier back to the action consumer.
type CheckAction struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Identifier  string `json:"identifier"`
}

// CheckReport supplies a credential-free desired native check. NativeID is a
// previous provider identifier when recorded; adapters recover an uncertain
// create before posting again. ExternalID remains the stack ID on GitHub.
type CheckReport struct {
	Name       string        `json:"name"`
	State      CheckState    `json:"state"`
	Title      string        `json:"title"`
	Summary    string        `json:"summary"`
	URL        string        `json:"url"`
	ExternalID string        `json:"externalId"`
	NativeID   string        `json:"nativeId,omitempty"`
	Actions    []CheckAction `json:"actions"`
}

// Checks sets one named commit check and returns its provider identifier when
// available. Only a queued VCS handler calls Set. Each adapter bounds native
// requests to thirty seconds and returns RateLimited without retrying locally.
type Checks interface {
	Set(ctx context.Context, repo Repository, sha string, check CheckReport) (string, error)
}
