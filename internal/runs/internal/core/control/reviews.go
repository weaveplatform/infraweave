package control

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/review"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Review captures a fresh authorized vote and evaluates this exact state. The
// canonical author key makes sessions and personal keys of one person one vote;
// the shared Idempotency-Key middleware owns exact HTTP request replay.
func (s *Service) Review(ctx context.Context, p kernel.Principal, slug, raw string, decision review.Decision, note *string, version *int64) (review.Record, error) {
	if decision != review.Approve && decision != review.Reject {
		return review.Record{}, &kernel.Invalid{
			Detail: "The review decision must be APPROVE or REJECT.",
			Errors: []kernel.FieldError{{Pointer: "/decision", Reason: kernel.ReasonEnum, Message: "Choose APPROVE or REJECT."}},
		}
	}
	if note != nil && utf8.RuneCountInString(*note) > MaxComment {
		return review.Record{}, &kernel.Invalid{
			Detail: "The review note is too long.",
			Errors: []kernel.FieldError{{Pointer: "/note", Reason: kernel.ReasonMaxLength, Message: "A review note holds at most 10000 characters."}},
		}
	}
	var record review.Record
	err := tx.Do(ctx, s.d.DB, tx.Options{Serializable: true}, func(ctx context.Context) error {
		return s.in(ctx, p, slug, raw, func(ctx context.Context, st stacksapi.Stack, run lifecycle.Run) error {
			ok, err := s.d.Authz.Authorize(ctx, p, accessapi.ActionRunReview, st.Space)
			if err != nil {
				return err //nolint:wrapcheck // the owner names the failing operation
			}
			if !ok {
				return &Blocked{Run: run.ID, Action: accessapi.ActionRunReview}
			}
			if run.State != kernel.RunStateQueued && run.State != kernel.RunStateUnconfirmed && run.State != kernel.RunStatePendingReview {
				return &Refused{Run: run.ID, State: run.State, Needs: "a review needs a run in QUEUED, UNCONFIRMED or PENDING_REVIEW"}
			}
			if version != nil && *version != run.Version {
				return &lifecycle.Conflict{Run: run.ID, State: run.State, Version: run.Version}
			}
			if s.d.ReviewMetadata == nil || s.d.Reviews == nil || s.d.Scheduler == nil {
				return kernel.ErrUnavailable
			}
			metadata, err := s.d.ReviewMetadata.ReviewMetadata(ctx, p, st.Space)
			if err != nil {
				return err //nolint:wrapcheck // the owner names the failing operation
			}
			if !metadata.CanReview {
				return &Blocked{Run: run.ID, Action: accessapi.ActionRunReview}
			}
			at := s.d.Now().UTC().Truncate(time.Microsecond)
			record = review.Record{
				Account: p.AccountID, Run: run.ID, ID: kernel.NewULID(), AuthorKey: metadata.AuthorKey,
				Caller: p.Caller(), Session: p.SessionID, Decision: decision, Note: note, State: run.State, StateVersion: run.Version,
				CreatedAt: at, RequestID: middleware.RequestIDFrom(ctx), Facts: reviewFacts(metadata, run, note, at, accessapi.ClientAddr(ctx)),
			}
			if err := s.d.Reviews.RecordReview(ctx, record); err != nil {
				return err //nolint:wrapcheck // the owner names the failing operation
			}
			result, err := s.d.Lifecycle.EvaluateApproval(ctx, run)
			if err != nil {
				return err //nolint:wrapcheck // the owner names the failing operation
			}
			moved, err := s.d.Lifecycle.ApplyApproval(ctx, run, result)
			if err != nil {
				return err //nolint:wrapcheck // the owner names the failing operation
			}
			if moved.Version != run.Version {
				return nil
			}
			if run.State == kernel.RunStateQueued {
				return s.d.Scheduler.ScheduleEvaluated(ctx, run, result) //nolint:wrapcheck // scheduler names the move
			}
			current, err := s.d.Store.Lock(ctx, run.Account, run.ID)
			if err != nil {
				return err //nolint:wrapcheck // the owner names the failing operation
			}
			if current.Version == run.Version {
				return s.d.Lifecycle.Refresh(ctx, current)
			}
			return nil
		})
	})
	return record, err //nolint:wrapcheck // each owner names its failure
}

func knownReview[T any](v T) policyapi.Fact[T] { return policyapi.Fact[T]{Known: true, Value: &v} }

func reviewFacts(m accessapi.ReviewMetadata, r lifecycle.Run, note *string, at time.Time, ip string) policyapi.ReviewFacts {
	roles := make([]policyapi.RoleFacts, 0, len(m.Roles))
	for _, role := range m.Roles {
		roles = append(roles, policyapi.RoleFacts{ID: role.ID, Slug: role.Slug, Name: role.Name})
	}
	identity := policyapi.Fact[policyapi.IdentityFacts]{}
	if m.Identity != nil {
		federations := map[string]policyapi.FederationFacts{}
		for _, f := range m.Identity.Federations {
			federations[f.Provider.String()] = policyapi.FederationFacts{ID: knownReview(f.Subject)}
		}
		identity = knownReview(policyapi.IdentityFacts{ULID: knownReview(m.Identity.Member.String()), Federations: knownReview(federations)})
	}
	p := m.Session
	admin := policyapi.SessionNotAdmin
	if p.Admin {
		admin = policyapi.SessionIsAdmin
	}
	if !p.Human() {
		admin = policyapi.SessionMachineAdmin
	}
	session := policyapi.SessionFacts{
		Machine: !p.Human(), Admin: knownReview(admin), Login: knownReview(p.Login), Name: knownReview(p.Name),
		Teams: knownReview(append([]string{}, p.Teams...)), Identity: identity,
	}
	if m.CreatorIP != nil {
		session.CreatorIP = knownReview(*m.CreatorIP)
	}
	if m.IDPSubject != nil {
		session.IDPSubject = knownReview(*m.IDPSubject)
	}
	request := policyapi.RequestFacts{At: at}
	if strings.TrimSpace(ip) != "" {
		request.RemoteIP = knownReview(ip)
	}
	facts := policyapi.ReviewFacts{
		Author: p.Login, State: r.State, StateVersion: r.Version, AuthorRoles: knownReview(roles),
		Identity: identity, Request: knownReview(request), Session: knownReview(session),
	}
	if note != nil {
		facts.Note = knownReview(*note)
	}
	return facts
}
