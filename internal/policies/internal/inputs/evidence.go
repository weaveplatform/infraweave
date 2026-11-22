//nolint:tagliatelle // Snapshot fields use the recorded policy input spelling.
package inputs

import (
	"encoding/json"
	"fmt"
)

// EvaluationFacts is an allowlist of searchable metadata. It contains no source,
// provider values, input document, result or print output.
type EvaluationFacts struct {
	PullRequestID                                             *int64
	PushBranch, RunType, ModuleID, StackName, StackRepository *string
	RunDriftDetection, ModuleEvent                            *bool
}

type (
	factStack struct {
		Name       *string `json:"name"`
		Repository *string `json:"repository"`
	}
	factRun struct {
		Type           *string `json:"type"`
		DriftDetection *bool   `json:"drift_detection"`
	}
)

// Facts snapshots only the named fields from the actual per-policy document.
// Narrow projections skip allocation of potentially large private plan values;
// pointers keep an unavailable/null fact distinct from a known false or empty.
func Facts(kind string, raw json.RawMessage) (EvaluationFacts, error) {
	var out EvaluationFacts
	if len(raw) == 0 || kind != "PLAN" && kind != "GIT_PUSH" && kind != "NOTIFICATION" {
		return out, nil
	}
	var doc struct {
		Infraweave struct {
			Run   factRun   `json:"run"`
			Stack factStack `json:"stack"`
		} `json:"infraweave"`
		Push *struct {
			Branch *string `json:"branch"`
		} `json:"push"`
		PullRequest *struct {
			ID   *int64 `json:"id"`
			Head struct {
				Branch *string `json:"branch"`
			} `json:"head"`
		} `json:"pull_request"`
		Stack      factStack `json:"stack"`
		RunUpdated *struct {
			Run   factRun   `json:"run"`
			Stack factStack `json:"stack"`
		} `json:"run_updated"`
		ModuleVersion *struct {
			Module struct {
				ID *string `json:"id"`
			} `json:"module"`
		} `json:"module_version"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out, fmt.Errorf("%w: evaluation metadata is invalid", ErrInput)
	}
	switch kind {
	case "PLAN":
		out.RunType, out.RunDriftDetection = doc.Infraweave.Run.Type, doc.Infraweave.Run.DriftDetection
		out.StackName, out.StackRepository = doc.Infraweave.Stack.Name, doc.Infraweave.Stack.Repository
	case "GIT_PUSH":
		out.StackName, out.StackRepository = doc.Stack.Name, doc.Stack.Repository
		if doc.Push != nil {
			out.PushBranch = doc.Push.Branch
		}
		if doc.PullRequest != nil {
			out.PullRequestID, out.PushBranch = doc.PullRequest.ID, doc.PullRequest.Head.Branch
		}
	case "NOTIFICATION":
		if doc.RunUpdated != nil {
			out.RunType, out.RunDriftDetection = doc.RunUpdated.Run.Type, doc.RunUpdated.Run.DriftDetection
			out.StackName, out.StackRepository = doc.RunUpdated.Stack.Name, doc.RunUpdated.Stack.Repository
			out.ModuleEvent = new(false)
		}
		if doc.ModuleVersion != nil {
			out.ModuleID, out.ModuleEvent = doc.ModuleVersion.Module.ID, new(true)
		}
	}
	return out, nil
}
