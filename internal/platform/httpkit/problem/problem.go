package problem

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ContentType is the media type of every problem body.
const ContentType = "application/problem+json"

// TypePrefix starts every problem type URI; the slugs are listed in
// docs/research/decisions/0022-api-errors-and-update-semantics.md.
const TypePrefix = "urn:infraweave:problem:"

// Type is a problem type of the register, docs/data/problems.json: its slug,
// the status it answers with and its title.
type Type struct {
	Slug   string
	Status int
	Title  string
}

// The problem types the server writes itself. Each must match its register
// record, which TestUnitTypesMatchRegister checks.
var (
	InvalidRequest         = Type{"invalid-request", http.StatusBadRequest, "invalid request"}
	InvalidCursor          = Type{"invalid-cursor", http.StatusBadRequest, "Invalid cursor"}
	IdempotencyKeyInFlight = Type{
		"idempotency-key-in-flight",
		http.StatusConflict,
		"Idempotency key in use",
	}
	PayloadTooLarge = Type{
		"payload-too-large",
		http.StatusRequestEntityTooLarge,
		"payload too large",
	}
	IdempotencyKeyReuse = Type{
		"idempotency-key-reuse",
		http.StatusUnprocessableEntity,
		"Idempotency key reused",
	}
	RateLimited = Type{"rate-limited", http.StatusTooManyRequests, "Rate limit exceeded"}
	APIDisabled = Type{
		"api-disabled",
		http.StatusServiceUnavailable,
		"API disabled for this caller",
	}
)

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
// the same number, as RFC 9457 requires.
func Write(w http.ResponseWriter, p Problem) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(p.Status)
	// The header is already sent; a failed write means the client has gone.
	_ = json.NewEncoder(w).Encode(p)
}

// New is a problem for status with the standard reason phrase as its title.
func New(status int, detail string) Problem {
	return Problem{Title: http.StatusText(status), Status: status, Detail: detail}
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
	Write(w, New(http.StatusInternalServerError, "The server failed to handle the request."))
}

// RequestError answers a request the generated handler couldn't decode
// (a malformed body or parameter) with 400.
func RequestError(w http.ResponseWriter, _ *http.Request, err error) {
	Write(w, New(http.StatusBadRequest, err.Error()))
}

// ResponseError answers an error a strict handler returned: its problem for
// an *Error, 501 naming the operation for a stub, and 500 otherwise. The 500
// detail is generic so internal errors never reach the client.
func ResponseError(w http.ResponseWriter, _ *http.Request, err error) {
	if pe, ok := errors.AsType[*Error](err); ok {
		Write(w, pe.Problem)
		return
	}
	if nie, ok := errors.AsType[*NotImplementedError](err); ok {
		Write(w, New(http.StatusNotImplemented, nie.Error()))
		return
	}
	Internal(w)
}
