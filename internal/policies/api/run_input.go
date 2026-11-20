package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Fact distinguishes unavailable facts, explicit null, and known values. A
// value with Known=false is invalid rather than silently discarded.
type Fact[T any] struct {
	Known bool
	Value *T
}

// RunInputBuilder projects workflow facts into the published typed contract.
// Workflows bind it lazily after policy selection, inside their transaction.
type RunInputBuilder interface {
	BuildRunInput(context.Context, RunInputFacts) (json.RawMessage, error)
}

// RunInputFacts contains facts owned by run and stack workflows. Plan is an
// opened, already sanitized artifact; this projection does not sanitize again.
// Review/session facts are added by their owning workflow when persisted.
type RunInputFacts struct {
	Account     kernel.AccountID
	Type        Type
	At          time.Time
	Run         RunFacts
	Stack       StackFacts
	PreviousRun Fact[RunFacts]
	Plan        *SanitizedPlan
}

// RunFacts carries persisted facts without using a transport Run projection,
// whose placeholder fields cannot establish a policy fact.
type RunFacts struct {
	ID                    kernel.ULID
	Type                  kernel.RunType
	State                 kernel.RunState
	CreatedAt, UpdatedAt  time.Time
	TriggeredBy           Fact[string]
	Branch                Fact[string]
	Commit                Fact[CommitFacts]
	Flags                 Fact[[]string]
	BasedOnLocalWorkspace Fact[bool]
	DriftDetection        Fact[bool]
	ForceApplyMode        Fact[string]
	Command               Fact[string]
	RuntimeConfig         Fact[RuntimeConfigFacts]
	Changes               Fact[[]ChangeFacts]
	UserProvidedMetadata  Fact[[]string]
}

// CommitFacts retains timestamps as time.Time until the contract owner emits
// nanoseconds. Optional provider facts retain absence and null independently.
type CommitFacts struct {
	Author, Branch, Message string
	At                      time.Time
	Hash, Tag, URL          Fact[string]
	ExistsOnTrackedBranch   Fact[bool]
	PullRequestID           Fact[int64]
}

// StackFacts separates immutable ownership identity from its public slug.
// Administrative is deliberately absent: ADR 0021 serves its deprecated value
// as false. Unknown optional settings remain absent rather than fabricated.
type StackFacts struct {
	ID                                       kernel.ULID
	Slug, Name, Branch, Namespace            string
	ProjectRoot, Repository                  string
	State                                    kernel.StackState
	Autodeploy                               bool
	Labels, AdditionalProjectGlobs, Contexts Fact[[]string]
	LockedBy, TerraformVersion, WorkflowTool Fact[string]
	TrackedCommit                            Fact[CommitFacts]
	Space                                    Fact[SpaceFacts]
	WorkerPool                               Fact[WorkerPoolFacts]
}

// SpaceFacts is an owner-supplied space snapshot, not a fabricated ID-only object.
type SpaceFacts struct {
	ID, Name string
	Labels   Fact[[]string]
}

// WorkerPoolFacts is an owner-supplied pool snapshot.
type WorkerPoolFacts struct {
	ID, Name Fact[string]
	Labels   Fact[[]string]
	Public   bool
}

// RuntimeConfigFacts requires actual winning environment values. A persisted
// key-only configuration is insufficient and must not substitute blank values.
type RuntimeConfigFacts struct {
	AfterApply, AfterDestroy, AfterInit, AfterPerform Fact[[]string]
	AfterPlan, AfterRun                               Fact[[]string]
	BeforeApply, BeforeDestroy, BeforeInit            Fact[[]string]
	BeforePerform, BeforePlan, GitSparseCheckoutPaths Fact[[]string]
	Environment                                       Fact[map[string]string]
	ProjectRoot, RunnerImage                          string
	TerraformVersion                                  Fact[string]
}

// ChangeFacts mirrors structural changes materialized by the run artifact.
// Raw resource attributes remain in the explicitly projected vendor document.
type ChangeFacts struct {
	Phase, Action, Address, Vendor string
	ResourceType, Name, Module     Fact[string]
	Moved                          Fact[bool]
}

// SanitizedPlan is the decrypted worker artifact, including selected units for
// Terragrunt. Unsupported vendors fail explicitly instead of losing fields.
type SanitizedPlan struct {
	Vendor   string
	Document json.RawMessage
}
