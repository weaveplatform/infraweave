package app

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	operatorapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	workerapi "github.com/weaveplatform/infraweave/internal/platform/gen/worker"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	policiesassembly "github.com/weaveplatform/infraweave/internal/policies/assembly"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	vcsassembly "github.com/weaveplatform/infraweave/internal/vcs/assembly"
)

// Operation is what the controls in front of the router need to know about
// the operation a request is routed to.
type Operation struct {
	// Cost is the operation's x-infraweave-cost.
	Cost ratelimit.Class
	// Search marks a search, which changes nothing and so ignores
	// Idempotency-Key.
	Search bool
	// Unkeyed marks an operation whose x-infraweave-idempotency is
	// ignored, which ignores Idempotency-Key: each call signs something
	// that expires, such as a URL, so a replay from the key store could
	// answer one that has expired, and a retry is already safe.
	//
	// ref: decision 0003 § "API surfaces"
	Unkeyed bool
	// Worker marks an operation of the worker API, which a worker session
	// token authenticates. It decides no account action and no feature,
	// and ignores Idempotency-Key: its answers carry session tokens, which
	// a replay would hand out again, and a registration's jti already
	// refuses a replay.
	//
	// ref: decision 0003 § "API surfaces"
	Worker bool
	// Runner marks an operation of the worker API whose security is
	// runToken, a claimed run's call, which a run token authenticates in
	// place of a worker session token.
	//
	// ref: decision 0006 § "The principal"
	Runner bool
	// State marks a route of the state backend, which a run token
	// authenticates as the password of HTTP Basic credentials and which
	// ignores Idempotency-Key.
	//
	// ref: decision 0003 § "API surfaces"
	State bool
	// Public marks an operation that takes no credential, whose security
	// is the empty list, such as POST /auth/token. Every other operation
	// requires the API's global bearerAuth.
	Public bool
	// Hold is how long an operation that holds its response on purpose may
	// hold it, past the listener's write timeout: the worker poll's
	// longest hold, which API sets. Zero for every other operation.
	//
	// ref: decision 0003 § "API surfaces"
	Hold time.Duration
	// OwnBodyBound marks an operation whose handler bounds its request body
	// itself, so server.max_body_bytes does not: the fs driver's blob PUT,
	// whose objects, such as state files, exceed any API body.
	//
	// ref: decision 0003 § "API surfaces"
	OwnBodyBound bool
	// Organization marks an operation whose x-infraweave-access is
	// organization: it serves any session of an organization, with an
	// account or none, such as the viewer or the account switcher. Every
	// other operation needs a credential of an account.
	//
	// ref: decision 0006
	Organization bool
	// OrganizationAdmin marks an operation whose x-infraweave-access is
	// organization-admin, the Organization tag's: it serves the principals
	// that may hold the organization role and organization tokens, which no
	// other operation accepts.
	//
	// ref: decision 0006
	OrganizationAdmin bool
	// Body is the request body of an operation whose JSON body schema sets
	// additionalProperties: false, which strictBodies checks before the
	// handler; nil for every other operation, whose body the generated
	// handler decodes leniently. A PATCH beside an update PUT checks its
	// merge patch against patchSchema of the PUT's input.
	Body *openapi3.RequestBody
	// SemanticBodyFields names body subtrees whose type and unknown-field
	// refusals are operation validation (422). Other schema refusals keep
	// their normal 400, including missing required fields.
	SemanticBodyFields []string
	// Path is the operation's path parameters with a schema, which
	// strictPaths checks before the handler as strictQueries checks the
	// query; empty for an operation that takes none.
	Path openapi3.Parameters
	// Query is the operation's query parameters with a schema, a scalar's
	// or the JSON object's of one such as a search's input, which
	// strictQueries checks before the handler as strictBodies checks a
	// body; empty for an operation that takes none.
	Query openapi3.Parameters
	// Gate is the operation's x-infraweave-feature, a capability of the
	// catalogue, and the options its request body's properties name.
	Gate featuresapi.Gate
	// Required is the API operation's x-infraweave-action and
	// x-infraweave-subject, which the authorization middleware decides on
	// the caller's roles; nil for the operator API's operations and the
	// routes beside the APIs, which no action decides.
	//
	// ref: decision 0007
	Required *accessapi.Required
}

// Operations maps "METHOD pattern", a method and the chi route pattern it is
// mounted at, to its operation.
type Operations map[string]Operation

var errSpec = errors.New("invalid operation in the API")

// operationsOf reads each operation of doc, mounted under base. A missing
// x-infraweave-cost is standard; one that names no class fails, so a typo
// in the spec cannot silently cost standard. Every operation names its
// capability in x-infraweave-feature, and one that names none, or a name
// the catalogue holds as no capability, fails, so no operation goes
// ungated. With authorized, every operation names its action and subject,
// and one that lacks either or names an action the catalogue lacks fails,
// so no operation goes undecided; one whose action creates runs and that
// is not expensive fails, so no run is created at the budget of an
// ordinary write.
//
// ref: decisions 0003, 0007 and 0021
func operationsOf(doc *openapi3.T, base string, authorized bool) (Operations, error) {
	ops := Operations{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			body := mediaOf(op)
			if method == http.MethodPatch && item.Put != nil && isMergePatch(body) {
				body = patchMedia(mediaOf(item.Put))
			}
			cost := ratelimit.Standard
			if v, ok := op.Extensions["x-infraweave-cost"]; ok {
				s, _ := v.(string)
				cost = ratelimit.Class(s)
				if cost != ratelimit.Standard && cost != ratelimit.Expensive {
					return nil, fmt.Errorf("%w: %s %s has x-infraweave-cost %v",
						errSpec, method, path, v)
				}
			}
			style, _ := op.Extensions["x-rest-style"].(string)
			var org, admin bool
			switch v, ok := op.Extensions["x-infraweave-access"]; {
			case !ok:
			case v == "organization":
				org = true
			case v == "organization-admin":
				admin = true
			default:
				return nil, fmt.Errorf("%w: %s %s has x-infraweave-access %v",
					errSpec, method, path, v)
			}
			var unkeyed bool
			switch v, ok := op.Extensions["x-infraweave-idempotency"]; {
			case !ok:
			case v == "ignored":
				unkeyed = true
			default:
				return nil, fmt.Errorf("%w: %s %s has x-infraweave-idempotency %v",
					errSpec, method, path, v)
			}
			gate, err := gateOf(op, body)
			if err != nil {
				return nil, fmt.Errorf("%w: %s %s %w", errSpec, method, path, err)
			}
			var required *accessapi.Required
			if authorized {
				if required, err = requiredOf(op); err != nil {
					return nil, fmt.Errorf("%w: %s %s %w", errSpec, method, path, err)
				}
				if createsRuns[required.Action] && cost != ratelimit.Expensive {
					return nil, fmt.Errorf("%w: %s %s %w", errSpec, method, path, errRunCost)
				}
			}
			var semanticBodyFields []string
			// The embedded generated spec capitalizes operation IDs.
			if op.OperationID == "runTrigger" || op.OperationID == "RunTrigger" {
				semanticBodyFields = []string{"/runtimeConfig"}
			}
			ops[method+" "+base+path] = Operation{
				Cost:               cost,
				Search:             style == "search" || strings.HasSuffix(path, "/search"),
				Unkeyed:            unkeyed,
				Public:             op.Security != nil && len(*op.Security) == 0,
				Organization:       org,
				OrganizationAdmin:  admin,
				Body:               strictBody(body),
				SemanticBodyFields: semanticBodyFields,
				Path:               strictParams(op, openapi3.ParameterInPath),
				Query:              strictParams(op, openapi3.ParameterInQuery),
				Gate:               gate,
				Required:           required,
			}
		}
	}
	return ops, nil
}

// workerOperationsOf reads each operation of the worker API's doc,
// mounted under base, as operationsOf does but for what Operation.Worker
// leaves out: no action, feature or organization access applies.
//
// ref: decisions 0003 and 0008
func workerOperationsOf(doc *openapi3.T, base string) (Operations, error) {
	ops := Operations{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			cost := ratelimit.Standard
			if v, ok := op.Extensions["x-infraweave-cost"]; ok {
				s, _ := v.(string)
				cost = ratelimit.Class(s)
				if cost != ratelimit.Standard && cost != ratelimit.Expensive {
					return nil, fmt.Errorf("%w: %s %s has x-infraweave-cost %v",
						errSpec, method, path, v)
				}
			}
			ops[method+" "+base+path] = Operation{
				Cost:   cost,
				Public: op.Security != nil && len(*op.Security) == 0,
				Worker: true,
				Runner: runToken(op.Security),
				Body:   strictBody(mediaOf(op)),
				Path:   strictParams(op, openapi3.ParameterInPath),
				Query:  strictParams(op, openapi3.ParameterInQuery),
			}
		}
	}
	return ops, nil
}

// runToken reports whether security is the worker API's runToken alone,
// which the authenticator decides between a worker session token and a
// run token by.
func runToken(security *openapi3.SecurityRequirements) bool {
	if security == nil || len(*security) != 1 {
		return false
	}
	_, ok := (*security)[0]["runToken"]
	return ok && len((*security)[0]) == 1
}

var errAction = errors.New("names no action of the catalogue")

var errRunCost = errors.New("creates runs and is not x-infraweave-cost expensive")

// createsRuns holds the actions of the operations that create runs, or
// plan one again, which draw on the expensive budget as other fan-out
// work does. stackDelete creates a destroy run only with destroyResources
// and decides STACK_DELETE, so it costs standard: a class cannot depend on
// a parameter.
//
// ref: decision 0003 § "Rate limits"
var createsRuns = map[accessapi.Action]bool{
	accessapi.ActionRunTrigger:               true,
	accessapi.ActionRunDestroyTrigger:        true,
	accessapi.ActionTaskCreate:               true,
	accessapi.ActionRunProposeWithOverrides:  true,
	accessapi.ActionRunProposeLocalWorkspace: true,
	accessapi.ActionRunRetry:                 true,
	accessapi.ActionRunPromote:               true,
	accessapi.ActionRunTargetedReplan:        true,
}

// requiredOf reads op's action and subject.
func requiredOf(op *openapi3.Operation) (*accessapi.Required, error) {
	name, _ := op.Extensions["x-infraweave-action"].(string)
	a := accessapi.Action(name)
	_, known := accessapi.ActionOf(a)
	switch a {
	case accessapi.ActionAccountAdmin, accessapi.ActionOrganizationAdmin,
		accessapi.ActionAuthenticated:
		known = true
	}
	if !known {
		return nil, fmt.Errorf("x-infraweave-action %q %w", name, errAction)
	}
	raw, _ := op.Extensions["x-infraweave-subject"].(string)
	s, err := accessapi.ParseSubject(raw)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the subject
	}
	return &accessapi.Required{Action: a, Subject: s}, nil
}

var errFeature = errors.New("names no feature of its kind")

// gateOf reads op's capability, and the options its JSON body's
// properties carry as x-infraweave-feature: {feature, value}, at any depth
// of nested objects, where no value means any value but null.
func gateOf(op *openapi3.Operation, mt *bodyMedia) (featuresapi.Gate, error) {
	name, _ := op.Extensions["x-infraweave-feature"].(string)
	if f, ok := features.Lookup(name); !ok || f.Kind != features.Capability {
		return featuresapi.Gate{}, fmt.Errorf("x-infraweave-feature %q %w", name, errFeature)
	}
	g := featuresapi.Gate{Feature: name}
	if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
		return g, nil
	}
	var err error
	g.Options, err = optionsOf("", mt.Schema.Value, map[*openapi3.Schema]bool{}, nil)
	if err != nil {
		return featuresapi.Gate{}, err
	}
	return g, nil
}

// pointerEscape escapes a property name as a JSON Pointer reference token.
var pointerEscape = strings.NewReplacer("~", "~0", "/", "~1")

// optionsOf appends to out the option gates of s's properties, whose
// pointers start with at, and of the objects below them. open holds the
// schemas being walked, so a schema that contains itself is walked once
// on each path, while one that two properties share is walked under both.
func optionsOf(at string, s *openapi3.Schema, open map[*openapi3.Schema]bool, out []featuresapi.OptionGate) ([]featuresapi.OptionGate, error) {
	if open[s] {
		return out, nil
	}
	open[s] = true
	defer delete(open, s)
	for _, prop := range slices.Sorted(maps.Keys(s.Properties)) {
		ref := s.Properties[prop]
		pointer := at + "/" + pointerEscape.Replace(prop)
		ext, ok := ref.Extensions["x-infraweave-feature"].(map[string]any)
		if !ok && ref.Value != nil {
			ext, ok = ref.Value.Extensions["x-infraweave-feature"].(map[string]any)
		}
		if ok {
			option, _ := ext["feature"].(string)
			if f, ok := features.Lookup(option); !ok || f.Kind != features.Option {
				return nil, fmt.Errorf("property %s's x-infraweave-feature %q %w",
					pointer, option, errFeature)
			}
			values, err := optionValues(ext)
			if err != nil {
				return nil, fmt.Errorf("property %s's x-infraweave-feature: %w", pointer, err)
			}
			for _, value := range values {
				out = append(out, featuresapi.OptionGate{Pointer: pointer, Value: value, Feature: option})
			}
		}
		if ref.Value != nil {
			var err error
			if out, err = optionsOf(pointer, ref.Value, open, out); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Each value becomes an ordinary option condition, so the features domain
// remains the single evaluator of entitlement and operator-switch policy.
func optionValues(ext map[string]any) ([]string, error) {
	raw, many := ext["values"]
	value, one := ext["value"]
	if many && one {
		return nil, fmt.Errorf("%w: value and values are mutually exclusive", errFeature)
	}
	if !many {
		if !one {
			return []string{""}, nil
		}
		v, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: value must be a string", errFeature)
		}
		return []string{v}, nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("%w: values must be a nonempty string array", errFeature)
	}
	out := make([]string, 0, len(list))
	for _, raw := range list {
		value, ok := raw.(string)
		if !ok || value == "" {
			return nil, fmt.Errorf("%w: values must contain nonempty strings", errFeature)
		}
		out = append(out, value)
	}
	return out, nil
}

// strictBody is the request body of mt, an operation's body media, when
// its schema forbids properties it does not name.
func strictBody(mt *bodyMedia) *openapi3.RequestBody {
	if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
		return nil
	}
	if ap := mt.Schema.Value.AdditionalProperties.Has; ap == nil || *ap {
		return nil
	}
	return &openapi3.RequestBody{
		Required: mt.required,
		Content:  openapi3.Content{mt.typ: &mt.MediaType},
	}
}

// strictParams is op's parameters in in, the path or the query, with a
// schema, a scalar's or the JSON object's one carries. The served
// documents declare every parameter on its operation, none on its path,
// which TestUnitAPIParameters holds them to.
func strictParams(op *openapi3.Operation, in string) openapi3.Parameters {
	var out openapi3.Parameters
	for _, p := range op.Parameters {
		if p.Value == nil || p.Value.In != in {
			continue
		}
		if p.Value.Schema != nil {
			out = append(out, p)
			continue
		}
		if mt := p.Value.Content.Get(jsonType); mt != nil && mt.Schema != nil {
			out = append(out, p)
		}
	}
	return out
}

// The media types of the API's request bodies: JSON, and a JSON Merge
// Patch for PATCH.
//
// ref: decision 0022
const (
	jsonType       = "application/json"
	mergePatchType = "application/merge-patch+json"
)

// bodyMedia is an operation's request body media: its type, its schema
// and whether the body is required.
type bodyMedia struct {
	openapi3.MediaType
	typ      string
	required bool
}

// mediaOf is op's JSON or merge-patch request body media, nil for an
// operation with neither.
func mediaOf(op *openapi3.Operation) *bodyMedia {
	if op == nil || op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil
	}
	for _, typ := range []string{jsonType, mergePatchType} {
		if mt := op.RequestBody.Value.Content.Get(typ); mt != nil {
			return &bodyMedia{MediaType: *mt, typ: typ, required: op.RequestBody.Value.Required}
		}
	}
	return nil
}

// isMergePatch reports whether mt is the generic MergePatch body of a
// PATCH beside an update PUT, which names no members of its own.
func isMergePatch(mt *bodyMedia) bool {
	return mt != nil && mt.typ == mergePatchType && mt.Schema != nil &&
		mt.Schema.Value != nil && len(mt.Schema.Value.Properties) == 0
}

// patchMedia is the merge-patch body that put, an update PUT's body,
// takes beside it: patchSchema of the PUT's input.
func patchMedia(put *bodyMedia) *bodyMedia {
	if put == nil || put.Schema == nil || put.Schema.Value == nil {
		return nil
	}
	return &bodyMedia{
		MediaType: openapi3.MediaType{Schema: &openapi3.SchemaRef{
			Value: patchSchema(put.Schema.Value, map[*openapi3.Schema]*openapi3.Schema{}),
		}},
		typ:      mergePatchType,
		required: true,
	}
}

// patchSchema is s as a merge patch of it may be: every member optional
// and nullable, since null resets it, and each object member a patch of
// its own, since a merge patch merges into objects. It allows no member
// that s refuses, so an unknown field in a PATCH is refused as a PUT
// refuses it. done holds the schemas already made, so a schema that
// contains itself is made once.
func patchSchema(s *openapi3.Schema, done map[*openapi3.Schema]*openapi3.Schema) *openapi3.Schema {
	if p, ok := done[s]; ok {
		return p
	}
	p := *s
	p.Required = nil
	p.Nullable = true
	done[s] = &p
	if len(s.Properties) > 0 {
		p.Properties = make(openapi3.Schemas, len(s.Properties))
		for name, ref := range s.Properties {
			if ref == nil || ref.Value == nil {
				p.Properties[name] = ref
				continue
			}
			p.Properties[name] = &openapi3.SchemaRef{
				Value:      patchSchema(ref.Value, done),
				Extensions: ref.Extensions,
			}
		}
	}
	return &p
}

// apiOperations reads the embedded specs once: the API's under BaseURL,
// the operator API's under OperatorBaseURL and the worker API's under
// WorkerBaseURL, which one router serves.
// It cannot fail at run time unless a generated package is corrupt, which
// TestUnitAPIOperations would already have caught.
var apiOperations = sync.OnceValue(func() Operations {
	ops := Operations{}
	for base, spec := range map[string]func() (*openapi3.T, error){
		BaseURL:         api.GetSpec,
		OperatorBaseURL: operatorapi.GetSpec,
	} {
		doc, err := spec()
		if err != nil {
			panic(fmt.Sprintf("load the embedded spec of %s: %v", base, err))
		}
		// Operator tokens alone reach the operator API, which no account
		// grant decides.
		some, err := operationsOf(doc, base, base == BaseURL)
		if err != nil {
			panic(err.Error())
		}
		maps.Copy(ops, some)
	}
	worker, err := workerapi.GetSpec()
	if err != nil {
		panic(fmt.Sprintf("load the embedded spec of %s: %v", WorkerBaseURL, err))
	}
	some, err := workerOperationsOf(worker, WorkerBaseURL)
	if err != nil {
		panic(err.Error())
	}
	maps.Copy(ops, some)
	// Root discovery is an authenticated account read on the same listener,
	// with the policies capability and all ordinary shared controls.
	// ref: decisions 0003 and 0009; api/src/api/paths-policy-contract.yaml
	ops[http.MethodGet+" "+policiesassembly.ContractPattern] = Operation{
		Cost:     ratelimit.Standard,
		Gate:     featuresapi.Gate{Feature: "policies"},
		Required: &accessapi.Required{Action: accessapi.ActionAuthenticated, Subject: accessapi.Subject{Kind: accessapi.SubjectAccount}},
	}
	// Webhook intake uses provider protocols, with these control deviations:
	// Authentication: no authenticator credential; the handler verifies the
	// integration secret, with empty 401 and no WWW-Authenticate on refusal.
	// Authorization: no account action; that integration secret is the credential.
	// Feature gates: none at intake; the durable job is gated when claimed.
	// Idempotency keys: ignored; the provider delivery ID deduplicates instead.
	// Strict bodies: the provider parses its own payload shape.
	// Bounded bodies: the handler's 25 MiB limit, not server.max_body_bytes.
	// Problem details: every answer is status-only, including guard failures.
	// Every request is an expensive draw on its client's address, regardless
	// of verification; all other shared controls remain in effect.
	// ref: decision 0003 § "API surfaces"; decision 0012
	ops[http.MethodPost+" "+vcsassembly.WebhookPattern] = Operation{
		Public: true, Unkeyed: true, OwnBodyBound: true, Cost: ratelimit.Expensive,
	}
	// The break-glass login is a page, not an operation of either API, but
	// the same controls stand in front of it: no credential, and each
	// sign-in an expensive draw on the client's address, which slows a
	// password guesser to the expensive class's rate. It names no feature,
	// so no switch-off or tier can lock the break-glass admin out.
	ops[http.MethodGet+" "+accessapi.LoginPattern] = Operation{Public: true}
	ops[http.MethodPost+" "+accessapi.LoginPattern] = Operation{
		Public: true,
		Cost:   ratelimit.Expensive,
	}
	// The authorization server for MCP clients takes no credential: a
	// client registers, authorizes and redeems before it holds one. Each
	// registration is an expensive draw on the client's address, because
	// anyone may register, and none names a feature, so the protocol
	// answers whatever an organization's tier holds; the consent
	// operations, which grant, are the API's and gated there.
	// ref: decision 0006
	ops[http.MethodGet+" "+accessapi.OAuthMetadataPattern] = Operation{Public: true}
	ops[http.MethodPost+" "+accessapi.OAuthRegisterPattern] = Operation{
		Public: true,
		Cost:   ratelimit.Expensive,
	}
	ops[http.MethodGet+" "+accessapi.OAuthAuthorizePattern] = Operation{Public: true}
	ops[http.MethodPost+" "+accessapi.OAuthTokenPattern] = Operation{Public: true}
	// The fs driver's signed URLs carry their credential in the token,
	// which the driver verifies; a bearer check in front would answer 401
	// to every one. Each draws on the standard budget of the client's
	// address and names no feature, since the URL was signed by an
	// operation already gated. A PUT's body is bounded by the size the URL
	// was signed for, which the driver enforces, not by the API's bound.
	// ref: decision 0005
	ops[http.MethodGet+" "+fs.Pattern] = Operation{Public: true}
	ops[http.MethodPut+" "+fs.Pattern] = Operation{Public: true, OwnBodyBound: true}
	// The state backend speaks Terraform's and OpenTofu's http backend
	// protocol, so each shared control it departs from does so here:
	//   - Authentication: HTTP Basic, challenged with Basic
	//     realm="infraweave-state", since the http backend sends Basic
	//     credentials alone: the user name run with a run token, or api with
	//     an API key's <id>:<secret> or a token the API admits.
	//   - Authorization: no account action decides a run token's route on
	//     its own stack; the runs domain's StateAccess grants what the run's
	//     current state allows, so the run is its own subject and loses
	//     access as it moves on. Every other caller, another stack's run
	//     included, may only read, as STACK_STATE_READ and
	//     STACK_STATE_DOWNLOAD on the stack's space and the stack's external
	//     access decide.
	//   - Feature gates: none; the operation that created the run was
	//     gated, and gating state would strand an apply part way.
	//   - Idempotency keys: ignored (State); Terraform sends none, an
	//     identical state adds no version, a write must name the exclusive
	//     lock its run holds, and a LOCK repeating a lock its run holds is
	//     granted again.
	//   - Strict bodies: none; the handler checks a state or a LockInfo as
	//     JSON, Terraform's shapes, which no schema describes.
	//   - Bounded bodies: the backend's own (OwnBodyBound): LOCK and UNLOCK
	//     apply the API's body bound at 16 KiB, a LockInfo's largest, and
	//     POST refuses a Content-Length over state.max_size before it
	//     streams, since a state exceeds any API body.
	//   - Bounded connections: once the credential verifies, a POST's
	//     deadlines move to moving.Stall past each read of its body and a
	//     GET's past each write of its answer, since a state exceeds any
	//     fixed time; LOCK and UNLOCK keep the listener's timeouts.
	//   - Problem details: a 423 answers the holder's LockInfo, from which
	//     Terraform names the lock's holder, or {} for a write when no lock
	//     is held.
	// Each draws on the standard budget of its caller: a run's, the same as
	// the worker API's calls of the run, or an API key's or token's own.
	// ref: decision 0003 § "API surfaces"; decision 0010
	for _, pattern := range []string{stateapi.StackPattern, stateapi.WorkspacePattern} {
		for _, method := range []string{
			http.MethodGet, http.MethodPost, stateapi.MethodLock, stateapi.MethodUnlock,
		} {
			ops[method+" "+pattern] = Operation{State: true, OwnBodyBound: true}
		}
	}
	// The state URLs' routes carry their credential in the token, which
	// the state domain verifies, as the fs driver's signed URLs do: a
	// Basic check in front would answer 401 to every one. Each draws on
	// the standard budget of the client's address and names no feature,
	// since stateUploadUrl or stateDownloadUrl signed it under its gate. A
	// PUT's body is bounded at core.MaxUpload by its handler, not by the
	// API's bound, since a state file exceeds any API body, and once its
	// token verifies each transfer's deadlines move as the backend's do.
	// ref: decision 0003 § "API surfaces"; decision 0010
	ops[http.MethodGet+" "+stateapi.DownloadPattern] = Operation{Public: true}
	ops[http.MethodPut+" "+stateapi.UploadPattern] = Operation{Public: true, OwnBodyBound: true}
	return ops
})

// Guards hold the state of the API's controls: the operator's switch, the
// rate-limit buckets and the idempotency keys. serve keeps all three in
// PostgreSQL, shared by every replica (postgresGuards).
//
// ref: decision 0003
type Guards struct {
	Switch  ratelimit.Switch
	Buckets ratelimit.Store
	Keys    idempotency.Store
}

// Gated builds the feature middleware from a resolver of each request's
// gate; the features domain's Middleware is one.
type Gated func(gate func(*http.Request) (featuresapi.Gate, bool)) func(http.Handler) http.Handler

// Guarded is an API listener router: request ID and recovery, the marks
// that keep every answer uncacheable and unsniffable, the access log, the
// body bound, authentication, then the operator switch, rate limits and
// idempotency keys in front of every route registered on it, so a request
// a control rejects is logged and marked too. The body bound,
// c.MaxBodyBytes, comes before every control that reads a body; it bounds
// every operation's body but an OwnBodyBound one's, and a request no
// operation serves. Last, once every control has passed the request and
// the strict body check has read its body within the listener's
// deadlines, an operation with a Hold has its write deadline extended
// past it.
// auth authenticates every request but those to a Public operation; nil
// refuses every credential. The operator switch runs next, then the rate
// limit, then idempotency keys, so a request one rejects never reaches the
// next and a rejected request never reaches the key store. Each control
// keys on the caller, the principal's <kind>:<actor ID>, or the client's
// address on a public route. ops names the operations its routes serve; a
// request no operation serves, such as a 404, still authenticates and draws
// from the standard budget. gated, when not nil, builds the feature
// middleware, which runs right after authentication, so a refused feature
// costs no budget and stores no idempotent answer; it gates every operation
// that names a capability. Between the two, a route the router serves that
// ops lacks answers 403, and require, when not nil, decides each API
// operation's action on its subject, so a refused action costs no budget
// either.
//
// ref: decisions 0003, 0006, 0007 and 0021
func Guarded(log *slog.Logger, c config.Server, ops Operations, g Guards, auth accessapi.Authenticator, require accessapi.Requirer, gated Gated) *chi.Mux {
	var r *chi.Mux
	find := func(req *http.Request) (string, *chi.Context) {
		rctx := chi.NewRouteContext()
		return r.Find(rctx, req.Method, routePath(req)), rctx
	}
	resolve := func(req *http.Request) (Operation, bool) {
		pattern, _ := find(req)
		op, ok := ops[req.Method+" "+pattern]
		return op, ok
	}
	if auth == nil {
		auth = refuseAll{}
	}
	if require == nil {
		require = passAll{}
	}
	if gated == nil {
		gated = func(func(*http.Request) (featuresapi.Gate, bool)) func(http.Handler) http.Handler {
			return func(next http.Handler) http.Handler { return next }
		}
	}
	r = chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Uncacheable, middleware.AccessLog(log), webhookAnswers, middleware.Recover(log))
	r.NotFound(middleware.NotFound)
	r.MethodNotAllowed(middleware.MethodNotAllowed)
	r.Use(
		middleware.LimitBodies(c.MaxBodyBytes, func(req *http.Request) bool {
			op, _ := resolve(req)
			return op.OwnBodyBound
		}),
		auth.Middleware(func(req *http.Request) accessapi.Access {
			op, _ := resolve(req)
			return op.access()
		}),
		unlisted(func(req *http.Request) bool {
			pattern, _ := find(req)
			_, ok := ops[req.Method+" "+pattern]
			return pattern != "" && !ok
		}),
		require.Middleware(func(req *http.Request) (accessapi.Required, bool) {
			pattern, rctx := find(req)
			op, ok := ops[req.Method+" "+pattern]
			if !ok || op.Required == nil {
				return accessapi.Required{}, false
			}
			out := *op.Required
			out.Params = map[string]string{}
			for i, k := range rctx.URLParams.Keys {
				out.Params[k] = rctx.URLParams.Values[i]
			}
			return out, true
		}),
		gated(func(req *http.Request) (featuresapi.Gate, bool) {
			op, ok := resolve(req)
			return op.Gate, ok && op.Gate.Feature != ""
		}),
		ratelimit.Disabled(g.Switch, caller),
		ratelimit.Middleware(c.RateLimit, g.Buckets, caller,
			func(req *http.Request) ratelimit.Class {
				op, _ := resolve(req)
				return op.Cost
			}),
		idempotency.Middleware(g.Keys, replayCaller,
			func(req *http.Request) bool {
				op, ok := resolve(req)
				return ok && !op.Search && !op.Unkeyed && !op.Worker && !op.State
			}, log),
		strictPaths(func(req *http.Request) (openapi3.Parameters, map[string]string) {
			pattern, rctx := find(req)
			op := ops[req.Method+" "+pattern]
			if len(op.Path) == 0 {
				return nil, nil
			}
			values := map[string]string{}
			for i, k := range rctx.URLParams.Keys {
				values[k] = rctx.URLParams.Values[i]
			}
			return op.Path, values
		}),
		strictQueries(func(req *http.Request) openapi3.Parameters {
			op, _ := resolve(req)
			return op.Query
		}),
		strictBodies(func(req *http.Request) Operation {
			op, _ := resolve(req)
			return op
		}),
		extendHeld(log, c.Timeouts.Write, func(req *http.Request) time.Duration {
			op, _ := resolve(req)
			return op.Hold
		}),
	)
	return r
}

// extendHeld extends the write deadline of a request whose operation
// holds its response, to its hold plus the write timeout an answer has
// after it, so the hold, not the listener, decides when it answers. The
// read deadline stays the listener's: the controls before it have read
// the body within it, and net/http clears it once a body is read whole. A
// connection whose deadline cannot move keeps the listener's, and the
// failure is logged, since its holds would end early.
func extendHeld(log *slog.Logger, write time.Duration, hold func(*http.Request) time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d := hold(r); d > 0 {
				err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d + write))
				if err != nil {
					log.ErrorContext(r.Context(), "extend a held response's deadline",
						"error", err)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// access is the credential the operation needs.
func (o Operation) access() accessapi.Access {
	switch {
	case o.Public:
		return accessapi.Public
	case o.Runner:
		return accessapi.Runner
	case o.State:
		return accessapi.State
	case o.Worker:
		return accessapi.Worker
	case o.Organization:
		return accessapi.Organization
	case o.OrganizationAdmin:
		return accessapi.OrganizationAdmin
	}
	return accessapi.Account
}

// strictBodies checks the body of a request whose operation has a strict
// one against its schema, answering 400 invalid-request for an unknown
// field, a missing required one or a wrong type before the handler decodes
// it. An operation's semantic-body policy promotes only its named fields'
// type and unknown-field refusals to 422 validation-failed. A body with no Content-Type is checked as the operation's one media
// type, JSON or a merge patch, as the generated handler decodes it.
//
// ref: decision 0022
func strictBodies(operation func(*http.Request) Operation) func(http.Handler) http.Handler {
	opts := &openapi3filter.Options{SkipSettingDefaults: true}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			op := operation(r)
			rb := op.Body
			if rb == nil {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("Content-Type") == "" {
				for typ := range rb.Content {
					r.Header.Set("Content-Type", typ)
				}
			}
			in := &openapi3filter.RequestValidationInput{Request: r, Options: opts}
			if err := openapi3filter.ValidateRequestBody(r.Context(), in, rb); err != nil {
				if semanticBodyError(w, err, op.SemanticBodyFields) {
					return
				}
				strictError(w, r, err, "", "The body")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// semanticBodyError applies only the operation's explicit validation policy,
// retaining the schema's precise pointer without exposing the rejected value.
func semanticBodyError(w http.ResponseWriter, err error, fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	field, ok := refusedField(err, "")
	if !ok || field.Reason != kernel.ReasonType && field.Reason != kernel.ReasonUnknownField {
		return false
	}
	for _, prefix := range fields {
		if field.Pointer == prefix || strings.HasPrefix(field.Pointer, prefix+"/") {
			p := problem.ValidationFailed.New("The request's configuration is refused.")
			p.Errors = []problem.FieldError{field}
			problem.Write(w, p)
			return true
		}
	}
	return false
}

// strictPaths checks each of a request's path parameters with a schema
// against it, as strictQueries checks the query: 400 invalid-request for
// a value its maxLength, pattern, enum, format or bounds refuses, or not
// of its type, before the generated binding decodes it. Each value is
// checked as the binding decodes it, unescaped, and as it is when it does
// not unescape, which the binding refuses too. The refused value is named
// by its pointer under #/path/ and the parameter's name. The path check
// runs before the handler's own, so a worker API run ID longer than any
// run's answers 400 here, not 401 as another run's: the same value
// answers the same on every surface.
//
// ref: decision 0022
func strictPaths(path func(*http.Request) (openapi3.Parameters, map[string]string)) func(http.Handler) http.Handler {
	opts := &openapi3filter.Options{SkipSettingDefaults: true}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			params, values := path(r)
			if len(params) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			for k, v := range values {
				if u, err := url.PathUnescape(v); err == nil {
					values[k] = u
				}
			}
			in := &openapi3filter.RequestValidationInput{
				Request: r, PathParams: values, Options: opts,
			}
			for _, p := range params {
				err := openapi3filter.ValidateParameter(r.Context(), in, p.Value)
				if err != nil {
					strictError(w, r, err, "#/path/"+p.Value.Name,
						"The path parameter "+p.Value.Name)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// strictQueries checks each of a request's query parameters with a
// schema against it, as strictBodies checks a body: 400 invalid-request
// for a value its enum, format, bounds or pattern refuses, a wrong type,
// a missing required parameter or an unknown member of a JSON object,
// before the generated binding, which checks only a value's Go type,
// decodes it. The refused value is named by its pointer under #/query/
// and the parameter's name.
//
// ref: decision 0022
func strictQueries(query func(*http.Request) openapi3.Parameters) func(http.Handler) http.Handler {
	opts := &openapi3filter.Options{SkipSettingDefaults: true}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			in := &openapi3filter.RequestValidationInput{Request: r, Options: opts}
			for _, p := range query(r) {
				err := openapi3filter.ValidateParameter(r.Context(), in, p.Value)
				if err != nil {
					strictError(w, r, err, "#/query/"+p.Value.Name,
						"The query parameter "+p.Value.Name)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// strictError answers an input its schema refuses as invalid-request,
// naming the refused value by its pointer under root with the reason the
// schema gives, so a client finds it without parsing the detail, which
// subject, the input's name, begins. A refusal that names no field
// answers kin-openapi's text alone.
func strictError(w http.ResponseWriter, r *http.Request, err error, root, subject string) {
	fe, ok := refusedField(err, root)
	if !ok {
		problem.RequestError(w, r, err)
		return
	}
	detail := subject + " holds a value its schema refuses."
	if fe.Reason == kernel.ReasonUnknownField {
		detail = subject + " sets a field its schema does not name."
	}
	p := problem.InvalidRequest.New(detail)
	p.Errors = []problem.FieldError{fe}
	problem.Write(w, p)
}

// schemaReasons is the field error's reason for a refusal under each
// schema keyword that has one; a refusal under another, such as oneOf or
// minItems, names no field.
var schemaReasons = map[string]string{
	"type":             kernel.ReasonType,
	"nullable":         kernel.ReasonType,
	"enum":             kernel.ReasonEnum,
	"format":           kernel.ReasonFormat,
	"pattern":          kernel.ReasonPattern,
	"minLength":        kernel.ReasonMinLength,
	"maxLength":        kernel.ReasonMaxLength,
	"minimum":          kernel.ReasonMinimum,
	"exclusiveMinimum": kernel.ReasonMinimum,
	"maximum":          kernel.ReasonMaximum,
	"exclusiveMaximum": kernel.ReasonMaximum,
	"required":         kernel.ReasonRequired,
}

// refusedField is the field error naming the value err, a refusal of the
// input under root, refuses: a schema keyword's at the value's pointer,
// and a member the schema does not list at the member's, reason
// unknown-field; and a parameter's whole value that is missing, or does
// not parse as its type or within its format's range, at the parameter's.
// A body or a JSON object that does not parse names nothing. Messages
// carry kin-openapi's reasons, which never quote the value, so a value
// sent in error, such as a token, is not echoed.
//
// kin-openapi names an unknown member only in its reason's text, and a
// missing one in its pointer; TestUnitStrictBodies pins both, so a change
// to either fails there rather than silently dropping the pointer.
func refusedField(err error, root string) (problem.FieldError, bool) {
	if se, ok := errors.AsType[*openapi3.SchemaError](err); ok {
		return schemaField(se, root)
	}
	re, ok := errors.AsType[*openapi3filter.RequestError](err)
	if !ok || re.Parameter == nil {
		return problem.FieldError{}, false
	}
	_, parse := errors.AsType[*openapi3filter.ParseError](err)
	switch {
	case errors.Is(err, openapi3filter.ErrInvalidRequired) || missing(re):
		return problem.FieldError{
			Pointer: root, Reason: kernel.ReasonRequired,
			Message: "A value is required.",
		}, true
	case parse && errors.Is(err, strconv.ErrRange):
		return problem.FieldError{
			Pointer: root, Reason: kernel.ReasonFormat,
			Message: "The value is out of its format's range.",
		}, true
	case parse:
		return problem.FieldError{
			Pointer: root, Reason: kernel.ReasonType,
			Message: "The value is not of its schema's type.",
		}, true
	}
	return problem.FieldError{}, false
}

// missing reports whether re refuses a required query parameter its
// request lacks, which kin-openapi gives no sentinel error for when the
// parameter carries JSON. A path parameter is never missing from a route
// that matched.
func missing(re *openapi3filter.RequestError) bool {
	return re.Parameter.In == openapi3.ParameterInQuery && re.Parameter.Required &&
		re.Input != nil && re.Input.Request != nil &&
		!re.Input.Request.URL.Query().Has(re.Parameter.Name)
}

// schemaField is refusedField of se.
func schemaField(se *openapi3.SchemaError, root string) (problem.FieldError, bool) {
	at := root
	for _, token := range se.JSONPointer() {
		at += "/" + pointerToken(token)
	}
	if se.SchemaField == "properties" {
		quoted, prefixed := strings.CutPrefix(se.Reason, "property ")
		quoted, suffixed := strings.CutSuffix(quoted, " is unsupported")
		name, err := strconv.Unquote(quoted)
		if !prefixed || !suffixed || err != nil {
			return problem.FieldError{}, false
		}
		return problem.FieldError{
			Pointer: at + "/" + pointerToken(name), Reason: kernel.ReasonUnknownField,
			Message: fmt.Sprintf("No field %q here.", name),
		}, true
	}
	reason, ok := schemaReasons[se.SchemaField]
	if !ok {
		return problem.FieldError{}, false
	}
	return problem.FieldError{Pointer: at, Reason: reason, Message: sentence(se.Reason)}, true
}

// pointerToken is name escaped as a JSON Pointer token (RFC 6901).
func pointerToken(name string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(name)
}

// sentence is reason, a kin-openapi reason, as a sentence.
func sentence(reason string) string {
	if reason == "" {
		return "The schema refuses the value."
	}
	r, n := utf8.DecodeRuneInString(reason)
	return string(unicode.ToUpper(r)) + strings.TrimSuffix(reason[n:], ".") + "."
}

// caller is the actor key of rate limits and disabled callers: the
// principal's, or the client's address on a route no credential
// authenticates.
//
// ref: decision 0003
func caller(r *http.Request) string {
	if p, ok := kernel.PrincipalFrom(r.Context()); ok {
		return p.Caller()
	}
	return ratelimit.ClientAddr(r)
}

// replayCaller keeps retries stable across tokens and replicas in one account.
// The account prefix is disjoint from legacy actor-only keys, so old encrypted
// responses retain their original AAD and cannot be reinterpreted after a switch.
func replayCaller(r *http.Request) string {
	p, ok := kernel.PrincipalFrom(r.Context())
	if !ok || p.AccountID.IsZero() {
		return caller(r)
	}
	return "account/" + p.AccountID.String() + "/" + p.Caller()
}

// unlisted answers 403 to a request for a route the router serves but the
// operations lack, so a route mounted without a route-table entry is
// never served undecided: authorization denies by default.
//
// ref: decision 0007
func unlisted(listed func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if listed(r) {
				problem.Write(w, problem.Forbidden.New("No route-table entry decides this route."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// passAll is the requirer of a router built with none, for tests of the
// other controls: it decides nothing.
type passAll struct{}

func (passAll) Middleware(func(*http.Request) (accessapi.Required, bool)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

// refuseAll is the authenticator of a router built with none: every route
// that needs a credential answers 401.
type refuseAll struct{}

func (refuseAll) Middleware(access func(*http.Request) accessapi.Access) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if access(r) == accessapi.Public {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", "Bearer")
			problem.Write(
				w,
				problem.Unauthenticated.New("The request carries no valid credential."),
			)
		})
	}
}

// routePath is the path chi routes by: the escaped path when the request has
// one, so the operation found is the one the router will serve.
func routePath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}
	if r.URL.Path == "" {
		return "/"
	}
	return r.URL.Path
}
