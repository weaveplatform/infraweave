package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Deps are what the service is constructed over. Enabled reports whether
// an account may use cloud integrations; Azure stores Azure integrations,
// and Applications is the installation's Graph client, nil without
// cloud.azure.
type Deps struct {
	Store        Store
	Azure        AzureStore
	Applications Applications
	DB           tx.Beginner
	Spaces       accessapi.Spaces
	Accounts     accessapi.Accounts
	Stacks       stacksapi.Stacks
	Authz        accessapi.Authorizer
	Roles        Roles
	Enabled      func(ctx context.Context, acct kernel.AccountID) (bool, error)
	Now          func() time.Time
}

// Service is the cloud domain's logic. Every write runs in one
// transaction with its audit entry, holding the spaces and stacks it
// stores the IDs of, so no delete or move removes them first.
type Service struct {
	store    Store
	azure    AzureStore
	apps     Applications
	db       tx.Beginner
	spaces   accessapi.Spaces
	accounts accessapi.Accounts
	stacks   stacksapi.Stacks
	authz    accessapi.Authorizer
	roles    Roles
	enabled  func(ctx context.Context, acct kernel.AccountID) (bool, error)
	now      func() time.Time
}

// New returns the service over d.
func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		store: d.Store, azure: d.Azure, apps: d.Applications, db: d.DB, spaces: d.Spaces,
		accounts: d.Accounts, stacks: d.Stacks,
		authz: d.Authz, roles: d.Roles, enabled: d.Enabled, now: now,
	}
}

// invalidDetail is the detail of an input the rules refuse.
const invalidDetail = "The AWS integration is refused."

// roleARN is an IAM role's ARN in any partition.
var roleARN = regexp.MustCompile(`^arn:aws[a-z-]*:iam::\d{12}:role/[\w+=,.@/-]+$`)

// check refuses in's fields the rules forbid, each pointing under at, and
// answers in's duration, DefaultDuration when it names none.
func check(at string, in Input) (int32, error) {
	var errs []kernel.FieldError
	add := func(field, reason, msg string) {
		errs = append(errs, kernel.FieldError{Pointer: at + "/" + field, Reason: reason, Message: msg})
	}
	if strings.TrimSpace(in.Name) == "" {
		add("name", kernel.ReasonRequired, "An AWS integration needs a name.")
	}
	if !roleARN.MatchString(in.RoleARN) {
		add("roleArn", kernel.ReasonFormat, "The role ARN is not an IAM role's ARN.")
	}
	if in.Region != nil && strings.TrimSpace(*in.Region) == "" {
		add("region", kernel.ReasonFormat, "A region is a name such as eu-west-1, or absent.")
	}
	duration := int32(DefaultDuration)
	if in.Duration != nil {
		duration = *in.Duration
	}
	most := int32(MaxServerDuration)
	if in.Worker {
		most = MaxWorkerDuration
	}
	if duration < MinDuration || duration > most {
		add("durationSeconds", kernel.ReasonRule, fmt.Sprintf(
			"The session duration is %d to %d seconds.", MinDuration, most))
	}
	if in.ExternalID != "" && !in.Worker {
		add("externalID", kernel.ReasonRule,
			"A custom external ID applies only with generateCredentialsInWorker.")
	}
	if len(errs) > 0 {
		return 0, &kernel.Invalid{Detail: invalidDetail, Errors: errs}
	}
	return duration, nil
}

// labels are in's labels, each once, sorted; never nil, since the column
// refuses NULL.
func labels(in []string) []string {
	out := append([]string{}, in...)
	slices.Sort(out)
	return slices.Compact(out)
}

// apply sets in's fields on i, in space.
func apply(i *Integration, space kernel.ULID, duration int32, in Input) {
	i.Space, i.Name, i.RoleARN, i.Region = space, strings.TrimSpace(in.Name), in.RoleARN, in.Region
	i.Duration, i.Worker, i.ExternalID = duration, in.Worker, in.ExternalID
	i.Labels, i.Autoattach, i.TagSession = labels(in.Labels), in.Autoattach, in.TagSession
}

// Get reads acct's integration id as the API names it.
func (s *Service) Get(ctx context.Context, acct kernel.AccountID, id string) (Integration, error) {
	uid, err := kernel.ParseULID(id)
	if err != nil {
		return Integration{}, fmt.Errorf("%w: integration %q", ErrNotFound, id)
	}
	return s.store.Get(ctx, acct, uid, NoLock) //nolint:wrapcheck // the store names the read
}

// SpaceOf is the space of acct's integration id, which the access
// domain's middleware decides an integration's operations on;
// accessapi.ErrNoSubject when acct lacks it.
func (s *Service) SpaceOf(ctx context.Context, acct kernel.AccountID, id string) (kernel.ULID, error) {
	i, err := s.Get(ctx, acct, id)
	if errors.Is(err, ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return i.Space, err
}

// ByName reads p's account's integration name, when p may read its
// space; a *Missing otherwise.
func (s *Service) ByName(ctx context.Context, p kernel.Principal, name string) (Integration, error) {
	i, err := s.store.ByName(ctx, p.AccountID, name)
	if errors.Is(err, ErrNotFound) {
		return Integration{}, &Missing{Kind: "AWS integration", ID: name}
	} else if err != nil {
		return Integration{}, err //nolint:wrapcheck // the store names the read
	}
	if err := s.visible(ctx, p, i); err != nil {
		return Integration{}, err
	}
	return i, nil
}

// visible refuses i as a *Missing unless p may read its space.
func (s *Service) visible(ctx context.Context, p kernel.Principal, i Integration) error {
	ok, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, i.Space)
	switch {
	case err != nil:
		return err //nolint:wrapcheck // the snapshot's read names itself
	case !ok:
		return &Missing{Kind: "AWS integration", ID: i.ID.String()}
	}
	return nil
}

// List reads a page of the integrations of p's account in the spaces it
// may read, by name, the legacy ones too when legacy is set.
func (s *Service) List(ctx context.Context, p kernel.Principal, legacy bool, page Page) ([]Integration, bool, error) {
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return nil, false, err //nolint:wrapcheck // the snapshot's read names itself
	}
	return s.store.List(ctx, p.AccountID, reach, legacy, page) //nolint:wrapcheck // the store names the read
}

// Attachable reads a page of the integrations a stack in the space slug
// may attach that p may read, by name; a space p cannot read is a
// *Missing.
func (s *Service) Attachable(ctx context.Context, p kernel.Principal, slug string, page Page) ([]Integration, bool, error) {
	space, err := s.readable(ctx, p, slug)
	if err != nil {
		return nil, false, err
	}
	return s.attachable(ctx, p, space, page)
}

// StackAttachable reads a page of the integrations the stack slug may
// attach that p may read, by name. The route has decided SPACE_READ on
// the stack's space.
func (s *Service) StackAttachable(ctx context.Context, p kernel.Principal, slug string, page Page) ([]Integration, bool, error) {
	st, err := s.stack(ctx, p.AccountID, slug)
	if err != nil {
		return nil, false, err
	}
	return s.attachable(ctx, p, st.Space, page)
}

func (s *Service) attachable(ctx context.Context, p kernel.Principal, space kernel.ULID, page Page) ([]Integration, bool, error) {
	reach, err := s.within(ctx, p, space)
	if err != nil {
		return nil, false, err
	}
	return s.store.List(ctx, p.AccountID, reach, false, page) //nolint:wrapcheck // the store names the read
}

// within are the spaces a stack in space reaches whose integrations p may
// read.
func (s *Service) within(ctx context.Context, p kernel.Principal, space kernel.ULID) ([]kernel.ULID, error) {
	tree, err := s.spaces.Tree(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return nil, err //nolint:wrapcheck // the snapshot's read names itself
	}
	within := Reach(index(tree), space)
	return slices.DeleteFunc(reach, func(id kernel.ULID) bool { return !slices.Contains(within, id) }), nil
}

// Enabled reports whether acct may use AWS integrations.
func (s *Service) Enabled(ctx context.Context, acct kernel.AccountID) (bool, error) {
	return s.enabled(ctx, acct)
}

// InstallationAccount is the AWS account the server assumes roles as;
// ErrUnsupported without AWS credentials.
func (s *Service) InstallationAccount(ctx context.Context) (string, error) {
	return s.roles.CallerAccount(ctx) //nolint:wrapcheck // the adapter's errors are the answers
}

// Create creates an integration of p's account from in. The route has
// decided SPACE_ADMIN on in's space; its refusals point into
// awsIntegrationCreate's body.
func (s *Service) Create(ctx context.Context, p kernel.Principal, in Input) (Integration, error) {
	duration, err := check("", in)
	if err != nil {
		return Integration{}, err
	}
	var i Integration
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		space, err := s.space(ctx, p, in.Space)
		if err != nil {
			return err
		}
		now := s.now()
		i = Integration{
			ID: kernel.NewULID(), Account: p.AccountID, CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		apply(&i, space, duration, in)
		if err := s.store.Create(ctx, i); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.audit(ctx, p, ActionCreate, "CREATE", i, nil, in)
	})
	return i, err //nolint:wrapcheck // each step in the transaction names itself
}

// Update replaces the fields of p's account's integration id with in's,
// when expect accepts its version. The route has decided SPACE_ADMIN on
// the integration's space; a move to another space also needs SPACE_ADMIN
// there, and is refused when a stack attached to the integration by hand
// could no longer reach it. Its refusals point into awsIntegrationUpdate's
// body.
func (s *Service) Update(ctx context.Context, p kernel.Principal, id string, expect kernel.Expect, in Input) (Integration, error) {
	duration, err := check("", in)
	if err != nil {
		return Integration{}, err
	}
	var i Integration
	err = tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if i, err = s.locked(ctx, p.AccountID, id); err != nil {
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
			if err := s.integrationMove(ctx, i, space); err != nil {
				return err
			}
		}
		apply(&i, space, duration, in)
		i.Version++
		i.UpdatedAt = s.now()
		if err := s.store.Write(ctx, i); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.audit(ctx, p, ActionUpdate, "UPDATE", i, nil, in)
	})
	return i, err //nolint:wrapcheck // each step in the transaction names itself
}

// Delete deletes p's account's integration id with its attachments, when
// expect accepts its version, and answers it as it was. The route has
// decided SPACE_ADMIN on its space.
func (s *Service) Delete(ctx context.Context, p kernel.Principal, id string, expect kernel.Expect) (Integration, error) {
	var i Integration
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if i, err = s.locked(ctx, p.AccountID, id); err != nil {
			return err
		}
		if err := expect.Check(i.Version); err != nil {
			return err //nolint:wrapcheck // kernel.ErrStale is the answer
		}
		if err := s.store.Delete(ctx, i.Account, i.ID); err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		return s.audit(ctx, p, ActionDelete, "DELETE", i, nil, nil)
	})
	return i, err //nolint:wrapcheck // each step in the transaction names itself
}

// locked reads acct's integration id locked FOR UPDATE.
func (s *Service) locked(ctx context.Context, acct kernel.AccountID, id string) (Integration, error) {
	uid, err := kernel.ParseULID(id)
	if err != nil {
		return Integration{}, fmt.Errorf("%w: integration %q", ErrNotFound, id)
	}
	return s.store.Get(ctx, acct, uid, Update) //nolint:wrapcheck // the store names the read
}

// space is the ID of the space slug, the top space when it is empty, held
// in the caller's transaction; a space p cannot read is a *Missing.
func (s *Service) space(ctx context.Context, p kernel.Principal, slug string) (kernel.ULID, error) {
	id, err := s.readable(ctx, p, slug)
	if err != nil {
		return id, err
	}
	if err := s.spaces.Hold(ctx, p.AccountID, id); errors.Is(err, accessapi.ErrNoSpace) {
		return kernel.ULID{}, &Missing{Kind: "space", ID: slug}
	} else if err != nil {
		return kernel.ULID{}, err //nolint:wrapcheck // the access domain names the lock
	}
	return id, nil
}

// readable is the ID of the space slug, the top space when it is empty,
// when p may read it; a *Missing otherwise.
func (s *Service) readable(ctx context.Context, p kernel.Principal, slug string) (kernel.ULID, error) {
	id := p.AccountID.ULID
	if slug != "" {
		var err error
		if id, err = s.spaces.Find(ctx, p.AccountID, slug); errors.Is(err, accessapi.ErrNoSpace) {
			return kernel.ULID{}, &Missing{Kind: "space", ID: slug}
		} else if err != nil {
			return kernel.ULID{}, err //nolint:wrapcheck // the access domain names the read
		}
	}
	read, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, id)
	switch {
	case err != nil:
		return kernel.ULID{}, err //nolint:wrapcheck // the snapshot's read names itself
	case !read:
		return kernel.ULID{}, &Missing{Kind: "space", ID: slug}
	}
	return id, nil
}

// require refuses p action in space, as a *Denied.
func (s *Service) require(ctx context.Context, p kernel.Principal, action accessapi.Action, space kernel.ULID) error {
	ok, err := s.authz.Authorize(ctx, p, action, space)
	switch {
	case err != nil:
		return err //nolint:wrapcheck // the snapshot's read names itself
	case !ok:
		return &Denied{Action: string(action)}
	}
	return nil
}

// stack reads acct's stack slug; a *Missing when acct lacks it.
func (s *Service) stack(ctx context.Context, acct kernel.AccountID, slug string) (stacksapi.Stack, error) {
	st, err := s.stacks.BySlug(ctx, acct, slug)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return stacksapi.Stack{}, &Missing{Kind: "stack", ID: slug}
	}
	return st, err //nolint:wrapcheck // the stacks domain names the read
}

// The audit trail's actions of the domain's writes, on its resource type.
const (
	ResourceType     = "AWS_INTEGRATION"
	ActionCreate     = "aws_integration.create"
	ActionUpdate     = "aws_integration.update"
	ActionDelete     = "aws_integration.delete"
	ActionAttach     = "aws_integration.attach"
	ActionDetach     = "aws_integration.detach"
	ActionAttachment = "aws_integration.update_attachment"
	resourceStack    = "STACK"
)

// audit records p's action on i, through the stack related when it is not
// nil, with body as the entry's body, in the caller's transaction.
func (s *Service) audit(ctx context.Context, p kernel.Principal, action, event string, i Integration, related *stacksapi.Stack, body any) error {
	// The metadata and every body are plain fields, so they always encode.
	meta, _ := json.Marshal(struct {
		Name    string `json:"name"`
		RoleARN string `json:"roleArn"`
		Legacy  bool   `json:"legacy"`
	}{i.Name, i.RoleARN, i.Legacy})
	e := kernel.AuditEntry{
		AccountID: p.AccountID.ULID,
		Actor:     actorOf(p),
		Action:    action,
		EventType: event,
		AffectedResource: kernel.AuditResource{
			Type: ResourceType, ID: i.ID.String(), Metadata: meta,
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

// Actor is the audit trail's username of a change a run makes.
const Actor = "infraweave"

func actorOf(p kernel.Principal) kernel.AuditActor {
	switch {
	case p.Kind == kernel.PrincipalRun:
		return kernel.AuditActor{Username: Actor, Run: p.ActorID}
	case p.Login != "":
		return kernel.AuditActor{Username: p.Login}
	}
	return kernel.AuditActor{Username: p.ActorID.String()}
}
