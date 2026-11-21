package reviewmeta

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Store reads only access-owned data inside the caller's transaction.
type Store interface {
	AuthzData(context.Context, kernel.AccountID) (authz.Data, error)
	Role(context.Context, kernel.AccountID, string) (role.Role, error)
	Session(context.Context, kernel.ULID) (session.Session, error)
	ReviewSubject(context.Context, kernel.ULID) (*string, error)
	ReviewIdentity(context.Context, kernel.Principal) (accessapi.ReviewIdentity, error)
}

// Deps supplies the database and its access-owned reader.
type Deps struct {
	DB    tx.Beginner
	Store Store
}

// Service reads the facts recorded by a review without exposing credentials.
type Service struct{ d Deps }

// New constructs the metadata reader on d.
func New(d Deps) *Service { return &Service{d: d} }

// ReviewMetadata joins the review's transaction and returns no authorization.
func (s *Service) ReviewMetadata(ctx context.Context, p kernel.Principal, space kernel.ULID) (accessapi.ReviewMetadata, error) {
	if p.AccountID.IsZero() || p.ActorID.IsZero() || p.SessionID.IsZero() || space.IsZero() ||
		p.Kind != kernel.PrincipalUser && p.Kind != kernel.PrincipalAPIKey || p.Kind == kernel.PrincipalUser && p.MemberID.IsZero() {
		return accessapi.ReviewMetadata{}, kernel.ErrUnavailable
	}
	var out accessapi.ReviewMetadata
	err := tx.Do(ctx, s.d.DB, tx.Options{Serializable: true}, func(ctx context.Context) error {
		out = accessapi.ReviewMetadata{Roles: []accessapi.ReviewRole{}}
		ss, err := s.d.Store.Session(ctx, p.SessionID)
		if err != nil {
			return fmt.Errorf("read reviewer session: %w", err)
		}
		actual := ss.Principal()
		if !ss.Live(time.Now()) || actual.AccountID != p.AccountID || actual.OrganizationID != p.OrganizationID || actual.ActorID != p.ActorID || actual.Kind != p.Kind ||
			actual.MemberID != p.MemberID {
			return kernel.ErrUnavailable
		}
		out.Session = actual
		out.AuthorKey = actual.Caller()
		if !actual.MemberID.IsZero() {
			identity, err := s.d.Store.ReviewIdentity(ctx, actual)
			if err != nil {
				return fmt.Errorf("read reviewer identity: %w", err)
			}
			out.Identity = &identity
			out.AuthorKey = "member:" + identity.Member.String()
		}
		if ss.IPAddress != "" {
			out.CreatorIP = new(ss.IPAddress)
		}
		out.IDPSubject, err = s.d.Store.ReviewSubject(ctx, ss.ID)
		if err != nil {
			return fmt.Errorf("read reviewer subject: %w", err)
		}
		data, err := s.d.Store.AuthzData(ctx, p.AccountID)
		if err != nil {
			return fmt.Errorf("read reviewer grants: %w", err)
		}
		if !slices.ContainsFunc(data.Spaces, func(node authz.Space) bool { return node.ID == space }) {
			return kernel.ErrUnavailable
		}
		who := authz.WhoOf(actual)
		snapshot := authz.Build(data)
		out.CanReview = snapshot.Authorize(who, accessapi.ActionRunReview, space)
		grants := snapshot.Explain(who, space)
		seen := map[kernel.ULID]bool{}
		for _, grant := range grants {
			if seen[grant.RoleID] {
				continue
			}
			definition, err := s.d.Store.Role(ctx, p.AccountID, grant.Role)
			if err != nil {
				return fmt.Errorf("read reviewer role: %w", err)
			}
			if definition.ID != grant.RoleID {
				return kernel.ErrUnavailable
			}
			seen[definition.ID] = true
			out.Roles = append(out.Roles, accessapi.ReviewRole{ID: definition.ID, Slug: definition.Slug, Name: definition.Name})
		}
		slices.SortFunc(out.Roles, func(a, b accessapi.ReviewRole) int { return strings.Compare(a.Slug, b.Slug) })
		return nil
	})
	if err != nil {
		return accessapi.ReviewMetadata{}, fmt.Errorf("access: read review metadata: %w", errors.Join(kernel.ErrUnavailable, err))
	}
	return out, nil
}
