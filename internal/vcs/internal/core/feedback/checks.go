package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var (
	ErrNoCheck     = errors.New("VCS commit check binding not found")
	ErrCheckReport = errors.New("VCS commit check report failed")
)

// CheckBinding fixes the credential-free source and policy selected for a run.
// Cap bindings have no run and retain the stack/commit/kind diagnosis instead.
type CheckBinding struct {
	Account      kernel.AccountID `json:"account"`
	Key          string           `json:"key"`
	Run          kernel.ULID      `json:"run"`
	Stack        kernel.ULID      `json:"stack"`
	Slug         string           `json:"slug"`
	Integration  kernel.ULID      `json:"integration"`
	Provider     string           `json:"provider"`
	NativeHost   string           `json:"nativeHost"`
	Namespace    string           `json:"namespace"`
	Repository   string           `json:"repository"`
	RepositoryID string           `json:"repositoryId,omitempty"`
	Commit       string           `json:"commit"`
	Ref          string           `json:"ref"`
	Type         kernel.RunType   `json:"type"`
	Policy       port.CheckPolicy `json:"policy"`
	CreatedAt    time.Time        `json:"createdAt"`
	Cap          bool             `json:"cap"`
	Limit        int              `json:"limit,omitempty"`
}

// CheckRequest carries only a binding and the transition that requested a
// refresh. A worker reads current run facts; it never presents today's delta
// as a historical snapshot of an older event's state.
type CheckRequest struct {
	Binding   CheckBinding `json:"binding"`
	Name      string       `json:"name"`
	Aggregate bool         `json:"aggregate"`
	Sequence  int64        `json:"sequence"`
}

// CheckResource is the last acknowledged owner of a native named resource.
// GitHub individual keys include the run; status-provider keys are shared.
type CheckResource struct {
	Account                        kernel.AccountID
	Key, Binding, NativeID, Digest string
	Owner                          kernel.ULID
	Epoch                          time.Time
	Sequence                       int64
	Aggregate                      bool
}

type checkOwner struct {
	ID        kernel.ULID
	CreatedAt time.Time
}

type CheckStore interface {
	BindCheck(context.Context, CheckBinding) (CheckBinding, error)
	CheckBinding(context.Context, kernel.AccountID, string) (CheckBinding, error)
	RequestCheck(context.Context, CheckRequest) (bool, error)
	ReserveCheck(context.Context, CheckResource) error
	HoldCheck(context.Context, CheckResource) (CheckResource, error)
	ReportedCheck(context.Context, CheckResource) error
	ResolveCheckRepository(context.Context, CheckBinding) (CheckBinding, error)
	CapChecks(context.Context, CheckBinding, kernel.RunType) ([]CheckBinding, error)
	CheckPullRequests(context.Context, CheckBinding) ([]int, error)
	DeleteStackChecks(context.Context, kernel.AccountID, kernel.ULID) error
	PurgeChecks(context.Context, kernel.AccountID, int) (bool, error)
}

type CheckProviders interface {
	port.Checks
	port.CheckHost
}

type CheckBindings interface {
	DeploymentBindings
	EventUse(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Use, bool, error)
}

type CheckDeps struct {
	DB           tx.Beginner
	Store        CheckStore
	Facts        runsapi.CheckFacts
	Histories    runsapi.RunHistories
	Stacks       stacksapi.Stacks
	Integrations CheckBindings
	Providers    map[string]CheckProviders
	Public       string
	Now          func() time.Time
	Log          *slog.Logger
}

// WithChecks adds commit feedback without replacing deployment or comment
// capabilities. Assembly configures the service before workers start.
func (s *Service) WithChecks(d CheckDeps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	s.checks = &d
	return s
}

func individualName(b CheckBinding) string {
	name := "infraweave/" + b.Slug
	if b.Type == kernel.RunTypeTracked {
		name += "/tracked"
	}
	return name
}

// Native status APIs key by commit and visible name, so ALL gives its fixed
// aggregate names precedence. GitHub has distinct per-run check identities.
func sharedNameCollision(b CheckBinding) bool {
	if b.Policy.Mode != "ALL" || b.Provider == "GITHUB_ENTERPRISE" {
		return false
	}
	switch individualName(b) {
	case "infraweave/tracked", "infraweave/proposed", "infraweave/modules":
		return true
	}
	return false
}

func checkResourceKey(b CheckBinding, name string, aggregate bool) string {
	namespace, repository := b.Namespace, b.Repository
	if b.Provider == "GITHUB_ENTERPRISE" {
		namespace = strings.ToLower(namespace)
		repository = strings.ToLower(repository)
	}
	identity := []string{"path", namespace, repository}
	if b.RepositoryID != "" {
		identity = []string{"native", b.RepositoryID}
	}
	parts := []any{b.Provider, b.NativeHost, identity, b.Commit, name}
	if b.Provider == "GITHUB_ENTERPRISE" && !aggregate {
		parts = append(parts, b.Key)
	}
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func checkBindingKey(run kernel.ULID) string { return "run:" + run.String() }
