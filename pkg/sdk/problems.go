package sdk

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// TypePrefix starts every problem type URI the API sends; the slug follows.
const TypePrefix = "urn:infraweave:problem:"

// Problem is an RFC 9457 problem detail, the body of every error answer
// (decision 0022). Each slug of docs/data/problems.json has a type that
// embeds it, so a caller tests for one with errors.As:
//
//	if errors.As(err, &sdk.NotFound{}) { … }
//
// A problem whose type no slug names is returned as a Problem.
type Problem struct {
	Type     string `json:"type,omitempty"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

func (p Problem) Error() string {
	if p.Detail == "" {
		return p.Title
	}
	return p.Title + ": " + p.Detail
}

// Slug is the problem's slug, or "" for a type outside the register.
func (p Problem) Slug() string {
	if s, ok := strings.CutPrefix(p.Type, TypePrefix); ok {
		return s
	}
	return ""
}

func (p Problem) problem() Problem { return p }

// AsProblem returns the problem err carries, whatever its type.
func AsProblem(err error) (Problem, bool) {
	var e interface{ problem() Problem }
	if errors.As(err, &e) {
		return e.problem(), true
	}
	return Problem{}, false
}

// The typed errors, one per slug of docs/data/problems.json;
// TestUnitSDKProblemSlugs holds them to it.
type (
	// InvalidRequest is the invalid-request problem (400).
	InvalidRequest struct{ Problem }
	// InvalidCursor is the invalid-cursor problem (400).
	InvalidCursor struct{ Problem }
	// Unauthenticated is the unauthenticated problem (401).
	Unauthenticated struct{ Problem }
	// Forbidden is the forbidden problem (403).
	Forbidden struct{ Problem }
	// FeatureDisabled is the feature-disabled problem (403).
	FeatureDisabled struct{ Problem }
	// FeatureNotInTier is the feature-not-in-tier problem (403).
	FeatureNotInTier struct{ Problem }
	// LimitReached is the limit-reached problem (403).
	LimitReached struct{ Problem }
	// InstallationUnsupported is the installation-unsupported problem (403).
	InstallationUnsupported struct{ Problem }
	// NotFound is the not-found problem (404).
	NotFound struct{ Problem }
	// MethodNotAllowed is the method-not-allowed problem (405).
	MethodNotAllowed struct{ Problem }
	// AlreadyExists is the already-exists problem (409).
	AlreadyExists struct{ Problem }
	// InvalidState is the invalid-state problem (409).
	InvalidState struct{ Problem }
	// ResourceLocked is the resource-locked problem (409).
	ResourceLocked struct{ Problem }
	// IntentProjectLocked is the intent-project-locked problem (409).
	IntentProjectLocked struct{ Problem }
	// IdempotencyKeyInFlight is the idempotency-key-in-flight problem (409).
	IdempotencyKeyInFlight struct{ Problem }
	// PreconditionFailed is the precondition-failed problem (412).
	PreconditionFailed struct{ Problem }
	// PayloadTooLarge is the payload-too-large problem (413).
	PayloadTooLarge struct{ Problem }
	// UnsupportedMediaType is the unsupported-media-type problem (415).
	UnsupportedMediaType struct{ Problem }
	// ValidationFailed is the validation-failed problem (422).
	ValidationFailed struct{ Problem }
	// WebhookVerificationFailed is the webhook-verification-failed problem (422).
	WebhookVerificationFailed struct{ Problem }
	// IdempotencyKeyReuse is the idempotency-key-reuse problem (422).
	IdempotencyKeyReuse struct{ Problem }
	// RateLimited is the rate-limited problem (429).
	RateLimited struct{ Problem }
	// InternalError is the internal-error problem (500).
	InternalError struct{ Problem }
	// NotImplemented is the not-implemented problem (501).
	NotImplemented struct{ Problem }
	// UpstreamError is the upstream-error problem (502).
	UpstreamError struct{ Problem }
	// APIDisabled is the api-disabled problem (503).
	APIDisabled struct{ Problem }
	// ServiceUnavailable is the service-unavailable problem (503).
	ServiceUnavailable struct{ Problem }
)

var bySlug = map[string]func(Problem) error{
	"invalid-request":             func(p Problem) error { return InvalidRequest{p} },
	"invalid-cursor":              func(p Problem) error { return InvalidCursor{p} },
	"unauthenticated":             func(p Problem) error { return Unauthenticated{p} },
	"forbidden":                   func(p Problem) error { return Forbidden{p} },
	"feature-disabled":            func(p Problem) error { return FeatureDisabled{p} },
	"feature-not-in-tier":         func(p Problem) error { return FeatureNotInTier{p} },
	"limit-reached":               func(p Problem) error { return LimitReached{p} },
	"installation-unsupported":    func(p Problem) error { return InstallationUnsupported{p} },
	"not-found":                   func(p Problem) error { return NotFound{p} },
	"method-not-allowed":          func(p Problem) error { return MethodNotAllowed{p} },
	"already-exists":              func(p Problem) error { return AlreadyExists{p} },
	"invalid-state":               func(p Problem) error { return InvalidState{p} },
	"resource-locked":             func(p Problem) error { return ResourceLocked{p} },
	"intent-project-locked":       func(p Problem) error { return IntentProjectLocked{p} },
	"idempotency-key-in-flight":   func(p Problem) error { return IdempotencyKeyInFlight{p} },
	"precondition-failed":         func(p Problem) error { return PreconditionFailed{p} },
	"payload-too-large":           func(p Problem) error { return PayloadTooLarge{p} },
	"unsupported-media-type":      func(p Problem) error { return UnsupportedMediaType{p} },
	"validation-failed":           func(p Problem) error { return ValidationFailed{p} },
	"webhook-verification-failed": func(p Problem) error { return WebhookVerificationFailed{p} },
	"idempotency-key-reuse":       func(p Problem) error { return IdempotencyKeyReuse{p} },
	"rate-limited":                func(p Problem) error { return RateLimited{p} },
	"internal-error":              func(p Problem) error { return InternalError{p} },
	"not-implemented":             func(p Problem) error { return NotImplemented{p} },
	"upstream-error":              func(p Problem) error { return UpstreamError{p} },
	"api-disabled":                func(p Problem) error { return APIDisabled{p} },
	"service-unavailable":         func(p Problem) error { return ServiceUnavailable{p} },
}

// Slugs lists the slugs that have a typed error.
func Slugs() []string {
	out := make([]string, 0, len(bySlug))
	for s := range bySlug {
		out = append(out, s)
	}
	return out
}

// HTTPError is an error answer whose body is no problem detail, such as a
// proxy's page or a malformed problem.
type HTTPError struct {
	Status int
	Body   []byte
}

func (e *HTTPError) Error() string {
	return http.StatusText(e.Status) + " (" + strconv.Itoa(e.Status) + "), not a problem detail"
}

// Check returns nil for a status below 400 and otherwise the typed error of
// the answer's problem, for a result of the generated client:
//
//	r, err := c.API.StacksWithResponse(ctx, nil)
//	if err == nil {
//		err = sdk.Check(r.HTTPResponse, r.Body)
//	}
func Check(resp *http.Response, body []byte) error {
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	var p Problem
	if mt != "application/problem+json" || json.Unmarshal(body, &p) != nil ||
		p.Status != resp.StatusCode {
		return &HTTPError{Status: resp.StatusCode, Body: body}
	}
	if typed, ok := bySlug[p.Slug()]; ok {
		return typed(p)
	}
	return p
}
