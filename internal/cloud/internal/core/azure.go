package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// AzureIntegration is an Azure integration: a dedicated multi-tenant
// Entra application the installation created for a customer's tenant,
// which runs of the stacks it attaches to act as once the tenant's admin
// consents. TenantID never changes after creation; ApplicationID and
// ObjectID are the application's client and object IDs, and DisplayName
// its generated name. ServicePrincipal is the application's service
// principal in the customer's tenant, nil until the application first
// gets credentials there. ConsentProvided is an admin's statement that
// consent was granted, never checked against Entra, and ConsentActor and
// ConsentAt name who made the latest statement, or cleared it, and when.
// An integration with Autoattach enabled has a DefaultSubscription, which
// its automatic attachments use.
type AzureIntegration struct {
	Account             kernel.AccountID
	ID                  kernel.ULID
	Space               kernel.ULID
	Name                string
	TenantID            string
	ApplicationID       string
	ObjectID            string
	DisplayName         string
	ServicePrincipal    *string
	DefaultSubscription *string
	Labels              []string
	Autoattach          bool
	ConsentProvided     bool
	ConsentActor        *string
	ConsentAt           *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Version             int64
}

// AzureInput is what a create or an update sets of an Azure integration.
// Space is the space's slug, the account's top space when empty; TenantID
// is read on create alone.
type AzureInput struct {
	Name                string
	Space               string
	TenantID            string
	DefaultSubscription *string
	Labels              []string
	Autoattach          bool
}

// Application is an Entra application the installation created: its
// client ID and the object ID Graph addresses it by.
type Application struct {
	AppID    string
	ObjectID string
}

// Applications is the installation's Microsoft Graph client: Create
// creates a multi-tenant application named displayName, and Delete
// deletes one by its object ID, an application already gone included.
// Either answers a *GraphFailure when Graph fails beyond its retries.
type Applications interface {
	Create(ctx context.Context, displayName string) (Application, error)
	Delete(ctx context.Context, objectID string) error
}

// GraphFailure is Microsoft Graph failing a call beyond its retries: its
// HTTP status, zero when no answer came, and Graph's error code.
type GraphFailure struct {
	Status int
	Code   string
	Err    error
}

func (e *GraphFailure) Error() string { return "cloud: microsoft graph: " + e.Err.Error() }

func (e *GraphFailure) Unwrap() error { return e.Err }

// ErrAzureUnsupported reports an operation the installation cannot serve
// because it has no application registered to create Entra applications
// with.
var ErrAzureUnsupported = errors.New("cloud: the installation has no Entra application in cloud.azure")

// AdminConsentURL is the URL an admin of tenant grants consent to the
// application whose client ID is app at.
//
// ref: report 12 § "3.1 Behaviour"
func AdminConsentURL(tenant, app string) string {
	return "https://login.microsoftonline.com/" + tenant + "/adminconsent?client_id=" + app
}

// DisplayName is the name of the Entra application of the integration
// id in the account whose slug is account, which names both, so an
// operator finds an integration's application in the installation's
// tenant.
func DisplayName(account string, id kernel.ULID) string {
	return "infraweave-" + account + "-" + id.String()
}

// azureInvalidDetail is the detail of an input the rules refuse.
const azureInvalidDetail = "The Azure integration is refused."

// IsGUID reports whether s is a GUID in its canonical form, as Entra
// writes tenant and subscription IDs.
func IsGUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for n, c := range s {
		switch {
		case n == 8 || n == 13 || n == 18 || n == 23:
			if c != '-' {
				return false
			}
		case (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F'):
			return false
		}
	}
	return true
}

// checkAzure refuses in's fields the rules forbid, each pointing under
// the body's root; create checks the tenant too.
func checkAzure(in AzureInput, create bool) error {
	var errs []kernel.FieldError
	add := func(field, reason, msg string) {
		errs = append(errs, kernel.FieldError{Pointer: "/" + field, Reason: reason, Message: msg})
	}
	if strings.TrimSpace(in.Name) == "" {
		add("name", kernel.ReasonRequired, "An Azure integration needs a name.")
	}
	if create && !IsGUID(in.TenantID) {
		add("tenantID", kernel.ReasonFormat, "The tenant ID is an Entra tenant's GUID.")
	}
	if in.DefaultSubscription != nil && !IsGUID(*in.DefaultSubscription) {
		add("defaultSubscriptionId", kernel.ReasonFormat, "A subscription ID is an Azure subscription's GUID.")
	}
	if in.Autoattach && in.DefaultSubscription == nil {
		add("defaultSubscriptionId", kernel.ReasonRule,
			"Auto-attachment needs a default subscription for the stacks its labels attach to.")
	}
	if len(errs) > 0 {
		return &kernel.Invalid{Detail: azureInvalidDetail, Errors: errs}
	}
	return nil
}

// applyAzure sets in's fields but the tenant on i, in space.
func applyAzure(i *AzureIntegration, space kernel.ULID, in AzureInput) {
	i.Space, i.Name, i.DefaultSubscription = space, strings.TrimSpace(in.Name), in.DefaultSubscription
	i.Labels, i.Autoattach = labels(in.Labels), in.Autoattach
}

// azureMissing is the integration id as one p's account lacks.
func azureMissing(id string) error { return &Missing{Kind: "Azure integration", ID: id} }

// GetAzure reads acct's Azure integration id as the API names it; a
// *Missing when acct lacks it.
func (s *Service) GetAzure(ctx context.Context, acct kernel.AccountID, id string) (AzureIntegration, error) {
	return s.azureLocked(ctx, acct, id, NoLock)
}

// azureLocked reads acct's Azure integration id locked as lock says; a
// *Missing when acct lacks it.
func (s *Service) azureLocked(ctx context.Context, acct kernel.AccountID, id string, lock Lock) (AzureIntegration, error) {
	uid, err := kernel.ParseULID(id)
	if err != nil {
		return AzureIntegration{}, azureMissing(id)
	}
	i, err := s.azure.Get(ctx, acct, uid, lock)
	if errors.Is(err, ErrNotFound) {
		return AzureIntegration{}, azureMissing(id)
	}
	return i, err //nolint:wrapcheck // the store names the read
}

// AzureSpaceOf is the space of acct's Azure integration id, which the
// access domain's middleware decides its operations on;
// accessapi.ErrNoSubject when acct lacks it.
func (s *Service) AzureSpaceOf(ctx context.Context, acct kernel.AccountID, id string) (kernel.ULID, error) {
	i, err := s.GetAzure(ctx, acct, id)
	if _, missing := errors.AsType[*Missing](err); missing {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return i.Space, err
}

// AzureByName reads p's account's Azure integration name, when p may
// read its space; a *Missing otherwise.
func (s *Service) AzureByName(ctx context.Context, p kernel.Principal, name string) (AzureIntegration, error) {
	i, err := s.azure.ByName(ctx, p.AccountID, name)
	if errors.Is(err, ErrNotFound) {
		return AzureIntegration{}, azureMissing(name)
	} else if err != nil {
		return AzureIntegration{}, err //nolint:wrapcheck // the store names the read
	}
	return i, s.azureVisible(ctx, p, i)
}

// azureVisible refuses i as a *Missing unless p may read its space.
func (s *Service) azureVisible(ctx context.Context, p kernel.Principal, i AzureIntegration) error {
	ok, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, i.Space)
	switch {
	case err != nil:
		return err //nolint:wrapcheck // the snapshot's read names itself
	case !ok:
		return azureMissing(i.ID.String())
	}
	return nil
}

// ListAzure reads a page of the Azure integrations of p's account in the
// spaces it may read, by name.
func (s *Service) ListAzure(ctx context.Context, p kernel.Principal, page Page) ([]AzureIntegration, bool, error) {
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return nil, false, err //nolint:wrapcheck // the snapshot's read names itself
	}
	return s.azure.List(ctx, p.AccountID, reach, page) //nolint:wrapcheck // the store names the read
}

// AzureAttachable reads a page of the Azure integrations a stack in the
// space slug may attach that p may read, by name; a space p cannot read
// is a *Missing.
func (s *Service) AzureAttachable(ctx context.Context, p kernel.Principal, slug string, page Page) ([]AzureIntegration, bool, error) {
	space, err := s.readable(ctx, p, slug)
	if err != nil {
		return nil, false, err
	}
	return s.azureAttachable(ctx, p, space, page)
}

// StackAzureAttachable reads a page of the Azure integrations the stack
// slug may attach that p may read, by name. The route has decided
// SPACE_READ on the stack's space.
func (s *Service) StackAzureAttachable(ctx context.Context, p kernel.Principal, slug string, page Page) ([]AzureIntegration, bool, error) {
	st, err := s.stack(ctx, p.AccountID, slug)
	if err != nil {
		return nil, false, err
	}
	return s.azureAttachable(ctx, p, st.Space, page)
}

func (s *Service) azureAttachable(ctx context.Context, p kernel.Principal, space kernel.ULID, page Page) ([]AzureIntegration, bool, error) {
	reach, err := s.within(ctx, p, space)
	if err != nil {
		return nil, false, err
	}
	return s.azure.List(ctx, p.AccountID, reach, page) //nolint:wrapcheck // the store names the read
}

// AzureEnabled reports whether acct may use Azure integrations: whether
// it may use cloud integrations and the installation can create Entra
// applications.
func (s *Service) AzureEnabled(ctx context.Context, acct kernel.AccountID) (bool, error) {
	if s.apps == nil {
		return false, nil
	}
	return s.enabled(ctx, acct)
}

// CreateAzure creates an Azure integration of p's account from in, with
// its Entra application. The route has decided SPACE_ADMIN on in's
// space; its refusals point into azureIntegrationCreate's body. Graph is
// called outside the transaction that stores the integration, and an
// application whose integration is not stored is deleted again, so a
// failure leaves neither.
func (s *Service) CreateAzure(ctx context.Context, p kernel.Principal, in AzureInput) (AzureIntegration, error) {
	if s.apps == nil {
		return AzureIntegration{}, ErrAzureUnsupported
	}
	if err := checkAzure(in, true); err != nil {
		return AzureIntegration{}, err
	}
	space, err := s.readable(ctx, p, in.Space)
	if err != nil {
		return AzureIntegration{}, err
	}
	name := strings.TrimSpace(in.Name)
	if _, err := s.azure.ByName(ctx, p.AccountID, name); err == nil {
		return AzureIntegration{}, &NameTaken{Kind: "Azure integration", Name: name}
	} else if !errors.Is(err, ErrNotFound) {
		return AzureIntegration{}, err //nolint:wrapcheck // the store names the read
	}
	account, err := s.accounts.Slug(ctx, p.AccountID)
	if err != nil {
		return AzureIntegration{}, err //nolint:wrapcheck // the access domain names the read
	}
	now := s.now()
	i := AzureIntegration{
		ID: kernel.NewULID(), Account: p.AccountID, TenantID: strings.ToLower(in.TenantID),
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	i.DisplayName = DisplayName(account, i.ID)
	applyAzure(&i, space, in)
	app, err := s.apps.Create(ctx, i.DisplayName)
	if err != nil {
		return AzureIntegration{}, err //nolint:wrapcheck // the adapter's errors are the answers
	}
	i.ApplicationID, i.ObjectID = app.AppID, app.ObjectID
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := s.space(ctx, p, in.Space); err != nil {
			return err
		}
		if err := s.azure.Create(ctx, i); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.auditAzure(ctx, p, AzureActionCreate, "CREATE", i, nil, in)
	})
	if err != nil {
		s.abandon(ctx, app.ObjectID)
		return AzureIntegration{}, err //nolint:wrapcheck // each step in the transaction names itself
	}
	return i, nil
}

// abandonTimeout bounds deleting an application whose integration was not
// stored, which outlives a request already past its deadline.
const abandonTimeout = 30 * time.Second

// abandon deletes the application id, which no stored integration names.
// A failure leaves it in the installation's tenant under its display
// name, which names the integration's ULID, for the operator to delete.
func (s *Service) abandon(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abandonTimeout)
	defer cancel()
	_ = s.apps.Delete(ctx, id) // the create's own error is the answer
}

// UpdateAzure replaces the fields of p's account's Azure integration id
// with in's, when expect accepts its version; the tenant never changes.
// The route has decided SPACE_ADMIN on the integration's space; a move to
// another space also needs SPACE_ADMIN there, and is refused when a stack
// attached to the integration by hand could no longer reach it. Its
// refusals point into azureIntegrationUpdate's body.
func (s *Service) UpdateAzure(ctx context.Context, p kernel.Principal, id string, expect kernel.Expect, in AzureInput) (AzureIntegration, error) {
	if err := checkAzure(in, false); err != nil {
		return AzureIntegration{}, err
	}
	var i AzureIntegration
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if i, err = s.azureLocked(ctx, p.AccountID, id, Update); err != nil {
			return err
		}
		if err := expect.Check(i.Version); err != nil {
			return err //nolint:wrapcheck // kernel.ErrStale is the answer
		}
		space, err := s.space(ctx, p, in.Space)
		if err != nil {
			return err
		}
		if space != i.Space {
			if err := s.require(ctx, p, accessapi.ActionSpaceAdmin, space); err != nil {
				return err
			}
			if err := s.moveFrom(ctx, s.azureKind(), i.Account, i.ID, i.Name, space); err != nil {
				return err
			}
		}
		applyAzure(&i, space, in)
		i.Version++
		i.UpdatedAt = s.now()
		if err := s.azure.Write(ctx, i); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.auditAzure(ctx, p, AzureActionUpdate, "UPDATE", i, nil, in)
	})
	return i, err //nolint:wrapcheck // each step in the transaction names itself
}

// DeleteAzure deletes p's account's Azure integration id with its
// attachments and its Entra application, when expect accepts its version,
// and answers it as it was. The application is deleted first, so a Graph
// failure keeps the integration for a retry. The route has decided
// SPACE_ADMIN on its space.
func (s *Service) DeleteAzure(ctx context.Context, p kernel.Principal, id string, expect kernel.Expect) (AzureIntegration, error) {
	i, err := s.GetAzure(ctx, p.AccountID, id)
	if err != nil {
		return AzureIntegration{}, err
	}
	if err := expect.Check(i.Version); err != nil {
		return AzureIntegration{}, err //nolint:wrapcheck // kernel.ErrStale is the answer
	}
	if s.apps == nil {
		return AzureIntegration{}, ErrAzureUnsupported
	}
	if err := s.apps.Delete(ctx, i.ObjectID); err != nil {
		return AzureIntegration{}, err //nolint:wrapcheck // the adapter's errors are the answers
	}
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		now, err := s.azureLocked(ctx, p.AccountID, id, Update)
		if err != nil {
			return err
		}
		if err := s.azure.Delete(ctx, now.Account, now.ID); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.auditAzure(ctx, p, AzureActionDelete, "DELETE", now, nil, nil)
	})
	return i, err //nolint:wrapcheck // each step in the transaction names itself
}

// SetConsent records p's statement that admin consent is provided, or no
// longer is, on p's account's Azure integration id, with p and the time,
// and answers the integration. Entra is never asked. The route has
// decided AZURE_INTEGRATION_CONFIRM_CONSENT or
// AZURE_INTEGRATION_CLEAR_CONSENT on its space.
func (s *Service) SetConsent(ctx context.Context, p kernel.Principal, id string, provided bool) (AzureIntegration, error) {
	var i AzureIntegration
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if i, err = s.azureLocked(ctx, p.AccountID, id, Update); err != nil {
			return err
		}
		now, actor := s.now(), actorOf(p).Username
		i.ConsentProvided, i.ConsentActor, i.ConsentAt = provided, &actor, &now
		i.Version++
		i.UpdatedAt = now
		if err := s.azure.Write(ctx, i); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		action := AzureActionClearConsent
		if provided {
			action = AzureActionConfirmConsent
		}
		return s.auditAzure(ctx, p, action, "UPDATE", i, nil,
			map[string]bool{"adminConsentProvided": provided})
	})
	return i, err //nolint:wrapcheck // each step in the transaction names itself
}

// The audit trail's actions of the Azure integrations' writes, on their
// resource type.
const (
	AzureResourceType         = "AZURE_INTEGRATION"
	AzureActionCreate         = "azure_integration.create"
	AzureActionUpdate         = "azure_integration.update"
	AzureActionDelete         = "azure_integration.delete"
	AzureActionAttach         = "azure_integration.attach"
	AzureActionDetach         = "azure_integration.detach"
	AzureActionAttachment     = "azure_integration.update_attachment"
	AzureActionConfirmConsent = "azure_integration.confirm_consent"
	AzureActionClearConsent   = "azure_integration.clear_consent"
)

// auditAzure records p's action on i, through the stack related when it
// is not nil, with body as the entry's body, in the caller's transaction.
func (s *Service) auditAzure(ctx context.Context, p kernel.Principal, action, event string, i AzureIntegration, related *stacksapi.Stack, body any) error {
	// The metadata and every body are plain fields, so they always encode.
	meta, _ := json.Marshal(struct {
		Name          string `json:"name"`
		TenantID      string `json:"tenantId"`
		ApplicationID string `json:"applicationId"`
	}{i.Name, i.TenantID, i.ApplicationID})
	e := kernel.AuditEntry{
		AccountID: p.AccountID.ULID,
		Actor:     actorOf(p),
		Action:    action,
		EventType: event,
		AffectedResource: kernel.AuditResource{
			Type: AzureResourceType, ID: i.ID.String(), Metadata: meta,
		},
	}
	if body != nil {
		e.Body, _ = json.Marshal(body)
	}
	if related != nil {
		e.RelatedResource = &kernel.AuditResource{Type: resourceStack, ID: related.ID.String()}
	}
	return s.store.Audit(ctx, e) //nolint:wrapcheck // the store names the write
}

// PurgeAzure deletes up to batch of the deleted account acct's Azure
// integrations with their attachments and their Entra applications, and
// reports true once none is left. An application Graph fails to delete
// fails the purge, which runs again; without cloud.azure the
// installation cannot reach the applications, so the rows go alone.
func (s *Service) PurgeAzure(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	all, err := s.azure.Oldest(ctx, acct, batch)
	if err != nil || len(all) == 0 {
		return err == nil, err //nolint:wrapcheck // the store names the read
	}
	for _, i := range all {
		if s.apps != nil {
			if err := s.apps.Delete(ctx, i.ObjectID); err != nil {
				return false, err //nolint:wrapcheck // the adapter's errors are the answers
			}
		}
		if err := s.azure.Delete(ctx, acct, i.ID); err != nil {
			return false, err //nolint:wrapcheck // the store names the write
		}
	}
	return false, nil
}

// AzureCount is how many Azure integrations acct has.
func (s *Service) AzureCount(ctx context.Context, acct kernel.AccountID) (int, error) {
	return s.azure.Count(ctx, acct) //nolint:wrapcheck // the store names the read
}
