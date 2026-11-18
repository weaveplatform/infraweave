package pg_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/source"
)

// This fixture implements the owned Stacks port over transactional rows, while
// the real VCS store supplies integration metadata, locks and native identities.
type sourceAliasStacks struct {
	stacksapi.Stacks
	db      tx.Querier
	tracked int
	inside  bool
	fail    bool
}

func (s *sourceAliasStacks) Settings(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error) {
	var st stacksapi.Settings
	var binding kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT namespace,name,binding FROM source_alias_stacks WHERE account_id=$1 AND id=$2`, acct, id).
		Scan(&st.Namespace, &st.Repository, &binding)
	st.Provider, st.Branch = "GITHUB_ENTERPRISE", "main"
	if !binding.IsZero() {
		st.VCSIntegration = &binding
	}
	return st, err
}

func (s *sourceAliasStacks) SetTracked(ctx context.Context, acct kernel.AccountID, id kernel.ULID, head, commit *stacksapi.Commit, _ string) error {
	s.tracked++
	s.inside = tx.In(ctx)
	if s.fail {
		s.fail = false
		return errDown
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE source_alias_stacks SET head=$3,tracked=$4 WHERE account_id=$1 AND id=$2`, acct, id, head.Hash, commit.Hash)
	return err
}

func (s *sourceAliasStacks) RepointVCSRepository(ctx context.Context, acct kernel.AccountID, use stacksapi.Use, from, to stacksapi.VCSRepository) error {
	if !tx.In(ctx) {
		return errDown
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE source_alias_stacks SET namespace=$5,name=$6
		WHERE account_id=$1 AND namespace=$3 AND name=$4 AND (binding=$2 OR (binding IS NULL AND $7))`,
		acct, use.Integration, from.Namespace, from.Name, to.Namespace, to.Name, use.Default)
	return err
}

type sourceAliasProvider struct {
	port.Source
	calls int
}

func (s *sourceAliasProvider) ResolveRepositoryRef(ctx context.Context, _ port.Repository, _ string) (port.Resolution, error) {
	if tx.In(ctx) {
		return port.Resolution{}, errDown
	}
	s.calls++
	return port.Resolution{Repository: vcsapi.Repository{ID: "7", Namespace: "canonical", Name: "infra"}, Commit: vcsapi.Commit{Hash: "head"}}, nil
}

type switchedSourceBinding struct {
	*integration.Service
	switchDefault func() error
}

func (b switchedSourceBinding) EventUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Use, bool, error) {
	use, found, err := b.Service.EventUse(ctx, acct, id)
	if err == nil {
		err = b.switchDefault()
	}
	return use, found, err
}

func (b switchedSourceBinding) HeldEventUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Use, bool, error) {
	if err := b.switchDefault(); err != nil {
		return stacksapi.Use{}, false, err
	}
	return b.Service.HeldEventUse(ctx, acct, id)
}

func TestAccSourceAliasReadsHeldCurrentDefault(t *testing.T) {
	for _, initial := range []bool{false, true} {
		for _, target := range []string{"named", "implicit", "failed-pointer"} {
			t.Run(fmt.Sprintf("initial-default-%t/%s", initial, target), func(t *testing.T) {
				w := newWebhookWorld(t)
				other := kernel.NewULID()
				if _, err := w.f.db.Exec(t.Context(), `INSERT INTO vcs_integrations
					(account_id,id,provider,slug,name,space_id) VALUES($1,$2,'GITHUB_ENTERPRISE','other','Other',$1)`, w.f.acct, other); err != nil {
					t.Fatal(err)
				}
				setDefault := func(id kernel.ULID) error {
					return tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
						if _, err := tx.Q(ctx, w.f.db).Exec(ctx, `UPDATE vcs_integrations SET is_default=false WHERE account_id=$1`, w.f.acct); err != nil {
							return err
						}
						_, err := tx.Q(ctx, w.f.db).Exec(ctx, `UPDATE vcs_integrations SET is_default=true WHERE account_id=$1 AND id=$2`, w.f.acct, id)
						return err
					})
				}
				before, after := other, w.id
				if initial {
					before, after = w.id, other
				}
				if err := setDefault(before); err != nil {
					t.Fatal(err)
				}
				if _, err := w.f.db.Exec(t.Context(), `CREATE TABLE source_alias_stacks(
					account_id uuid NOT NULL,id uuid PRIMARY KEY,binding uuid,namespace text NOT NULL,name text NOT NULL,
					head text NOT NULL DEFAULT 'seed-head',tracked text NOT NULL DEFAULT 'seed-tracked')`); err != nil {
					t.Fatal(err)
				}
				ids := []kernel.ULID{kernel.NewULID(), kernel.NewULID(), kernel.NewULID()}
				for n, binding := range []kernel.ULID{{}, w.id, other} {
					if _, err := w.f.db.Exec(t.Context(), `INSERT INTO source_alias_stacks(account_id,id,binding,namespace,name)
						VALUES($1,$2,$3,'alias','infra')`, w.f.acct, ids[n], binding); err != nil {
						t.Fatal(err)
					}
				}
				stack := ids[1]
				if target == "implicit" {
					stack = ids[0]
				}
				rows := &sourceAliasStacks{db: w.f.db, fail: target == "failed-pointer"}
				native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: rows, Now: time.Now}
				provider := &sourceAliasProvider{}
				bindings := switchedSourceBinding{Service: w.f.svc, switchDefault: func() error { return setDefault(after) }}
				svc := source.New(source.Deps{
					DB: w.f.db, Stacks: rows, Integrations: bindings, Identities: native,
					Providers: map[string]port.Source{"GITHUB_ENTERPRISE": provider}, Now: time.Now,
				})
				err := svc.Track(t.Context(), w.f.acct, stack)
				failed := target == "failed-pointer"
				if failed && !errors.Is(err, errDown) || !failed && err != nil {
					t.Fatal(err)
				}
				for n, id := range ids {
					var namespace, name, head, tracked string
					if err := w.f.db.QueryRow(t.Context(), `SELECT namespace,name,head,tracked FROM source_alias_stacks WHERE id=$1`, id).
						Scan(&namespace, &name, &head, &tracked); err != nil {
						t.Fatal(err)
					}
					changed := target == "named" && (n == 1 || n == 0 && !initial)
					if (namespace == "canonical") != changed || name != "infra" {
						t.Error("source alias crossed or missed current default binding", n, namespace, name)
					}
					if target == "named" && id == stack {
						if head != "head" || tracked != "head" {
							t.Error("named source lost pointer initialization", head, tracked)
						}
					} else if head != "seed-head" || tracked != "seed-tracked" {
						t.Error("stale implicit binding or failed transaction advanced pointers", n, head, tracked)
					}
				}
				wantWrites := 1
				if target == "implicit" {
					wantWrites = 0
				}
				if provider.calls != 1 || rows.tracked != wantWrites || wantWrites > 0 && !rows.inside {
					t.Error("native read repeated or pointer write escaped binding fence", provider.calls, rows.tracked, rows.inside)
				}
				if failed {
					if _, found, err := w.f.store.RepositoryIdentity(t.Context(), w.f.acct, w.id, "7"); err != nil || found {
						t.Fatal("failed pointer write committed native map", found, err)
					}
					if err := svc.Track(t.Context(), w.f.acct, stack); err != nil {
						t.Fatal("retry could not initialize canonical source", err)
					}
					var namespace, tracked string
					if err := w.f.db.QueryRow(t.Context(), `SELECT namespace,tracked FROM source_alias_stacks WHERE id=$1`, stack).
						Scan(&namespace, &tracked); err != nil || namespace != "canonical" ||
						tracked != "head" {
						t.Fatal("retry lost canonical source/pointers", namespace, tracked, err)
					}
				}
			})
		}
	}
}
