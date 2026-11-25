package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/cloud/awsfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register registers the cloud area's steps on sc, sharing the scenario's
// World w.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	(&stsSteps{w: w}).register(sc)
	(&entraSteps{w: w}).register(sc)
}

// The servers' AWS identity: the installation's account and the access
// key its credentials carry. The secret is 42 characters, since SigV4's
// HMAC key under FIPS 140-3 needs at least 112 bits.
const (
	accessKey = "AKIAINFRAWEAVEACCEPT"
	secretKey = "acceptance0secret0key0for0the0fake0sts0000"
)

// stsSteps serve a fake AWS STS for the scenario's servers.
type stsSteps struct {
	w       *harness.World
	fake    *awsfake.Fake
	srv     *httptest.Server
	account string
}

func (s *stsSteps) register(sc *godog.ScenarioContext) {
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.srv != nil {
			s.srv.Close()
		}
		return ctx, err
	})
	sc.Step(`^the servers hold AWS credentials of the account "(\d{12})"$`, s.credentials)
	sc.Step(`^the servers hold no AWS credentials$`, s.none)
	sc.Step(`^the AWS role "([^"]+)" trusts the installation with the external ID "([^"]+)"$`, s.trust)
	sc.Step(`^AWS STS fails every call to assume the role "([^"]+)"$`, s.fail)
	sc.Step(`^AWS STS was asked to assume the role "([^"]+)" with the external ID "([^"]+)"$`, s.asked)
	sc.Step(`^the AWS role "([^"]+)" trusts the installation no longer$`, s.refuse)
	sc.Step(`^AWS STS issued the run "(\w+)" credentials with the external IDs "([^"]*)" alone$`,
		s.externalIDs)
	sc.Step(`^AWS STS issued the run "(\w+)" credentials of the role "([^"]+)" with the external ID "([^"]+)", `+
		`the session name "([^"]+)", (\d+) seconds and the session tags "([^"]*)"$`, s.assumption)
	sc.Step(`^the digests of the credentials AWS STS issued the run "(\w+)" with the external ID "([^"]+)" `+
		`are kept as "(\w+)"$`, s.digests)
	sc.Step(`^no credential AWS STS issued is in an answer, a server's log or any of at least (\d+) objects `+
		`of the scenario's bucket$`, s.nowhere)
	sc.Step(`^the answer's runtimeConfig lists the computed write-only variables "([^"]+)"$`, s.computed)
	sc.Step(`^the answer lists the (?:AWS|Azure) integrations "([^"]*)"$`, s.lists)
	sc.Step(`^the answer's attached stacks are "([^"]*)"$`, s.attached)
	sc.Step(`^the stack "([^"]+)" lists the AWS integrations "([^"]*)"$`, s.stackLists)
	sc.Step(`^the stack "([^"]+)" shows the AWS integration "([^"]+)" with the external ID "([^"]+)"$`, s.stackExternalID)
}

// serve starts the fake and points the scenario's servers at it, with the
// SDK's other sources of credentials, configuration and endpoints
// blanked, so neither the host's AWS setup nor instance metadata reaches
// them.
func (s *stsSteps) serve() {
	s.fake = awsfake.New()
	s.srv = httptest.NewServer(s.fake)
	cloudSettings(s.w)["aws"] = map[string]any{"sts_endpoint": s.srv.URL}
	none := filepath.Join(s.w.Dir, "no-aws-config")
	for k, v := range map[string]string{
		"AWS_ACCESS_KEY_ID": "", "AWS_SECRET_ACCESS_KEY": "", "AWS_SESSION_TOKEN": "",
		"AWS_ACCESS_KEY": "", "AWS_SECRET_KEY": "", "AWS_PROFILE": "", "AWS_DEFAULT_PROFILE": "",
		"AWS_CONFIG_FILE": none, "AWS_SHARED_CREDENTIALS_FILE": none, "AWS_ROLE_ARN": "",
		"AWS_WEB_IDENTITY_TOKEN_FILE": "", "AWS_CONTAINER_CREDENTIALS_FULL_URI": "",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI": "", "AWS_ENDPOINT_URL": "",
		"AWS_ENDPOINT_URL_STS": "", "AWS_REGION": "us-east-1", "AWS_DEFAULT_REGION": "us-east-1",
		"AWS_EC2_METADATA_DISABLED": "true",
	} {
		s.w.Setenv(k, v)
	}
}

// credentials gives the servers an access key of account, which the fake
// knows, before they start. An @objectstore scenario's servers hold the
// object store's key instead, since the SDK's default credentials reach
// both services, so the fake knows that key as the installation's too.
func (s *stsSteps) credentials(ctx context.Context, account string) error {
	s.serve()
	s.account = account
	arn := "arn:aws:iam::" + account + ":user/infraweave"
	s.fake.AddCaller(accessKey, account, arn)
	s.w.Setenv("AWS_ACCESS_KEY_ID", accessKey)
	s.w.Setenv("AWS_SECRET_ACCESS_KEY", secretKey)
	if slices.Contains(s.w.Tags, "@objectstore") {
		store, err := s.w.Suite.Deps.ObjectStore(ctx)
		if err != nil {
			return err //nolint:wrapcheck // names @objectstore
		}
		s.fake.AddCaller(store.AccessKey, account, arn)
	}
	return nil
}

// none starts the servers without AWS credentials.
func (s *stsSteps) none() error {
	s.serve()
	return nil
}

// trust adds role, whose trust policy names the installation's account
// and the external ID id, a pattern after the scenario's names resolve.
func (s *stsSteps) trust(role, id string) error {
	if s.account == "" {
		return fmt.Errorf("%w: the servers hold no AWS credentials", harness.ErrStep)
	}
	s.fake.AddRole(awsfake.Role{ARN: role, Trusted: []string{s.account}, ExternalID: s.w.Resolve(id)})
	return nil
}

// fail answers every assumption of role with a server error.
func (s *stsSteps) fail(role string) error {
	if s.fake == nil {
		return fmt.Errorf("%w: no AWS STS in this scenario", harness.ErrStep)
	}
	s.fake.FailRole(role, awsfake.FaultServerError)
	return nil
}

// asked checks that a call assumed role with the external ID id.
func (s *stsSteps) asked(role, id string) error {
	if s.fake == nil {
		return fmt.Errorf("%w: no AWS STS in this scenario", harness.ErrStep)
	}
	id = s.w.Resolve(id)
	var seen []string
	for _, c := range s.fake.Calls() {
		if c.Action == "AssumeRole" && c.RoleARN == role {
			if c.ExternalID == id {
				return nil
			}
			seen = append(seen, c.ExternalID)
		}
	}
	return fmt.Errorf("%w: %s was assumed with the external IDs %v, not %s", harness.ErrStep,
		role, seen, id)
}

// page is the last answer, a page of integrations.
func (s *stsSteps) page() ([]map[string]any, error) {
	var page struct {
		Items []map[string]any `json:"items"`
	}
	r := s.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &page) != nil {
		return nil, fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	return page.Items, nil
}

// lists checks the names of the last answer's integrations, in order.
func (s *stsSteps) lists(want string) error {
	items, err := s.page()
	if err != nil {
		return err
	}
	got := make([]string, len(items))
	for n, i := range items {
		got[n] = fmt.Sprint(i["name"])
	}
	if strings.Join(got, ", ") != want {
		return fmt.Errorf("%w: lists %q, want %q", harness.ErrStep, strings.Join(got, ", "), want)
	}
	return nil
}

// attached checks the last answer's attachedStacks, an integration's, as
// stack:sides[:auto], comma-separated, in order.
func (s *stsSteps) attached(want string) error {
	var v struct {
		AttachedStacks []struct {
			StackID        string `json:"stackId"`
			Read           bool   `json:"read"`
			Write          bool   `json:"write"`
			IsAutoattached bool   `json:"isAutoattached"`
		} `json:"attachedStacks"`
	}
	r := s.w.Last()
	if r.Status/100 != 2 || json.Unmarshal(r.Body, &v) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	got := make([]string, 0, len(v.AttachedStacks))
	for _, a := range v.AttachedStacks {
		sides := ""
		if a.Read {
			sides += "r"
		}
		if a.Write {
			sides += "w"
		}
		item := a.StackID + ":" + sides
		if a.IsAutoattached {
			item += ":auto"
		}
		got = append(got, item)
	}
	if !slices.Equal(got, splitList(want)) {
		return fmt.Errorf("%w: attached %q, want %q", harness.ErrStep, strings.Join(got, ", "), want)
	}
	return nil
}

func splitList(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ", ")
}

// stackAttachment is an AWS integration attachment as a stack answers it.
type stackAttachment struct {
	Name           string `json:"name"`
	ExternalID     string `json:"externalId"`
	Read           bool   `json:"read"`
	Write          bool   `json:"write"`
	IsAutoattached bool   `json:"isAutoattached"`
}

// stackAttachments are the attachedAwsIntegrations of the stack slug in
// the last answer, a stack or a page of stacks.
func (s *stsSteps) stackAttachments(slug string) ([]stackAttachment, error) {
	type stack struct {
		ID       string            `json:"id"`
		Attached []stackAttachment `json:"attachedAwsIntegrations"`
	}
	var v struct {
		stack
		Items []stack `json:"items"`
	}
	r := s.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &v) != nil {
		return nil, fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	for _, st := range append(v.Items, v.stack) {
		if st.ID == slug {
			return st.Attached, nil
		}
	}
	return nil, fmt.Errorf("%w: the answer holds no stack %s: %s", harness.ErrStep, slug, r.Body)
}

// stackLists checks the AWS integrations the stack slug lists in the last
// answer as name:sides[:auto], comma-separated, in order.
func (s *stsSteps) stackLists(slug, want string) error {
	all, err := s.stackAttachments(slug)
	if err != nil {
		return err
	}
	got := make([]string, 0, len(all))
	for _, a := range all {
		item := a.Name + ":"
		if a.Read {
			item += "r"
		}
		if a.Write {
			item += "w"
		}
		if a.IsAutoattached {
			item += ":auto"
		}
		got = append(got, item)
	}
	if !slices.Equal(got, splitList(want)) {
		return fmt.Errorf("%w: stack %s lists %q, want %q", harness.ErrStep, slug, strings.Join(got, ", "), want)
	}
	return nil
}

// stackExternalID checks the external ID the stack slug's attachment of
// the integration name shows in the last answer, after the scenario's
// names resolve.
func (s *stsSteps) stackExternalID(slug, name, want string) error {
	all, err := s.stackAttachments(slug)
	if err != nil {
		return err
	}
	want = s.w.Resolve(want)
	for _, a := range all {
		if a.Name == name {
			if a.ExternalID != want {
				return fmt.Errorf("%w: %s shows the external ID %q, want %q", harness.ErrStep, name, a.ExternalID, want)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: stack %s lists no AWS integration %s", harness.ErrStep, slug, name)
}
