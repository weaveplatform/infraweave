package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// ReportDeployment serializes provider reports for a run or a shared native
// GitLab deployment. Cursor fencing also rejects late, recovered job claims.
var ReportDeployment = queue.Kind{Name: "vcs.report_deployment", Queue: queue.Deliveries, Policy: queue.Ignore}

// ErrNoDeployment identifies a run with no recorded deployment binding.
var ErrNoDeployment = errors.New("VCS deployment not recorded")

// Deployment is the credential-free binding fixed by the first eligible event.
// Epoch is the first eligible transition, rather than the run's creation time:
// an older-created run can first become eligible after another run has applied.
type Deployment struct {
	Account     kernel.AccountID     `json:"account"`
	Run         port.DeploymentRun   `json:"run"`
	Stack       port.DeploymentStack `json:"stack"`
	Integration kernel.ULID          `json:"integration"`
	Provider    string               `json:"provider"`
	Namespace   string               `json:"namespace"`
	Repository  string               `json:"repository"`
	NativeHost  string               `json:"nativeHost,omitempty"`
	Epoch       time.Time            `json:"epoch"`
	CreatedAt   time.Time            `json:"createdAt"`
}

// DeploymentRequest names one state transition and its first binding candidate.
type DeploymentRequest struct {
	Deployment Deployment      `json:"deployment"`
	State      kernel.RunState `json:"state"`
	Sequence   int64           `json:"sequence"`
}

// DeploymentCursor is the newest eligible resource owner and its last report.
type DeploymentCursor struct {
	Run      kernel.ULID
	Sequence int64
}

// DeploymentStore joins every write and lock to the transaction on ctx.
type DeploymentStore interface {
	Deployment(context.Context, kernel.AccountID, kernel.ULID) (Deployment, error)
	BindDeployment(context.Context, Deployment) (Deployment, error)
	RequestDeployment(context.Context, DeploymentRequest) (bool, error)
	ReserveDeployment(context.Context, Deployment, string) error
	HoldDeployment(context.Context, kernel.AccountID, string) (DeploymentCursor, error)
	ReportedDeployment(context.Context, kernel.AccountID, string, int64) error
	PurgeDeployments(context.Context, kernel.AccountID, int) (bool, error)
}

// DeploymentBindings reads plain binding IDs, opening credentials only in Use.
type DeploymentBindings interface {
	Binding(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error)
	Use(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error)
}

// DeploymentDeps supplies fact readers and provider capabilities. The events
// domain only dispatches these targets; deployment eligibility belongs here.
type DeploymentDeps struct {
	DB           tx.Beginner
	Store        DeploymentStore
	Histories    runsapi.RunHistories
	Stacks       stacksapi.Stacks
	Integrations DeploymentBindings
	Providers    map[string]port.Deployments
}

// NewDeployments enables deployment targets on the existing feedback sink.
func NewDeployments(jobs *queue.Queue, hold func(context.Context, kernel.AccountID) error, d DeploymentDeps) *Service {
	s := New(jobs, hold)
	s.deployments = &d
	return s
}

// PurgeDeployments removes account-owned metadata after the account fence closes.
func (s *Service) PurgeDeployments(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	if s.deployments == nil {
		return true, nil
	}
	return s.deployments.Store.PurgeDeployments(ctx, acct, batch) //nolint:wrapcheck // store names the purge
}

func (s *Service) deploymentTargets(ctx context.Context, event outbox.Event) ([]vcsapi.FeedbackTarget, error) {
	if s.deployments == nil || event.Kind != runsapi.UpdatedKind || event.SchemaVersion != runsapi.UpdatedVersion ||
		event.Account.IsZero() || event.ID.IsZero() || event.Sequence <= 0 {
		return nil, nil
	}
	var update runsapi.Updated
	if err := json.Unmarshal(event.Document, &update); err != nil || update.Run.ID.IsZero() ||
		update.State != update.Run.State || update.Run.Type != kernel.RunTypeTracked ||
		event.Subject.Kind != "run" || event.Subject.ID != update.Run.ID.String() {
		return nil, nil
	}
	if _, ok := deploymentState(update.State); !ok {
		return nil, nil
	}
	acct := kernel.AccountID{ULID: event.Account}
	history, err := s.deployments.Histories.HistoryAt(ctx, acct, update.Run.ID, event.Sequence)
	if errors.Is(err, runsapi.ErrHistoryNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("VCS deployment history: %w", err)
	}
	if !deploymentHistoryMatches(history, update, event.Sequence) {
		return nil, nil
	}
	d, err := s.deployments.Store.Deployment(ctx, acct, history.ID)
	if errors.Is(err, ErrNoDeployment) {
		d, err = s.selectDeployment(ctx, acct, history)
	}
	if errors.Is(err, ErrNoDeployment) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	request := DeploymentRequest{Deployment: d, State: update.State, Sequence: event.Sequence}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("VCS deployment target: %w", err)
	}
	return []vcsapi.FeedbackTarget{{
		Account: event.Account, EventID: event.ID,
		Key: fmt.Sprintf("deployment:%s:%s", history.ID, update.State), Job: ReportDeployment.Name, Payload: body,
	}}, nil
}

func deploymentHistoryMatches(h runsapi.RunHistory, u runsapi.Updated, sequence int64) bool {
	return h.ID == u.Run.ID && h.Stack == u.Stack.ULID && h.Type == kernel.RunTypeTracked && !h.Local &&
		h.CommitHash != "" && h.CommitHash == u.Run.Commit.Hash && h.Through == sequence &&
		len(h.Transitions) > 0 && h.Transitions[len(h.Transitions)-1].Sequence == sequence &&
		h.Transitions[len(h.Transitions)-1].State == u.State && deploymentReached(h)
}

func (s *Service) selectDeployment(ctx context.Context, acct kernel.AccountID, h runsapi.RunHistory) (Deployment, error) {
	d := s.deployments
	stack, err := d.Stacks.Settings(ctx, acct, h.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) || err == nil && (stack.VCSDetached || d.Providers[stack.Provider] == nil) {
		return Deployment{}, ErrNoDeployment
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("VCS deployment stack: %w", err)
	}
	labels, err := d.Stacks.Labels(ctx, acct, []kernel.ULID{h.Stack})
	if err != nil {
		return Deployment{}, fmt.Errorf("VCS deployment labels: %w", err)
	}
	environment, enabled := deploymentEnvironment(stack.Slug, labels[h.Stack])
	if !enabled {
		return Deployment{}, ErrNoDeployment
	}
	bound, ok, err := d.Integrations.Binding(ctx, acct, stack.Provider, stack.VCSIntegration)
	if err != nil {
		return Deployment{}, fmt.Errorf("VCS deployment binding: %w", err)
	}
	if !ok {
		return Deployment{}, ErrNoDeployment
	}
	var nativeHost string
	if host, ok := d.Providers[stack.Provider].(port.DeploymentHost); ok {
		nativeHost, err = host.DeploymentHost(bound.Settings)
		if err != nil {
			return Deployment{}, fmt.Errorf("VCS deployment host: %w", err)
		}
	}
	ref, tag := h.Branch, false
	if h.Tag != "" {
		ref, tag = h.Tag, true
	}
	if ref == "" {
		ref = stack.Branch
	}
	var epoch time.Time
	for _, step := range h.Transitions {
		if step.State == kernel.RunStateUnconfirmed || step.State == kernel.RunStateApplying {
			epoch = step.At
			break
		}
	}
	return Deployment{
		Account: acct, Run: port.DeploymentRun{ID: h.ID, Commit: h.CommitHash, Ref: ref, Tag: tag},
		Stack:       port.DeploymentStack{ID: stack.ID, Slug: stack.Slug, Environment: environment},
		Integration: bound.ID, Provider: stack.Provider, Namespace: stack.Namespace, Repository: stack.Repository, NativeHost: nativeHost,
		Epoch: epoch, CreatedAt: h.CreatedAt,
	}, nil
}

func resourceKey(d Deployment) string {
	parts := []any{d.Namespace, d.Repository, d.Stack.Environment}
	if d.Provider == "GITLAB" {
		parts = append(parts, d.NativeHost, d.Run.Ref, d.Run.Commit)
	} else {
		parts = append(parts, d.Integration, d.Run.ID)
	}
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func validDeployment(r DeploymentRequest) bool {
	d := r.Deployment
	_, state := deploymentState(r.State)
	return state && r.Sequence > 0 && !d.Account.IsZero() && !d.Run.ID.IsZero() && !d.Stack.ID.IsZero() &&
		!d.Integration.IsZero() && d.Run.Commit != "" && d.Stack.Slug != "" && d.Stack.Environment != "" &&
		d.Namespace != "" && d.Repository != "" && !d.Epoch.IsZero() && !d.CreatedAt.IsZero() &&
		(d.Provider != "GITLAB" || d.NativeHost != "")
}

func (s *Service) enqueueDeployment(ctx context.Context, target vcsapi.FeedbackTarget) error {
	var request DeploymentRequest
	if s.deployments == nil || json.Unmarshal(target.Payload, &request) != nil ||
		!validDeployment(request) || request.Deployment.Account.ULID != target.Account ||
		s.deployments.Providers[request.Deployment.Provider] == nil {
		return vcsapi.ErrFeedbackTarget
	}
	d, err := s.deployments.Store.BindDeployment(ctx, request.Deployment)
	if err != nil {
		return fmt.Errorf("VCS deployment bind: %w", err)
	}
	request.Deployment = d
	inserted, err := s.deployments.Store.RequestDeployment(ctx, request)
	if err != nil || !inserted {
		return err //nolint:wrapcheck // the store names the request
	}
	resource := resourceKey(d)
	if err := s.deployments.Store.ReserveDeployment(ctx, d, resource); err != nil {
		return err //nolint:wrapcheck // the store names the reservation
	}
	_, err = s.jobs.EnqueueTx(ctx, ReportDeployment, request, queue.Account(target.Account),
		queue.UniqueKey(fmt.Sprintf("%s:%s:%s", target.Account, d.Run.ID, request.State)),
		queue.OrderingKey("vcs.deployment:"+target.Account.String()+":"+resource))
	if err != nil {
		return fmt.Errorf("enqueue VCS deployment: %w", err)
	}
	return nil
}
