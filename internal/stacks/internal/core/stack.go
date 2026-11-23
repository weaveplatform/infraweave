package core

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Stack is a stack as the domain keeps it: the settings its runs read, the
// ones only the API shows, and State and Blocked, the runs domain's
// projection of its tracked run and run lock, which a write of its
// settings leaves as they are.
type Stack struct {
	stacksapi.Settings
	Description *string
	Labels      []string
	CreatedAt   time.Time
	State       kernel.StackState
	Blocked     bool
}

// DefaultProvider is the VCS provider of a stack whose input names none.
const DefaultProvider = "GITHUB"

// Input is a stack's settings as a write gives them, with its space by
// slug, empty for the account's top space. VendorConfig is the input's
// vendorConfig as JSON, nil when it has none. WorkerPool and
// VCSIntegration are the IDs the input names, nil when it names none.
type Input struct {
	Space                        string
	Name                         string
	Description                  *string
	Repository                   string
	RepositoryURL                *string
	Branch                       string
	Provider                     *string
	Namespace                    *string
	ProjectRoot                  *string
	AdditionalProjectGlobs       []string
	GitSparseCheckoutPaths       []string
	RunnerImage                  *string
	WorkerPool                   *string
	VCSIntegration               *string
	VendorConfig                 json.RawMessage
	TerraformVersion             *string
	Hooks                        map[string][]string
	Labels                       []string
	Autodeploy                   *bool
	Autoretry                    *bool
	ProtectFromDeletion          *bool
	Disabled                     *bool
	LocalPreviewEnabled          *bool
	GithubActionDeploy           *bool
	EnableSensitiveOutputUpload  *bool
	EnableWellKnownSecretMasking *bool
	SkipVendorBinaryDownload     *bool
}

// invalidDetail is the detail of an input the checks refuse.
const invalidDetail = "The stack's settings are refused."

// check refuses each field of in a stack cannot hold, with pointers
// under at, the input's place in the request body. A worker pool ID and
// a VCS integration ID are the workers and vcs domains' to check, once
// the stack's space is known.
func check(at string, in Input) error {
	var errs []kernel.FieldError
	refuse := func(field, reason, message string) {
		errs = append(errs, kernel.FieldError{
			Pointer: at + "/" + field, Reason: reason, Message: message,
		})
	}
	switch {
	case strings.TrimSpace(in.Name) == "":
		refuse("name", kernel.ReasonRequired, "A stack needs a name.")
	case !kernel.SlugFrom(in.Name).Valid():
		refuse("name", kernel.ReasonRule, "A stack's name needs a letter a to z or a digit, "+
			"from which its slug is made.")
	}
	if strings.TrimSpace(in.Repository) == "" {
		refuse("repository", kernel.ReasonRequired, "A stack needs a repository.")
	}
	if strings.TrimSpace(in.Branch) == "" {
		refuse("branch", kernel.ReasonRequired, "A stack needs a branch.")
	}
	errs = append(errs, kernel.CheckLabels(at+"/labels", in.Labels)...)
	err := iac.ValidateSettings(iac.Input{
		VendorConfig: in.VendorConfig, TerraformVersion: in.TerraformVersion,
	})
	if inv, ok := err.(*kernel.Invalid); ok { //nolint:errorlint // ValidateSettings returns it bare
		for _, e := range inv.Errors {
			e.Pointer = at + e.Pointer
			errs = append(errs, e)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &kernel.Invalid{Detail: invalidDetail, Errors: errs}
}

// vendorOf is the vendor in's vendorConfig sets and that vendor's
// settings, Terraform's empty settings when it sets none. check has
// refused a vendorConfig that sets more than one.
func vendorOf(raw json.RawMessage) (string, json.RawMessage) {
	var members map[string]json.RawMessage
	_ = json.Unmarshal(raw, &members) // check has refused one that is not an object
	for _, name := range slices.Sorted(maps.Keys(members)) {
		if v := members[name]; len(v) > 0 && string(v) != "null" {
			return name, v
		}
	}
	return string(iac.Terraform), json.RawMessage(`{}`)
}

// ExternalAccess is whether vendor's settings config turn on
// externalStateAccessEnabled, which only Terraform's and OpenTofu's carry.
func ExternalAccess(vendor string, config json.RawMessage) bool {
	switch iac.Kind(vendor) {
	case iac.Terraform, iac.OpenTofu:
	default:
		return false
	}
	var c struct {
		On bool `json:"externalStateAccessEnabled"`
	}
	_ = json.Unmarshal(config, &c) // check has refused a setting that is not a boolean
	return c.On
}

// checkExternal refuses external state access on st when it keeps no
// managed state, the only state external access reads, pointing at the
// setting under at.
//
// ref: decision 0010 § "Who may call what"
func checkExternal(at string, st *Stack) error {
	if !st.ExternalStateAccess || st.ManagesState {
		return nil
	}
	return &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{{
		Pointer: at + "/vendorConfig/" + st.Vendor + "/externalStateAccessEnabled",
		Reason:  kernel.ReasonRule,
		Message: "External state access reads the state infraweave manages, " +
			"which this stack does not.",
	}}}
}

// managesState reports whether a stack created with st's vendor settings
// and manageState keeps its state, as its vendor decides. check has
// refused a vendor the registry lacks, so the lookup finds one.
func managesState(st stacksapi.Settings, manageState bool) bool {
	v, ok := iac.Lookup(iac.Kind(st.Vendor))
	return ok && v.ManagesState(st.VendorConfig, manageState)
}

// apply sets st's settings from in, the space's ID already resolved, as
// a PUT replaces them: a setting in leaves out takes its default.
func apply(st *Stack, space kernel.ULID, in Input) {
	st.Space = space
	st.Name = strings.TrimSpace(in.Name)
	st.Description = in.Description
	st.Repository, st.RepositoryURL, st.Branch = in.Repository, in.RepositoryURL, in.Branch
	st.Provider = DefaultProvider
	if in.Provider != nil {
		st.Provider = *in.Provider
	}
	st.Namespace = ""
	if in.Namespace != nil {
		st.Namespace = *in.Namespace
	}
	st.ProjectRoot, st.RunnerImage = in.ProjectRoot, in.RunnerImage
	st.AdditionalProjectGlobs = orEmpty(in.AdditionalProjectGlobs)
	st.GitSparseCheckoutPaths = in.GitSparseCheckoutPaths
	st.WorkerPool, st.VCSIntegration = nil, nil
	st.Vendor, st.VendorConfig = vendorOf(in.VendorConfig)
	st.ExternalStateAccess = ExternalAccess(st.Vendor, st.VendorConfig)
	st.TerraformVersion = in.TerraformVersion
	st.Hooks = map[string][]string{}
	for _, phase := range stacksapi.HookPhases {
		if cmds := in.Hooks[phase]; len(cmds) > 0 {
			st.Hooks[phase] = slices.Clone(cmds)
		}
	}
	st.Labels = slices.Sorted(slices.Values(in.Labels))
	st.Autodeploy = deref(in.Autodeploy, false)
	st.Autoretry = deref(in.Autoretry, false)
	st.ProtectFromDeletion = deref(in.ProtectFromDeletion, false)
	st.Disabled = deref(in.Disabled, false)
	st.LocalPreviewEnabled = deref(in.LocalPreviewEnabled, false)
	st.GithubActionDeploy = deref(in.GithubActionDeploy, true)
	st.EnableSensitiveOutputUpload = deref(in.EnableSensitiveOutputUpload, false)
	st.EnableWellKnownSecretMasking = deref(in.EnableWellKnownSecretMasking, false)
	st.SkipVendorBinaryDownload = deref(in.SkipVendorBinaryDownload, false)
}

func deref(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
