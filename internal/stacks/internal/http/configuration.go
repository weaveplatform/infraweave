package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// unbound is the configuration, run status, state and cloud integrations
// of a server whose configuration, runs, state and cloud domains are not
// bound: no stack has a context, a config element, an output or an AWS
// integration attachment, no run blocks one, no run exists, and no stack
// is rolled back.
type unbound struct{}

func (unbound) AttachedPolicies(context.Context, kernel.Principal, []kernel.ULID) (map[kernel.ULID][]stacksapi.AttachedPolicy, error) {
	return map[kernel.ULID][]stacksapi.AttachedPolicy{}, nil
}

func (unbound) PullRequests(context.Context, kernel.AccountID, []stacksapi.Settings) (map[kernel.ULID][]stacksapi.PullRequest, error) {
	return map[kernel.ULID][]stacksapi.PullRequest{}, nil
}

func (unbound) RolledBack(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]bool, error) {
	return map[kernel.ULID]bool{}, nil
}

func (unbound) Blockers(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]stacksapi.Blocker, error) {
	return map[kernel.ULID]stacksapi.Blocker{}, nil
}

func (unbound) RunStack(context.Context, kernel.AccountID, kernel.ULID) (kernel.ULID, error) {
	return kernel.ULID{}, stacksapi.ErrNotFound
}

func (unbound) Outputs(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]stacksapi.Output, error) {
	return map[kernel.ULID][]stacksapi.Output{}, nil
}

func (unbound) AttachedContexts(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]stacksapi.AttachedContext, error) {
	return map[kernel.ULID][]stacksapi.AttachedContext{}, nil
}

func (unbound) Config(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]stacksapi.ConfigElement, error) {
	return map[kernel.ULID][]stacksapi.ConfigElement{}, nil
}

func (unbound) RuntimeConfig(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]stacksapi.ConfigElementWithSource, error) {
	return map[kernel.ULID][]stacksapi.ConfigElementWithSource{}, nil
}

func (unbound) AwsAttachments(context.Context, kernel.Principal, []kernel.ULID) (map[kernel.ULID][]stacksapi.AwsAttachment, error) {
	return map[kernel.ULID][]stacksapi.AwsAttachment{}, nil
}

// edges holds the server's back edges. Every copy of a Server shares
// one, so a binding made after the server was handed out reaches it.
type edges struct {
	aws       stacksapi.StackAwsAttachments
	policies  stacksapi.StackPolicies
	requests  stacksapi.PullRequestReader
	config    stacksapi.StackConfiguration
	status    stacksapi.StackStatus
	rollbacks stacksapi.StateRollbacks
}

// BindPolicies binds the policy projections without a stacks-to-policies import.
func (s Server) BindPolicies(p stacksapi.StackPolicies) { s.edges.policies = p }

func policyView(p stacksapi.AttachedPolicy) api.StackPolicyAttachment {
	records := make([]api.PolicyEvaluationRecord, 0, len(p.EvaluationRecords))
	for _, record := range p.EvaluationRecords {
		records = append(records, api.PolicyEvaluationRecord{Key: record.ID.String(), Outcome: record.Outcome, Timestamp: unix(record.CreatedAt.Unix())})
	}
	return api.StackPolicyAttachment{
		Id:                p.ID.String(),
		PolicyId:          p.Policy.String(),
		PolicyName:        p.Name,
		PolicyBody:        p.Body,
		PolicyLabels:      orEmpty(p.Labels),
		PolicyType:        api.PolicyType(p.Type),
		IsAutoattached:    p.Autoattached,
		EvaluationRecords: records,
	}
}

// BindPullRequests binds the request history read after VCS construction.
func (s Server) BindPullRequests(r stacksapi.PullRequestReader) { s.edges.requests = r }

// BindConfiguration makes c the configuration every copy of s reads. It
// runs during construction, before the server serves a request.
func (s Server) BindConfiguration(c stacksapi.StackConfiguration) { s.edges.config = c }

// BindAwsAttachments makes a what every copy of s reads a stack's AWS
// integration attachments through. It runs during construction, before
// the server serves a request.
func (s Server) BindAwsAttachments(a stacksapi.StackAwsAttachments) { s.edges.aws = a }

// awsAttachmentViews are a stack's AWS integration attachments as the API
// answers them.
func awsAttachmentViews(as []stacksapi.AwsAttachment) []api.StackAwsIntegrationAttachment {
	out := make([]api.StackAwsIntegrationAttachment, 0, len(as))
	for _, a := range as {
		out = append(out, api.StackAwsIntegrationAttachment{
			Id: a.ID, IntegrationId: a.Integration.String(), Name: a.Name, RoleArn: a.RoleARN,
			ExternalId: a.ExternalID, DurationSeconds: a.Duration, Labels: orEmpty(a.Labels),
			GenerateCredentialsInWorker: a.Worker, Legacy: a.Legacy, Read: a.Read, Write: a.Write,
			IsAutoattached: a.Autoattached,
		})
	}
	return out
}

// SetStackStatus makes st the run status every copy of s reads. It runs
// during construction, before the server serves a request.
func (s Server) SetStackStatus(st stacksapi.StackStatus) { s.edges.status = st }

// BindStateRollbacks makes r what every copy of s reads isStateRollback
// through. It runs during construction, before the server serves a
// request.
func (s Server) BindStateRollbacks(r stacksapi.StateRollbacks) { s.edges.rollbacks = r }

// contextView is a context attachment as the API answers it.
func contextView(c stacksapi.AttachedContext) api.StackContextAttachment {
	return api.StackContextAttachment{
		Id:                 c.ID.String(),
		ContextId:          c.Context,
		ContextName:        c.Name,
		ContextDescription: c.Description,
		ContextLabels:      orEmpty(c.Labels),
		ContextHooks:       hooksOf(c.Hooks),
		Config:             elementViews(c.Config),
		Priority:           c.Priority,
		IsAutoattached:     c.Autoattached,
	}
}

// outputViews are a stack's outputs as the API answers them, with no
// value for a sensitive output, and each value the JSON of the tool's
// output, as a string.
func outputViews(outs []stacksapi.Output) []api.StackOutput {
	out := make([]api.StackOutput, 0, len(outs))
	for _, o := range outs {
		v := api.StackOutput{Id: o.Name, Sensitive: o.Sensitive}
		if o.Description != "" {
			v.Description = new(o.Description)
		}
		if !o.Sensitive && o.Value != nil {
			v.Value = new(string(o.Value))
		}
		out = append(out, v)
	}
	return out
}

// elementViews are config elements as the API answers them.
func elementViews(es []stacksapi.ConfigElement) []api.ConfigElement {
	out := make([]api.ConfigElement, 0, len(es))
	for _, e := range es {
		out = append(out, elementView(e))
	}
	return out
}

func elementView(e stacksapi.ConfigElement) api.ConfigElement {
	v := api.ConfigElement{
		Id: e.ID, Type: api.ENVIRONMENTVARIABLE, Checksum: e.Checksum,
		Description: e.Description, FileMode: e.FileMode, WriteOnly: e.WriteOnly,
	}
	if e.FileMount {
		v.Type = api.FILEMOUNT
	}
	if !e.WriteOnly {
		v.Value = e.Value
	}
	if !e.CreatedAt.IsZero() {
		v.CreatedAt = new(unix(e.CreatedAt.Unix()))
	}
	return v
}

func runtimeViews(es []stacksapi.ConfigElementWithSource) []api.ConfigElementWithSource {
	out := make([]api.ConfigElementWithSource, 0, len(es))
	for _, e := range es {
		v := api.ConfigElementWithSource{Element: elementView(e.ConfigElement)}
		if e.Runtime {
			v.Element = api.ConfigElement{
				Id: e.ID, Type: api.ENVIRONMENTVARIABLE, Runtime: true,
				WriteOnly: e.WriteOnly, Category: new(api.INFRAWEAVEENVIRONMENT),
			}
		} else if e.Context != nil {
			v.Context = new(contextView(*e.Context))
		}
		out = append(out, v)
	}
	return out
}
