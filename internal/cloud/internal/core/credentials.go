package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	cloudapi "github.com/weaveplatform/infraweave/internal/cloud/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// The session tags of an integration that tags its sessions: the run's
// ULID, the stack's slug and its space's slug. They are not transitive.
//
// ref: report 12 § "2.5 Session tagging"
const (
	TagRun   = "run"
	TagStack = "stack"
	TagSpace = "space"
)

// errScope is a phase request whose scope is neither read nor write.
var errScope = errors.New("cloud: a phase's scope is read or write")

// ForPhase answers the cloud credentials of the phase r names: those of
// the integration whose attachment, stored or computed, serves the
// stack's side r.Scope, assumed with the external ID of that side, or the
// legacy external ID of a stack's legacy AWS role, and the run's session
// name, for the integration's duration in its region, with the session
// tags when it tags its sessions. An integration the server assumes is
// assumed now, once per handover, and its credentials are answered as
// the four computed AWS variables, each a secret, and kept nowhere. An
// integration a private worker assumes is answered as the assumption the
// worker makes, with the integration's custom external ID when it sets
// one. A side no attachment serves answers nothing. STS refusing or
// failing the assumption is a *kernel.Invalid naming the integration and
// STS's error code, never a credential.
//
// ref: decision 0006 "AWS external ID and session name"
// ref: report 12 § "2.1 Behaviour"
func (s *Service) ForPhase(ctx context.Context, r cloudapi.PhaseRequest) (cloudapi.Phase, error) {
	side := Side(r.Scope)
	if side != Read && side != Write {
		return cloudapi.Phase{}, fmt.Errorf("%w: %q", errScope, r.Scope)
	}
	st, err := s.stacks.Get(ctx, r.Account, r.Stack)
	if err != nil {
		return cloudapi.Phase{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	labels, err := s.stacks.Labels(ctx, r.Account, []kernel.ULID{st.ID})
	if err != nil {
		return cloudapi.Phase{}, err //nolint:wrapcheck // as above
	}
	nodes, err := s.spaces.Tree(ctx, r.Account)
	if err != nil {
		return cloudapi.Phase{}, err //nolint:wrapcheck // the access domain names the read
	}
	tree := index(nodes)
	attached, err := s.Effective(ctx, r.Account, st, labels[st.ID], tree)
	if err != nil {
		return cloudapi.Phase{}, err
	}
	n := slices.IndexFunc(attached, func(a Attachment) bool {
		return (side == Read && a.Read) || (side == Write && a.Write)
	})
	if n < 0 {
		return cloudapi.Phase{}, nil
	}
	i, err := s.store.Get(ctx, r.Account, attached[n].Integration, NoLock)
	if err != nil {
		return cloudapi.Phase{}, err //nolint:wrapcheck // the store names the read
	}
	account, err := s.accounts.Slug(ctx, r.Account)
	if err != nil {
		return cloudapi.Phase{}, err //nolint:wrapcheck // the access domain names the read
	}
	a := PhaseAssumption(i, st, tree[st.Space].Slug, account, r.Run, side)
	if i.Worker {
		return cloudapi.Phase{WorkerAssumptions: []cloudapi.Assumption{{
			Integration: i.ID, IntegrationName: i.Name, RoleARN: a.RoleARN,
			ExternalID: a.ExternalID, SessionName: a.SessionName, DurationSeconds: i.Duration,
			Region: a.Region, Tags: a.Tags,
		}}}, nil
	}
	session, err := s.roles.Assume(ctx, a)
	if err != nil {
		return cloudapi.Phase{}, assumeFailed(i, err)
	}
	return cloudapi.Phase{Environment: []cloudapi.Variable{
		{Name: cloudapi.AWSAccessKeyID, Value: session.AccessKeyID, Secret: true},
		{Name: cloudapi.AWSSecretAccessKey, Value: session.SecretAccessKey, Secret: true},
		{Name: cloudapi.AWSSecurityToken, Value: session.SessionToken, Secret: true},
		{Name: cloudapi.AWSSessionToken, Value: session.SessionToken, Secret: true},
	}}, nil
}

// PhaseAssumption is the assumption of i's role for run's phase on side
// of the stack st, in the space whose slug is space, in the account whose
// slug is account: the side's external ID, or the legacy external ID of
// a legacy integration, either replaced by a worker's integration's
// custom external ID when it sets one; the run's session name; the
// integration's duration and region; and the session tags when the
// integration tags its sessions.
//
// ref: decision 0006 "AWS external ID and session name"
func PhaseAssumption(i Integration, st stacksapi.Stack, space, account string, run kernel.ULID, side Side) Assumption {
	external := ExternalID(account, i.ID, st.Slug, side)
	if i.Legacy {
		external = LegacyExternalID(account, st.Slug)
	}
	if i.Worker {
		external = cmp.Or(i.ExternalID, external)
	}
	a := Assumption{
		RoleARN: i.RoleARN, ExternalID: external, SessionName: SessionName(run, st.Slug, account),
		Duration: time.Duration(i.Duration) * time.Second,
	}
	if i.Region != nil {
		a.Region = *i.Region
	}
	if i.TagSession {
		a.Tags = map[string]string{TagRun: run.String(), TagStack: st.Slug, TagSpace: space}
	}
	return a
}

// accessDenied is STS's error code for a refused assumption, the one
// Roles answers as a *Refused.
const accessDenied = "AccessDenied"

// assumeFailed is err, the failure of assuming i's role at a handover, as
// the *kernel.Invalid that fails the run in preparation: it names the
// integration, the role and STS's error code, and for a refusal the trust
// text clients match on. STS's message is left out, since it names the
// installation's own principal.
func assumeFailed(i Integration, err error) error {
	prefix := fmt.Sprintf("AWS integration %s could not assume role %s", i.Name, i.RoleARN)
	if errors.Is(err, ErrUnsupported) {
		return &kernel.Invalid{Detail: prefix + ": the installation holds no AWS credentials."}
	}
	if upstream, ok := errors.AsType[*Upstream](err); ok {
		return &kernel.Invalid{Detail: fmt.Sprintf("%s: AWS STS failed: %s.", prefix, cmp.Or(upstream.Code, "no answer"))}
	}
	if _, ok := errors.AsType[*Refused](err); ok {
		return &kernel.Invalid{Detail: fmt.Sprintf("%s: %s: %s.", prefix, accessDenied, TrustDetail)}
	}
	return err
}

// CredentialVariables are the computed variables the runs of each of
// stacks receive their cloud credentials in, keyed by stack ID: the AWS
// variables for a stack an attachment serves, stored or computed, on
// either side; a stack none serves is absent.
func (s *Service) CredentialVariables(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID][]string, error) {
	attached, err := s.Attached(ctx, acct, stacks)
	if err != nil {
		return nil, err
	}
	out := map[kernel.ULID][]string{}
	for id, as := range attached {
		if len(as) > 0 {
			out[id] = cloudapi.AWSVariables()
		}
	}
	return out, nil
}
