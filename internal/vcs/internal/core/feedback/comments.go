package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
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

// ReportComment delivers a native comment; persistent mappings survive queue
// uniqueness expiration. Ordering serializes comments on one pull request.
var ReportComment = queue.Kind{Name: "vcs.report_comment", Queue: queue.Deliveries, Policy: queue.Coalesce}

// ErrNoComment identifies an absent account-owned comment or pull request.
var ErrNoComment = errors.New("VCS comment not found")

// CommentPullRequest contains current native routing facts, without credentials.
// Mapped records known native-identity routing; losing that map cannot redirect
// a queued effect to an unrelated repository occupying its old path.
type CommentPullRequest struct {
	ID           kernel.ULID `json:"id"`
	Integration  kernel.ULID `json:"integration"`
	RepositoryID string      `json:"repositoryId"`
	Number       string      `json:"number"`
	Namespace    string      `json:"namespace"`
	Repository   string      `json:"repository"`
	Head         string      `json:"head"`
	Open         bool        `json:"open"`
	Provider     string      `json:"provider"`
	Mapped       bool        `json:"mapped"`
}

// Comment retains the first credential-free native binding. PlanVersion names
// the exact PLANNING transition, rather than a later targeted replan's log.
// Body is supplied only by Upsert callers; plan bodies read worker-masked logs.
type Comment struct {
	Account      kernel.AccountID      `json:"account"`
	ID           kernel.ULID           `json:"id"`
	Target       CommentPullRequest    `json:"target"`
	NativeHost   string                `json:"nativeHost"`
	DedupKey     string                `json:"dedupKey"`
	Stack        kernel.ULID           `json:"stack,omitempty"`
	StackSlug    string                `json:"stackSlug,omitempty"`
	Run          kernel.ULID           `json:"run,omitempty"`
	Commit       string                `json:"commit,omitempty"`
	PlanVersion  int64                 `json:"planVersion,omitempty"`
	State        kernel.RunState       `json:"state,omitempty"`
	RunCreatedAt time.Time             `json:"runCreatedAt,omitempty"`
	Body         string                `json:"body,omitempty"`
	Revision     int64                 `json:"revision"`
	Reported     int64                 `json:"-"`
	Reference    port.CommentReference `json:"-"`
}

// CommentStore keeps mappings and acknowledges effects in the handler's
// completing transaction. HoldComment locks integration, request and mapping in
// that order, also serializing head movement and per-stack minimization.
type CommentStore interface {
	CommentPullRequests(context.Context, kernel.AccountID, kernel.ULID, string, string, string) ([]CommentPullRequest, error)
	CommentPullRequest(context.Context, kernel.AccountID, kernel.ULID) (CommentPullRequest, error)
	BindComment(context.Context, Comment) (Comment, error)
	HoldComment(context.Context, kernel.AccountID, kernel.ULID) (Comment, error)
	RecordComment(context.Context, Comment, port.CommentReference) error
	SupersededComments(context.Context, Comment) ([]Comment, error)
	MinimizedComment(context.Context, kernel.AccountID, kernel.ULID) error
	PurgeComments(context.Context, kernel.AccountID, int) (bool, error)
}

// CommentDeps supplies VCS-owned routing and effects and owned fact readers.
type CommentDeps struct {
	DB           tx.Beginner
	Store        CommentStore
	Histories    runsapi.RunHistories
	Stacks       stacksapi.Stacks
	Integrations DeploymentBindings
	Providers    map[string]port.Comments
	Logs         runsapi.PlanningLogs
	Public       string
}

// WithComments enables comments on the existing VCS feedback sink.
func (s *Service) WithComments(d CommentDeps) *Service {
	s.comments = &d
	return s
}

func (s *Service) commentTargets(ctx context.Context, event outbox.Event) ([]vcsapi.FeedbackTarget, error) {
	if s.comments == nil || event.Kind != runsapi.UpdatedKind || event.SchemaVersion != runsapi.UpdatedVersion ||
		event.Account.IsZero() || event.ID.IsZero() || event.Sequence <= 0 || event.Subject.Kind != "run" {
		return nil, nil
	}
	var u runsapi.Updated
	if json.Unmarshal(event.Document, &u) != nil || u.Run.ID.IsZero() || u.State != u.Run.State || event.Subject.ID != u.Run.ID.String() {
		return nil, nil
	}
	acct := kernel.AccountID{ULID: event.Account}
	h, err := s.comments.Histories.HistoryAt(ctx, acct, u.Run.ID, event.Sequence)
	if errors.Is(err, runsapi.ErrHistoryNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("VCS comment history: %w", err)
	}
	version := commentPlanningVersion(h, u, event.Sequence)
	if version == 0 {
		return nil, nil
	}
	st, err := s.comments.Stacks.Settings(ctx, acct, h.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) || err == nil && (st.VCSDetached || s.comments.Providers[st.Provider] == nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("VCS comment stack: %w", err)
	}
	labels, err := s.comments.Stacks.Labels(ctx, acct, []kernel.ULID{h.Stack})
	if err != nil {
		return nil, fmt.Errorf("VCS comment labels: %w", err)
	}
	if !slices.Contains(labels[h.Stack], "feature:add_plan_pr_comment") {
		return nil, nil
	}
	in, found, err := s.comments.Integrations.Binding(ctx, acct, st.Provider, st.VCSIntegration)
	if err != nil {
		return nil, fmt.Errorf("VCS comment integration: %w", err)
	}
	if !found {
		return nil, nil
	}
	requests, err := s.comments.Store.CommentPullRequests(ctx, acct, in.ID, st.Namespace, st.Repository, h.CommitHash)
	if err != nil {
		return nil, fmt.Errorf("VCS comment pull requests: %w", err)
	}
	out := make([]vcsapi.FeedbackTarget, 0, len(requests))
	for _, pr := range requests {
		c := Comment{
			Account: acct, ID: kernel.NewULID(), Target: pr, NativeHost: commentHost(in.Settings),
			DedupKey: "plan:" + h.ID.String(), Stack: h.Stack, StackSlug: st.Slug, Run: h.ID, Commit: h.CommitHash,
			PlanVersion: version, State: u.State, RunCreatedAt: h.CreatedAt, Revision: 1,
		}
		body, err := json.Marshal(c)
		if err != nil {
			return nil, fmt.Errorf("VCS comment target: %w", err)
		}
		out = append(out, vcsapi.FeedbackTarget{
			Account: event.Account, EventID: event.ID,
			Key: "comment:" + h.ID.String() + ":" + pr.ID.String(), Job: ReportComment.Name, Payload: body,
		})
	}
	return out, nil
}

func commentPlanningVersion(h runsapi.RunHistory, u runsapi.Updated, sequence int64) int64 {
	if h.ID != u.Run.ID || h.Stack != u.Stack.ULID || h.Type != u.Run.Type ||
		(h.Type != kernel.RunTypeTracked && h.Type != kernel.RunTypeProposed) || h.Local ||
		!port.ValidCommit(h.CommitHash) || h.CommitHash != u.Run.Commit.Hash || h.Through != sequence || len(h.Transitions) < 2 {
		return 0
	}
	last, before := h.Transitions[len(h.Transitions)-1], h.Transitions[len(h.Transitions)-2]
	if last.Sequence != sequence || last.State != u.State || before.State != kernel.RunStatePlanning {
		return 0
	}
	switch last.State {
	case kernel.RunStateUnconfirmed, kernel.RunStateFinished, kernel.RunStateFailed:
		return before.Sequence
	default:
		return 0
	}
}

func commentHost(settings port.Settings) string {
	body, _ := json.Marshal([]string{settings.APIHost, settings.OrganizationURL, settings.AppID})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func validComment(c Comment) bool {
	_, markerErr := port.CommentMarker(c.DedupKey)
	pr := c.Target
	number, numberErr := strconv.ParseInt(pr.Number, 10, 64)
	if markerErr != nil || numberErr != nil || number <= 0 || c.Account.IsZero() || c.ID.IsZero() || pr.ID.IsZero() || pr.Integration.IsZero() ||
		pr.RepositoryID == "" || pr.Number == "" || pr.Namespace == "" || pr.Repository == "" || pr.Provider == "" || c.NativeHost == "" {
		return false
	}
	if c.Run.IsZero() {
		return c.Stack.IsZero() && c.Body != "" && port.HasCommentMarker(c.Body, c.DedupKey)
	}
	return !c.Stack.IsZero() && c.StackSlug != "" && c.PlanVersion > 0 && !c.RunCreatedAt.IsZero() &&
		port.ValidCommit(c.Commit) && c.DedupKey == "plan:"+c.Run.String() && c.Body == "" &&
		(c.State == kernel.RunStateUnconfirmed || c.State == kernel.RunStateFinished || c.State == kernel.RunStateFailed)
}

func (s *Service) enqueueComment(ctx context.Context, target vcsapi.FeedbackTarget) error {
	var c Comment
	if s.comments == nil || json.Unmarshal(target.Payload, &c) != nil || !validComment(c) ||
		c.Account.ULID != target.Account || s.comments.Providers[c.Target.Provider] == nil {
		return vcsapi.ErrFeedbackTarget
	}
	return s.queueComment(ctx, c)
}

func (s *Service) queueComment(ctx context.Context, c Comment) error {
	bound, err := s.comments.Store.BindComment(ctx, c)
	if err != nil {
		return fmt.Errorf("VCS comment bind: %w", err)
	}
	key := fmt.Sprintf("%s:%s", c.Account, bound.ID)
	_, err = s.jobs.EnqueueTx(ctx, ReportComment, commentJob{Account: c.Account, ID: bound.ID},
		queue.Account(c.Account.ULID), queue.UniqueKey(key),
		queue.OrderingKey(fmt.Sprintf("vcs.comment:%s:%s", c.Account, c.Target.ID)))
	if err != nil {
		return fmt.Errorf("enqueue VCS comment: %w", err)
	}
	return nil
}

// PurgeComments removes the account's comment mappings after its fence closes.
func (s *Service) PurgeComments(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	if s.comments == nil {
		return true, nil
	}
	return s.comments.Store.PurgeComments(ctx, acct, batch) //nolint:wrapcheck // store names its purge
}
