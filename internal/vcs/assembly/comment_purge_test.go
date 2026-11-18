package assembly

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

func TestAccCommentAccountPurgePropagatesFailureAndRetries(t *testing.T) {
	w := newWorld(t)
	d, err := New(Deps{DB: w.db, Keys: w.keys, Settings: DefaultSettings()})
	if err != nil {
		t.Fatal(err)
	}
	foreign, integration, request := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	for _, row := range []struct {
		account kernel.AccountID
		count   int
	}{{w.acct, PurgeBatch + 1}, {foreign, 1}} {
		if _, err := w.db.Exec(t.Context(), `INSERT INTO vcs_pr_comments
			(account_id,id,integration_id,repository_id,pull_request,request_id,dedup_key,binding,reference)
			SELECT $1,gen_random_uuid(),$2,'7','7',$3,'policy:'||n,'{}',jsonb_build_object('id',n::text)
			FROM generate_series(1,$4::integer) n`, row.account, integration, request, row.count); err != nil {
			t.Fatal(err)
		}
	}
	// The published capability must refuse a missing owner before reading a
	// target or creating a queue job; this domain has no account-hold adapter.
	err = tx.Do(t.Context(), w.db, tx.Options{}, func(ctx context.Context) error {
		return d.Comments().Upsert(ctx, w.acct, vcsapi.PullRequestComment{PullRequest: request, DedupKey: "policy:owner", Body: "notice"})
	})
	if !errors.Is(err, accessapi.ErrNoAccount) {
		t.Fatal("public comment enqueue bypassed its owner fence", err)
	}
	assertRows := func(home, other int) {
		t.Helper()
		var actualHome, actualOther, jobs int
		if err := w.db.QueryRow(t.Context(), `SELECT
			(SELECT count(*) FROM vcs_pr_comments WHERE account_id=$1),
			(SELECT count(*) FROM vcs_pr_comments WHERE account_id=$2),
			(SELECT count(*) FROM jobs WHERE kind='vcs.report_comment')`, w.acct, foreign).Scan(&actualHome, &actualOther, &jobs); err != nil ||
			actualHome != home || actualOther != other || jobs != 0 {
			t.Fatalf("purge/owner scope: home=%d other=%d jobs=%d err=%v", actualHome, actualOther, jobs, err)
		}
	}
	if _, err := w.db.Exec(t.Context(), `CREATE FUNCTION refuse_comment_purge() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'comment purge refused' USING ERRCODE='P0001'; END $$;
		CREATE TRIGGER refuse_comment_purge BEFORE DELETE ON vcs_pr_comments
		FOR EACH ROW EXECUTE FUNCTION refuse_comment_purge()`); err != nil {
		t.Fatal(err)
	}
	err = d.Purge(t.Context(), w.acct)
	cause, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || cause.Code != "P0001" || cause.Message != "comment purge refused" {
		t.Fatal("domain purge hid the comment cleanup refusal", err)
	}
	assertRows(PurgeBatch+1, 1)
	if _, err := w.db.Exec(t.Context(), `DROP TRIGGER refuse_comment_purge ON vcs_pr_comments`); err != nil {
		t.Fatal(err)
	}
	if err := d.Purge(t.Context(), w.acct); err != nil {
		t.Fatal("comment purge could not resume", err)
	}
	assertRows(0, 1)
	if err := d.Purge(t.Context(), w.acct); err != nil {
		t.Fatal("repeated comment purge is not idempotent", err)
	}
	assertRows(0, 1)
}
