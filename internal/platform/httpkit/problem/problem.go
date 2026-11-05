package problem

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ContentType is the media type of every problem body.
const ContentType = "application/problem+json"

// TypePrefix starts every problem type URI; the slugs are listed in
// docs/research/decisions/0022-api-errors-and-update-semantics.md.
const TypePrefix = "urn:infraweave:problem:"

// InstancePrefix starts every problem's instance, which names the request it
// answers (decision 0022, "Correlation").
const InstancePrefix = "urn:infraweave:request:"

// RequestIDHeader carries the request ID both ways (decision 0017).
// httpkit/middleware sets it on the response before any handler runs, so
// Write finds it there whichever handler or guard answers.
const RequestIDHeader = "X-Request-Id"

// Type is a problem type of the register, docs/data/problems.json: its slug,
// the status it answers with and its title.
type Type struct {
	Slug   string
	Status int
	Title  string
}

// The problem types of the register, one per slug. Types lists them all,
// and TestUnitProblemRegistry holds them to the register in both directions.
var (
	InvalidRequest  = Type{"invalid-request", http.StatusBadRequest, "invalid request"}
	InvalidCursor   = Type{"invalid-cursor", http.StatusBadRequest, "Invalid cursor"}
	Unauthenticated = Type{
		"unauthenticated",
		http.StatusUnauthorized,
		"unauthorized: authentication required",
	}
	Forbidden = Type{
		"forbidden",
		http.StatusForbidden,
		"unauthorized: permission denied",
	}
	FeatureDisabled  = Type{"feature-disabled", http.StatusForbidden, "feature disabled"}
	FeatureNotInTier = Type{
		"feature-not-in-tier",
		http.StatusForbidden,
		"feature not in tier",
	}
	LimitReached            = Type{"limit-reached", http.StatusForbidden, "limit reached"}
	InstallationUnsupported = Type{
		"installation-unsupported",
		http.StatusForbidden,
		"installation unsupported",
	}
	NotFound         = Type{"not-found", http.StatusNotFound, "not found"}
	MethodNotAllowed = Type{
		"method-not-allowed",
		http.StatusMethodNotAllowed,
		"method not allowed",
	}
	AlreadyExists       = Type{"already-exists", http.StatusConflict, "already exists"}
	InvalidState        = Type{"invalid-state", http.StatusConflict, "invalid state"}
	ResourceLocked      = Type{"resource-locked", http.StatusConflict, "resource locked"}
	IntentProjectLocked = Type{
		"intent-project-locked",
		http.StatusConflict,
		"intent project locked",
	}
	IdempotencyKeyInFlight = Type{
		"idempotency-key-in-flight",
		http.StatusConflict,
		"Idempotency key in use",
	}
	PreconditionFailed = Type{
		"precondition-failed",
		http.StatusPreconditionFailed,
		"precondition failed",
	}
	PayloadTooLarge = Type{
		"payload-too-large",
		http.StatusRequestEntityTooLarge,
		"payload too large",
	}
	UnsupportedMediaType = Type{
		"unsupported-media-type",
		http.StatusUnsupportedMediaType,
		"unsupported media type",
	}
	ValidationFailed = Type{
		"validation-failed",
		http.StatusUnprocessableEntity,
		"validation failed",
	}
	WebhookVerificationFailed = Type{
		"webhook-verification-failed",
		http.StatusUnprocessableEntity,
		"webhook verification failed",
	}
	IdempotencyKeyReuse = Type{
		"idempotency-key-reuse",
		http.StatusUnprocessableEntity,
		"Idempotency key reused",
	}
	RateLimited    = Type{"rate-limited", http.StatusTooManyRequests, "Rate limit exceeded"}
	InternalError  = Type{"internal-error", http.StatusInternalServerError, "internal error"}
	NotImplemented = Type{"not-implemented", http.StatusNotImplemented, "not implemented"}
	UpstreamError  = Type{"upstream-error", http.StatusBadGateway, "upstream error"}
	APIDisabled    = Type{
		"api-disabled",
		http.StatusServiceUnavailable,
		"API disabled for this caller",
	}
	ServiceUnavailable = Type{
		"service-unavailable",
		http.StatusServiceUnavailable,
		"service unavailable",
	}
)

// Types is every problem type, ordered by slug: the list a client such as
// pkg/sdk branches on.
func Types() []Type {
	types := []Type{
		InvalidRequest, InvalidCursor, Unauthenticated, Forbidden, FeatureDisabled,
		FeatureNotInTier, LimitReached, InstallationUnsupported, NotFound,
		MethodNotAllowed, AlreadyExists, InvalidState, ResourceLocked,
		IntentProjectLocked, IdempotencyKeyInFlight, PreconditionFailed,
		PayloadTooLarge, UnsupportedMediaType, ValidationFailed,
		WebhookVerificationFailed, IdempotencyKeyReuse, RateLimited, InternalError,
		NotImplemented, UpstreamError, APIDisabled, ServiceUnavailable,
	}
	slices.SortFunc(types, func(a, b Type) int { return strings.Compare(a.Slug, b.Slug) })
	return types
}

// New is a problem of type t.
func (t Type) New(detail string) Problem {
	return Problem{Type: TypePrefix + t.Slug, Title: t.Title, Status: t.Status, Detail: detail}
}

// Error is an error of type t, for a handler to return so ResponseError
// answers with it.
func (t Type) Error(detail string) *Error { return &Error{Problem: t.New(detail)} }

// Error is a handler error that carries the problem to answer with.
type Error struct {
	Problem Problem
}

func (e *Error) Error() string { return e.Problem.Title + ": " + e.Problem.Detail }

// Problem is one problem detail. Field names match the Problem schema.
type Problem struct {
	Type     string `json:"type,omitempty"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// Write sends p with its status. The status line and the body's status are
// the same number, as RFC 9457 requires. A p without an instance gets the
// request's, from the X-Request-Id the response already carries, so an
// operator finds the request's log lines from the body alone.
func Write(w http.ResponseWriter, p Problem) {
	if id := w.Header().Get(RequestIDHeader); p.Instance == "" && id != "" {
		p.Instance = InstancePrefix + id
	}
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(p.Status)
	// The header is already sent; a failed write means the client has gone.
	_ = json.NewEncoder(w).Encode(p)
}

// NotImplementedError is returned by the generated UnimplementedStrict for
// every operation a family hasn't implemented yet.
type NotImplementedError struct {
	// OperationID is the operation's operationId in the API.
	OperationID string
	// Route is the method and path template, such as "GET /stacks".
	Route string
}

func (e *NotImplementedError) Error() string {
	return fmt.Sprintf("%s (%s) is not implemented yet", e.OperationID, e.Route)
}

// Internal answers 500 with a generic detail, for a failure whose cause must
// not reach the client.
func Internal(w http.ResponseWriter) {
	Write(w, InternalError.New("The server failed to handle the request."))
}

// UnavailableRetryAfter is the Retry-After of service-unavailable. pkg/sdk's
// DefaultRetry tries five times, so two seconds rides out a database
// failover or restart of about eight seconds without retrying any faster.
const UnavailableRetryAfter = 2 * time.Second

// Unavailable answers 503 service-unavailable with Retry-After, for a
// request that failed because a dependency cannot be reached now.
func Unavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(int(UnavailableRetryAfter/time.Second)))
	Write(w, ServiceUnavailable.New("infraweave cannot reach a dependency it needs; retry later."))
}

// Failure answers an unexpected error: Unavailable when it is
// kernel.ErrUnavailable, so the client retries, and Internal otherwise.
// Neither detail names the cause.
func Failure(w http.ResponseWriter, err error) {
	if errors.Is(err, kernel.ErrUnavailable) {
		Unavailable(w)
		return
	}
	Internal(w)
}

// RequestError answers a request the generated handler couldn't decode
// (a malformed body or parameter) with 400.
func RequestError(w http.ResponseWriter, _ *http.Request, err error) {
	Write(w, InvalidRequest.New(err.Error()))
}

// ResponseError answers an error a strict handler returned: its problem for
// an *Error, 501 naming the operation for a stub, and Failure's 503 or 500
// otherwise, whose details are generic so internal errors never reach the
// client.
func ResponseError(w http.ResponseWriter, _ *http.Request, err error) {
	if pe, ok := errors.AsType[*Error](err); ok {
		Write(w, pe.Problem)
		return
	}
	if nie, ok := errors.AsType[*NotImplementedError](err); ok {
		Write(w, NotImplemented.New(nie.Error()))
		return
	}
	Failure(w, err)
}
