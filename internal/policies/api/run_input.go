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
	Known bool `json:"known"`
	Value *T   `json:"value"`
}

// RunInputBuilder projects workflow facts into the published typed contract.
// Workflows bind it lazily after policy selection, inside their transaction.
type RunInputBuilder interface {
	BuildRunInput(context.Context, RunInputFacts) (json.RawMessage, error)
}

// RunInputFacts contains facts owned by run and stack workflows. Plan is an
// opened, already sanitized artifact; this projection does not sanitize again.
type RunInputFacts struct {
	Account     kernel.AccountID
	Type        Type
	At          time.Time
	Run         RunFacts
	Stack       StackFacts
	PreviousRun Fact[RunFacts]
	Plan        *SanitizedPlan
	Reviews     Fact[ReviewsFacts]
}

// ReviewsFacts is the run owner's state/version-scoped selection of the latest
// review per author. Known empty groups are explicit arrays, never a fabricated
// approval or a review carried from a previous state.
type ReviewsFacts struct {
	Current ReviewGroupFacts `json:"current"`
	Older   ReviewGroupFacts `json:"older"`
}

// ReviewGroupFacts separates approval and rejection without rewriting history.
type ReviewGroupFacts struct {
	Approvals  []ReviewFacts `json:"approvals"`
	Rejections []ReviewFacts `json:"rejections"`
}

// ReviewFacts retains the metadata recorded at the authorized review, not the
// reviewer's current roles or session at a later policy evaluation.
type ReviewFacts struct {
	Author       string              `json:"author"`
	State        kernel.RunState     `json:"state"`
	StateVersion int64               `json:"stateVersion"`
	AuthorRoles  Fact[[]RoleFacts]   `json:"authorRoles"`
	Identity     Fact[IdentityFacts] `json:"identity"`
	Note         Fact[string]        `json:"note"`
	Request      Fact[RequestFacts]  `json:"request"`
	Session      Fact[SessionFacts]  `json:"session"`
}

// RoleFacts keeps the public role identity and immutable ULID distinct.
type RoleFacts struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	ULID string `json:"ulid"`
}

// IdentityFacts contains only identity data supplied by its owning domain.
type IdentityFacts struct {
	ULID        Fact[string]                     `json:"ulid"`
	Federations Fact[map[string]FederationFacts] `json:"federations"`
}

// FederationFacts is the recorded external identity, when available.
type FederationFacts struct {
	ID       Fact[string] `json:"id"`
	Username Fact[string] `json:"username"`
}

// RequestFacts fixes the request clock when the review is recorded. A later
// evaluation must not replace it with its own timestamp or remote address.
type RequestFacts struct {
	At       time.Time    `json:"at"`
	RemoteIP Fact[string] `json:"remoteIp"`
}

// SessionAdmin preserves the published human boolean and machine empty-string
// alternatives without accepting an arbitrary JSON value from another owner.
type SessionAdmin uint8

const (
	SessionNotAdmin SessionAdmin = iota
	SessionIsAdmin
	SessionMachineAdmin
)

// SessionFacts projects the recorded session without credentials or token data.
type SessionFacts struct {
	Machine    bool                `json:"machine"`
	Admin      Fact[SessionAdmin]  `json:"admin"`
	Login      Fact[string]        `json:"login"`
	Name       Fact[string]        `json:"name"`
	CreatorIP  Fact[string]        `json:"creatorIp"`
	IDPSubject Fact[string]        `json:"idpSubject"`
	StackID    Fact[string]        `json:"stackId"`
	Teams      Fact[[]string]      `json:"teams"`
	Identity   Fact[IdentityFacts] `json:"identity"`
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
