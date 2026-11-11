package integration

import (
	"context"
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
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Integration is one VCS integration of an account. Its name, and the
// slug made from it, and whether it is its provider's default never
// change once it is made.
type Integration struct {
	Account        kernel.AccountID
	ID             kernel.ULID
	Provider       string
	Slug           string
	Name           string
	Space          kernel.ULID
	Description    string
	Labels         []string
	IsDefault      bool
	Checks         Checks
	UseGitCheckout bool
	Settings       port.Settings
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Version        int64
}

// Checks are how an integration reports runs back as commit checks: Mode
// is INDIVIDUAL, AGGREGATED or ALL, and the options thin out the
// individual ones.
type Checks struct {
	Mode                    string
	IndividualFailuresOnly  bool
	IndividualSkipUnchanged bool
}

// The check modes, INDIVIDUAL when an input names none.
var checkModes = []string{"INDIVIDUAL", "AGGREGATED", "ALL"}

// Secrets are an integration's credential and webhook secret in the
// clear, which only the store and a provider's client see.
type Secrets struct {
	Credential    []byte
	WebhookSecret []byte
}

var (
	// ErrNotFound reports an integration the account lacks.
	ErrNotFound = errors.New("vcs: no such integration")
	// ErrSlugTaken reports a name another integration of the account and
	// provider holds the slug of.
	ErrSlugTaken = errors.New("vcs: integration slug taken")
	// ErrDefaultTaken reports a second default integration of a provider.
	ErrDefaultTaken = errors.New("vcs: default integration taken")
	// ErrNoTester reports a provider whose integrations no test checks.
	ErrNoTester = errors.New("vcs: no test for the provider")
	// ErrNotConfigured reports a built-in provider whose credentials the
	// operator has not set, or GitHub App work with no Apps bound.
	ErrNotConfigured = errors.New("vcs: provider not configured")
)

// Fields are what an integration search filters and orders by: name,
// description, label, any of its labels; space, its place in the tree;
// provider and isDefault. A search that names no order reads by name.
var Fields = []search.Field{
	{Name: "name", Type: search.String, Orderable: true},
	{Name: "description", Type: search.String},
	{Name: "label", Type: search.String},
	{Name: "space", Type: search.Hierarchy},
	{Name: "provider", Type: search.String, Orderable: true},
	{Name: "isDefault", Type: search.Boolean},
	{Name: "createdAt", Type: search.Time, Orderable: true},
	{Name: "updatedAt", Type: search.Time, Orderable: true},
}

// Scope is what an integration search sees: the spaces whose integrations
// it may keep, the account's space tree, which the space field reads,
// and, when not empty, the one provider it keeps.
type Scope struct {
	Reachable []kernel.ULID
	Spaces    []accessapi.SpaceNode
	Provider  string
}

// Store is what the service needs of the integrations' storage. Every
// method joins the transaction its context carries.
type Store interface {
	// Create writes a new integration with its secrets sealed;
	// ErrSlugTaken or ErrDefaultTaken when another holds its slug or its
	// provider's default.
	Create(ctx context.Context, in Integration, sec Secrets) error
	// Write replaces the integration's fields and sets its version, and
	// its secrets when sec is not nil.
	Write(ctx context.Context, in Integration, sec *Secrets) error
	// Get reads acct's integration id, locked FOR UPDATE when lock is set;
	// ErrNotFound when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock bool) (Integration, error)
	// Default reads acct's default integration of provider; ErrNotFound
	// when it has none.
	Default(ctx context.Context, acct kernel.AccountID, provider string) (Integration, error)
	// Secrets opens acct's integration id's secrets.
	Secrets(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (Secrets, error)
	// Delete deletes acct's integration id.
	Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error
	// InSpace reports whether an integration of acct lives in space.
	InSpace(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error)
	// Search reads a page of acct's integrations in the scope that in
	// keeps.
	Search(
		ctx context.Context,
		acct kernel.AccountID,
		in search.Input,
		scope Scope,
	) (search.Result[Integration], error)
	// Suggest lists the values acct's integrations in the scope that in
	// keeps hold.
	Suggest(
		ctx context.Context,
		acct kernel.AccountID,
		in search.SuggestionsInput,
		scope Scope,
	) (search.Suggestions, error)
	// Purge deletes up to batch of the deleted account acct's
	// integrations and manifest forms; true when none is left.
	Purge(ctx context.Context, acct kernel.AccountID, batch int) (bool, error)
	// OpenManifest keeps m, a manifest form of acct, until it expires.
	OpenManifest(ctx context.Context, acct kernel.AccountID, m Manifest) error
	// TakeManifest deletes acct's manifest form id and answers it when it
	// has not expired at now; ErrNotFound otherwise.
	TakeManifest(
		ctx context.Context,
		acct kernel.AccountID,
		id kernel.ULID,
		now time.Time,
	) (Manifest, error)
}

// Deps are what the service is constructed over: the store, the pool its
// writes begin transactions on, the spaces integrations live in, the
// authorizer, the stacks a delete detaches, each provider infraweave
// stores integrations of, and the Tester of each provider that has one;
// Builtin, the integration of each provider whose credentials the
// operator sets, with no row of its own; Apps, the GitHub provider's App
// work; and Public, the server's public origin, which webhook and
// manifest URLs start with.
type Deps struct {
	Store     Store
	DB        tx.Beginner
	Spaces    accessapi.Spaces
	Authz     accessapi.Authorizer
	Stacks    stacksapi.Stacks
	Providers []string
	Testers   map[string]port.Tester
	Builtin   map[string]port.Integration
	Apps      port.Apps
	Public    string
	Now       func() time.Time
}

// Service is the integrations' logic. Every write runs in one
// transaction.
type Service struct {
	store     Store
	db        tx.Beginner
	spaces    accessapi.Spaces
	authz     accessapi.Authorizer
	stacks    stacksapi.Stacks
	providers []string
	testers   map[string]port.Tester
	builtin   map[string]port.Integration
	apps      port.Apps
	public    string
	now       func() time.Time
}

// New returns the service over d.
func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		store: d.Store, db: d.DB, spaces: d.Spaces, authz: d.Authz, stacks: d.Stacks,
		providers: d.Providers, testers: d.Testers, builtin: d.Builtin, apps: d.Apps,
		public: strings.TrimSuffix(d.Public, "/"), now: now,
	}
}

var (
	_ accessapi.SpaceGuard        = SpaceGuard{}
	_ stacksapi.StackIntegrations = (*Service)(nil)
)

// Missing is an entity a request names that the account lacks or the
// caller cannot read, which answers as one that does not exist: a space
// or an integration, by Kind.
type Missing struct{ Kind, ID string }

func (m *Missing) Error() string { return fmt.Sprintf("vcs: no %s %q", m.Kind, m.ID) }

// SpaceOf is the space of acct's integration id, which the access
// domain's middleware decides an integration's operations on;
// accessapi.ErrNoSubject when acct lacks it.
func (s *Service) SpaceOf(
	ctx context.Context,
	acct kernel.AccountID,
	id string,
) (kernel.ULID, error) {
	in, err := s.get(ctx, acct, id, "")
	if errors.Is(err, ErrNotFound) {
		return kernel.ULID{}, accessapi.ErrNoSubject
	}
	return in.Space, err
}

// Read reads p's account's integration id of provider, and whether p may
// see its provider details, which need SPACE_ADMIN in its space. The
// route has decided SPACE_READ there; one of another provider is
// ErrNotFound.
func (s *Service) Read(
	ctx context.Context,
	p kernel.Principal,
	id, provider string,
) (Integration, bool, error) {
	in, err := s.get(ctx, p.AccountID, id, provider)
	if err != nil {
		return in, false, err
	}
	admin, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceAdmin, in.Space)
	return in, admin, err //nolint:wrapcheck // the snapshot's read names itself
}

// get reads acct's integration id, of provider when it is not empty.
func (s *Service) get(
	ctx context.Context,
	acct kernel.AccountID,
	id, provider string,
) (Integration, error) {
	ulid, err := kernel.ParseULID(id)
	if err != nil {
		return Integration{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	in, err := s.store.Get(ctx, acct, ulid, false)
	if err != nil {
		return Integration{}, err //nolint:wrapcheck // the store names the read
	}
	if provider != "" && in.Provider != provider {
		return Integration{}, fmt.Errorf("%w: %s %s", ErrNotFound, provider, id)
	}
	return in, nil
}

// Search reads a page of the integrations of p's account in the spaces it
// may read.
func (s *Service) Search(
	ctx context.Context,
	p kernel.Principal,
	in search.Input,
) (search.Result[Integration], error) {
	scope, err := s.scope(ctx, p, nil)
	if err != nil {
		return search.Result[Integration]{}, err
	}
	return s.store.Search( //nolint:wrapcheck // the store names the read
		ctx,
		p.AccountID,
		in,
		scope,
	)
}

// Suggest lists the values the integrations of p's account in the spaces
// it may read hold, so no value or count names one p cannot read.
func (s *Service) Suggest(
	ctx context.Context,
	p kernel.Principal,
	in search.SuggestionsInput,
) (search.Suggestions, error) {
	scope, err := s.scope(ctx, p, nil)
	if err != nil {
		return search.Suggestions{}, err
	}
	return s.store.Suggest(ctx, p.AccountID, in, scope) //nolint:wrapcheck // as above
}

// Attachable reads a page of the integrations of provider that a stack
// in the space slug may bind and p may read: in the space or one it
// inherits from. A space p cannot read is a *Missing.
func (s *Service) Attachable(
	ctx context.Context,
	p kernel.Principal,
	slug, provider string,
	in search.Input,
) (search.Result[Integration], error) {
	space, err := s.readable(ctx, p, slug)
	if err != nil {
		return search.Result[Integration]{}, err
	}
	tree, err := s.spaces.Tree(ctx, p.AccountID)
	if err != nil {
		return search.Result[Integration]{}, err //nolint:wrapcheck // the access domain names the read
	}
	scope, err := s.scope(ctx, p, tree)
	if err != nil {
		return search.Result[Integration]{}, err
	}
	reach := Reach(tree, space)
	scope.Reachable = slices.DeleteFunc(scope.Reachable, func(id kernel.ULID) bool {
		return !slices.Contains(reach, id)
	})
	scope.Provider = provider
	return s.store.Search( //nolint:wrapcheck // the store names the read
		ctx,
		p.AccountID,
		in,
		scope,
	)
}

// Test runs the Tester of the provider of p's account's integration id;
// ErrNoTester when the provider has none. The route has decided its
// action on the integration's space.
func (s *Service) Test(
	ctx context.Context,
	p kernel.Principal,
	id, provider string,
) ([]port.Check, error) {
	in, err := s.get(ctx, p.AccountID, id, provider)
	if err != nil {
		return nil, err
	}
	t, ok := s.testers[in.Provider]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoTester, in.Provider)
	}
	use, err := s.use(ctx, in)
	if err != nil {
		return nil, err
	}
	return t.Test(ctx, use) //nolint:wrapcheck // the provider names its failure
}

// Input is an integration's fields as a create or an update gives them,
// with its space by slug, empty for the top space. An update keeps the
// name, the default and the space, and keeps the secrets when Secrets is
// nil.
type Input struct {
	Provider       string
	Name           string
	Space          string
	Description    string
	Labels         []string
	IsDefault      bool
	Checks         Checks
	UseGitCheckout bool
	Settings       port.Settings
	Secrets        *Secrets
}

// invalidDetail is the detail of an input the checks refuse.
const invalidDetail = "The integration's settings are refused."

// refusal is one refused field of an input, under customInput, where the
// provider operations carry the fields every provider shares.
func refusal(field, reason, message string) kernel.FieldError {
	return kernel.FieldError{Pointer: "/customInput/" + field, Reason: reason, Message: message}
}

// check refuses each field of in an integration cannot hold; create also
// checks its name.
func (s *Service) check(in Input, create bool) error {
	var errs []kernel.FieldError
	if !slices.Contains(s.providers, in.Provider) {
		errs = append(errs, kernel.FieldError{
			Pointer: "/provider", Reason: kernel.ReasonRule,
			Message: fmt.Sprintf("infraweave stores no %s integrations.", in.Provider),
		})
	}
	if create {
		switch {
		case strings.TrimSpace(in.Name) == "":
			errs = append(
				errs,
				refusal("name", kernel.ReasonRequired, "An integration needs a name."),
			)
		case !kernel.SlugFrom(in.Name).Valid():
			errs = append(errs, refusal(
				"name",
				kernel.ReasonRule,
				"An integration's name needs a letter a to z or a digit, from which its slug is made.",
			))
		}
	}
	if in.Checks.Mode != "" && !slices.Contains(checkModes, in.Checks.Mode) {
		errs = append(errs, refusal("vcsChecks", kernel.ReasonEnum,
			"vcsChecks is one of INDIVIDUAL, AGGREGATED and ALL."))
	}
	errs = append(errs, kernel.CheckLabels("/customInput/labels", in.Labels)...)
	errs = append(errs, checkSettings(in)...)
	if len(errs) == 0 {
		return nil
	}
	return &kernel.Invalid{Detail: invalidDetail, Errors: errs}
}

// defaultRefused is the refusal of a default its provider has already,
// or one outside the top space.
func defaultRefused(message string) error {
	return &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{
		refusal("isDefault", kernel.ReasonRule, message),
	}}
}

// Create creates an integration of p's account from in. The route has
// decided SPACE_ADMIN on in's space. A default outside the top space,
// and a second default of a provider, are refused at isDefault.
func (s *Service) Create(ctx context.Context, p kernel.Principal, in Input) (Integration, error) {
	return s.create(ctx, p, kernel.NewULID(), in)
}

// create is Create of the integration id.
func (s *Service) create(
	ctx context.Context,
	p kernel.Principal,
	id kernel.ULID,
	in Input,
) (Integration, error) {
	if err := s.check(in, true); err != nil {
		return Integration{}, err
	}
	var out Integration
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		space, err := s.readable(ctx, p, in.Space)
		if err != nil {
			return err
		}
		if in.IsDefault && space != p.AccountID.ULID {
			return defaultRefused("A default integration lives in the account's top space.")
		}
		if err := s.spaces.Hold(ctx, p.AccountID, space); errors.Is(err, accessapi.ErrNoSpace) {
			return &Missing{Kind: "space", ID: in.Space}
		} else if err != nil {
			return err //nolint:wrapcheck // the access domain names the lock
		}
		now := s.now()
		out = Integration{
			Account: p.AccountID, ID: id, Provider: in.Provider,
			Slug: kernel.SlugFrom(in.Name).String(), Name: strings.TrimSpace(in.Name),
			Space: space, IsDefault: in.IsDefault, CreatedAt: now, Version: 1,
		}
		apply(&out, in, now)
		var sec Secrets
		if in.Secrets != nil {
			sec = *in.Secrets
		}
		err = s.store.Create(ctx, out, sec)
		if errors.Is(err, ErrDefaultTaken) {
			return defaultRefused(
				fmt.Sprintf("The account has a default %s integration.", in.Provider),
			)
		}
		if errors.Is(err, ErrSlugTaken) {
			return &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{
				refusal("name", kernel.ReasonRule, fmt.Sprintf(
					"Another %s integration of this account has the slug %q.",
					in.Provider,
					out.Slug,
				)),
			}}
		}
		return err //nolint:wrapcheck // the store names the write
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// apply sets in's changeable fields on out.
func apply(out *Integration, in Input, now time.Time) {
	out.Description, out.Labels = in.Description, slices.Clone(in.Labels)
	if out.Labels == nil {
		out.Labels = []string{}
	}
	out.Checks = in.Checks
	if out.Checks.Mode == "" {
		out.Checks.Mode = checkModes[0]
	}
	out.UseGitCheckout, out.Settings, out.UpdatedAt = in.UseGitCheckout, in.Settings, now
}

// Update replaces the changeable fields of p's account's integration id
// with in's, and its secrets when in carries them, keeping each secret
// in leaves nil, when expect accepts its version. The route has
// decided SPACE_ADMIN on its space. A space other than the integration's
// is refused at space: moving one could leave a bound stack out of its
// reach.
func (s *Service) Update(
	ctx context.Context,
	p kernel.Principal,
	id string,
	expect kernel.Expect,
	in Input,
) (Integration, error) {
	if err := s.check(in, false); err != nil {
		return Integration{}, err
	}
	var out Integration
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if out, err = s.locked(ctx, p.AccountID, id, in.Provider); err != nil {
			return err
		}
		if err := expect.Check(out.Version); err != nil {
			return err //nolint:wrapcheck // kernel.ErrStale is the answer
		}
		if err := s.sameSpace(ctx, p.AccountID, out.Space, in.Space); err != nil {
			return err
		}
		sec := in.Secrets
		if sec != nil && (sec.Credential == nil || sec.WebhookSecret == nil) {
			kept, err := s.store.Secrets(ctx, p.AccountID, out.ID)
			if err != nil {
				return err //nolint:wrapcheck // the store names the read
			}
			sec = &Secrets{Credential: sec.Credential, WebhookSecret: sec.WebhookSecret}
			if sec.Credential == nil {
				sec.Credential = kept.Credential
			}
			if sec.WebhookSecret == nil {
				sec.WebhookSecret = kept.WebhookSecret
			}
		}
		apply(&out, in, s.now())
		out.Version++
		return s.store.Write(ctx, out, sec) //nolint:wrapcheck // the store names the write
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// Delete deletes p's account's integration id of provider and detaches
// every stack bound to it, in one transaction, answering it as it was.
// The route has decided SPACE_ADMIN on its space.
func (s *Service) Delete(
	ctx context.Context,
	p kernel.Principal,
	id, provider string,
) (Integration, error) {
	var out Integration
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		var err error
		if out, err = s.locked(ctx, p.AccountID, id, provider); err != nil {
			return err
		}
		if err := s.stacks.DetachVCS(ctx, p.AccountID, out.ID); err != nil {
			return err //nolint:wrapcheck // the stacks domain names the write
		}
		return s.store.Delete(ctx, p.AccountID, out.ID) //nolint:wrapcheck // the store names it
	})
	return out, err //nolint:wrapcheck // each step in the transaction names itself
}

// locked reads acct's integration id of provider FOR UPDATE.
func (s *Service) locked(
	ctx context.Context,
	acct kernel.AccountID,
	id, provider string,
) (Integration, error) {
	ulid, err := kernel.ParseULID(id)
	if err != nil {
		return Integration{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	in, err := s.store.Get(ctx, acct, ulid, true)
	if err != nil {
		return Integration{}, err //nolint:wrapcheck // the store names the read
	}
	if in.Provider != provider {
		return Integration{}, fmt.Errorf("%w: %s %s", ErrNotFound, provider, id)
	}
	return in, nil
}

// Bindable implements stacksapi.StackIntegrations.
func (s *Service) Bindable(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
	provider string,
	id kernel.ULID,
) error {
	in, err := s.store.Get(ctx, acct, id, false)
	if errors.Is(err, ErrNotFound) || err == nil && in.Provider != provider {
		return fmt.Errorf("%w: %s %s", stacksapi.ErrNoIntegration, provider, id)
	}
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	tree, err := s.spaces.Tree(ctx, acct)
	if err != nil {
		return err //nolint:wrapcheck // the access domain names the read
	}
	if !slices.Contains(Reach(tree, space), in.Space) {
		return fmt.Errorf("%w: %s %s out of reach", stacksapi.ErrNoIntegration, provider, id)
	}
	return nil
}

// Use is the integration a stack of acct on provider uses now, with its
// secrets opened: its own, id, or the provider's default when id is nil.
// It is false when the stack names none and the provider has no default,
// and for a provider infraweave stores no integrations of, such as raw
// Git, without a read. A built-in provider's stack uses the operator's
// integration, whose row holds only the account's check options.
func (s *Service) Use(
	ctx context.Context,
	acct kernel.AccountID,
	provider string,
	id *kernel.ULID,
) (port.Integration, bool, error) {
	if use, ok := s.builtin[provider]; ok {
		return use, true, nil
	}
	if !slices.Contains(s.providers, provider) {
		return port.Integration{}, false, nil
	}
	var in Integration
	var err error
	if id == nil {
		in, err = s.store.Default(ctx, acct, provider)
	} else {
		in, err = s.store.Get(ctx, acct, *id, false)
	}
	if errors.Is(err, ErrNotFound) {
		return port.Integration{}, false, nil
	}
	if err != nil {
		return port.Integration{}, false, err //nolint:wrapcheck // the store names the read
	}
	use, err := s.use(ctx, in)
	return use, err == nil, err
}

// use is in with its secrets opened, as its provider's client uses it.
func (s *Service) use(ctx context.Context, in Integration) (port.Integration, error) {
	sec, err := s.store.Secrets(ctx, in.Account, in.ID)
	if err != nil {
		return port.Integration{}, err //nolint:wrapcheck // the store names the read
	}
	return port.Integration{
		Provider: in.Provider, Settings: in.Settings, Credential: sec.Credential,
		WebhookSecret: sec.WebhookSecret,
	}, nil
}

// Purge deletes up to batch of the deleted account acct's integrations;
// true when none is left.
func (s *Service) Purge(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	return s.store.Purge(ctx, acct, batch) //nolint:wrapcheck // the store names the write
}

// scope is what p's searches see: the spaces it may read and its
// account's tree, read unless tree is given.
func (s *Service) scope(
	ctx context.Context,
	p kernel.Principal,
	tree []accessapi.SpaceNode,
) (Scope, error) {
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return Scope{}, err //nolint:wrapcheck // the snapshot's read names itself
	}
	if tree == nil {
		if tree, err = s.spaces.Tree(ctx, p.AccountID); err != nil {
			return Scope{}, err //nolint:wrapcheck // the access domain names the read
		}
	}
	return Scope{Reachable: reach, Spaces: tree}, nil
}

// readable is the ID of the space slug, the top space when it is empty,
// when p may read it; a *Missing otherwise.
func (s *Service) readable(
	ctx context.Context,
	p kernel.Principal,
	slug string,
) (kernel.ULID, error) {
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

// Reach is the spaces whose integrations a stack in space may bind:
// space, then each ancestor reached through an unbroken chain of spaces
// that inherit their parent's entities, nearest first.
//
// ref: decision 0007 § "Evaluation"
func Reach(tree []accessapi.SpaceNode, space kernel.ULID) []kernel.ULID {
	byID := make(map[kernel.ULID]accessapi.SpaceNode, len(tree))
	for _, n := range tree {
		byID[n.ID] = n
	}
	out := []kernel.ULID{space}
	for x := byID[space]; x.Inherit && !x.Parent.IsZero(); x = byID[x.Parent] {
		out = append(out, x.Parent)
	}
	return out
}

// SpaceGuard is the service's say in the access domain's space deletes
// and moves.
type SpaceGuard struct{ s *Service }

// SpaceGuard is the service as an accessapi.SpaceGuard.
func (s *Service) SpaceGuard() SpaceGuard { return SpaceGuard{s} }

// SpaceInUse reports whether an integration lives in space, which
// refuses its deletion.
func (g SpaceGuard) SpaceInUse(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) (bool, error) {
	return g.s.store.InSpace(ctx, acct, space) //nolint:wrapcheck // the store names the read
}

// LostOnMove refuses no move: a stack checks that its integration is in
// reach when it is written, and a run resolves its integration when it
// starts.
func (SpaceGuard) LostOnMove(context.Context, accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	return nil, nil
}
