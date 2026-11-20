package api

import (
	"encoding/json"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PolicyReceipt is run-owned immutable evidence supplied by a trusted creation
// workflow. It carries the decision, never the policy input or print output.
type PolicyReceipt struct {
	ID            kernel.ULID     `json:"id"`
	Policy        kernel.ULID     `json:"policy"`
	PolicyName    string          `json:"policyName"`
	PolicyVersion int64           `json:"policyVersion"`
	Type          string          `json:"type"`
	Engine        string          `json:"engineType"`
	Outcome       string          `json:"outcome"`
	Decision      json.RawMessage `json:"decision"`
	Flags         []string        `json:"flags"`
	ResetFlags    []string        `json:"resetFlags"`
	Sample        bool            `json:"sample"`
	Failure       *PolicyFailure  `json:"failure,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
}

// PolicyFailure is a safe category and message for retained decision evidence.
type PolicyFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
