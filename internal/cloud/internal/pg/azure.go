package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// AzureStore keeps Azure integrations and their attachments in
// PostgreSQL. Every method joins the transaction ctx carries.
type AzureStore struct{ db DB }

// NewAzure returns the Azure store on db.
func NewAzure(db DB) *AzureStore { return &AzureStore{db: db} }

var _ core.AzureStore = (*AzureStore)(nil)

// azureColumns are an Azure integration's columns as scanAzure reads
// them.
const azureColumns = `account_id, id, space_id, name, tenant_id, application_id, object_id,
	display_name, service_principal_object_id, default_subscription_id, labels, autoattach_enabled,
	admin_consent_provided, consent_actor, consent_at, created_at, updated_at, version`

func scanAzure(row pgx.Row) (core.AzureIntegration, error) {
	var i core.AzureIntegration
	err := row.Scan(&i.Account, &i.ID, &i.Space, &i.Name, &i.TenantID, &i.ApplicationID,
		&i.ObjectID, &i.DisplayName, &i.ServicePrincipal, &i.DefaultSubscription, &i.Labels,
		&i.Autoattach, &i.ConsentProvided, &i.ConsentActor, &i.ConsentAt, &i.CreatedAt,
		&i.UpdatedAt, &i.Version)
	return i, err //nolint:wrapcheck // callers name the read
}

// The Azure constraints whose violation is a refusal rather than a
// failure.
const (
	azureUniqueName       = "azure_integrations_name"
	azureUniqueAttachment = "azure_integration_attachments_pair"
	azureUniqueRead       = "azure_integration_attachments_read"
	azureUniqueWrite      = "azure_integration_attachments_write"
)

// azureWritten answers a write of i's error: a *core.NameTaken for its
// name.
func azureWritten(err error, i core.AzureIntegration) error {
	switch {
	case violated(err) == azureUniqueName:
		return &core.NameTaken{Kind: "Azure integration", Name: i.Name}
	case err != nil:
		return fmt.Errorf("cloud: write Azure integration %s: %w", i.ID, sql.Unavailable(err))
	}
	return nil
}

// Create writes a new integration.
func (s *AzureStore) Create(ctx context.Context, i core.AzureIntegration) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO azure_integrations (`+azureColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		i.Account, i.ID, i.Space, i.Name, i.TenantID, i.ApplicationID, i.ObjectID, i.DisplayName,
		i.ServicePrincipal, i.DefaultSubscription, i.Labels, i.Autoattach, i.ConsentProvided,
		i.ConsentActor, i.ConsentAt, i.CreatedAt, i.UpdatedAt, i.Version)
	return azureWritten(err, i)
}

// Write replaces the integration's fields but its tenant and its
// application, and sets its version.
func (s *AzureStore) Write(ctx context.Context, i core.AzureIntegration) error {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE azure_integrations SET space_id = $3, name = $4,
			service_principal_object_id = $5, default_subscription_id = $6, labels = $7,
			autoattach_enabled = $8, admin_consent_provided = $9, consent_actor = $10,
			consent_at = $11, updated_at = $12, version = $13
		WHERE account_id = $1 AND id = $2`,
		i.Account, i.ID, i.Space, i.Name, i.ServicePrincipal, i.DefaultSubscription, i.Labels,
		i.Autoattach, i.ConsentProvided, i.ConsentActor, i.ConsentAt, i.UpdatedAt, i.Version)
	if err := azureWritten(err, i); err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Azure integration %s", core.ErrNotFound, i.ID)
	}
	return nil
}

// Get reads acct's integration id.
func (s *AzureStore) Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock core.Lock) (core.AzureIntegration, error) {
	return s.one(ctx, `SELECT `+azureColumns+` FROM azure_integrations
		WHERE account_id = $1 AND id = $2`+locks[lock], id.String(), acct, id)
}

// ByName reads acct's integration name.
func (s *AzureStore) ByName(ctx context.Context, acct kernel.AccountID, name string) (core.AzureIntegration, error) {
	return s.one(ctx, `SELECT `+azureColumns+` FROM azure_integrations
		WHERE account_id = $1 AND name = $2`, name, acct, name)
}

func (s *AzureStore) one(ctx context.Context, q, what string, args ...any) (core.AzureIntegration, error) {
	i, err := scanAzure(tx.Q(ctx, s.db).QueryRow(ctx, q, args...))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return core.AzureIntegration{}, fmt.Errorf("%w: Azure integration %s", core.ErrNotFound, what)
	case err != nil:
		return core.AzureIntegration{}, fmt.Errorf("cloud: read Azure integration %s: %w", what,
			sql.Unavailable(err))
	}
	return i, nil
}

func (s *AzureStore) many(ctx context.Context, q string, args ...any) ([]core.AzureIntegration, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("cloud: read Azure integrations: %w", sql.Unavailable(err))
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.AzureIntegration, error) {
		return scanAzure(row)
	})
	if err != nil {
		return nil, fmt.Errorf("cloud: read Azure integrations: %w", sql.Unavailable(err))
	}
	return all, nil
}

// ByIDs reads those of acct's integrations ids that it has, keyed by ID.
func (s *AzureStore) ByIDs(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]core.AzureIntegration, error) {
	all, err := s.many(ctx, `SELECT `+azureColumns+` FROM azure_integrations
		WHERE account_id = $1 AND id = ANY($2)`, acct, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[kernel.ULID]core.AzureIntegration, len(all))
	for _, i := range all {
		out[i.ID] = i
	}
	return out, nil
}

// List reads a page of acct's integrations in spaces, by name in byte
// order, which the names' unique key keeps total.
func (s *AzureStore) List(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, page core.Page) ([]core.AzureIntegration, bool, error) {
	all, err := s.many(ctx, `SELECT `+azureColumns+` FROM azure_integrations
		WHERE account_id = $1 AND space_id = ANY($2) AND name COLLATE "C" > $3
		ORDER BY name COLLATE "C" LIMIT $4`, acct, spaces, page.After, page.Limit+1)
	if err != nil {
		return nil, false, err
	}
	if len(all) > page.Limit {
		return all[:page.Limit], true, nil
	}
	return all, false, nil
}

// Labelled reads acct's integrations in spaces with auto-attachment
// enabled and a default subscription that carry any of labels, through
// the labels' GIN index.
func (s *AzureStore) Labelled(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string) ([]core.AzureIntegration, error) {
	return s.many(ctx, `SELECT `+azureColumns+` FROM azure_integrations
		WHERE account_id = $1 AND space_id = ANY($2) AND autoattach_enabled
			AND default_subscription_id IS NOT NULL AND labels && $3::text[]
		ORDER BY name COLLATE "C"`, acct, spaces, labels)
}

// InSpace reports whether an integration of acct lives in space.
func (s *AzureStore) InSpace(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error) {
	var in bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM azure_integrations
		WHERE account_id = $1 AND space_id = $2)`, acct, space).Scan(&in)
	if err != nil {
		return false, fmt.Errorf("cloud: read the Azure integrations of space %s: %w", space,
			sql.Unavailable(err))
	}
	return in, nil
}

// Count is how many integrations acct has.
func (s *AzureStore) Count(ctx context.Context, acct kernel.AccountID) (int, error) {
	return count(ctx, s.db, `SELECT count(*) FROM azure_integrations WHERE account_id = $1`, acct)
}

// count answers the count q reads of acct.
func count(ctx context.Context, db DB, q string, acct kernel.AccountID) (int, error) {
	var n int
	if err := tx.Q(ctx, db).QueryRow(ctx, q, acct).Scan(&n); err != nil {
		return 0, fmt.Errorf("cloud: count account %s's integrations: %w", acct, sql.Unavailable(err))
	}
	return n, nil
}

// Oldest reads up to n of acct's integrations, oldest first.
func (s *AzureStore) Oldest(ctx context.Context, acct kernel.AccountID, n int) ([]core.AzureIntegration, error) {
	return s.many(ctx, `SELECT `+azureColumns+` FROM azure_integrations
		WHERE account_id = $1 ORDER BY created_at, id LIMIT $2`, acct, n)
}

// Delete deletes acct's integration id; its attachments cascade.
func (s *AzureStore) Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx,
		`DELETE FROM azure_integrations WHERE account_id = $1 AND id = $2`, acct, id)
	if err != nil {
		return fmt.Errorf("cloud: delete Azure integration %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// azureAttachmentColumns are an Azure attachment's columns as
// scanAzureAttachment reads them.
const azureAttachmentColumns = `account_id, id, integration_id, stack_id, read, write,
	subscription_id, created_at`

func scanAzureAttachment(row pgx.Row) (core.Attachment, error) {
	var a core.Attachment
	err := row.Scan(&a.Account, &a.ID, &a.Integration, &a.Stack, &a.Read, &a.Write,
		&a.Subscription, &a.CreatedAt)
	return a, err //nolint:wrapcheck // callers name the read
}

// azureConflict answers a write of an attachment of stack's error: a
// *core.Conflict for each unique key it breaks.
func azureConflict(err error, stack kernel.ULID) error {
	switch violated(err) {
	case "":
	case azureUniqueRead:
		return &core.Conflict{Detail: "the stack has a read Azure attachment already; " +
			"a stack has one read and one write Azure attachment"}
	case azureUniqueWrite:
		return &core.Conflict{Detail: "the stack has a write Azure attachment already; " +
			"a stack has one read and one write Azure attachment"}
	case azureUniqueAttachment:
		return &core.Conflict{Detail: "the Azure integration is attached to the stack already"}
	}
	if err != nil {
		return fmt.Errorf("cloud: attach an Azure integration to stack %s: %w", stack, sql.Unavailable(err))
	}
	return nil
}

// Attach writes a.
func (s *AzureStore) Attach(ctx context.Context, a core.Attachment) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO azure_integration_attachments (`+
		azureAttachmentColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		a.Account, a.ID, a.Integration, a.Stack, a.Read, a.Write, a.Subscription, a.CreatedAt)
	return azureConflict(err, a.Stack)
}

// SetAttachment sets the sides and the subscription of acct's attachment
// id.
func (s *AzureStore) SetAttachment(ctx context.Context, acct kernel.AccountID, id kernel.ULID, read, write bool, subscription *string) error {
	var stack kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `UPDATE azure_integration_attachments
		SET read = $3, write = $4, subscription_id = $5 WHERE account_id = $1 AND id = $2
		RETURNING stack_id`, acct, id, read, write, subscription).Scan(&stack)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: attachment %s", core.ErrNotFound, id)
	}
	return azureConflict(err, stack)
}

// Attachment reads acct's attachment id.
func (s *AzureStore) Attachment(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (core.Attachment, error) {
	a, err := scanAzureAttachment(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+azureAttachmentColumns+`
		FROM azure_integration_attachments WHERE account_id = $1 AND id = $2`, acct, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return core.Attachment{}, fmt.Errorf("%w: attachment %s", core.ErrNotFound, id)
	case err != nil:
		return core.Attachment{}, fmt.Errorf("cloud: read Azure attachment %s: %w", id,
			sql.Unavailable(err))
	}
	return a, nil
}

// Detach deletes acct's attachment id.
func (s *AzureStore) Detach(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx,
		`DELETE FROM azure_integration_attachments WHERE account_id = $1 AND id = $2`, acct, id)
	if err != nil {
		return fmt.Errorf("cloud: detach Azure attachment %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// Attachments reads the attachments of integrations, or of stacks when
// byStack is set, keyed by that ID, oldest first.
func (s *AzureStore) Attachments(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID, byStack bool) (map[kernel.ULID][]core.Attachment, error) {
	key := "integration_id"
	if byStack {
		key = "stack_id"
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+azureAttachmentColumns+`
		FROM azure_integration_attachments WHERE account_id = $1 AND `+key+` = ANY($2)
		ORDER BY created_at, id`, acct, ids)
	if err != nil {
		return nil, fmt.Errorf("cloud: read Azure attachments: %w", sql.Unavailable(err))
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Attachment, error) {
		return scanAzureAttachment(row)
	})
	if err != nil {
		return nil, fmt.Errorf("cloud: read Azure attachments: %w", sql.Unavailable(err))
	}
	out := map[kernel.ULID][]core.Attachment{}
	for _, a := range all {
		k := a.Integration
		if byStack {
			k = a.Stack
		}
		out[k] = append(out[k], a)
	}
	return out, nil
}

// AttachedStacks lists the stacks an attachment of acct names.
func (s *AzureStore) AttachedStacks(ctx context.Context, acct kernel.AccountID) ([]kernel.ULID, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT DISTINCT stack_id
		FROM azure_integration_attachments WHERE account_id = $1`, acct)
	if err != nil {
		return nil, fmt.Errorf("cloud: read Azure attached stacks: %w", sql.Unavailable(err))
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[kernel.ULID])
	if err != nil {
		return nil, fmt.Errorf("cloud: read Azure attached stacks: %w", sql.Unavailable(err))
	}
	return ids, nil
}

// DeleteStack deletes stack's attachments.
func (s *AzureStore) DeleteStack(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx,
		`DELETE FROM azure_integration_attachments WHERE account_id = $1 AND stack_id = $2`, acct, stack)
	if err != nil {
		return fmt.Errorf("cloud: delete stack %s's Azure attachments: %w", stack, sql.Unavailable(err))
	}
	return nil
}
