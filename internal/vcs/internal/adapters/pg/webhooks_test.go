package pg_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type rotationSnapshot struct {
	current, previous []byte
	until             *time.Time
	version           int64
}

func snapshot(t *testing.T, f *fixture, id kernel.ULID) rotationSnapshot {
	t.Helper()
	var out rotationSnapshot
	if err := f.db.QueryRow(t.Context(), `SELECT webhook_secret_sealed,previous_webhook_secret_sealed,
		previous_secret_valid_until,version FROM vcs_integrations WHERE account_id=$1 AND id=$2`, f.acct, id).
		Scan(&out.current, &out.previous, &out.until, &out.version); err != nil {
		t.Fatal(err)
	}
	return out
}

func sameSnapshot(a, b rotationSnapshot) bool {
	return bytes.Equal(a.current, b.current) && bytes.Equal(a.previous, b.previous) && a.version == b.version &&
		(a.until == nil && b.until == nil || a.until != nil && b.until != nil && a.until.Equal(*b.until))
}

func TestAccWebhookRotation(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	in, err := f.svc.Create(ctx, f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1_700_000_000, 0).UTC()
	f.deps.Now = func() time.Time { return clock }
	f.deps.SecretOverlap = 90 * time.Minute
	svc := integration.New(f.deps)
	initial := snapshot(t, f, in.ID)
	rotated, err := svc.RotateWebhookSecret(ctx, f.admin, in.ID.String(), integration.GitLab, "192.0.2.1")
	if err != nil || rotated.Version != in.Version+1 || !rotated.UpdatedAt.Equal(clock) {
		t.Fatalf("rotation: %+v %v", rotated, err)
	}
	first := snapshot(t, f, in.ID)
	if bytes.Equal(first.current, initial.current) || bytes.Equal(first.previous, initial.current) || first.until == nil ||
		!first.until.Equal(clock.Add(90*time.Minute)) {
		t.Fatal("rotation did not reseal the old column or set the overlap")
	}
	columns := pg.SealedColumns()
	column := func(name string) crypt.Column {
		for _, col := range columns {
			if col.Name == name {
				return col
			}
		}
		t.Fatalf("missing sealed column %s", name)
		return crypt.Column{}
	}
	key := []string{f.acct.UUID(), in.ID.UUID()}
	current, err := f.keys.OpenColumn(ctx, column("webhook_secret_sealed"), key, first.current)
	if err != nil {
		t.Fatal(err)
	}
	entropy, err := hex.DecodeString(string(current.Reveal()))
	if err != nil || len(entropy) != 32 {
		t.Fatal("new secret is not 32 bytes of printable entropy")
	}
	previous, err := f.keys.OpenColumn(ctx, column("previous_webhook_secret_sealed"), key, first.previous)
	if err != nil || string(previous.Reveal()) != "hook-secret" {
		t.Fatal("previous secret failed its column AAD", err)
	}
	for _, bad := range []struct {
		col    crypt.Column
		key    []string
		sealed []byte
	}{
		{column("webhook_secret_sealed"), key, first.previous},
		{column("previous_webhook_secret_sealed"), key, first.current},
		{column("previous_webhook_secret_sealed"), []string{f.acct.UUID(), kernel.NewULID().UUID()}, first.previous},
		{column("previous_webhook_secret_sealed"), []string{kernel.NewAccountID().UUID(), in.ID.UUID()}, first.previous},
	} {
		if _, err := f.keys.OpenColumn(ctx, bad.col, bad.key, bad.sealed); err == nil {
			t.Fatal("moved webhook ciphertext opened")
		}
	}
	reader := pg.NewDeliveries(f.db, f.keys)
	for _, at := range []time.Time{clock, clock.Add(90*time.Minute - time.Nanosecond), clock.Add(90 * time.Minute)} {
		read, err := reader.Integration(ctx, in.ID, at)
		want := 2
		if !at.Before(*first.until) {
			want = 1
		}
		if err != nil || len(read.Secrets(at)) != want || !bytes.Equal(read.Current, current.Reveal()) {
			t.Fatal("intake overlap lookup", want, err)
		}
	}
	clock = clock.Add(30 * time.Minute)
	if _, err := svc.RotateWebhookSecret(ctx, f.admin, in.ID.String(), integration.GitLab, "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	second, err := reader.Integration(ctx, in.ID, clock)
	if err != nil || !bytes.Equal(second.Previous, current.Reveal()) || bytes.Equal(second.Current, current.Reveal()) ||
		second.PreviousUntil == nil || !second.PreviousUntil.Equal(clock.Add(90*time.Minute)) {
		t.Fatal("second rotation retained an older previous secret", err)
	}
	var audits int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM audit_trail_entries WHERE account_id=$1 AND
		action='vcs_integration.rotate_webhook_secret' AND actor_username=$2 AND event_type='UPDATE'
		AND affected_type='VCS_INTEGRATION' AND affected_id=$3 AND remote_ip='192.0.2.1' AND body IS NULL`,
		f.acct, f.admin.Login, in.ID.String()).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("rotation audit", audits, err)
	}
	for _, secret := range [][]byte{[]byte("hook-secret"), current.Reveal(), second.Current} {
		var leaked int
		if err := f.db.QueryRow(ctx, `SELECT count(*) FROM audit_trail_entries e WHERE account_id=$1 AND strpos(e::text,$2)>0`, f.acct, string(secret)).
			Scan(&leaked); err != nil ||
			leaked != 0 {
			t.Fatal("secret leaked to audit", err)
		}
	}
}

type auditRefusal struct {
	integration.Store
	id       kernel.ULID
	account  kernel.AccountID
	version  int64
	sawWrite bool
}

func (s *auditRefusal) Audit(ctx context.Context, _ kernel.AuditEntry) error {
	in, err := s.Get(ctx, s.account, s.id, false)
	if err != nil {
		return err
	}
	s.sawWrite = in.Version == s.version+1
	return errDown
}

type (
	rollbackRotationDB struct{ *pgxpool.Pool }
	rollbackRotationTx struct{ pgx.Tx }
)

var errRotationCommit = errors.New("rotation commit refused")

func (d rollbackRotationDB) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	transaction, err := d.Pool.BeginTx(ctx, opts)
	return rollbackRotationTx{Tx: transaction}, err
}

func (t rollbackRotationTx) Commit(ctx context.Context) error {
	if err := t.Rollback(ctx); err != nil {
		return err
	}
	return errRotationCommit
}

func TestAccWebhookRotationAtomicity(t *testing.T) {
	f := newFixture(t)
	in, err := f.svc.Create(t.Context(), f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, f, in.ID)
	audit := &auditRefusal{Store: f.store, id: in.ID, account: f.acct, version: in.Version}
	deps := f.deps
	deps.Store = audit
	if _, err := integration.New(deps).RotateWebhookSecret(t.Context(), f.admin, in.ID.String(), integration.GitLab, ""); !errors.Is(err, errDown) || !audit.sawWrite {
		t.Fatalf("audit rollback did not reach the secret write: saw=%t %v", audit.sawWrite, err)
	}
	if !sameSnapshot(before, snapshot(t, f, in.ID)) {
		t.Fatal("audit failure committed part of a rotation")
	}
	deps = f.deps
	deps.DB = rollbackRotationDB{Pool: f.db}
	if _, err := integration.New(deps).RotateWebhookSecret(t.Context(), f.admin, in.ID.String(), integration.GitLab, ""); !errors.Is(err, errRotationCommit) {
		t.Fatal(err)
	}
	if !sameSnapshot(before, snapshot(t, f, in.ID)) {
		t.Fatal("commit failure retained part of a rotation")
	}
	var audits int
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM audit_trail_entries WHERE account_id=$1`, f.acct).Scan(&audits); err != nil || audits != 0 {
		t.Fatal("failed rotation retained audit", audits, err)
	}
	for _, provider := range []string{integration.BitbucketCloud, "unknown"} {
		if _, err := f.svc.RotateWebhookSecret(t.Context(), f.admin, in.ID.String(), provider, ""); !errors.Is(err, integration.ErrNotFound) {
			t.Fatal("wrong provider", err)
		}
	}
	missing := f.admin
	missing.AccountID = kernel.NewAccountID()
	if _, err := f.svc.RotateWebhookSecret(t.Context(), missing, in.ID.String(), integration.GitLab, ""); !errors.Is(err, integration.ErrNotFound) {
		t.Fatal("foreign account", err)
	}
	if !sameSnapshot(before, snapshot(t, f, in.ID)) {
		t.Fatal("refused rotation changed secrets")
	}
}

type rotationCredentialRefusal struct {
	*pg.Store
	version  int64
	sawWrite bool
}

func (s *rotationCredentialRefusal) Secrets(ctx context.Context, account kernel.AccountID, id kernel.ULID) (integration.Secrets, error) {
	in, err := s.Get(ctx, account, id, false)
	if err != nil {
		return integration.Secrets{}, err
	}
	s.sawWrite = in.Version == s.version+1
	return integration.Secrets{}, errDown
}

func TestAccWebhookRotationCredentialSnapshotAtomicity(t *testing.T) {
	f := newFixture(t)
	in, err := f.svc.Create(t.Context(), f.admin, integration.Input{
		Provider: integration.GitHubEnterprise, Name: "App", Space: "acme", IsDefault: true,
		Settings: port.Settings{APIHost: "https://api.github.com", AppID: "42"},
		Secrets:  &integration.Secrets{Credential: appKey(), WebhookSecret: []byte("hook-secret")},
	})
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, f, in.ID)
	store := &rotationCredentialRefusal{Store: f.store, version: in.Version}
	deps := f.deps
	deps.Store = store
	out, err := integration.New(deps).RotateWebhookSecret(t.Context(), f.admin, in.ID.String(), integration.GitHubEnterprise, "")
	if !errors.Is(err, errDown) || !store.sawWrite || !out.ID.IsZero() {
		t.Fatalf("credential failure did not discard the transaction result: saw=%t err=%v", store.sawWrite, err)
	}
	if !sameSnapshot(before, snapshot(t, f, in.ID)) {
		t.Fatal("credential snapshot failure committed part of the rotation")
	}
	var audits int
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM audit_trail_entries WHERE account_id=$1`, f.acct).Scan(&audits); err != nil || audits != 0 {
		t.Fatal("credential snapshot failure retained audit", audits, err)
	}
}

type rotationSealFailure struct {
	pg.Sealer
	column string
	open   bool
}

func (s rotationSealFailure) SealColumn(ctx context.Context, c crypt.Column, key []string, plain []byte) ([]byte, error) {
	if !s.open && c.Name == s.column {
		return nil, errDown
	}
	return s.Sealer.SealColumn(ctx, c, key, plain)
}

func (s rotationSealFailure) OpenColumn(ctx context.Context, c crypt.Column, key []string, sealed []byte) (crypt.Value, error) {
	if s.open && c.Name == s.column {
		return crypt.Value{}, errDown
	}
	return s.Sealer.OpenColumn(ctx, c, key, sealed)
}

func TestAccWebhookRotationSealingFailures(t *testing.T) {
	f := newFixture(t)
	in, err := f.svc.Create(t.Context(), f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, f, in.ID)
	for _, fault := range []rotationSealFailure{
		{Sealer: f.keys, column: "webhook_secret_sealed", open: true},
		{Sealer: f.keys, column: "previous_webhook_secret_sealed"},
		{Sealer: f.keys, column: "webhook_secret_sealed"},
	} {
		deps := f.deps
		deps.Store = pg.New(f.db, fault)
		if _, err := integration.New(deps).RotateWebhookSecret(t.Context(), f.admin, in.ID.String(), integration.GitLab, ""); !errors.Is(err, errDown) {
			t.Fatal(err)
		}
		if !sameSnapshot(before, snapshot(t, f, in.ID)) {
			t.Fatal("sealing failure changed a rotation")
		}
	}
	input := gitlab("Empty old secret", "", false)
	input.Secrets.WebhookSecret = nil
	empty, err := f.svc.Create(t.Context(), f.admin, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RotateWebhookSecret(t.Context(), f.admin, empty.ID.String(), integration.GitLab, ""); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, f, empty.ID); len(after.previous) != 0 || len(after.current) == 0 {
		t.Fatal("initial secret fabricated a previous secret")
	}
}
