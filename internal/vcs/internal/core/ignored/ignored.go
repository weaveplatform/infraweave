package ignored

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

const (
	NoAffectedFiles       = "NO_AFFECTED_FILES_IN_PROJECT_ROOT"
	TagsIgnored           = "TAGS_IGNORED_BY_DEFAULT"
	RunLimitReached       = "RUN_LIMIT_REACHED"
	CustomPolicy          = "CUSTOM_POLICY"
	CustomPolicyNoTrigger = "CUSTOM_POLICY_NO_TRIGGER"
	DisabledByLabel       = "DISABLED_BY_LABEL"
	Retention             = 7 * 24 * time.Hour
	Batch                 = 500
)

var (
	ErrNoTransaction = errors.New("vcs: ignored trigger write requires a transaction")
	ErrForbidden     = errors.New("vcs: ignored trigger search requires space admin")
	ErrRecord        = errors.New("vcs: invalid ignored trigger record")
)

var Fields = []search.Field{
	{Name: "createdAt", Type: search.Time, Orderable: true},
	{Name: "reasonType", Type: search.Enum, Values: reasons},
}

var reasons = []string{DisabledByLabel, TagsIgnored, NoAffectedFiles, CustomPolicy, CustomPolicyNoTrigger, RunLimitReached}

type Record struct {
	Account   kernel.AccountID
	ID, Stack kernel.ULID
	Commit    stacksapi.Commit
	Reason    string
	CreatedAt time.Time
}

type Scope struct {
	Account      kernel.AccountID
	Stack, Space kernel.ULID
	Since        time.Time
}

type Store interface {
	RecordIgnored(context.Context, Record) error
	SearchIgnored(context.Context, Scope, search.Input) (search.Result[Record], error)
	PruneIgnored(context.Context, *kernel.AccountID, *time.Time, int) (int, error)
	DeleteIgnoredStack(context.Context, kernel.AccountID, kernel.ULID) error
}

type (
	Deps struct {
		Store  Store
		Stacks stacksapi.Stacks
		Authz  accessapi.Authorizer
		Now    func() time.Time
	}
	Service struct{ d Deps }
)

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// Record joins the live evaluator's completion transaction. Its caller already
// holds the account and stack; this package never gates recording by tier.
func (s *Service) Record(ctx context.Context, account kernel.AccountID, stack kernel.ULID, commit stacksapi.Commit, reason string) error {
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	if account.IsZero() || stack.IsZero() || !validCommit(commit.Hash) || !slices.Contains(reasons, reason) {
		return ErrRecord
	}
	commit.Message, _, _ = strings.Cut(commit.Message, "\n")
	commit.Message = strings.TrimSuffix(commit.Message, "\r")
	if err := s.d.Store.RecordIgnored(ctx, Record{Account: account, ID: kernel.NewULID(), Stack: stack, Commit: commit, Reason: reason, CreatedAt: s.d.Now()}); err != nil {
		return fmt.Errorf("vcs: record ignored trigger: %w", err)
	}
	return nil
}

func validCommit(hash string) bool {
	if len(hash) != 40 && len(hash) != 64 || strings.Trim(hash, "0") == "" {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

// Search uses the current stack owner. Hidden stacks remain not found, and a
// reader without admin is forbidden, matching the shared operation controls.
func (s *Service) Search(ctx context.Context, p kernel.Principal, slug string, in search.Input) (search.Result[Record], error) {
	st, err := s.d.Stacks.BySlug(ctx, p.AccountID, slug)
	if err != nil {
		return search.Result[Record]{}, fmt.Errorf("vcs: ignored trigger stack: %w", err)
	}
	read, err := s.d.Authz.Authorize(ctx, p, accessapi.ActionSpaceRead, st.Space)
	if err != nil {
		return search.Result[Record]{}, fmt.Errorf("vcs: ignored trigger reach: %w", err)
	}
	if !read {
		return search.Result[Record]{}, stacksapi.ErrNotFound
	}
	admin, err := s.d.Authz.Authorize(ctx, p, accessapi.ActionSpaceAdmin, st.Space)
	if err != nil {
		return search.Result[Record]{}, fmt.Errorf("vcs: ignored trigger admin: %w", err)
	}
	if !admin {
		return search.Result[Record]{}, ErrForbidden
	}
	rows, err := s.d.Store.SearchIgnored(ctx, Scope{Account: p.AccountID, Stack: st.ID, Space: st.Space, Since: s.d.Now().Add(-Retention)}, in)
	if err != nil {
		return rows, fmt.Errorf("vcs: search ignored triggers: %w", err)
	}
	return rows, nil
}

func (s *Service) Pruned(ctx context.Context) error {
	before := s.d.Now().Add(-Retention)
	return s.prune(ctx, nil, &before)
}

func (s *Service) Purge(ctx context.Context, account kernel.AccountID) error {
	return s.prune(ctx, &account, nil)
}

func (s *Service) prune(ctx context.Context, account *kernel.AccountID, before *time.Time) error {
	for {
		n, err := s.d.Store.PruneIgnored(ctx, account, before, Batch)
		if err != nil {
			return fmt.Errorf("vcs: prune ignored triggers: %w", err)
		}
		if n < Batch {
			return nil
		}
	}
}

func (s *Service) LostOnMove(context.Context, stacksapi.StackMove) ([]string, error) { return nil, nil }

// Deleting runs after the stacks domain locks its owner row. An evaluator
// either committed before this deletion or can no longer acquire that owner.
func (s *Service) Deleting(ctx context.Context, st stacksapi.Stack) error {
	if !tx.In(ctx) {
		return ErrNoTransaction
	}
	return s.d.Store.DeleteIgnoredStack(ctx, st.Account, st.ID) //nolint:wrapcheck // the store names the owned delete
}
